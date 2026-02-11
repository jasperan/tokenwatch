"""Response interceptor for parsing usage from Anthropic and OpenAI API responses."""

import json
import logging
import time
import uuid

from .config import estimate_cost
from .models import UsageRecord

logger = logging.getLogger("tokenwatch")


def _safe_json(text: str) -> dict | None:
    try:
        return json.loads(text)
    except (json.JSONDecodeError, ValueError):
        return None


def parse_anthropic_response(body: bytes, status_code: int, latency_ms: int, source_app: str) -> UsageRecord:
    """Parse a non-streaming Anthropic response."""
    record = UsageRecord(
        request_id=str(uuid.uuid4()),
        api_type="anthropic",
        status_code=status_code,
        latency_ms=latency_ms,
        source_app=source_app,
    )
    data = _safe_json(body)
    if data:
        record.model = data.get("model", "")
        record.request_id = data.get("id", record.request_id)
        usage = data.get("usage", {})
        record.input_tokens = usage.get("input_tokens", 0)
        record.output_tokens = usage.get("output_tokens", 0)
        record.cache_creation_tokens = usage.get("cache_creation_input_tokens", 0)
        record.cache_read_tokens = usage.get("cache_read_input_tokens", 0)
    record.estimated_cost = estimate_cost(record.model, record.input_tokens, record.output_tokens)
    return record


def parse_anthropic_sse_event(event_text: str, record: UsageRecord):
    """Parse a single SSE event from an Anthropic streaming response, updating record in-place."""
    for line in event_text.split("\n"):
        if line.startswith("data: "):
            data = _safe_json(line[6:])
            if not data:
                continue
            event_type = data.get("type", "")
            if event_type == "message_start":
                msg = data.get("message", {})
                record.model = msg.get("model", record.model)
                record.request_id = msg.get("id", record.request_id)
                usage = msg.get("usage", {})
                record.input_tokens = usage.get("input_tokens", 0)
                record.cache_creation_tokens = usage.get("cache_creation_input_tokens", 0)
                record.cache_read_tokens = usage.get("cache_read_input_tokens", 0)
            elif event_type == "message_delta":
                usage = data.get("usage", {})
                record.output_tokens = usage.get("output_tokens", record.output_tokens)


def parse_openai_response(body: bytes, status_code: int, latency_ms: int, source_app: str) -> UsageRecord:
    """Parse a non-streaming OpenAI-compatible response."""
    record = UsageRecord(
        request_id=str(uuid.uuid4()),
        api_type="openai",
        status_code=status_code,
        latency_ms=latency_ms,
        source_app=source_app,
    )
    data = _safe_json(body)
    if data:
        record.model = data.get("model", "")
        record.request_id = data.get("id", record.request_id)
        usage = data.get("usage", {})
        record.input_tokens = usage.get("prompt_tokens", 0)
        record.output_tokens = usage.get("completion_tokens", 0)
    record.estimated_cost = estimate_cost(record.model, record.input_tokens, record.output_tokens)
    return record


def parse_openai_sse_event(event_text: str, record: UsageRecord):
    """Parse a single SSE event from an OpenAI-compatible streaming response."""
    for line in event_text.split("\n"):
        if line.startswith("data: "):
            payload = line[6:].strip()
            if payload == "[DONE]":
                continue
            data = _safe_json(payload)
            if not data:
                continue
            if not record.model:
                record.model = data.get("model", "")
            if not record.request_id:
                record.request_id = data.get("id", "")
            # OpenAI includes usage in the final chunk when stream_options.include_usage=true
            usage = data.get("usage")
            if usage:
                record.input_tokens = usage.get("prompt_tokens", record.input_tokens)
                record.output_tokens = usage.get("completion_tokens", record.output_tokens)


def new_streaming_record(api_type: str, source_app: str) -> UsageRecord:
    """Create a fresh UsageRecord for a streaming request."""
    return UsageRecord(
        request_id=str(uuid.uuid4()),
        api_type=api_type,
        source_app=source_app,
    )
