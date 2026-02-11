"""Tests for the proxy application."""

import pytest
from httpx import ASGITransport, AsyncClient

from tokenwatch.proxy import app, db


@pytest.fixture(autouse=True)
async def setup_db(tmp_path):
    """Use a temp database for tests."""
    db.db_path = tmp_path / "test.db"
    await db.init()
    yield
    await db.close()


@pytest.mark.asyncio
async def test_health():
    transport = ASGITransport(app=app)
    async with AsyncClient(transport=transport, base_url="http://test") as client:
        resp = await client.get("/health")
        assert resp.status_code == 200
        data = resp.json()
        assert data["status"] == "ok"
        assert data["service"] == "tokenwatch-proxy"
