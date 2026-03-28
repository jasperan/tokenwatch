"""Automatic feature tagging for cost attribution and budgets."""

import logging
import re

logger = logging.getLogger("tokenwatch")


def auto_tag(source_app: str, first_message: str, rules: list[dict]) -> str:
    """Return the first matching tag from the configured rules."""
    for rule in rules:
        rule_type = rule.get("condition_type", "")
        rule_value = rule.get("condition_value", "")
        tag = rule.get("tag", "")

        if rule_type == "tag_app" and rule_value and (source_app == rule_value or rule_value in source_app):
            return tag

        if rule_type == "tag_regex" and rule_value:
            try:
                if re.search(rule_value, first_message):
                    return tag
            except re.error:
                logger.warning("Invalid tag regex ignored: %s", rule_value)

    return ""
