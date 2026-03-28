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
from .cache import cache_lookup, cache_store_response, normalize_prompt, hash_prompt
from .config import (
    BUDGET_ENABLED,
    CACHE_ENABLED,
    CONNECT_TIMEOUT,
    OVERALL_TIMEOUT,
    STORE_PROMPTS,
    estimate_cost,
)
from .db import Database
from .failover import get_upstream_candidates, health_check_loop, report_upstream_failure, report_upstream_success
from .interceptor import (
    new_streaming_record,
    parse_anthropic_response,
    parse_anthropic_sse_event,
    parse_openai_response,
    parse_openai_sse_event,
)
from .privacy import sanitize_stored_payload
from .router import evaluate_ab_test, evaluate_routing
from .tagging import auto_tag
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


async def _resolve_feature_tag(db: Database, explicit_tag: str, source_app: str, first_message: str) -> str:
    """Resolve the request tag from the header first, then auto-tag rules."""
    if explicit_tag:
        return explicit_tag
    try:
        rules = await db.get_tag_rules()
    except Exception:
        logger.exception("Failed to load tag rules")
        return explicit_tag
    return auto_tag(source_app, first_message, rules)


def _parse_json_body(body_or_data: bytes | dict | None) -> dict | None:
    """Return parsed request JSON or None for invalid payloads."""
    if isinstance(body_or_data, dict):
        return body_or_data
    if not body_or_data:
        return None
    try:
        return json.loads(body_or_data)
    except (json.JSONDecodeError, ValueError):
        return None


def _is_streaming(body_or_data: bytes | dict) -> bool:
    data = _parse_json_body(body_or_data)
    return bool(data and data.get("stream", False))


def _extract_request_info(body_or_data: bytes | dict) -> dict:
    """Extract model, first message content, and estimated token count from request body."""
    data = _parse_json_body(body_or_data)
    if data is None:
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

    if isinstance(body_or_data, (bytes, bytearray)):
        est_tokens = len(body_or_data) // 4
    else:
        est_tokens = len(json.dumps(data, separators=(",", ":"))) // 4
    return {"model": model, "first_message": first_message, "est_tokens": est_tokens}


def _rewrite_model_in_body(body: bytes, new_model: str, body_data: dict | None = None) -> bytes:
    """Rewrite the model field in the request body JSON."""
    data = _parse_json_body(body_data if body_data is not None else body)
    if data is None:
        return body
    data["model"] = new_model
    return json.dumps(data, separators=(",", ":")).encode()


def _build_upstream_url(base_url: str, path: str, query: str) -> str:
    """Build the upstream URL for a proxied request."""
    upstream_url = f"{base_url}/{path}"
    if query:
        upstream_url += f"?{query}"
    return upstream_url


def _upstream_error_response(error_type: str) -> Response:
    """Create a consistent upstream failure response."""
    if error_type == "timeout":
        return Response(
            content=json.dumps({"error": "TokenWatch: upstream timeout"}).encode(),
            status_code=504,
            media_type="application/json",
        )
    return Response(
        content=json.dumps({"error": "TokenWatch: cannot connect to upstream"}).encode(),
        status_code=502,
        media_type="application/json",
    )


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
    parsed_body = _parse_json_body(body)
    info = _extract_request_info(parsed_body if parsed_body is not None else body)
    requested_model = info["model"]
    is_streaming = _is_streaming(parsed_body if parsed_body is not None else body)
    feature_tag = await _resolve_feature_tag(db, feature_tag, source_app, info["first_message"])

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
    if CACHE_ENABLED and not is_streaming:
        cache_result = await cache_lookup(db, parsed_body if parsed_body is not None else body, api_type)
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
        body = _rewrite_model_in_body(body, routed_model, parsed_body)
        parsed_body = _parse_json_body(body)
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
            body = _rewrite_model_in_body(body, routed_model, parsed_body)
            parsed_body = _parse_json_body(body)
            extra_headers["X-TokenWatch-AB-Test"] = str(ab_id)

    # --- Stage 4: Upstream selection ---
    override_upstream = routing_decision.upstream if routing_decision.was_rerouted else ""
    base_urls = await get_upstream_candidates(db, api_type, override_upstream)

    client = await get_client()

    # --- Stage 5: Forward request ---
    if is_streaming:
        return await _proxy_streaming(
            client, request.method, base_urls, path, request.url.query, headers, body,
            api_type, source_app, session_id, feature_tag,
            requested_model, routed_model, routing_rule_id, ab_test_id,
            start, extra_headers,
        )
    return await _proxy_non_streaming(
        client, request.method, base_urls, path, request.url.query, headers, body,
        api_type, source_app, session_id, feature_tag,
        requested_model, routed_model, routing_rule_id, ab_test_id,
        start, extra_headers,
    )


async def _proxy_non_streaming(
    client, method, base_urls, path, query, headers, body,
    api_type, source_app, session_id, feature_tag,
    requested_model, routed_model, routing_rule_id, ab_test_id,
    start, extra_headers,
):
    parse_fn = parse_anthropic_response if api_type == "anthropic" else parse_openai_response

    resp = None
    selected_base_url = None
    last_error_type = "connect"

    for base_url in base_urls:
        url = _build_upstream_url(base_url, path, query)
        try:
            resp = await client.request(method, url, headers=headers, content=body)
            selected_base_url = base_url
            break
        except httpx.ConnectError:
            last_error_type = "connect"
            logger.error("Cannot connect to upstream: %s", url)
            await report_upstream_failure(db, api_type, base_url)
        except httpx.TimeoutException:
            last_error_type = "timeout"
            logger.error("Upstream timeout: %s", url)
            await report_upstream_failure(db, api_type, base_url)

    if resp is None or selected_base_url is None:
        return _upstream_error_response(last_error_type)

    await report_upstream_success(db, api_type, selected_base_url)
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
            request_body = sanitize_stored_payload(body.decode("utf-8", errors="replace"))
            response_body = sanitize_stored_payload(resp_body.decode("utf-8", errors="replace"))
            await db.store_prompt(record.request_id, request_body, response_body, p_hash)
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
    client, method, base_urls, path, query, headers, body,
    api_type, source_app, session_id, feature_tag,
    requested_model, routed_model, routing_rule_id, ab_test_id,
    start, extra_headers,
):
    parse_event_fn = parse_anthropic_sse_event if api_type == "anthropic" else parse_openai_sse_event

    resp = None
    selected_base_url = None
    last_error_type = "connect"

    for base_url in base_urls:
        url = _build_upstream_url(base_url, path, query)
        try:
            req = client.build_request(method, url, headers=headers, content=body)
            resp = await client.send(req, stream=True)
            selected_base_url = base_url
            break
        except httpx.ConnectError:
            last_error_type = "connect"
            logger.error("Cannot connect to upstream: %s", url)
            await report_upstream_failure(db, api_type, base_url)
        except httpx.TimeoutException:
            last_error_type = "timeout"
            logger.error("Upstream timeout: %s", url)
            await report_upstream_failure(db, api_type, base_url)

    if resp is None or selected_base_url is None:
        return _upstream_error_response(last_error_type)

    await report_upstream_success(db, api_type, selected_base_url)

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
