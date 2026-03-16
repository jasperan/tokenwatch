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

    headers = {}
    if not result["allowed"]:
        budget = result["blocking_budget"]
        logger.warning(
            "Budget exceeded: scope=%s limit=$%.2f spent=$%.2f",
            budget["scope"], budget["limit"], budget["spent"],
        )
        return {
            "allowed": False,
            "headers": {},
            "block_response": {
                "error": "TokenWatch: budget exceeded",
                "budget_scope": budget["scope"],
                "budget_limit": budget["limit"],
                "budget_spent": budget["spent"],
                "budget_period": budget["period"],
            },
        }

    for warning in result["warnings"]:
        if warning.get("action") == "approaching":
            headers["X-TokenWatch-Budget-Warning"] = (
                f"scope={warning['scope']} spent=${warning['spent']:.2f}/{warning['limit']:.2f}"
            )
        elif warning.get("action") == "webhook" and warning.get("webhook_url"):
            # Fire webhook asynchronously (best-effort)
            try:
                async with httpx.AsyncClient(timeout=5) as client:
                    await client.post(warning["webhook_url"], json={
                        "event": "budget_exceeded",
                        "scope": warning["scope"],
                        "limit": warning["limit"],
                        "spent": warning["spent"],
                        "period": warning["period"],
                    })
            except Exception:
                logger.warning("Failed to fire budget webhook to %s", warning["webhook_url"])
        if "X-TokenWatch-Budget-Exceeded" not in headers:
            headers["X-TokenWatch-Budget-Exceeded"] = (
                f"scope={warning['scope']} spent=${warning['spent']:.2f}/{warning['limit']:.2f}"
            )

    return {"allowed": True, "headers": headers, "block_response": None}
