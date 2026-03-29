"""Tests for cache prompt normalization and request eligibility."""

import json

from tokenwatch.cache import extract_model, hash_prompt, normalize_prompt, should_cache


def test_normalize_prompt_extracts_text_from_anthropic_payloads():
    body = json.dumps(
        {
            "system": [{"type": "text", "text": "You are terse."}],
            "messages": [
                {"role": "user", "content": [{"type": "text", "text": "Hello"}]},
                {"role": "assistant", "content": "Hi there"},
            ],
        }
    ).encode()

    normalized = normalize_prompt(body, "anthropic")

    assert normalized == "you are terse. hello hi there"
    assert hash_prompt(normalized)



def test_normalize_prompt_extracts_text_from_openai_payloads():
    body = json.dumps(
        {
            "messages": [
                {"role": "system", "content": "You are terse."},
                {"role": "user", "content": "Hello"},
            ]
        }
    ).encode()

    assert normalize_prompt(body, "openai") == "you are terse. hello"



def test_should_cache_only_allows_zero_temperature_requests():
    assert should_cache(b'{"temperature": 0}') is True
    assert should_cache(b'{"temperature": null}') is True
    assert should_cache(b'{"temperature": 0.7}') is False
    assert should_cache(b"not-json") is False



def test_extract_model_returns_empty_string_for_invalid_json():
    assert extract_model(b'{"model": "claude-sonnet-4-5"}') == "claude-sonnet-4-5"
    assert extract_model(b"not-json") == ""
