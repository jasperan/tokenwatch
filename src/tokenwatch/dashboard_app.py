"""Dashboard web application for TokenWatch."""

from contextlib import asynccontextmanager
from pathlib import Path

from fastapi import FastAPI, Query
from fastapi.responses import HTMLResponse

from .db import Database


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

    return app
