"""Auto-tagging rules for cost attribution."""

import re
import logging

from .db import Database

logger = logging.getLogger("tokenwatch")

# In-memory tag rules (loaded from DB, refreshed periodically)
_tag_rules: list[dict] = []


async def load_tag_rules(db: Database):
    """Load tag rules from the routing_rules table (condition_type starts with 'tag_')."""
    global _tag_rules
    async with db._pool.acquire() as conn:
        cursor = await conn.execute(
            """SELECT condition_type, condition_value, target_model as tag
               FROM routing_rules
               WHERE condition_type LIKE 'tag_%' AND is_active = 1
               ORDER BY priority"""
        )
        rows = await cursor.fetchall()
        _tag_rules = [{"type": r[0], "value": r[1], "tag": r[2]} for r in rows]


def auto_tag(source_app: str, first_message: str) -> str:
    """Apply auto-tagging rules. Returns tag or empty string."""
    for rule in _tag_rules:
        if rule["type"] == "tag_app" and (rule["value"] == source_app or rule["value"] in source_app):
            return rule["tag"]
        if rule["type"] == "tag_regex":
            try:
                if re.search(rule["value"], first_message):
                    return rule["tag"]
            except re.error:
                continue
    return ""
