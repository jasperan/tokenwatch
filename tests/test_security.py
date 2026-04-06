"""Security-focused tests for fail-secure defaults."""

import importlib

import pytest
from click.testing import CliRunner


@pytest.mark.asyncio
async def test_database_init_requires_explicit_oracle_credentials(monkeypatch):
    from tokenwatch.db import Database, oracledb

    called = False

    def fake_create_pool_async(**kwargs):
        nonlocal called
        called = True
        raise AssertionError("create_pool_async should not be called when credentials are missing")

    monkeypatch.setattr(oracledb, "create_pool_async", fake_create_pool_async)

    db = Database(dsn="localhost:1521/FREEPDB1", user="", password="")

    with pytest.raises(RuntimeError, match="TOKENWATCH_ORACLE_USER and TOKENWATCH_ORACLE_PASSWORD"):
        await db.init()

    assert called is False



def test_config_defaults_are_local_and_fail_closed(monkeypatch):
    import tokenwatch.config as config_module

    with monkeypatch.context() as context:
        context.delenv("TOKENWATCH_ORACLE_USER", raising=False)
        context.delenv("TOKENWATCH_ORACLE_PASSWORD", raising=False)
        context.delenv("TOKENWATCH_HOST", raising=False)
        context.delenv("TOKENWATCH_CACHE_ENABLED", raising=False)

        config = importlib.reload(config_module)

        assert config.ORACLE_USER == ""
        assert config.ORACLE_PASSWORD == ""
        assert config.HOST == "127.0.0.1"
        assert config.CACHE_ENABLED is False

    importlib.reload(config_module)



def test_start_binds_proxy_and_dashboard_to_localhost_by_default(monkeypatch):
    from tokenwatch.cli import cli

    runs = []

    class FakeThread:
        def __init__(self, target, daemon=False):
            self.target = target
            self.daemon = daemon

        def start(self):
            self.target()

    def fake_run(app, host, port, log_level, **kwargs):
        runs.append({"app": app, "host": host, "port": port, "log_level": log_level})
        return None

    monkeypatch.setattr("threading.Thread", FakeThread)
    monkeypatch.setattr("tokenwatch.cli.uvicorn.run", fake_run)

    result = CliRunner().invoke(cli, ["start"])

    assert result.exit_code == 0
    assert [run["host"] for run in runs] == ["127.0.0.1", "127.0.0.1"]
    assert runs[0]["port"] == 8878
    assert runs[1]["port"] == 8877
