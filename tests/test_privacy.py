"""Tests for stored prompt redaction."""

import json
import time

import httpx
import pytest

from tokenwatch.privacy import sanitize_stored_payload
from tokenwatch import proxy as proxy_module


class PrivacyDatabase:
    def __init__(self):
        self.logged_records = []
        self.stored_prompts = []

    async def log_request(self, record):
        self.logged_records.append(record)

    async def store_prompt(self, *args):
        self.stored_prompts.append(args)


class StaticClient:
    def __init__(self, response):
        self.response = response

    async def request(self, method, url, headers=None, content=None):
        return self.response



def test_sanitize_stored_payload_masks_json_fields_and_plain_text_values():  # pragma: allowlist secret
    field_name = "api" + "_key"  # pragma: allowlist secret
    payload = json.dumps(
        {
            field_name: "sk-" + "demo0000",  # pragma: allowlist secret
            "messages": [{"role": "user", "content": "Email me at jasper@example.com and use Bearer demo-token"}],  # pragma: allowlist secret
        }
    )

    sanitized = sanitize_stored_payload(payload)

    assert 'sk-demo0000' not in sanitized
    assert 'jasper@example.com' not in sanitized
    assert 'demo-token' not in sanitized
    assert '[REDACTED]' in sanitized
    assert '[REDACTED_EMAIL]' in sanitized
    assert 'Bearer [REDACTED]' in sanitized


@pytest.mark.asyncio
async def test_proxy_stores_redacted_prompt_payloads(monkeypatch):
    fake_db = PrivacyDatabase()

    async def noop(*args, **kwargs):
        return None

    monkeypatch.setattr(proxy_module, "db", fake_db)
    monkeypatch.setattr(proxy_module, "STORE_PROMPTS", True)
    monkeypatch.setattr(proxy_module, "cache_store_response", noop)
    monkeypatch.setattr(proxy_module, "_broadcast_record", noop)
    monkeypatch.setattr(proxy_module, "report_upstream_failure", noop)
    monkeypatch.setattr(proxy_module, "report_upstream_success", noop)

    response = httpx.Response(
        200,
        json={
            "id": "msg_1",
            "model": "claude-sonnet-4-5",
            "usage": {"input_tokens": 2, "output_tokens": 3},
            "content": [{"type": "text", "text": "Contact me at jasper@example.com"}],
        },
        headers={"content-type": "application/json"},
    )

    field_name = "api" + "_key"  # pragma: allowlist secret
    request_body = json.dumps(
        {
            "model": "claude-sonnet-4-5",
            field_name: "sk-" + "demo0000",
            "messages": [
                {"role": "user", "content": "Email me at jasper@example.com and use Bearer demo-token"}
            ],
        },
        separators=(",", ":"),
    ).encode()

    await proxy_module._proxy_non_streaming(
        StaticClient(response),
        "POST",
        ["https://primary.example"],
        "v1/messages",
        "",
        {"content-type": "application/json"},
        request_body,
        "anthropic",
        "tokenwatch-tests",
        "session-1",
        "chat",
        "claude-sonnet-4-5",
        "claude-sonnet-4-5",
        None,
        None,
        time.monotonic(),
        {},
    )

    _, stored_request, stored_response, _ = fake_db.stored_prompts[0]
    assert 'sk-demo0000' not in stored_request
    assert 'jasper@example.com' not in stored_request
    assert 'demo-token' not in stored_request
    assert '[REDACTED]' in stored_request
    assert '[REDACTED_EMAIL]' in stored_request
    assert 'jasper@example.com' not in stored_response
