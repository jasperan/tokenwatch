"""Configuration management for TokenWatch."""

import os

from dotenv import load_dotenv

load_dotenv()

# Oracle DB connection
ORACLE_DSN = os.getenv("TOKENWATCH_ORACLE_DSN", "localhost:1521/FREEPDB1")
ORACLE_USER = os.getenv("TOKENWATCH_ORACLE_USER", "")
ORACLE_PASSWORD = os.getenv("TOKENWATCH_ORACLE_PASSWORD", "")

# Server ports
HOST = os.getenv("TOKENWATCH_HOST", "127.0.0.1")
PROXY_PORT = int(os.getenv("TOKENWATCH_PROXY_PORT", "8877"))
DASHBOARD_PORT = int(os.getenv("TOKENWATCH_DASHBOARD_PORT", "8878"))

# Upstream API URLs
ANTHROPIC_UPSTREAM = os.getenv("TOKENWATCH_ANTHROPIC_URL", "https://api.anthropic.com")
OPENAI_UPSTREAM = os.getenv("TOKENWATCH_OPENAI_URL", "https://api.z.ai")

# Timeouts (seconds)
CONNECT_TIMEOUT = int(os.getenv("TOKENWATCH_CONNECT_TIMEOUT", "10"))
OVERALL_TIMEOUT = int(os.getenv("TOKENWATCH_OVERALL_TIMEOUT", "300"))

# Cache settings
CACHE_ENABLED = os.getenv("TOKENWATCH_CACHE_ENABLED", "false").lower() == "true"
CACHE_TTL = int(os.getenv("TOKENWATCH_CACHE_TTL", "86400"))
CACHE_SIMILARITY_THRESHOLD = float(os.getenv("TOKENWATCH_CACHE_SIMILARITY_THRESHOLD", "0.05"))

# Prompt storage
STORE_PROMPTS = os.getenv("TOKENWATCH_STORE_PROMPTS", "false").lower() == "true"
REDACT_STORED_PROMPTS = os.getenv("TOKENWATCH_REDACT_STORED_PROMPTS", "true").lower() == "true"
PROMPT_RETENTION_DAYS = int(os.getenv("TOKENWATCH_PROMPT_RETENTION_DAYS", "30"))

# Budget
BUDGET_ENABLED = os.getenv("TOKENWATCH_BUDGET_ENABLED", "true").lower() == "true"

# OpenTelemetry
OTEL_ENABLED = os.getenv("TOKENWATCH_OTEL_ENABLED", "false").lower() == "true"
OTEL_ENDPOINT = os.getenv("TOKENWATCH_OTEL_ENDPOINT", "http://localhost:4317")
OTEL_SERVICE_NAME = os.getenv("TOKENWATCH_OTEL_SERVICE_NAME", "tokenwatch")

# Cost per 1M tokens (input, output) in USD
MODEL_PRICING: dict[str, tuple[float, float]] = {
    # Anthropic
    "claude-opus-4-6": (15.00, 75.00),
    "claude-opus-4-6-20250610": (15.00, 75.00),
    "claude-sonnet-4-5": (3.00, 15.00),
    "claude-sonnet-4-5-20250514": (3.00, 15.00),
    "claude-sonnet-4-5-20250929": (3.00, 15.00),
    "claude-haiku-4-5": (0.80, 4.00),
    "claude-haiku-4-5-20251001": (0.80, 4.00),
    # Z.AI / GLM
    "glm-4.7": (0.60, 0.60),
    "glm-4.7-flash": (0.06, 0.06),
}


def estimate_cost(model: str, input_tokens: int, output_tokens: int) -> float | None:
    """Estimate cost in USD for a request. Returns None if model pricing unknown."""
    pricing = MODEL_PRICING.get(model)
    if pricing is None:
        return None
    input_price, output_price = pricing
    return (input_tokens * input_price + output_tokens * output_price) / 1_000_000
