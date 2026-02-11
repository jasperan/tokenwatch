"""FastAPI proxy application for TokenWatch."""

import json
import logging
import time
from contextlib import asynccontextmanager

import httpx
from fastapi import FastAPI, Request
from fastapi.responses import Response, StreamingResponse

from .config import (
    ANTHROPIC_UPSTREAM,
    CONNECT_TIMEOUT,
    OPENAI_UPSTREAM,
    OVERALL_TIMEOUT,
    estimate_cost,
)
from .db import Database
from .interceptor import (
    new_streaming_record,
    parse_anthropic_response,
    parse_anthropic_sse_event,
    parse_openai_response,
    parse_openai_sse_event,
)

logger = logging.getLogger("tokenwatch")

db = Database()

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
    logger.info("TokenWatch proxy started")
    yield
    await db.close()
    global _client
    if _client:
        await _client.aclose()
        _client = None


app = FastAPI(title="TokenWatch Proxy", lifespan=lifespan)


# Hop-by-hop headers that should not be forwarded
HOP_BY_HOP = frozenset(
    [
        "connection",
        "keep-alive",
        "proxy-authenticate",
        "proxy-authorization",
        "te",
        "trailers",
        "transfer-encoding",
        "upgrade",
        "host",
        "content-length",
    ]
)


def _forward_headers(request_headers) -> dict[str, str]:
    """Filter request headers for forwarding to upstream."""
    return {k: v for k, v in request_headers.items() if k.lower() not in HOP_BY_HOP}


def _source_app(request: Request) -> str:
    return request.headers.get("user-agent", "unknown")


def _is_streaming(body: bytes) -> bool:
    try:
        return json.loads(body).get("stream", False) if body else False
    except (json.JSONDecodeError, ValueError):
        return False


# --- Anthropic proxy ---


@app.api_route("/anthropic/{path:path}", methods=["GET", "POST", "PUT", "DELETE", "PATCH"])
async def proxy_anthropic(request: Request, path: str):
    upstream_url = f"{ANTHROPIC_UPSTREAM}/{path}"
    if request.url.query:
        upstream_url += f"?{request.url.query}"
    body = await request.body()
    headers = _forward_headers(request.headers)
    source_app = _source_app(request)
    client = await get_client()
    start = time.monotonic()

    if _is_streaming(body):
        return await _proxy_streaming(
            client, request.method, upstream_url, headers, body,
            "anthropic", source_app, start, parse_anthropic_sse_event,
        )
    return await _proxy_non_streaming(
        client, request.method, upstream_url, headers, body,
        "anthropic", source_app, start, parse_anthropic_response,
    )


# --- OpenAI proxy ---


@app.api_route("/openai/{path:path}", methods=["GET", "POST", "PUT", "DELETE", "PATCH"])
async def proxy_openai(request: Request, path: str):
    upstream_url = f"{OPENAI_UPSTREAM}/{path}"
    if request.url.query:
        upstream_url += f"?{request.url.query}"
    body = await request.body()
    headers = _forward_headers(request.headers)
    source_app = _source_app(request)
    client = await get_client()
    start = time.monotonic()

    if _is_streaming(body):
        return await _proxy_streaming(
            client, request.method, upstream_url, headers, body,
            "openai", source_app, start, parse_openai_sse_event,
        )
    return await _proxy_non_streaming(
        client, request.method, upstream_url, headers, body,
        "openai", source_app, start, parse_openai_response,
    )


# --- Health check ---


@app.get("/health")
async def health():
    return {"status": "ok", "service": "tokenwatch-proxy"}


# --- Shared proxy logic ---


async def _proxy_non_streaming(
    client, method, url, headers, body,
    api_type, source_app, start, parse_fn,
):
    try:
        resp = await client.request(method, url, headers=headers, content=body)
    except httpx.ConnectError:
        logger.error("Cannot connect to upstream: %s", url)
        return Response(
            content=json.dumps({"error": "TokenWatch: cannot connect to upstream"}).encode(),
            status_code=502, media_type="application/json",
        )
    except httpx.TimeoutException:
        logger.error("Upstream timeout: %s", url)
        return Response(
            content=json.dumps({"error": "TokenWatch: upstream timeout"}).encode(),
            status_code=504, media_type="application/json",
        )

    latency_ms = int((time.monotonic() - start) * 1000)
    resp_body = resp.content

    record = parse_fn(resp_body, resp.status_code, latency_ms, source_app)

    try:
        await db.log_request(record)
    except Exception:
        logger.exception("Failed to log request")

    logger.info(
        "REQ %s model=%s in=%d out=%d cost=$%.4f latency=%dms",
        api_type,
        record.model,
        record.input_tokens,
        record.output_tokens,
        record.estimated_cost or 0,
        latency_ms,
    )

    resp_headers = {
        k: v for k, v in resp.headers.items()
        if k.lower() not in HOP_BY_HOP
    }

    return Response(
        content=resp_body,
        status_code=resp.status_code,
        headers=resp_headers,
        media_type=resp.headers.get("content-type", "application/json"),
    )


async def _proxy_streaming(
    client, method, url, headers, body,
    api_type, source_app, start, parse_event_fn,
):
    try:
        req = client.build_request(method, url, headers=headers, content=body)
        resp = await client.send(req, stream=True)
    except httpx.ConnectError:
        logger.error("Cannot connect to upstream: %s", url)
        return Response(
            content=json.dumps({"error": "TokenWatch: cannot connect to upstream"}).encode(),
            status_code=502, media_type="application/json",
        )
    except httpx.TimeoutException:
        logger.error("Upstream timeout: %s", url)
        return Response(
            content=json.dumps({"error": "TokenWatch: upstream timeout"}).encode(),
            status_code=504, media_type="application/json",
        )

    record = new_streaming_record(api_type, source_app)
    record.status_code = resp.status_code

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
            # Flush remaining buffer
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
            record.estimated_cost = estimate_cost(record.model, record.input_tokens, record.output_tokens)
            try:
                await db.log_request(record)
            except Exception:
                logger.exception("Failed to log streaming request")
            logger.info(
                "STREAM %s model=%s in=%d out=%d cost=$%.4f latency=%dms",
                api_type,
                record.model,
                record.input_tokens,
                record.output_tokens,
                record.estimated_cost or 0,
                latency_ms,
            )

    resp_headers = {
        k: v for k, v in resp.headers.items()
        if k.lower() not in HOP_BY_HOP
    }

    return StreamingResponse(
        stream_generator(),
        status_code=resp.status_code,
        headers=resp_headers,
        media_type=resp.headers.get("content-type", "text/event-stream"),
    )
