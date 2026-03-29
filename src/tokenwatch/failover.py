"""Upstream failover and health management."""

import asyncio
import logging

import httpx

from .config import ANTHROPIC_UPSTREAM, OPENAI_UPSTREAM
from .db import Database

logger = logging.getLogger("tokenwatch")


async def get_upstream_candidates(db: Database, api_type: str, override_url: str = "") -> list[str]:
    """Return upstream URLs ordered from best to worst candidate."""
    if override_url:
        return [override_url]

    upstreams = await db.get_upstreams(api_type)
    if not upstreams:
        return [ANTHROPIC_UPSTREAM if api_type == "anthropic" else OPENAI_UPSTREAM]

    healthy = [u.base_url for u in upstreams if u.is_healthy]
    unhealthy = [u.base_url for u in upstreams if not u.is_healthy]
    if not healthy:
        logger.warning("All %s upstreams unhealthy, trying all in priority order", api_type)

    return healthy + unhealthy


async def get_upstream_url(db: Database, api_type: str, override_url: str = "") -> str:
    """Get the best upstream URL for this request, considering health status."""
    return (await get_upstream_candidates(db, api_type, override_url))[0]


async def report_upstream_failure(db: Database, api_type: str, base_url: str):
    """Mark an upstream as unhealthy after a failure."""
    upstreams = await db.get_upstreams(api_type)
    for u in upstreams:
        if u.base_url == base_url:
            await db.mark_upstream_health(u.id, False)
            logger.warning("Marked upstream unhealthy: %s %s (fail_count=%d)", api_type, base_url, u.fail_count + 1)
            break


async def report_upstream_success(db: Database, api_type: str, base_url: str):
    """Mark an upstream as healthy after a success."""
    upstreams = await db.get_upstreams(api_type)
    for u in upstreams:
        if u.base_url == base_url and not u.is_healthy:
            await db.mark_upstream_health(u.id, True)
            logger.info("Upstream restored: %s %s", api_type, base_url)
            break


async def health_check_loop(db: Database, interval: int = 60):
    """Background task: periodically check unhealthy upstreams."""
    while True:
        await asyncio.sleep(interval)
        try:
            for api_type in ("anthropic", "openai"):
                upstreams = await db.get_upstreams(api_type)
                for u in upstreams:
                    if not u.is_healthy:
                        try:
                            async with httpx.AsyncClient(timeout=5) as client:
                                resp = await client.get(f"{u.base_url}/")
                                if resp.status_code < 500:
                                    await db.mark_upstream_health(u.id, True)
                                    logger.info("Health check passed: %s %s", api_type, u.base_url)
                        except Exception:
                            pass
        except Exception:
            logger.exception("Health check loop error")
