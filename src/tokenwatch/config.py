"""Configuration management for TokenWatch."""

import os
from pathlib import Path

from dotenv import load_dotenv

load_dotenv()


def _expand(path: str) -> Path:
    return Path(os.path.expanduser(path))


PROXY_PORT = int(os.getenv("TOKENWATCH_PROXY_PORT", "8877"))
DASHBOARD_PORT = int(os.getenv("TOKENWATCH_DASHBOARD_PORT", "8878"))
DB_PATH = _expand(os.getenv("TOKENWATCH_DB_PATH", "~/.tokenwatch/usage.db"))

ANTHROPIC_UPSTREAM = os.getenv("TOKENWATCH_ANTHROPIC_URL", "https://api.anthropic.com")
OPENAI_UPSTREAM = os.getenv("TOKENWATCH_OPENAI_URL", "https://api.z.ai")

# Timeouts (seconds)
CONNECT_TIMEOUT = 10
OVERALL_TIMEOUT = 300

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
