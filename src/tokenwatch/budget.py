"""Budget enforcement gate for TokenWatch proxy."""

import logging

import httpx

from .db import Database

logger = logging.getLogger("tokenwatch")


async def check_budget_gate(db: Database, source_app: str, model: str, feature_tag: str) -> dict:
    """Check budget before forwarding request.

    Returns:
        {
            "allowed": bool,
            "headers": dict,       # headers to add to response
            "block_response": dict | None,  # if blocked, the response body
        }
    """
    result = await db.check_budget(source_app, model, feature_tag)

    def _entry_values(entry: dict) -> tuple[str, float, float, str]:
        scope = entry.get("scope", "global")
        limit = float(entry.get("limit", entry.get("limit_amount", 0.0)) or 0.0)
        spent = float(entry.get("spent", entry.get("current_spend", 0.0)) or 0.0)
        period = entry.get("period", "daily")
        return scope, limit, spent, period

    headers = {}
    if not result["allowed"]:
        budget = result["blocking_budget"] or {}
        scope, limit, spent, period = _entry_values(budget)
        logger.warning("Budget exceeded: scope=%s limit=$%.2f spent=$%.2f", scope, limit, spent)
        return {
            "allowed": False,
            "headers": {},
            "block_response": {
                "error": "TokenWatch: budget exceeded",
                "budget_scope": scope,
                "budget_limit": limit,
                "budget_spent": spent,
                "budget_period": period,
            },
        }

    for warning in result["warnings"]:
        scope, limit, spent, period = _entry_values(warning)
        header_value = f"scope={scope} spent=${spent:.2f}/{limit:.2f}"
        if warning.get("action") == "approaching":
            headers["X-TokenWatch-Budget-Warning"] = header_value
        elif warning.get("action") == "webhook" and warning.get("webhook_url"):
            # Fire webhook asynchronously (best-effort)
            try:
                async with httpx.AsyncClient(timeout=5) as client:
                    await client.post(
                        warning["webhook_url"],
                        json={
                            "event": "budget_exceeded",
                            "scope": scope,
                            "limit": limit,
                            "spent": spent,
                            "period": period,
                        },
                    )
            except Exception:
                logger.warning("Failed to fire budget webhook to %s", warning["webhook_url"])
        if "X-TokenWatch-Budget-Exceeded" not in headers:
            headers["X-TokenWatch-Budget-Exceeded"] = header_value

    return {"allowed": True, "headers": headers, "block_response": None}
