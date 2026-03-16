"""Dashboard web application for TokenWatch."""

from contextlib import asynccontextmanager
from pathlib import Path

from fastapi import FastAPI, Query
from fastapi.responses import HTMLResponse
from pydantic import BaseModel

from .db import Database


class ABFeedback(BaseModel):
    request_id: str
    rating: int
    tags: list[str] = []


def create_dashboard_app() -> FastAPI:
    db = Database()

    @asynccontextmanager
    async def lifespan(app):
        await db.init()
        yield
        await db.close()

    app = FastAPI(title="TokenWatch Dashboard", lifespan=lifespan)

    @app.get("/", response_class=HTMLResponse)
    async def index():
        html_path = Path(__file__).parent / "dashboard" / "index.html"
        return html_path.read_text()

    # --- Existing endpoints (enhanced) ---

    @app.get("/api/stats")
    async def api_stats(timeframe: str = Query("24h")):
        stats = await db.get_stats(timeframe)
        return stats.model_dump()

    @app.get("/api/recent")
    async def api_recent(limit: int = Query(50, ge=1, le=500)):
        return await db.get_recent(limit)

    @app.get("/api/timeseries")
    async def api_timeseries(timeframe: str = Query("24h")):
        return await db.get_timeseries(timeframe)

    # --- Cost attribution ---

    @app.get("/api/cost/by-tag")
    async def api_cost_by_tag(timeframe: str = Query("24h")):
        return await db.cost_by_tag(timeframe)

    @app.get("/api/cost/by-app")
    async def api_cost_by_app(timeframe: str = Query("24h")):
        return await db.cost_by_app(timeframe)

    @app.get("/api/cost/by-session")
    async def api_cost_by_session(top: int = Query(20, ge=1, le=100)):
        return await db.cost_by_session(top)

    @app.get("/api/cost/forecast")
    async def api_cost_forecast():
        return await db.cost_forecast()

    # --- Routing ---

    @app.get("/api/routing/stats")
    async def api_routing_stats():
        return await db.routing_stats()

    # --- Cache ---

    @app.get("/api/cache/stats")
    async def api_cache_stats():
        return await db.cache_stats()

    # --- Budget ---

    @app.get("/api/budget/status")
    async def api_budget_status():
        return await db.get_budget_status()

    # --- A/B Tests ---

    @app.get("/api/ab/list")
    async def api_ab_list():
        tests = await db.get_active_ab_tests()
        return [t.model_dump() for t in tests]

    @app.get("/api/ab/report/{test_name}")
    async def api_ab_report(test_name: str):
        return await db.get_ab_report(test_name)

    @app.post("/api/ab/feedback")
    async def api_ab_feedback(feedback: ABFeedback):
        # Store feedback (could extend DB schema, for now just log)
        import logging
        logger = logging.getLogger("tokenwatch")
        logger.info("A/B feedback: request=%s rating=%d tags=%s", feedback.request_id, feedback.rating, feedback.tags)
        return {"status": "received"}

    # --- Upstreams ---

    @app.get("/api/upstreams")
    async def api_upstreams():
        upstreams = await db.get_upstreams()
        return [u.model_dump() for u in upstreams]

    return app
