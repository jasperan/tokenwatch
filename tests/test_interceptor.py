"""Tests for the response interceptor."""

import json

from tokenwatch.interceptor import (
    new_streaming_record,
    parse_anthropic_response,
    parse_anthropic_sse_event,
    parse_openai_response,
    parse_openai_sse_event,
)


def test_parse_anthropic_response():
    body = json.dumps({
        "id": "msg_123",
        "model": "claude-haiku-4-5-20251001",
        "usage": {
            "input_tokens": 100,
            "output_tokens": 50,
            "cache_creation_input_tokens": 10,
            "cache_read_input_tokens": 5,
        },
    }).encode()
    record = parse_anthropic_response(body, 200, 500, "test-agent")
    assert record.api_type == "anthropic"
    assert record.model == "claude-haiku-4-5-20251001"
    assert record.input_tokens == 100
    assert record.output_tokens == 50
    assert record.cache_creation_tokens == 10
    assert record.cache_read_tokens == 5
    assert record.request_id == "msg_123"
    assert record.latency_ms == 500
    assert record.estimated_cost is not None


def test_parse_anthropic_response_error():
    body = json.dumps({"type": "error", "error": {"message": "bad"}}).encode()
    record = parse_anthropic_response(body, 400, 100, "test")
    assert record.status_code == 400
    assert record.input_tokens == 0


def test_parse_anthropic_sse_events():
    record = new_streaming_record("anthropic", "test")

    msg_start = 'event: message_start\ndata: {"type":"message_start","message":{"id":"msg_456","model":"claude-sonnet-4-5-20250514","usage":{"input_tokens":200,"cache_creation_input_tokens":0,"cache_read_input_tokens":50}}}'
    parse_anthropic_sse_event(msg_start, record)
    assert record.model == "claude-sonnet-4-5-20250514"
    assert record.input_tokens == 200
    assert record.cache_read_tokens == 50

    msg_delta = 'event: message_delta\ndata: {"type":"message_delta","usage":{"output_tokens":75}}'
    parse_anthropic_sse_event(msg_delta, record)
    assert record.output_tokens == 75


def test_parse_openai_response():
    body = json.dumps({
        "id": "chatcmpl-abc",
        "model": "glm-4.7-flash",
        "usage": {
            "prompt_tokens": 150,
            "completion_tokens": 80,
        },
    }).encode()
    record = parse_openai_response(body, 200, 300, "curl")
    assert record.api_type == "openai"
    assert record.model == "glm-4.7-flash"
    assert record.input_tokens == 150
    assert record.output_tokens == 80
    assert record.estimated_cost is not None


def test_parse_openai_sse_events():
    record = new_streaming_record("openai", "test")

    chunk1 = 'data: {"id":"chatcmpl-x","model":"glm-4.7","choices":[{"delta":{"content":"Hi"}}]}'
    parse_openai_sse_event(chunk1, record)
    assert record.model == "glm-4.7"

    # Final chunk with usage
    chunk_final = 'data: {"id":"chatcmpl-x","model":"glm-4.7","choices":[],"usage":{"prompt_tokens":50,"completion_tokens":30}}'
    parse_openai_sse_event(chunk_final, record)
    assert record.input_tokens == 50
    assert record.output_tokens == 30

    done = "data: [DONE]"
    parse_openai_sse_event(done, record)  # Should not crash


def test_parse_invalid_json():
    body = b"not json at all"
    record = parse_anthropic_response(body, 500, 100, "test")
    assert record.input_tokens == 0
    assert record.model == ""
