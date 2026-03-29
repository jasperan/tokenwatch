"""Tests for budget evaluation and the proxy budget gate."""

import pytest

from tokenwatch.budget import check_budget_gate
from tokenwatch.db import Database
from tokenwatch.models import BudgetRecord


class StubBudgetDatabase(Database):
    def __init__(self, budgets, spends):
        self._budgets = budgets
        self._spends = spends

    async def get_budgets(self):
        return self._budgets

    async def _get_period_spend(self, budget):
        return self._spends[budget.id]


class GateDatabase:
    def __init__(self, result):
        self._result = result

    async def check_budget(self, source_app, model, feature_tag):
        return self._result


@pytest.mark.asyncio
async def test_database_check_budget_returns_structured_blocking_budget():
    db = StubBudgetDatabase(
        budgets=[
            BudgetRecord(
                id=1,
                scope="app",
                scope_value="tokenwatch-tests",
                limit_amount=10.0,
                period="daily",
                action_on_limit="block",
            )
        ],
        spends={1: 12.5},
    )

    result = await db.check_budget(
        source_app="tokenwatch-tests",
        model="claude-sonnet-4-5",
        feature_tag="chat",
    )

    assert result["allowed"] is False
    assert result["warnings"] == []
    assert result["blocking_budget"] == {
        "budget_id": 1,
        "scope": "app",
        "scope_value": "tokenwatch-tests",
        "limit": 10.0,
        "spent": 12.5,
        "period": "daily",
        "action": "block",
        "webhook_url": "",
    }


@pytest.mark.asyncio
async def test_database_check_budget_ignores_non_matching_scopes():
    db = StubBudgetDatabase(
        budgets=[
            BudgetRecord(
                id=1,
                scope="app",
                scope_value="other-app",
                limit_amount=1.0,
                period="daily",
                action_on_limit="block",
            ),
            BudgetRecord(
                id=2,
                scope="model",
                scope_value="other-model",
                limit_amount=1.0,
                period="daily",
                action_on_limit="block",
            ),
            BudgetRecord(
                id=3,
                scope="tag",
                scope_value="other-tag",
                limit_amount=1.0,
                period="daily",
                action_on_limit="block",
            ),
        ],
        spends={1: 999.0, 2: 999.0, 3: 999.0},
    )

    result = await db.check_budget(
        source_app="tokenwatch-tests",
        model="claude-sonnet-4-5",
        feature_tag="chat",
    )

    assert result == {
        "allowed": True,
        "warnings": [],
        "blocking_budget": None,
    }


@pytest.mark.asyncio
async def test_check_budget_gate_returns_warning_headers():
    gate = GateDatabase(
        {
            "allowed": True,
            "warnings": [
                {
                    "budget_id": 1,
                    "scope": "app",
                    "scope_value": "tokenwatch-tests",
                    "limit": 10.0,
                    "spent": 8.1,
                    "period": "daily",
                    "action": "approaching",
                    "webhook_url": "",
                }
            ],
            "blocking_budget": None,
        }
    )

    result = await check_budget_gate(gate, "tokenwatch-tests", "claude-sonnet-4-5", "chat")

    assert result["allowed"] is True
    assert result["block_response"] is None
    assert result["headers"]["X-TokenWatch-Budget-Warning"] == "scope=app spent=$8.10/10.00"
    assert result["headers"]["X-TokenWatch-Budget-Exceeded"] == "scope=app spent=$8.10/10.00"


@pytest.mark.asyncio
async def test_check_budget_gate_returns_block_response():
    gate = GateDatabase(
        {
            "allowed": False,
            "warnings": [],
            "blocking_budget": {
                "budget_id": 7,
                "scope": "model",
                "scope_value": "claude-sonnet-4-5",
                "limit": 5.0,
                "spent": 5.5,
                "period": "daily",
                "action": "block",
                "webhook_url": "",
            },
        }
    )

    result = await check_budget_gate(gate, "tokenwatch-tests", "claude-sonnet-4-5", "chat")

    assert result["allowed"] is False
    assert result["headers"] == {}
    assert result["block_response"] == {
        "error": "TokenWatch: budget exceeded",
        "budget_scope": "model",
        "budget_limit": 5.0,
        "budget_spent": 5.5,
        "budget_period": "daily",
    }
