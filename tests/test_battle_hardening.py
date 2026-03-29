"""Battle-hardening tests for proxy failure modes."""

import time

import httpx
import pytest

from tokenwatch.failover import get_upstream_candidates
from tokenwatch.models import Upstream
from tokenwatch import proxy as proxy_module


class StubFailoverDatabase:
    def __init__(self, upstreams):
        self._upstreams = upstreams

    async def get_upstreams(self, api_type=None):
        if api_type is None:
            return self._upstreams
        return [upstream for upstream in self._upstreams if upstream.api_type == api_type]


class FakeProxyDatabase:
    def __init__(self):
        self.logged_records = []
        self.stored_prompts = []

    async def log_request(self, record):
        self.logged_records.append(record)

    async def store_prompt(self, *args):
        self.stored_prompts.append(args)


class FailingThenWorkingClient:
    def __init__(self, response):
        self.urls = []
        self.response = response

    async def request(self, method, url, headers=None, content=None):
        self.urls.append(url)
        if len(self.urls) == 1:
            raise httpx.ConnectError("boom")
        return self.response


class TimeoutClient:
    def __init__(self):
        self.urls = []

    async def request(self, method, url, headers=None, content=None):
        self.urls.append(url)
        raise httpx.TimeoutException("slow upstream")


class FakeStreamResponse:
    def __init__(self, chunks, status_code=200, headers=None):
        self._chunks = chunks
        self.status_code = status_code
        self.headers = headers or {"content-type": "text/event-stream"}
        self.closed = False

    async def aiter_bytes(self):
        for chunk in self._chunks:
            yield chunk

    async def aclose(self):
        self.closed = True


class StreamingClient:
    def __init__(self, responses):
        self.responses = list(responses)
        self.urls = []

    def build_request(self, method, url, headers=None, content=None):
        self.urls.append(url)
        return {"method": method, "url": url, "headers": headers, "content": content}

    async def send(self, request, stream=False):
        response = self.responses.pop(0)
        if isinstance(response, Exception):
            raise response
        return response


@pytest.mark.asyncio
async def test_get_upstream_candidates_tries_healthy_then_unhealthy():
    db = StubFailoverDatabase(
        [
            Upstream(id=1, api_type="openai", base_url="https://healthy.example", priority=10, is_healthy=True),
            Upstream(id=2, api_type="openai", base_url="https://recovering.example", priority=20, is_healthy=False),
        ]
    )

    candidates = await get_upstream_candidates(db, "openai")

    assert candidates == ["https://healthy.example", "https://recovering.example"]


@pytest.mark.asyncio
async def test_non_streaming_proxy_fails_over_after_connect_error(monkeypatch):
    fake_db = FakeProxyDatabase()
    failures = []
    successes = []
    broadcasts = []

    async def cache_store_response(*args, **kwargs):
        return None

    async def broadcast(record):
        broadcasts.append(record)

    monkeypatch.setattr(proxy_module, "db", fake_db)
    monkeypatch.setattr(proxy_module, "cache_store_response", cache_store_response)
    monkeypatch.setattr(proxy_module, "_broadcast_record", broadcast)

    async def record_failure(db, api_type, base_url):
        failures.append((api_type, base_url))

    async def record_success(db, api_type, base_url):
        successes.append((api_type, base_url))

    monkeypatch.setattr(proxy_module, "report_upstream_failure", record_failure)
    monkeypatch.setattr(proxy_module, "report_upstream_success", record_success)

    response = httpx.Response(
        200,
        json={
            "id": "chatcmpl-1",
            "model": "glm-4.7",
            "usage": {"prompt_tokens": 3, "completion_tokens": 5},
        },
        headers={"content-type": "application/json"},
    )
    client = FailingThenWorkingClient(response)

    result = await proxy_module._proxy_non_streaming(
        client,
        "POST",
        ["https://primary.example", "https://secondary.example"],
        "v1/chat/completions",
        "",
        {"content-type": "application/json"},
        b'{"model":"glm-4.7"}',
        "openai",
        "tokenwatch-tests",
        "session-1",
        "chat",
        "glm-4.7",
        "glm-4.7",
        None,
        None,
        time.monotonic(),
        {},
    )

    assert result.status_code == 200
    assert client.urls == [
        "https://primary.example/v1/chat/completions",
        "https://secondary.example/v1/chat/completions",
    ]
    assert failures == [("openai", "https://primary.example")]
    assert successes == [("openai", "https://secondary.example")]
    assert fake_db.logged_records[0].input_tokens == 3
    assert fake_db.logged_records[0].output_tokens == 5
    assert broadcasts and broadcasts[0].request_id == "chatcmpl-1"


@pytest.mark.asyncio
async def test_non_streaming_proxy_returns_last_timeout_after_all_candidates_fail(monkeypatch):
    fake_db = FakeProxyDatabase()
    failures = []

    async def cache_store_response(*args, **kwargs):
        return None

    async def broadcast(record):
        return None

    monkeypatch.setattr(proxy_module, "db", fake_db)
    monkeypatch.setattr(proxy_module, "cache_store_response", cache_store_response)
    monkeypatch.setattr(proxy_module, "_broadcast_record", broadcast)

    async def record_failure(db, api_type, base_url):
        failures.append((api_type, base_url))

    async def record_success(db, api_type, base_url):
        raise AssertionError("success should not be recorded")

    monkeypatch.setattr(proxy_module, "report_upstream_failure", record_failure)
    monkeypatch.setattr(proxy_module, "report_upstream_success", record_success)

    result = await proxy_module._proxy_non_streaming(
        TimeoutClient(),
        "POST",
        ["https://primary.example", "https://secondary.example"],
        "v1/chat/completions",
        "",
        {"content-type": "application/json"},
        b"not-json",
        "openai",
        "tokenwatch-tests",
        "session-1",
        "chat",
        "glm-4.7",
        "glm-4.7",
        None,
        None,
        time.monotonic(),
        {},
    )

    assert result.status_code == 504
    assert failures == [
        ("openai", "https://primary.example"),
        ("openai", "https://secondary.example"),
    ]
    assert fake_db.logged_records == []


@pytest.mark.asyncio
async def test_streaming_proxy_tolerates_garbage_events_and_logs_usage(monkeypatch):
    fake_db = FakeProxyDatabase()
    failures = []
    successes = []
    broadcasts = []

    monkeypatch.setattr(proxy_module, "db", fake_db)

    async def record_failure(db, api_type, base_url):
        failures.append((api_type, base_url))

    async def record_success(db, api_type, base_url):
        successes.append((api_type, base_url))

    async def broadcast(record):
        broadcasts.append(record)

    monkeypatch.setattr(proxy_module, "report_upstream_failure", record_failure)
    monkeypatch.setattr(proxy_module, "report_upstream_success", record_success)
    monkeypatch.setattr(proxy_module, "_broadcast_record", broadcast)

    client = StreamingClient(
        [
            httpx.ConnectError("boom"),
            FakeStreamResponse(
                [
                    b"data: not-json\n\n",
                    b'data: {"id":"chatcmpl-1","model":"glm-4.7","usage":{"prompt_tokens":7,"completion_tokens":11}}\n\n',
                    b"data: [DONE]\n\n",
                ]
            ),
        ]
    )

    response = await proxy_module._proxy_streaming(
        client,
        "POST",
        ["https://primary.example", "https://secondary.example"],
        "v1/chat/completions",
        "",
        {"content-type": "application/json"},
        b'{"model":"glm-4.7","stream":true}',
        "openai",
        "tokenwatch-tests",
        "session-1",
        "chat",
        "glm-4.7",
        "glm-4.7",
        None,
        None,
        time.monotonic(),
        {},
    )

    chunks = [chunk async for chunk in response.body_iterator]

    assert response.status_code == 200
    assert b"data: not-json\n\n" in chunks
    assert failures == [("openai", "https://primary.example")]
    assert successes == [("openai", "https://secondary.example")]
    assert fake_db.logged_records[0].input_tokens == 7
    assert fake_db.logged_records[0].output_tokens == 11
    assert broadcasts and broadcasts[0].request_id == "chatcmpl-1"


@pytest.mark.asyncio
async def test_non_streaming_proxy_survives_empty_upstream_response(monkeypatch):
    fake_db = FakeProxyDatabase()

    async def noop(*args, **kwargs):
        return None

    monkeypatch.setattr(proxy_module, "db", fake_db)
    monkeypatch.setattr(proxy_module, "cache_store_response", noop)
    monkeypatch.setattr(proxy_module, "_broadcast_record", noop)
    monkeypatch.setattr(proxy_module, "report_upstream_failure", noop)
    monkeypatch.setattr(proxy_module, "report_upstream_success", noop)

    client = FailingThenWorkingClient(
        httpx.Response(200, content=b"", headers={"content-type": "application/json"})
    )

    response = await proxy_module._proxy_non_streaming(
        client,
        "POST",
        ["https://primary.example", "https://secondary.example"],
        "v1/messages",
        "",
        {"content-type": "application/json"},
        b"not-json",
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

    assert response.status_code == 200
    assert response.body == b""
    assert fake_db.logged_records[0].input_tokens == 0
    assert fake_db.logged_records[0].output_tokens == 0
