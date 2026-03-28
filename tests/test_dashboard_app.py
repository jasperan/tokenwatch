"""Tests for the dashboard API surface."""

import pytest
from httpx import ASGITransport, AsyncClient

from tokenwatch.models import ABTest, UsageStats


class FakeDashboardDatabase:
    async def init(self):
        return None

    async def close(self):
        return None

    async def get_stats(self, timeframe):
        return UsageStats(
            total_requests=2,
            total_input_tokens=120,
            total_output_tokens=80,
            total_estimated_cost=1.25,
            models={
                "claude-sonnet-4-5": {
                    "requests": 2,
                    "input_tokens": 120,
                    "output_tokens": 80,
                    "cost": 1.25,
                }
            },
        )

    async def get_recent(self, limit):
        return [{"request_id": "req-1", "model_used": "claude-sonnet-4-5"}]

    async def get_timeseries(self, timeframe):
        return [{"bucket": "2026-03-28 10:00", "requests": 1, "cost": 0.5}]

    async def cost_by_tag(self, timeframe):
        return [{"tag": "chat", "requests": 1, "total_cost": 0.5, "avg_cost": 0.5}]

    async def cost_by_app(self, timeframe):
        return [{"app": "tokenwatch-tests", "requests": 1, "total_cost": 0.5, "avg_cost": 0.5}]

    async def cost_by_session(self, top):
        return []

    async def cost_forecast(self):
        return {"daily_avg": 0.5, "monthly_projection": 15.0, "data_points": 7}

    async def routing_stats(self):
        return [{"rule_name": "small-prompts", "total_requests": 1}]

    async def cache_stats(self):
        return {"entries": 1, "active_entries": 1, "total_hits": 2}

    async def get_budget_status(self):
        return [{"id": 1, "scope": "global"}]

    async def get_active_ab_tests(self):
        return [ABTest(id=3, test_name="latency", model_a="claude-haiku-4-5", model_b="claude-sonnet-4-5")]

    async def get_ab_report(self, test_name):
        return {"test_name": test_name, "variants": []}

    async def get_upstreams(self, api_type=None):
        return []


@pytest.mark.asyncio
async def test_dashboard_endpoints_return_expected_shapes(monkeypatch):
    from tokenwatch import dashboard_app as dashboard_module

    monkeypatch.setattr(dashboard_module, "Database", FakeDashboardDatabase)
    app = dashboard_module.create_dashboard_app()

    transport = ASGITransport(app=app)
    async with AsyncClient(transport=transport, base_url="http://test") as client:
        stats = await client.get("/api/stats?timeframe=24h")
        recent = await client.get("/api/recent?limit=1")
        cache = await client.get("/api/cache/stats")
        ab_tests = await client.get("/api/ab/list")

    assert stats.status_code == 200
    assert stats.json()["total_requests"] == 2

    assert recent.status_code == 200
    assert recent.json() == [{"request_id": "req-1", "model_used": "claude-sonnet-4-5"}]

    assert cache.status_code == 200
    assert cache.json() == {"entries": 1, "active_entries": 1, "total_hits": 2}

    assert ab_tests.status_code == 200
    assert ab_tests.json()[0]["test_name"] == "latency"
