"""Privacy helpers for stored request and response payloads."""

import json
import re

from .config import REDACT_STORED_PROMPTS

SENSITIVE_KEY_RE = re.compile(r"api[-_]?key|authorization|token|secret|password", re.IGNORECASE)
BEARER_RE = re.compile(r"Bearer\s+[A-Za-z0-9._\-]+", re.IGNORECASE)
EMAIL_RE = re.compile(r"\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b", re.IGNORECASE)
GENERIC_TOKEN_RE = re.compile(r"\b(?:sk|rk)-[A-Za-z0-9]{8,}\b")


def _redact_text(text: str) -> str:
    text = BEARER_RE.sub("Bearer [REDACTED]", text)
    text = GENERIC_TOKEN_RE.sub("[REDACTED_TOKEN]", text)
    text = EMAIL_RE.sub("[REDACTED_EMAIL]", text)
    return text



def _redact_json(value):
    if isinstance(value, dict):
        redacted = {}
        for key, item in value.items():
            if SENSITIVE_KEY_RE.search(key):
                redacted[key] = "[REDACTED]"
            else:
                redacted[key] = _redact_json(item)
        return redacted
    if isinstance(value, list):
        return [_redact_json(item) for item in value]
    if isinstance(value, str):
        return _redact_text(value)
    return value



def sanitize_stored_payload(text: str) -> str:
    """Redact obvious secrets and personal data from stored payloads."""
    if not REDACT_STORED_PROMPTS:
        return text
    try:
        parsed = json.loads(text)
    except (json.JSONDecodeError, ValueError):
        return _redact_text(text)
    return json.dumps(_redact_json(parsed), separators=(",", ":"))
