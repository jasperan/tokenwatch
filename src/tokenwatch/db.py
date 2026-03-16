"""Async Oracle database layer for TokenWatch."""

import json
import logging
from pathlib import Path

import oracledb

from .config import ORACLE_DSN, ORACLE_PASSWORD, ORACLE_USER
from .models import (
    ABTest,
    BudgetRecord,
    RoutingRule,
    Upstream,
    UsageRecord,
    UsageStats,
)

logger = logging.getLogger(__name__)

SCHEMA_PATH = Path(__file__).parent / "schema.sql"

# ORA codes to ignore during schema bootstrap
_ORA_TABLE_EXISTS = 955
_ORA_INDEX_EXISTS = 1408
_ORA_PARTITION_EXISTS = 14312


def _timeframe_where(timeframe: str) -> str:
    """Return a SQL WHERE fragment filtering created_at by timeframe."""
    mapping = {
        "1h": "created_at >= SYSTIMESTAMP - INTERVAL '1' HOUR",
        "24h": "created_at >= SYSTIMESTAMP - INTERVAL '1' DAY",
        "7d": "created_at >= SYSTIMESTAMP - INTERVAL '7' DAY",
        "30d": "created_at >= SYSTIMESTAMP - INTERVAL '30' DAY",
        "all": "1=1",
    }
    return mapping.get(timeframe, mapping["24h"])


def _bucket_format(timeframe: str) -> str:
    """Return Oracle TO_CHAR format for time-series bucketing."""
    mapping = {
        "1h": "YYYY-MM-DD HH24:MI",
        "24h": "YYYY-MM-DD HH24:MI",
        "7d": "YYYY-MM-DD HH24",
        "30d": "YYYY-MM-DD",
        "all": "YYYY-MM-DD",
    }
    return mapping.get(timeframe, mapping["24h"])


class Database:
    """Oracle async database interface for TokenWatch."""

    def __init__(self, dsn=None, user=None, password=None):
        self._dsn = dsn or ORACLE_DSN
        self._user = user or ORACLE_USER
        self._password = password or ORACLE_PASSWORD
        self._pool: oracledb.AsyncConnectionPool | None = None

    async def init(self):
        """Create async connection pool and ensure schema exists."""
        self._pool = oracledb.create_pool_async(
            dsn=self._dsn,
            user=self._user,
            password=self._password,
            min=2,
            max=10,
        )
        await self._ensure_schema()

    async def _ensure_schema(self):
        """Read schema.sql and execute each statement, ignoring already-exists errors."""
        sql_text = SCHEMA_PATH.read_text()
        statements = [s.strip() for s in sql_text.split("\n\n") if s.strip()]

        async with self._pool.acquire() as conn:
            for stmt in statements:
                try:
                    await conn.execute(stmt)
                except oracledb.DatabaseError as exc:
                    err = exc.args[0]
                    if err.code in (_ORA_TABLE_EXISTS, _ORA_INDEX_EXISTS, _ORA_PARTITION_EXISTS):
                        logger.debug("Schema object already exists (ORA-%05d), skipping", err.code)
                    else:
                        raise
            await conn.commit()

    async def close(self):
        """Close the connection pool."""
        if self._pool:
            await self._pool.close()
            self._pool = None

    # ------------------------------------------------------------------ #
    # Request operations
    # ------------------------------------------------------------------ #

    async def log_request(self, record: UsageRecord):
        """Insert a usage record into the requests table."""
        sql = """INSERT INTO requests (
            request_id, api_type, model_requested, model_used,
            input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens,
            latency_ms, status_code, source_app, session_id, feature_tag,
            estimated_cost, cache_hit, ab_test_id, routing_rule_id
        ) VALUES (
            :1, :2, :3, :4, :5, :6, :7, :8, :9, :10, :11, :12, :13, :14, :15, :16, :17
        )"""
        async with self._pool.acquire() as conn:
            await conn.execute(sql, [
                record.request_id,
                record.api_type,
                record.model_requested,
                record.model,
                record.input_tokens,
                record.output_tokens,
                record.cache_creation_tokens,
                record.cache_read_tokens,
                record.latency_ms,
                record.status_code,
                record.source_app,
                record.session_id,
                record.feature_tag,
                record.estimated_cost,
                1 if record.cache_hit else 0,
                record.ab_test_id,
                record.routing_rule_id,
            ])
            await conn.commit()

    async def get_recent(self, limit: int = 50) -> list[dict]:
        """Return the most recent requests."""
        sql = """SELECT id, request_id, api_type, model_requested, model_used,
                    input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens,
                    latency_ms, status_code, source_app, session_id, feature_tag,
                    estimated_cost, cache_hit, ab_test_id, routing_rule_id,
                    TO_CHAR(created_at, 'YYYY-MM-DD"T"HH24:MI:SS') AS created_at
                 FROM requests ORDER BY created_at DESC
                 FETCH FIRST :1 ROWS ONLY"""
        async with self._pool.acquire() as conn:
            cursor = await conn.execute(sql, [limit])
            columns = [col[0].lower() for col in cursor.description]
            rows = await cursor.fetchall()
            return [dict(zip(columns, row)) for row in rows]

    async def get_stats(self, timeframe: str = "24h") -> UsageStats:
        """Return aggregated usage statistics for the given timeframe."""
        where = _timeframe_where(timeframe)

        agg_sql = f"""SELECT
            COUNT(*) AS total_requests,
            NVL(SUM(input_tokens), 0) AS total_input_tokens,
            NVL(SUM(output_tokens), 0) AS total_output_tokens,
            NVL(SUM(cache_creation_tokens), 0) AS total_cache_creation_tokens,
            NVL(SUM(cache_read_tokens), 0) AS total_cache_read_tokens,
            NVL(SUM(estimated_cost), 0) AS total_estimated_cost,
            NVL(SUM(cache_hit), 0) AS total_cache_hits,
            NVL(SUM(CASE WHEN cache_hit = 1 THEN estimated_cost ELSE 0 END), 0) AS total_cache_savings
        FROM requests WHERE {where}"""

        model_sql = f"""SELECT model_used,
            COUNT(*) AS requests,
            NVL(SUM(input_tokens), 0) AS input_tokens,
            NVL(SUM(output_tokens), 0) AS output_tokens,
            NVL(SUM(estimated_cost), 0) AS cost
        FROM requests WHERE {where}
        GROUP BY model_used ORDER BY cost DESC"""

        async with self._pool.acquire() as conn:
            cursor = await conn.execute(agg_sql)
            row = await cursor.fetchone()

            model_cursor = await conn.execute(model_sql)
            model_rows = await model_cursor.fetchall()

        models = {}
        for r in model_rows:
            models[r[0] or ""] = {
                "requests": r[1],
                "input_tokens": r[2],
                "output_tokens": r[3],
                "cost": r[4],
            }

        return UsageStats(
            total_requests=row[0],
            total_input_tokens=row[1],
            total_output_tokens=row[2],
            total_cache_creation_tokens=row[3],
            total_cache_read_tokens=row[4],
            total_estimated_cost=float(row[5]),
            total_cache_hits=row[6],
            total_cache_savings=float(row[7]),
            models=models,
        )

    async def get_timeseries(self, timeframe: str = "24h") -> list[dict]:
        """Return time-bucketed usage data for charting."""
        where = _timeframe_where(timeframe)
        fmt = _bucket_format(timeframe)

        sql = f"""SELECT
            TO_CHAR(created_at, '{fmt}') AS bucket,
            SUM(input_tokens) AS input_tokens,
            SUM(output_tokens) AS output_tokens,
            COUNT(*) AS requests,
            NVL(SUM(estimated_cost), 0) AS cost,
            NVL(SUM(cache_hit), 0) AS cache_hits
        FROM requests WHERE {where}
        GROUP BY TO_CHAR(created_at, '{fmt}')
        ORDER BY bucket"""

        async with self._pool.acquire() as conn:
            cursor = await conn.execute(sql)
            columns = [col[0].lower() for col in cursor.description]
            rows = await cursor.fetchall()
            return [dict(zip(columns, row)) for row in rows]

    async def reset(self):
        """Truncate the requests table."""
        async with self._pool.acquire() as conn:
            await conn.execute("TRUNCATE TABLE requests")
            await conn.commit()

    # ------------------------------------------------------------------ #
    # Budget operations
    # ------------------------------------------------------------------ #

    async def get_budgets(self) -> list[BudgetRecord]:
        """Return all budget records."""
        sql = "SELECT id, scope, scope_value, limit_amount, period, action_on_limit, webhook_url, is_active FROM budgets ORDER BY id"
        async with self._pool.acquire() as conn:
            cursor = await conn.execute(sql)
            rows = await cursor.fetchall()
            return [
                BudgetRecord(
                    id=r[0], scope=r[1], scope_value=r[2] or "",
                    limit_amount=float(r[3]), period=r[4],
                    action_on_limit=r[5] or "block",
                    webhook_url=r[6] or "", is_active=bool(r[7]),
                )
                for r in rows
            ]

    async def add_budget(self, budget: BudgetRecord) -> int:
        """Insert a budget and return its id."""
        sql = """INSERT INTO budgets (scope, scope_value, limit_amount, period, action_on_limit, webhook_url, is_active)
                 VALUES (:1, :2, :3, :4, :5, :6, :7) RETURNING id INTO :out_id"""
        async with self._pool.acquire() as conn:
            out_id = conn.var(oracledb.NUMBER)
            await conn.execute(sql, [
                budget.scope, budget.scope_value, budget.limit_amount,
                budget.period, budget.action_on_limit, budget.webhook_url,
                1 if budget.is_active else 0, out_id,
            ])
            await conn.commit()
            return int(out_id.getvalue()[0])

    async def remove_budget(self, budget_id: int):
        """Delete a budget by id."""
        async with self._pool.acquire() as conn:
            await conn.execute("DELETE FROM budgets WHERE id = :1", [budget_id])
            await conn.commit()

    async def check_budget(self, source_app: str, model: str, feature_tag: str) -> dict:
        """Check all active budgets and return allow/block decision."""
        budgets = await self.get_budgets()
        warnings = []
        blocking_budget = None

        for b in budgets:
            if not b.is_active:
                continue
            spend = await self._get_period_spend(b)
            if spend >= b.limit_amount:
                if b.action_on_limit == "block":
                    blocking_budget = b
                    break
                warnings.append({"budget_id": b.id, "spend": spend, "limit": b.limit_amount})
            elif spend >= b.limit_amount * 0.8:
                warnings.append({"budget_id": b.id, "spend": spend, "limit": b.limit_amount})

        return {
            "allowed": blocking_budget is None,
            "warnings": warnings,
            "blocking_budget": blocking_budget.model_dump() if blocking_budget else None,
        }

    async def _get_period_spend(self, budget: BudgetRecord) -> float:
        """Sum estimated_cost for the budget's period and scope."""
        period_map = {
            "hourly": "TRUNC(SYSTIMESTAMP, 'HH')",
            "daily": "TRUNC(SYSTIMESTAMP, 'DD')",
            "monthly": "TRUNC(SYSTIMESTAMP, 'MM')",
        }
        period_start = period_map.get(budget.period, "TRUNC(SYSTIMESTAMP, 'DD')")

        scope_where = "1=1"
        params = []
        if budget.scope == "app":
            scope_where = "source_app = :1"
            params.append(budget.scope_value)
        elif budget.scope == "model":
            scope_where = "model_used = :1"
            params.append(budget.scope_value)
        elif budget.scope == "tag":
            scope_where = "feature_tag = :1"
            params.append(budget.scope_value)

        sql = f"""SELECT NVL(SUM(estimated_cost), 0) FROM requests
                  WHERE created_at >= {period_start} AND {scope_where}"""

        async with self._pool.acquire() as conn:
            cursor = await conn.execute(sql, params)
            row = await cursor.fetchone()
            return float(row[0])

    async def get_budget_status(self) -> list[dict]:
        """Return all budgets with current utilization."""
        budgets = await self.get_budgets()
        result = []
        for b in budgets:
            spend = await self._get_period_spend(b)
            result.append({
                **b.model_dump(),
                "current_spend": spend,
                "utilization_pct": round((spend / b.limit_amount) * 100, 2) if b.limit_amount > 0 else 0,
            })
        return result

    # ------------------------------------------------------------------ #
    # Routing operations
    # ------------------------------------------------------------------ #

    async def get_routing_rules(self) -> list[RoutingRule]:
        """Return active routing rules ordered by priority."""
        sql = """SELECT id, rule_name, priority, condition_type, condition_value,
                    target_model, target_upstream, is_active
                 FROM routing_rules WHERE is_active = 1 ORDER BY priority"""
        async with self._pool.acquire() as conn:
            cursor = await conn.execute(sql)
            rows = await cursor.fetchall()
            return [
                RoutingRule(
                    id=r[0], rule_name=r[1], priority=r[2],
                    condition_type=r[3], condition_value=r[4] or "",
                    target_model=r[5], target_upstream=r[6] or "",
                    is_active=bool(r[7]),
                )
                for r in rows
            ]

    async def add_routing_rule(self, rule: RoutingRule) -> int:
        """Insert a routing rule and return its id."""
        sql = """INSERT INTO routing_rules (rule_name, priority, condition_type, condition_value,
                    target_model, target_upstream, is_active)
                 VALUES (:1, :2, :3, :4, :5, :6, :7) RETURNING id INTO :out_id"""
        async with self._pool.acquire() as conn:
            out_id = conn.var(oracledb.NUMBER)
            await conn.execute(sql, [
                rule.rule_name, rule.priority, rule.condition_type,
                rule.condition_value, rule.target_model, rule.target_upstream,
                1 if rule.is_active else 0, out_id,
            ])
            await conn.commit()
            return int(out_id.getvalue()[0])

    async def set_routing_rule_active(self, rule_id: int, active: bool):
        """Enable or disable a routing rule."""
        async with self._pool.acquire() as conn:
            await conn.execute(
                "UPDATE routing_rules SET is_active = :1 WHERE id = :2",
                [1 if active else 0, rule_id],
            )
            await conn.commit()

    # ------------------------------------------------------------------ #
    # A/B test operations
    # ------------------------------------------------------------------ #

    async def get_active_ab_tests(self) -> list[ABTest]:
        """Return all active A/B tests."""
        sql = """SELECT id, test_name, model_a, model_b, split_pct, status
                 FROM ab_tests WHERE status = 'active'"""
        async with self._pool.acquire() as conn:
            cursor = await conn.execute(sql)
            rows = await cursor.fetchall()
            return [
                ABTest(id=r[0], test_name=r[1], model_a=r[2], model_b=r[3], split_pct=r[4], status=r[5])
                for r in rows
            ]

    async def create_ab_test(self, test: ABTest) -> int:
        """Insert an A/B test and return its id."""
        sql = """INSERT INTO ab_tests (test_name, model_a, model_b, split_pct, status)
                 VALUES (:1, :2, :3, :4, :5) RETURNING id INTO :out_id"""
        async with self._pool.acquire() as conn:
            out_id = conn.var(oracledb.NUMBER)
            await conn.execute(sql, [
                test.test_name, test.model_a, test.model_b, test.split_pct,
                test.status, out_id,
            ])
            await conn.commit()
            return int(out_id.getvalue()[0])

    async def update_ab_test_status(self, test_name: str, status: str):
        """Update an A/B test's status."""
        async with self._pool.acquire() as conn:
            await conn.execute(
                "UPDATE ab_tests SET status = :1 WHERE test_name = :2",
                [status, test_name],
            )
            await conn.commit()

    async def get_ab_report(self, test_name: str) -> dict:
        """Generate per-variant stats for an A/B test."""
        sql = """SELECT
            r.model_used AS variant,
            COUNT(*) AS total_requests,
            ROUND(AVG(r.latency_ms), 2) AS avg_latency_ms,
            ROUND(AVG(r.output_tokens), 2) AS avg_output_tokens,
            NVL(SUM(r.estimated_cost), 0) AS total_cost,
            ROUND(SUM(CASE WHEN r.status_code >= 400 THEN 1 ELSE 0 END) / COUNT(*) * 100, 2) AS error_rate
        FROM requests r
        JOIN ab_tests t ON r.ab_test_id = t.id
        WHERE t.test_name = :1
        GROUP BY r.model_used"""
        async with self._pool.acquire() as conn:
            cursor = await conn.execute(sql, [test_name])
            columns = [col[0].lower() for col in cursor.description]
            rows = await cursor.fetchall()
            variants = [dict(zip(columns, row)) for row in rows]
            return {"test_name": test_name, "variants": variants}

    # ------------------------------------------------------------------ #
    # Upstream operations
    # ------------------------------------------------------------------ #

    async def get_upstreams(self, api_type: str | None = None) -> list[Upstream]:
        """Return upstream endpoints, optionally filtered by api_type."""
        if api_type:
            sql = """SELECT id, api_type, base_url, priority, is_healthy, fail_count
                     FROM upstreams WHERE api_type = :1 ORDER BY priority"""
            params = [api_type]
        else:
            sql = """SELECT id, api_type, base_url, priority, is_healthy, fail_count
                     FROM upstreams ORDER BY priority"""
            params = []
        async with self._pool.acquire() as conn:
            cursor = await conn.execute(sql, params)
            rows = await cursor.fetchall()
            return [
                Upstream(
                    id=r[0], api_type=r[1], base_url=r[2],
                    priority=r[3], is_healthy=bool(r[4]), fail_count=r[5],
                )
                for r in rows
            ]

    async def add_upstream(self, upstream: Upstream) -> int:
        """Insert an upstream and return its id."""
        sql = """INSERT INTO upstreams (api_type, base_url, priority, is_healthy, fail_count)
                 VALUES (:1, :2, :3, :4, :5) RETURNING id INTO :out_id"""
        async with self._pool.acquire() as conn:
            out_id = conn.var(oracledb.NUMBER)
            await conn.execute(sql, [
                upstream.api_type, upstream.base_url, upstream.priority,
                1 if upstream.is_healthy else 0, upstream.fail_count, out_id,
            ])
            await conn.commit()
            return int(out_id.getvalue()[0])

    async def mark_upstream_health(self, upstream_id: int, healthy: bool):
        """Update upstream health status and reset/increment fail_count."""
        if healthy:
            sql = "UPDATE upstreams SET is_healthy = 1, fail_count = 0, last_check = SYSTIMESTAMP WHERE id = :1"
        else:
            sql = "UPDATE upstreams SET is_healthy = 0, fail_count = fail_count + 1, last_check = SYSTIMESTAMP WHERE id = :1"
        async with self._pool.acquire() as conn:
            await conn.execute(sql, [upstream_id])
            await conn.commit()

    async def remove_upstream(self, upstream_id: int):
        """Delete an upstream by id."""
        async with self._pool.acquire() as conn:
            await conn.execute("DELETE FROM upstreams WHERE id = :1", [upstream_id])
            await conn.commit()

    # ------------------------------------------------------------------ #
    # Prompt store operations
    # ------------------------------------------------------------------ #

    async def store_prompt(self, request_id: str, request_body: str, response_body: str, prompt_hash: str):
        """Store request/response bodies for replay."""
        sql = """INSERT INTO prompt_store (request_id, request_body, response_body, prompt_hash)
                 VALUES (:1, :2, :3, :4)"""
        async with self._pool.acquire() as conn:
            await conn.execute(sql, [request_id, request_body, response_body, prompt_hash])
            await conn.commit()

    async def get_prompts_for_replay(self, source_model: str, from_date: str, to_date: str) -> list[dict]:
        """Return stored prompts for a model within a date range."""
        sql = """SELECT p.request_id, p.request_body, p.response_body, p.prompt_hash,
                    TO_CHAR(p.created_at, 'YYYY-MM-DD"T"HH24:MI:SS') AS created_at,
                    r.model_used, r.source_app
                 FROM prompt_store p
                 JOIN requests r ON p.request_id = r.request_id
                 WHERE r.model_used = :1
                   AND p.created_at >= TO_TIMESTAMP(:2, 'YYYY-MM-DD')
                   AND p.created_at < TO_TIMESTAMP(:3, 'YYYY-MM-DD')
                 ORDER BY p.created_at"""
        async with self._pool.acquire() as conn:
            cursor = await conn.execute(sql, [source_model, from_date, to_date])
            columns = [col[0].lower() for col in cursor.description]
            rows = await cursor.fetchall()
            return [dict(zip(columns, row)) for row in rows]

    # ------------------------------------------------------------------ #
    # Cache operations
    # ------------------------------------------------------------------ #

    async def cache_lookup_exact(self, prompt_hash: str, model: str) -> dict | None:
        """Look up a cached response by exact prompt hash. Increments hit_count."""
        sql = """SELECT id, response_body FROM prompt_vectors
                 WHERE prompt_hash = :1 AND model = :2
                   AND (ttl_expires IS NULL OR ttl_expires > SYSTIMESTAMP)
                 FETCH FIRST 1 ROWS ONLY"""
        async with self._pool.acquire() as conn:
            cursor = await conn.execute(sql, [prompt_hash, model])
            row = await cursor.fetchone()
            if row is None:
                return None
            cache_id, response_body = row[0], row[1]
            await conn.execute(
                "UPDATE prompt_vectors SET hit_count = hit_count + 1 WHERE id = :1",
                [cache_id],
            )
            await conn.commit()
            body = json.loads(response_body) if isinstance(response_body, str) else response_body
            return {"id": cache_id, "response_body": body}

    async def cache_lookup_semantic(self, embedding: list[float], model: str, threshold: float = 0.05) -> dict | None:
        """Look up a cached response by vector similarity."""
        sql = """SELECT id, prompt_hash, response_body,
                    VECTOR_DISTANCE(embedding, :1, COSINE) AS distance
                 FROM prompt_vectors
                 WHERE model = :2
                   AND (ttl_expires IS NULL OR ttl_expires > SYSTIMESTAMP)
                 ORDER BY distance
                 FETCH FIRST 1 ROWS ONLY"""
        async with self._pool.acquire() as conn:
            cursor = await conn.execute(sql, [str(embedding), model])
            row = await cursor.fetchone()
            if row is None or row[3] > threshold:
                return None
            cache_id = row[0]
            await conn.execute(
                "UPDATE prompt_vectors SET hit_count = hit_count + 1 WHERE id = :1",
                [cache_id],
            )
            await conn.commit()
            body = json.loads(row[2]) if isinstance(row[2], str) else row[2]
            return {"id": cache_id, "prompt_hash": row[1], "response_body": body, "distance": row[3]}

    async def cache_store(self, prompt_hash: str, model: str, embedding: list[float] | None, response_body: str, ttl_seconds: int = 86400):
        """Store a response in the vector cache."""
        sql = """INSERT INTO prompt_vectors (prompt_hash, model, embedding, response_body, ttl_expires)
                 VALUES (:1, :2, :3, :4, SYSTIMESTAMP + NUMTODSINTERVAL(:5, 'SECOND'))"""
        async with self._pool.acquire() as conn:
            await conn.execute(sql, [
                prompt_hash, model,
                str(embedding) if embedding else None,
                response_body, ttl_seconds,
            ])
            await conn.commit()

    async def cache_clear(self, model: str | None = None):
        """Clear cached entries, optionally filtered by model."""
        if model:
            sql = "DELETE FROM prompt_vectors WHERE model = :1"
            params = [model]
        else:
            sql = "TRUNCATE TABLE prompt_vectors"
            params = []
        async with self._pool.acquire() as conn:
            await conn.execute(sql, params) if params else await conn.execute(sql)
            await conn.commit()

    async def cache_stats(self) -> dict:
        """Return cache statistics."""
        sql = """SELECT
            COUNT(*) AS total_entries,
            NVL(SUM(hit_count), 0) AS total_hits,
            COUNT(CASE WHEN ttl_expires IS NULL OR ttl_expires > SYSTIMESTAMP THEN 1 END) AS active_entries
        FROM prompt_vectors"""
        async with self._pool.acquire() as conn:
            cursor = await conn.execute(sql)
            row = await cursor.fetchone()
            return {
                "total_entries": row[0],
                "total_hits": row[1],
                "active_entries": row[2],
            }

    # ------------------------------------------------------------------ #
    # Cost attribution
    # ------------------------------------------------------------------ #

    async def cost_by_tag(self, timeframe: str = "24h") -> list[dict]:
        """Return cost breakdown by feature_tag."""
        where = _timeframe_where(timeframe)
        sql = f"""SELECT feature_tag,
            COUNT(*) AS requests,
            NVL(SUM(estimated_cost), 0) AS total_cost,
            NVL(SUM(input_tokens), 0) AS input_tokens,
            NVL(SUM(output_tokens), 0) AS output_tokens
        FROM requests WHERE {where}
        GROUP BY feature_tag ORDER BY total_cost DESC"""
        async with self._pool.acquire() as conn:
            cursor = await conn.execute(sql)
            columns = [col[0].lower() for col in cursor.description]
            rows = await cursor.fetchall()
            return [dict(zip(columns, row)) for row in rows]

    async def cost_by_app(self, timeframe: str = "24h") -> list[dict]:
        """Return cost breakdown by source_app."""
        where = _timeframe_where(timeframe)
        sql = f"""SELECT source_app,
            COUNT(*) AS requests,
            NVL(SUM(estimated_cost), 0) AS total_cost,
            NVL(SUM(input_tokens), 0) AS input_tokens,
            NVL(SUM(output_tokens), 0) AS output_tokens
        FROM requests WHERE {where}
        GROUP BY source_app ORDER BY total_cost DESC"""
        async with self._pool.acquire() as conn:
            cursor = await conn.execute(sql)
            columns = [col[0].lower() for col in cursor.description]
            rows = await cursor.fetchall()
            return [dict(zip(columns, row)) for row in rows]

    async def cost_by_session(self, top: int = 20) -> list[dict]:
        """Return top sessions by cost."""
        sql = """SELECT session_id,
            COUNT(*) AS requests,
            NVL(SUM(estimated_cost), 0) AS conversation_cost,
            MIN(TO_CHAR(created_at, 'YYYY-MM-DD"T"HH24:MI:SS')) AS first_request,
            MAX(TO_CHAR(created_at, 'YYYY-MM-DD"T"HH24:MI:SS')) AS last_request
        FROM requests
        WHERE session_id IS NOT NULL AND session_id != ''
        GROUP BY session_id
        ORDER BY conversation_cost DESC
        FETCH FIRST :1 ROWS ONLY"""
        async with self._pool.acquire() as conn:
            cursor = await conn.execute(sql, [top])
            columns = [col[0].lower() for col in cursor.description]
            rows = await cursor.fetchall()
            return [dict(zip(columns, row)) for row in rows]

    async def cost_forecast(self) -> dict:
        """Forecast monthly cost based on last 7 days."""
        sql = """SELECT
            NVL(SUM(estimated_cost), 0) AS week_total,
            COUNT(DISTINCT TRUNC(created_at)) AS active_days
        FROM requests
        WHERE created_at >= SYSTIMESTAMP - INTERVAL '7' DAY"""
        async with self._pool.acquire() as conn:
            cursor = await conn.execute(sql)
            row = await cursor.fetchone()
            week_total = float(row[0])
            active_days = row[1] or 1
            daily_avg = week_total / max(active_days, 1)
            return {
                "last_7_days_total": round(week_total, 4),
                "daily_avg": round(daily_avg, 4),
                "monthly_projection": round(daily_avg * 30, 4),
            }

    # ------------------------------------------------------------------ #
    # Routing stats
    # ------------------------------------------------------------------ #

    async def routing_stats(self) -> list[dict]:
        """Return per-rule request stats."""
        sql = """SELECT
            rr.id, rr.rule_name, rr.target_model,
            COUNT(r.id) AS total_requests,
            NVL(SUM(r.estimated_cost), 0) AS total_cost,
            ROUND(AVG(r.latency_ms), 2) AS avg_latency_ms
        FROM routing_rules rr
        LEFT JOIN requests r ON r.routing_rule_id = rr.id
        GROUP BY rr.id, rr.rule_name, rr.target_model
        ORDER BY total_requests DESC"""
        async with self._pool.acquire() as conn:
            cursor = await conn.execute(sql)
            columns = [col[0].lower() for col in cursor.description]
            rows = await cursor.fetchall()
            return [dict(zip(columns, row)) for row in rows]
