"""Tests for the proxy application."""

import pytest
from httpx import ASGITransport, AsyncClient

from tokenwatch.proxy import app


@pytest.mark.asyncio
async def test_health():
    transport = ASGITransport(app=app)
    async with AsyncClient(transport=transport, base_url="http://test") as client:
        resp = await client.get("/health")

    assert resp.status_code == 200
    assert resp.json() == {"status": "ok", "service": "tokenwatch-proxy"}
