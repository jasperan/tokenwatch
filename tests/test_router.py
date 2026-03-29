"""Tests for routing rules and A/B assignment."""

import pytest

from tokenwatch.models import ABTest, RoutingRule
from tokenwatch.router import _matches, evaluate_ab_test, evaluate_routing


class StubRoutingDatabase:
    def __init__(self, rules=None, tests=None):
        self._rules = rules or []
        self._tests = tests or []

    async def get_routing_rules(self):
        return self._rules

    async def get_active_ab_tests(self):
        return self._tests


@pytest.mark.asyncio
async def test_evaluate_routing_returns_matching_rule_details():
    db = StubRoutingDatabase(
        rules=[
            RoutingRule(
                id=4,
                rule_name="small-prompts",
                priority=10,
                condition_type="token_count",
                condition_value="<100",
                target_model="claude-haiku-4-5",
                target_upstream="https://cheap.example",
            )
        ]
    )

    decision = await evaluate_routing(
        db,
        requested_model="claude-sonnet-4-5",
        source_app="tokenwatch-tests",
        first_message="hello",
        estimated_input_tokens=42,
        daily_cost=0.0,
    )

    assert decision.was_rerouted is True
    assert decision.model == "claude-haiku-4-5"
    assert decision.upstream == "https://cheap.example"
    assert decision.rule_id == 4
    assert decision.rule_name == "small-prompts"


@pytest.mark.asyncio
async def test_evaluate_ab_test_returns_requested_model_when_no_tests_are_active():
    db = StubRoutingDatabase(tests=[])

    model, test_id = await evaluate_ab_test(db, "req-1", "claude-sonnet-4-5", "tokenwatch-tests")

    assert model == "claude-sonnet-4-5"
    assert test_id is None


@pytest.mark.asyncio
async def test_evaluate_ab_test_is_deterministic_for_same_request_id():
    db = StubRoutingDatabase(
        tests=[ABTest(id=11, test_name="latency", model_a="claude-haiku-4-5", model_b="claude-sonnet-4-5", split_pct=50)]
    )

    first = await evaluate_ab_test(db, "req-1", "claude-sonnet-4-5", "tokenwatch-tests")
    second = await evaluate_ab_test(db, "req-1", "claude-sonnet-4-5", "tokenwatch-tests")

    assert first == second
    assert first[1] == 11
    assert first[0] in {"claude-haiku-4-5", "claude-sonnet-4-5"}


def test_matches_handles_invalid_regex_without_crashing():
    rule = RoutingRule(condition_type="regex", condition_value="(", target_model="claude-haiku-4-5")

    assert _matches(rule, "claude-sonnet-4-5", "tokenwatch-tests", "hello", 42, 0.0) is False


def test_matches_supports_daily_cost_thresholds():
    rule = RoutingRule(condition_type="cost_today", condition_value=">5", target_model="claude-haiku-4-5")

    assert _matches(rule, "claude-sonnet-4-5", "tokenwatch-tests", "hello", 42, 6.0) is True
    assert _matches(rule, "claude-sonnet-4-5", "tokenwatch-tests", "hello", 42, 4.0) is False
