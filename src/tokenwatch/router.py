"""Smart model routing engine for TokenWatch."""

import hashlib
import logging
import re

from .db import Database
from .models import RoutingRule

logger = logging.getLogger("tokenwatch")


class RoutingDecision:
    """Result of routing evaluation."""

    def __init__(self, model: str, upstream: str = "", rule_id: int | None = None, rule_name: str = ""):
        self.model = model
        self.upstream = upstream
        self.rule_id = rule_id
        self.rule_name = rule_name
        self.was_rerouted = False


async def evaluate_routing(
    db: Database,
    requested_model: str,
    source_app: str,
    first_message: str,
    estimated_input_tokens: int,
    daily_cost: float,
) -> RoutingDecision:
    """Evaluate routing rules and return a decision."""
    decision = RoutingDecision(model=requested_model)
    rules = await db.get_routing_rules()

    for rule in rules:
        if _matches(rule, requested_model, source_app, first_message, estimated_input_tokens, daily_cost):
            decision.model = rule.target_model
            decision.upstream = rule.target_upstream
            decision.rule_id = rule.id
            decision.rule_name = rule.rule_name
            decision.was_rerouted = True
            logger.info(
                "Routing: %s -> %s (rule: %s)",
                requested_model, rule.target_model, rule.rule_name,
            )
            break

    return decision


def _matches(
    rule: RoutingRule,
    model: str,
    source_app: str,
    first_message: str,
    token_count: int,
    daily_cost: float,
) -> bool:
    """Check if a routing rule matches the current request."""
    ct = rule.condition_type
    cv = rule.condition_value

    if ct == "model":
        return model == cv

    if ct == "source_app":
        return source_app == cv or cv in source_app

    if ct == "regex":
        try:
            return bool(re.search(cv, first_message))
        except re.error:
            return False

    if ct == "token_count":
        try:
            if cv.startswith("<"):
                return token_count < int(cv[1:])
            elif cv.startswith(">"):
                return token_count > int(cv[1:])
        except ValueError:
            return False

    if ct == "cost_today":
        try:
            if cv.startswith(">"):
                return daily_cost > float(cv[1:])
        except ValueError:
            return False

    if ct == "time":
        import datetime
        try:
            start_s, end_s = cv.split("-")
            now = datetime.datetime.now().time()
            start_t = datetime.time(*map(int, start_s.split(":")))
            end_t = datetime.time(*map(int, end_s.split(":")))
            return start_t <= now <= end_t
        except (ValueError, TypeError):
            return False

    return False


async def evaluate_ab_test(
    db: Database,
    request_id: str,
    requested_model: str,
    source_app: str,
) -> tuple[str, int | None]:
    """Check if an A/B test applies. Returns (model_to_use, ab_test_id)."""
    tests = await db.get_active_ab_tests()
    for test in tests:
        # Deterministic assignment
        hash_input = f"{request_id}{test.id}"
        hash_val = int(hashlib.md5(hash_input.encode()).hexdigest(), 16) % 100
        if hash_val < test.split_pct:
            return test.model_a, test.id
        else:
            return test.model_b, test.id
    return requested_model, None
