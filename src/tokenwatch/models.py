"""Pydantic models for TokenWatch usage records."""

from datetime import datetime, timezone

from pydantic import BaseModel, Field


def _utcnow():
    return datetime.now(timezone.utc)


class UsageRecord(BaseModel):
    """A single API request's usage data."""

    request_id: str = ""
    api_type: str = ""  # "anthropic" or "openai"
    model: str = ""
    model_requested: str = ""  # what the client asked for (before routing)
    input_tokens: int = 0
    output_tokens: int = 0
    cache_creation_tokens: int = 0
    cache_read_tokens: int = 0
    latency_ms: int = 0
    status_code: int = 0
    timestamp: datetime = Field(default_factory=_utcnow)
    source_app: str = ""
    session_id: str = ""
    feature_tag: str = ""
    estimated_cost: float | None = None
    cache_hit: bool = False
    ab_test_id: int | None = None
    routing_rule_id: int | None = None


class UsageStats(BaseModel):
    """Aggregated usage statistics."""

    total_requests: int = 0
    total_input_tokens: int = 0
    total_output_tokens: int = 0
    total_cache_creation_tokens: int = 0
    total_cache_read_tokens: int = 0
    total_estimated_cost: float = 0.0
    total_cache_hits: int = 0
    total_cache_savings: float = 0.0
    models: dict[str, dict] = Field(default_factory=dict)


class BudgetRecord(BaseModel):
    """A budget limit definition."""

    id: int | None = None
    scope: str = "global"  # "global", "app", "model", "tag"
    scope_value: str = ""
    limit_amount: float = 0.0
    period: str = "daily"  # "hourly", "daily", "monthly"
    action_on_limit: str = "block"  # "block", "warn", "webhook"
    webhook_url: str = ""
    is_active: bool = True


class RoutingRule(BaseModel):
    """A smart routing rule."""

    id: int | None = None
    rule_name: str = ""
    priority: int = 100
    condition_type: str = ""  # "token_count", "source_app", "regex", "model", "time", "cost_today"
    condition_value: str = ""
    target_model: str = ""
    target_upstream: str = ""
    is_active: bool = True


class ABTest(BaseModel):
    """An A/B test definition."""

    id: int | None = None
    test_name: str = ""
    model_a: str = ""
    model_b: str = ""
    split_pct: int = 50
    status: str = "active"  # "active", "paused", "completed"


class Upstream(BaseModel):
    """An upstream provider endpoint."""

    id: int | None = None
    api_type: str = ""
    base_url: str = ""
    priority: int = 100
    is_healthy: bool = True
    fail_count: int = 0
