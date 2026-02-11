"""Async SQLite database layer for TokenWatch."""

import aiosqlite

from .config import DB_PATH
from .models import UsageRecord, UsageStats

SCHEMA = """
CREATE TABLE IF NOT EXISTS requests (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    request_id TEXT,
    api_type TEXT NOT NULL,
    model TEXT NOT NULL DEFAULT '',
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    latency_ms INTEGER NOT NULL DEFAULT 0,
    status_code INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    source_app TEXT NOT NULL DEFAULT '',
    estimated_cost REAL
);

CREATE INDEX IF NOT EXISTS idx_model_created ON requests(model, created_at);
CREATE INDEX IF NOT EXISTS idx_api_type_created ON requests(api_type, created_at);
"""


class Database:
    def __init__(self, db_path=None):
        self.db_path = db_path or DB_PATH
        self._db: aiosqlite.Connection | None = None

    async def init(self):
        self.db_path.parent.mkdir(parents=True, exist_ok=True)
        self._db = await aiosqlite.connect(str(self.db_path))
        self._db.row_factory = aiosqlite.Row
        await self._db.executescript(SCHEMA)
        await self._db.commit()

    async def close(self):
        if self._db:
            await self._db.close()
            self._db = None

    async def log_request(self, record: UsageRecord):
        await self._db.execute(
            """INSERT INTO requests
               (request_id, api_type, model, input_tokens, output_tokens,
                cache_creation_tokens, cache_read_tokens, latency_ms,
                status_code, created_at, source_app, estimated_cost)
               VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
            (
                record.request_id,
                record.api_type,
                record.model,
                record.input_tokens,
                record.output_tokens,
                record.cache_creation_tokens,
                record.cache_read_tokens,
                record.latency_ms,
                record.status_code,
                record.timestamp.isoformat(),
                record.source_app,
                record.estimated_cost,
            ),
        )
        await self._db.commit()

    async def get_recent(self, limit: int = 50) -> list[dict]:
        cursor = await self._db.execute(
            """SELECT * FROM requests ORDER BY created_at DESC LIMIT ?""",
            (limit,),
        )
        rows = await cursor.fetchall()
        return [dict(r) for r in rows]

    async def get_stats(self, timeframe: str = "24h") -> UsageStats:
        tf_map = {"1h": "1 hour", "24h": "1 day", "7d": "7 days", "30d": "30 days", "all": "100 years"}
        interval = tf_map.get(timeframe, "1 day")

        cursor = await self._db.execute(
            f"""SELECT
                COUNT(*) as total_requests,
                COALESCE(SUM(input_tokens), 0) as total_input_tokens,
                COALESCE(SUM(output_tokens), 0) as total_output_tokens,
                COALESCE(SUM(cache_creation_tokens), 0) as total_cache_creation_tokens,
                COALESCE(SUM(cache_read_tokens), 0) as total_cache_read_tokens,
                COALESCE(SUM(estimated_cost), 0.0) as total_estimated_cost
            FROM requests
            WHERE created_at >= datetime('now', '-{interval}')""",
        )
        row = await cursor.fetchone()

        # Per-model breakdown
        model_cursor = await self._db.execute(
            f"""SELECT model,
                COUNT(*) as requests,
                COALESCE(SUM(input_tokens), 0) as input_tokens,
                COALESCE(SUM(output_tokens), 0) as output_tokens,
                COALESCE(SUM(estimated_cost), 0.0) as cost
            FROM requests
            WHERE created_at >= datetime('now', '-{interval}')
            GROUP BY model ORDER BY cost DESC""",
        )
        model_rows = await model_cursor.fetchall()
        models = {
            r["model"]: {
                "requests": r["requests"],
                "input_tokens": r["input_tokens"],
                "output_tokens": r["output_tokens"],
                "cost": r["cost"],
            }
            for r in model_rows
        }

        return UsageStats(
            total_requests=row["total_requests"],
            total_input_tokens=row["total_input_tokens"],
            total_output_tokens=row["total_output_tokens"],
            total_cache_creation_tokens=row["total_cache_creation_tokens"],
            total_cache_read_tokens=row["total_cache_read_tokens"],
            total_estimated_cost=row["total_estimated_cost"],
            models=models,
        )

    async def get_timeseries(self, timeframe: str = "24h", buckets: int = 30) -> list[dict]:
        tf_map = {"1h": "1 hour", "24h": "1 day", "7d": "7 days", "30d": "30 days", "all": "100 years"}
        interval = tf_map.get(timeframe, "1 day")

        cursor = await self._db.execute(
            f"""SELECT
                strftime('%Y-%m-%dT%H:%M', created_at) as bucket,
                SUM(input_tokens) as input_tokens,
                SUM(output_tokens) as output_tokens,
                COUNT(*) as requests
            FROM requests
            WHERE created_at >= datetime('now', '-{interval}')
            GROUP BY bucket ORDER BY bucket""",
        )
        rows = await cursor.fetchall()
        return [dict(r) for r in rows]

    async def reset(self):
        await self._db.execute("DELETE FROM requests")
        await self._db.commit()
