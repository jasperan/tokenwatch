"""Pydantic models for TokenWatch usage records."""

from datetime import UTC, datetime

from pydantic import BaseModel, Field


def _utcnow():
    return datetime.now(UTC)


class UsageRecord(BaseModel):
    """A single API request's usage data."""

    request_id: str = ""
    api_type: str = ""  # "anthropic" or "openai"
    model: str = ""
    input_tokens: int = 0
    output_tokens: int = 0
    cache_creation_tokens: int = 0
    cache_read_tokens: int = 0
    latency_ms: int = 0
    status_code: int = 0
    timestamp: datetime = Field(default_factory=_utcnow)
    source_app: str = ""
    estimated_cost: float | None = None


class UsageStats(BaseModel):
    """Aggregated usage statistics."""

    total_requests: int = 0
    total_input_tokens: int = 0
    total_output_tokens: int = 0
    total_cache_creation_tokens: int = 0
    total_cache_read_tokens: int = 0
    total_estimated_cost: float = 0.0
    models: dict[str, dict] = Field(default_factory=dict)
