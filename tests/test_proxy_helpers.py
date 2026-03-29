"""Tests for proxy helper functions that do not require a live upstream or database."""

import json

from tokenwatch.proxy import (
    _extract_request_info,
    _forward_headers,
    _is_streaming,
    _rewrite_model_in_body,
)


def test_forward_headers_filters_hop_by_hop_headers():
    headers = {
        "content-type": "application/json",
        "connection": "keep-alive",
        "host": "localhost:8877",
        "x-api-key": "secret",
    }

    forwarded = _forward_headers(headers)

    assert forwarded == {
        "content-type": "application/json",
        "x-api-key": "secret",
    }


def test_extract_request_info_handles_text_blocks():
    body = json.dumps(
        {
            "model": "claude-sonnet-4-5",
            "messages": [
                {
                    "role": "user",
                    "content": [
                        {"type": "image", "source": {"type": "base64", "data": "..."}},
                        {"type": "text", "text": "hello from tokenwatch"},
                    ],
                }
            ],
        }
    ).encode()

    info = _extract_request_info(body)

    assert info["model"] == "claude-sonnet-4-5"
    assert info["first_message"] == "hello from tokenwatch"
    assert info["est_tokens"] > 0


def test_rewrite_model_in_body_only_updates_valid_json_payloads():
    valid = _rewrite_model_in_body(b'{"model": "claude-sonnet-4-5"}', "claude-haiku-4-5")
    invalid = _rewrite_model_in_body(b"not-json", "claude-haiku-4-5")

    assert json.loads(valid)["model"] == "claude-haiku-4-5"
    assert invalid == b"not-json"


def test_is_streaming_detects_valid_and_invalid_payloads():
    assert _is_streaming(b'{"stream": true}') is True
    assert _is_streaming(b'{"stream": false}') is False
    assert _is_streaming(b"not-json") is False
