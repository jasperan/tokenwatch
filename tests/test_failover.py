"""Tests for upstream selection and health tracking."""

import pytest

from tokenwatch.failover import (
    get_upstream_url,
    report_upstream_failure,
    report_upstream_success,
)
from tokenwatch.models import Upstream


class StubFailoverDatabase:
    def __init__(self, upstreams):
        self._upstreams = upstreams
        self.marked = []

    async def get_upstreams(self, api_type=None):
        if api_type is None:
            return self._upstreams
        return [upstream for upstream in self._upstreams if upstream.api_type == api_type]

    async def mark_upstream_health(self, upstream_id, healthy):
        self.marked.append((upstream_id, healthy))


@pytest.mark.asyncio
async def test_get_upstream_url_uses_override_before_database_lookup():
    db = StubFailoverDatabase([])

    url = await get_upstream_url(db, "anthropic", override_url="https://override.example")

    assert url == "https://override.example"


@pytest.mark.asyncio
async def test_get_upstream_url_prefers_first_healthy_upstream():
    db = StubFailoverDatabase(
        [
            Upstream(id=1, api_type="anthropic", base_url="https://down.example", priority=10, is_healthy=False),
            Upstream(id=2, api_type="anthropic", base_url="https://healthy.example", priority=20, is_healthy=True),
        ]
    )

    url = await get_upstream_url(db, "anthropic")

    assert url == "https://healthy.example"


@pytest.mark.asyncio
async def test_get_upstream_url_falls_back_to_first_when_all_are_unhealthy():
    db = StubFailoverDatabase(
        [
            Upstream(id=1, api_type="openai", base_url="https://primary.example", priority=10, is_healthy=False),
            Upstream(id=2, api_type="openai", base_url="https://secondary.example", priority=20, is_healthy=False),
        ]
    )

    url = await get_upstream_url(db, "openai")

    assert url == "https://primary.example"


@pytest.mark.asyncio
async def test_report_upstream_failure_marks_matching_upstream_unhealthy():
    db = StubFailoverDatabase(
        [Upstream(id=9, api_type="anthropic", base_url="https://healthy.example", priority=10, is_healthy=True)]
    )

    await report_upstream_failure(db, "anthropic", "https://healthy.example")

    assert db.marked == [(9, False)]


@pytest.mark.asyncio
async def test_report_upstream_success_only_marks_previously_unhealthy_upstream():
    db = StubFailoverDatabase(
        [
            Upstream(id=3, api_type="anthropic", base_url="https://healthy.example", priority=10, is_healthy=True),
            Upstream(id=4, api_type="anthropic", base_url="https://recovering.example", priority=20, is_healthy=False),
        ]
    )

    await report_upstream_success(db, "anthropic", "https://healthy.example")
    await report_upstream_success(db, "anthropic", "https://recovering.example")

    assert db.marked == [(4, True)]
