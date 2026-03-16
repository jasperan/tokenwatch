"""FastAPI proxy application for TokenWatch."""

import asyncio
import json
import logging
import time
from contextlib import asynccontextmanager

import httpx
from fastapi import FastAPI, Request, WebSocket
from fastapi.responses import Response, StreamingResponse

from .budget import check_budget_gate
from .cache import cache_lookup, cache_store_response, extract_model, normalize_prompt, hash_prompt
from .config import (
    ANTHROPIC_UPSTREAM,
    BUDGET_ENABLED,
    CACHE_ENABLED,
    CONNECT_TIMEOUT,
    OPENAI_UPSTREAM,
    OVERALL_TIMEOUT,
    STORE_PROMPTS,
    estimate_cost,
)
from .db import Database
from .failover import get_upstream_url, health_check_loop, report_upstream_failure, report_upstream_success
from .interceptor import (
    new_streaming_record,
    parse_anthropic_response,
    parse_anthropic_sse_event,
    parse_openai_response,
    parse_openai_sse_event,
)
from .router import evaluate_ab_test, evaluate_routing
from .ws import ConnectionManager

logger = logging.getLogger("tokenwatch")

db = Database()
ws_manager = ConnectionManager()

# Shared httpx client
_client: httpx.AsyncClient | None = None


async def get_client() -> httpx.AsyncClient:
    global _client
    if _client is None:
        _client = httpx.AsyncClient(
            timeout=httpx.Timeout(OVERALL_TIMEOUT, connect=CONNECT_TIMEOUT),
            follow_redirects=True,
        )
    return _client


@asynccontextmanager
async def lifespan(app):
    await db.init()
    # Init OTEL if enabled
    try:
        from .telemetry import init_telemetry
        init_telemetry()
    except ImportError:
        pass
    # Start health check background task
    health_task = asyncio.create_task(health_check_loop(db))
    logger.info("TokenWatch proxy started")
    yield
    health_task.cancel()
    await db.close()
    global _client
    if _client:
        await _client.aclose()
        _client = None


app = FastAPI(title="TokenWatch Proxy", lifespan=lifespan)


# Hop-by-hop headers that should not be forwarded
HOP_BY_HOP = frozenset([
    "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
    "te", "trailers", "transfer-encoding", "upgrade", "host", "content-length",
])


def _forward_headers(request_headers) -> dict[str, str]:
    return {k: v for k, v in request_headers.items() if k.lower() not in HOP_BY_HOP}


def _source_app(request: Request) -> str:
    return request.headers.get("user-agent", "unknown")


def _session_id(request: Request) -> str:
    return request.headers.get("x-tokenwatch-session", "")


def _feature_tag(request: Request) -> str:
    return request.headers.get("x-tokenwatch-tag", "")


def _is_streaming(body: bytes) -> bool:
    try:
        return json.loads(body).get("stream", False) if body else False
    except (json.JSONDecodeError, ValueError):
        return False


def _extract_request_info(body: bytes) -> dict:
    """Extract model, first message content, and estimated token count from request body."""
    try:
        data = json.loads(body)
    except (json.JSONDecodeError, ValueError):
        return {"model": "", "first_message": "", "est_tokens": 0}

    model = data.get("model", "")
    first_message = ""
    messages = data.get("messages", [])
    if messages:
        content = messages[0].get("content", "")
        if isinstance(content, str):
            first_message = content
        elif isinstance(content, list):
            for block in content:
                if isinstance(block, dict) and block.get("type") == "text":
                    first_message = block.get("text", "")
                    break

    est_tokens = len(json.dumps(data)) // 4
    return {"model": model, "first_message": first_message, "est_tokens": est_tokens}


def _rewrite_model_in_body(body: bytes, new_model: str) -> bytes:
    """Rewrite the model field in the request body JSON."""
    try:
        data = json.loads(body)
        data["model"] = new_model
        return json.dumps(data).encode()
    except (json.JSONDecodeError, ValueError):
        return body


# --- WebSocket endpoint ---

@app.websocket("/ws/live")
async def websocket_live(websocket: WebSocket):
    await ws_manager.connect(websocket)
    try:
        while True:
            await websocket.receive_text()
    except Exception:
        ws_manager.disconnect(websocket)


# --- Health check ---

@app.get("/health")
async def health():
    return {"status": "ok", "service": "tokenwatch-proxy"}


# --- Anthropic proxy ---

@app.api_route("/anthropic/{path:path}", methods=["GET", "POST", "PUT", "DELETE", "PATCH"])
async def proxy_anthropic(request: Request, path: str):
    body = await request.body()
    return await _proxy_request(request, path, body, "anthropic")


# --- OpenAI proxy ---

@app.api_route("/openai/{path:path}", methods=["GET", "POST", "PUT", "DELETE", "PATCH"])
async def proxy_openai(request: Request, path: str):
    body = await request.body()
    return await _proxy_request(request, path, body, "openai")


# --- Unified proxy pipeline ---

async def _proxy_request(request: Request, path: str, body: bytes, api_type: str):
    """Main proxy pipeline: budget -> cache -> route -> forward -> log."""
    source_app = _source_app(request)
    session_id = _session_id(request)
    feature_tag = _feature_tag(request)
    headers = _forward_headers(request.headers)
    start = time.monotonic()
    info = _extract_request_info(body)
    requested_model = info["model"]

    extra_headers = {}

    # --- Stage 1: Budget gate ---
    if BUDGET_ENABLED:
        budget_result = await check_budget_gate(db, source_app, requested_model, feature_tag)
        if not budget_result["allowed"]:
            return Response(
                content=json.dumps(budget_result["block_response"]).encode(),
                status_code=429,
                media_type="application/json",
            )
        extra_headers.update(budget_result["headers"])

    # --- Stage 2: Cache lookup ---
    if CACHE_ENABLED and not _is_streaming(body):
        cache_result = await cache_lookup(db, body, api_type)
        if cache_result:
            latency_ms = int((time.monotonic() - start) * 1000)
            # Log cache hit
            from .models import UsageRecord
            record = UsageRecord(
                api_type=api_type,
                model=requested_model,
                model_requested=requested_model,
                source_app=source_app,
                session_id=session_id,
                feature_tag=feature_tag,
                cache_hit=True,
                latency_ms=latency_ms,
                status_code=200,
                estimated_cost=0.0,
            )
            try:
                await db.log_request(record)
            except Exception:
                logger.exception("Failed to log cache hit")
            await _broadcast_record(record)

            resp_headers = {
                "X-TokenWatch-Cache": "HIT",
                "X-TokenWatch-Cache-Tier": cache_result["tier"],
                "X-TokenWatch-Cache-Similarity": str(cache_result.get("similarity", 1.0)),
            }
            resp_headers.update(extra_headers)
            return Response(
                content=cache_result["response_body"].encode() if isinstance(cache_result["response_body"], str) else cache_result["response_body"],
                status_code=200,
                headers=resp_headers,
                media_type="application/json",
            )

    # --- Stage 3: Smart routing ---
    routed_model = requested_model
    routing_rule_id = None
    ab_test_id = None

    routing_decision = await evaluate_routing(
        db, requested_model, source_app,
        info["first_message"], info["est_tokens"], 0.0,
    )
    if routing_decision.was_rerouted:
        routed_model = routing_decision.model
        routing_rule_id = routing_decision.rule_id
        body = _rewrite_model_in_body(body, routed_model)
        extra_headers["X-TokenWatch-Requested-Model"] = requested_model
        extra_headers["X-TokenWatch-Routed-Model"] = routed_model
        extra_headers["X-TokenWatch-Routing-Rule"] = routing_decision.rule_name

    # A/B test (only if routing didn't already reroute)
    if not routing_decision.was_rerouted:
        import uuid
        req_id = str(uuid.uuid4())
        ab_model, ab_id = await evaluate_ab_test(db, req_id, routed_model, source_app)
        if ab_id is not None:
            routed_model = ab_model
            ab_test_id = ab_id
            body = _rewrite_model_in_body(body, routed_model)
            extra_headers["X-TokenWatch-AB-Test"] = str(ab_id)

    # --- Stage 4: Upstream selection ---
    override_upstream = routing_decision.upstream if routing_decision.was_rerouted else ""
    base_url = await get_upstream_url(db, api_type, override_upstream)
    upstream_url = f"{base_url}/{path}"
    if request.url.query:
        upstream_url += f"?{request.url.query}"

    client = await get_client()

    # --- Stage 5: Forward request ---
    if _is_streaming(body):
        return await _proxy_streaming(
            client, request.method, upstream_url, headers, body,
            api_type, source_app, session_id, feature_tag,
            requested_model, routed_model, routing_rule_id, ab_test_id,
            start, extra_headers, base_url,
        )
    return await _proxy_non_streaming(
        client, request.method, upstream_url, headers, body,
        api_type, source_app, session_id, feature_tag,
        requested_model, routed_model, routing_rule_id, ab_test_id,
        start, extra_headers, base_url,
    )


async def _proxy_non_streaming(
    client, method, url, headers, body,
    api_type, source_app, session_id, feature_tag,
    requested_model, routed_model, routing_rule_id, ab_test_id,
    start, extra_headers, base_url,
):
    parse_fn = parse_anthropic_response if api_type == "anthropic" else parse_openai_response

    try:
        resp = await client.request(method, url, headers=headers, content=body)
    except httpx.ConnectError:
        logger.error("Cannot connect to upstream: %s", url)
        await report_upstream_failure(db, api_type, base_url)
        return Response(
            content=json.dumps({"error": "TokenWatch: cannot connect to upstream"}).encode(),
            status_code=502, media_type="application/json",
        )
    except httpx.TimeoutException:
        logger.error("Upstream timeout: %s", url)
        await report_upstream_failure(db, api_type, base_url)
        return Response(
            content=json.dumps({"error": "TokenWatch: upstream timeout"}).encode(),
            status_code=504, media_type="application/json",
        )

    await report_upstream_success(db, api_type, base_url)
    latency_ms = int((time.monotonic() - start) * 1000)
    resp_body = resp.content

    record = parse_fn(resp_body, resp.status_code, latency_ms, source_app)
    record.model_requested = requested_model
    record.session_id = session_id
    record.feature_tag = feature_tag
    record.routing_rule_id = routing_rule_id
    record.ab_test_id = ab_test_id
    if routed_model and routed_model != requested_model:
        record.model = routed_model

    try:
        await db.log_request(record)
    except Exception:
        logger.exception("Failed to log request")

    # Store in cache (background, best-effort)
    if CACHE_ENABLED and resp.status_code == 200:
        try:
            await cache_store_response(db, body, api_type, resp_body.decode("utf-8", errors="replace"))
        except Exception:
            logger.exception("Failed to cache response")

    # Store prompt (if opt-in)
    if STORE_PROMPTS and resp.status_code == 200:
        try:
            p_hash = hash_prompt(normalize_prompt(body, api_type))
            await db.store_prompt(record.request_id, body.decode("utf-8", errors="replace"), resp_body.decode("utf-8", errors="replace"), p_hash)
        except Exception:
            logger.exception("Failed to store prompt")

    # Broadcast via WebSocket
    await _broadcast_record(record)

    # OTEL metrics
    try:
        from .telemetry import record_request_metrics
        record_request_metrics(record)
    except ImportError:
        pass

    logger.info(
        "REQ %s model=%s in=%d out=%d cost=$%.4f latency=%dms",
        api_type, record.model, record.input_tokens, record.output_tokens,
        record.estimated_cost or 0, latency_ms,
    )

    resp_headers = {k: v for k, v in resp.headers.items() if k.lower() not in HOP_BY_HOP}
    resp_headers.update(extra_headers)
    resp_headers["X-TokenWatch-Cache"] = "MISS"

    return Response(
        content=resp_body, status_code=resp.status_code,
        headers=resp_headers,
        media_type=resp.headers.get("content-type", "application/json"),
    )


async def _proxy_streaming(
    client, method, url, headers, body,
    api_type, source_app, session_id, feature_tag,
    requested_model, routed_model, routing_rule_id, ab_test_id,
    start, extra_headers, base_url,
):
    parse_event_fn = parse_anthropic_sse_event if api_type == "anthropic" else parse_openai_sse_event

    try:
        req = client.build_request(method, url, headers=headers, content=body)
        resp = await client.send(req, stream=True)
    except httpx.ConnectError:
        logger.error("Cannot connect to upstream: %s", url)
        await report_upstream_failure(db, api_type, base_url)
        return Response(
            content=json.dumps({"error": "TokenWatch: cannot connect to upstream"}).encode(),
            status_code=502, media_type="application/json",
        )
    except httpx.TimeoutException:
        logger.error("Upstream timeout: %s", url)
        await report_upstream_failure(db, api_type, base_url)
        return Response(
            content=json.dumps({"error": "TokenWatch: upstream timeout"}).encode(),
            status_code=504, media_type="application/json",
        )

    await report_upstream_success(db, api_type, base_url)

    record = new_streaming_record(api_type, source_app)
    record.status_code = resp.status_code
    record.model_requested = requested_model
    record.session_id = session_id
    record.feature_tag = feature_tag
    record.routing_rule_id = routing_rule_id
    record.ab_test_id = ab_test_id

    async def stream_generator():
        buffer = ""
        try:
            async for raw_chunk in resp.aiter_bytes():
                chunk_str = raw_chunk.decode("utf-8", errors="replace")
                buffer += chunk_str
                while "\n\n" in buffer:
                    event, buffer = buffer.split("\n\n", 1)
                    try:
                        parse_event_fn(event, record)
                    except Exception:
                        logger.exception("Failed to parse SSE event")
                    yield (event + "\n\n").encode()
            if buffer.strip():
                try:
                    parse_event_fn(buffer, record)
                except Exception:
                    pass
                yield buffer.encode()
        finally:
            await resp.aclose()
            latency_ms = int((time.monotonic() - start) * 1000)
            record.latency_ms = latency_ms
            if routed_model and routed_model != requested_model:
                record.model = routed_model
            record.estimated_cost = estimate_cost(record.model, record.input_tokens, record.output_tokens)
            try:
                await db.log_request(record)
            except Exception:
                logger.exception("Failed to log streaming request")
            await _broadcast_record(record)
            try:
                from .telemetry import record_request_metrics
                record_request_metrics(record)
            except ImportError:
                pass
            logger.info(
                "STREAM %s model=%s in=%d out=%d cost=$%.4f latency=%dms",
                api_type, record.model, record.input_tokens, record.output_tokens,
                record.estimated_cost or 0, latency_ms,
            )

    resp_headers = {k: v for k, v in resp.headers.items() if k.lower() not in HOP_BY_HOP}
    resp_headers.update(extra_headers)

    return StreamingResponse(
        stream_generator(), status_code=resp.status_code,
        headers=resp_headers,
        media_type=resp.headers.get("content-type", "text/event-stream"),
    )


async def _broadcast_record(record):
    """Broadcast a completed request to WebSocket clients."""
    from datetime import UTC, datetime
    await ws_manager.broadcast({
        "type": "request_complete",
        "data": {
            "request_id": record.request_id,
            "model": record.model,
            "model_requested": record.model_requested,
            "input_tokens": record.input_tokens,
            "output_tokens": record.output_tokens,
            "cost": record.estimated_cost or 0,
            "latency_ms": record.latency_ms,
            "cache_hit": record.cache_hit,
            "source_app": record.source_app,
            "feature_tag": record.feature_tag,
            "timestamp": datetime.now(UTC).isoformat(),
        },
    })
