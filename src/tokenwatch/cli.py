"""CLI interface for TokenWatch."""

import asyncio
import logging
import json

import click
import uvicorn
from rich.console import Console
from rich.table import Table
from rich.live import Live

from . import __version__
from .config import DASHBOARD_PORT, ORACLE_DSN, ORACLE_USER, PROXY_PORT
from .db import Database
from .models import BudgetRecord, RoutingRule, ABTest, Upstream

console = Console()


def _run(coro):
    """Run an async function."""
    return asyncio.run(coro)


@click.group()
@click.version_option(__version__)
def cli():
    """TokenWatch - AI proxy with semantic caching, smart routing, and budget enforcement."""
    pass


# --- Start ---

@cli.command()
@click.option("--proxy-port", default=PROXY_PORT, help="Proxy port")
@click.option("--dashboard-port", default=DASHBOARD_PORT, help="Dashboard port")
@click.option("--log-level", default="info", type=click.Choice(["debug", "info", "warning", "error"]))
def start(proxy_port, dashboard_port, log_level):
    """Start the TokenWatch proxy and dashboard servers."""
    logging.basicConfig(
        level=getattr(logging, log_level.upper()),
        format="%(asctime)s [%(name)s] %(levelname)s: %(message)s",
    )
    console.print(f"[bold green]TokenWatch v{__version__}[/]")
    console.print(f"  Proxy:     http://localhost:{proxy_port}")
    console.print(f"  Dashboard: http://localhost:{dashboard_port}")
    console.print(f"  Oracle DB: {ORACLE_DSN} (user: {ORACLE_USER})")
    console.print()
    console.print("[dim]Configure your apps to use:[/]")
    console.print(f"[dim]  Anthropic: http://localhost:{proxy_port}/anthropic[/dim]")
    console.print(f"[dim]  OpenAI:    http://localhost:{proxy_port}/openai[/dim]")
    console.print()

    import threading

    def run_dashboard():
        from .dashboard_app import create_dashboard_app
        dash_app = create_dashboard_app()
        uvicorn.run(dash_app, host="0.0.0.0", port=dashboard_port, log_level=log_level)

    dash_thread = threading.Thread(target=run_dashboard, daemon=True)
    dash_thread.start()

    uvicorn.run(
        "tokenwatch.proxy:app",
        host="0.0.0.0",
        port=proxy_port,
        log_level=log_level,
    )


# --- Stats ---

@cli.command()
@click.option("--timeframe", "-t", default="24h", type=click.Choice(["1h", "24h", "7d", "30d", "all"]))
def stats(timeframe):
    """Show token usage statistics."""
    _run(_show_stats(timeframe))


async def _show_stats(timeframe):
    db = Database()
    await db.init()
    try:
        s = await db.get_stats(timeframe)
    finally:
        await db.close()

    console.print(f"\n[bold]TokenWatch Stats[/] ({timeframe})\n")

    table = Table(show_header=True, header_style="bold cyan")
    table.add_column("Model", style="white")
    table.add_column("Requests", justify="right")
    table.add_column("Input Tokens", justify="right")
    table.add_column("Output Tokens", justify="right")
    table.add_column("Est. Cost", justify="right", style="yellow")

    for model, data in s.models.items():
        table.add_row(
            model or "(unknown)",
            str(data["requests"]),
            f"{data['input_tokens']:,}",
            f"{data['output_tokens']:,}",
            f"${data['cost']:.4f}" if data["cost"] else "-",
        )

    table.add_section()
    table.add_row(
        "[bold]TOTAL[/]",
        f"[bold]{s.total_requests}[/]",
        f"[bold]{s.total_input_tokens:,}[/]",
        f"[bold]{s.total_output_tokens:,}[/]",
        f"[bold yellow]${s.total_estimated_cost:.4f}[/]",
    )
    console.print(table)

    if s.total_cache_hits:
        console.print(f"  Cache hits: {s.total_cache_hits} (saved ~${s.total_cache_savings:.4f})")
    console.print()


# --- Tail ---

@cli.command()
@click.option("--limit", "-n", default=20, help="Number of recent requests")
def tail(limit):
    """Show recent requests (live updating)."""
    _run(_tail(limit))


async def _tail(limit):
    db = Database()
    await db.init()

    def build_table(rows):
        t = Table(show_header=True, header_style="bold cyan")
        t.add_column("Time", style="dim")
        t.add_column("API")
        t.add_column("Model")
        t.add_column("In", justify="right")
        t.add_column("Out", justify="right")
        t.add_column("Latency", justify="right")
        t.add_column("Cost", justify="right", style="yellow")
        t.add_column("Cache", justify="center")
        for r in rows:
            api_style = "blue" if r.get("api_type") == "anthropic" else "green"
            cache_icon = "[green]HIT[/]" if r.get("cache_hit") else ""
            created = r.get("created_at", "")
            time_str = created.split("T")[-1][:8] if "T" in str(created) else str(created)
            t.add_row(
                time_str,
                f"[{api_style}]{r.get('api_type', '')}[/]",
                r.get("model_used", "") or r.get("model", ""),
                f"{r.get('input_tokens', 0):,}",
                f"{r.get('output_tokens', 0):,}",
                f"{r.get('latency_ms', 0)}ms",
                f"${r.get('estimated_cost', 0):.4f}" if r.get("estimated_cost") else "-",
                cache_icon,
            )
        return t

    try:
        with Live(console=console, refresh_per_second=1) as live:
            while True:
                rows = await db.get_recent(limit)
                live.update(build_table(rows))
                await asyncio.sleep(2)
    except KeyboardInterrupt:
        pass
    finally:
        await db.close()


# --- Status ---

@cli.command()
def status():
    """Check if TokenWatch proxy is running."""
    import httpx
    try:
        resp = httpx.get(f"http://localhost:{PROXY_PORT}/health", timeout=3)
        if resp.status_code == 200:
            console.print(f"[green]TokenWatch proxy is running on port {PROXY_PORT}[/]")
        else:
            console.print(f"[yellow]Proxy responded with status {resp.status_code}[/]")
    except Exception:
        console.print(f"[red]TokenWatch proxy is not running on port {PROXY_PORT}[/]")


# --- Reset ---

@cli.command()
@click.confirmation_option(prompt="Are you sure you want to clear all usage data?")
def reset():
    """Clear all usage data from the database."""
    _run(_reset())


async def _reset():
    db = Database()
    await db.init()
    await db.reset()
    await db.close()
    console.print("[green]Database cleared.[/]")


# --- Budget ---

@cli.group()
def budget():
    """Manage spending budgets."""
    pass


@budget.command("set")
@click.option("--limit", type=float, required=True, help="Budget limit in USD")
@click.option("--period", type=click.Choice(["hourly", "daily", "monthly"]), required=True)
@click.option("--app", default="", help="Scope to a specific app")
@click.option("--model", default="", help="Scope to a specific model")
@click.option("--tag", default="", help="Scope to a specific feature tag")
@click.option("--action", default="block", type=click.Choice(["block", "warn", "webhook"]))
@click.option("--webhook-url", default="", help="Webhook URL for notifications")
def budget_set(limit, period, app, model, tag, action, webhook_url):
    """Set a spending budget."""
    scope = "global"
    scope_value = ""
    if app:
        scope, scope_value = "app", app
    elif model:
        scope, scope_value = "model", model
    elif tag:
        scope, scope_value = "tag", tag

    _run(_budget_set(BudgetRecord(
        scope=scope, scope_value=scope_value, limit_amount=limit,
        period=period, action_on_limit=action, webhook_url=webhook_url,
    )))


async def _budget_set(budget):
    db = Database()
    await db.init()
    budget_id = await db.add_budget(budget)
    await db.close()
    console.print(f"[green]Budget #{budget_id} created: ${budget.limit_amount:.2f}/{budget.period} ({budget.scope})[/]")


@budget.command("status")
def budget_status():
    """Show all budgets and current spend."""
    _run(_budget_status())


async def _budget_status():
    db = Database()
    await db.init()
    statuses = await db.get_budget_status()
    await db.close()

    if not statuses:
        console.print("[dim]No budgets configured.[/]")
        return

    table = Table(show_header=True, header_style="bold cyan")
    table.add_column("ID", justify="right")
    table.add_column("Scope")
    table.add_column("Limit", justify="right")
    table.add_column("Spent", justify="right")
    table.add_column("Util", justify="right")
    table.add_column("Period")
    table.add_column("Action")

    for s in statuses:
        util = s["utilization_pct"] / 100
        util_style = "green" if util < 0.8 else ("yellow" if util < 1.0 else "red")
        table.add_row(
            str(s["id"]),
            f"{s['scope']}: {s['scope_value']}" if s["scope_value"] else s["scope"],
            f"${s['limit_amount']:.2f}",
            f"${s['current_spend']:.2f}",
            f"[{util_style}]{util:.0%}[/]",
            s["period"],
            s["action_on_limit"],
        )
    console.print(table)


@budget.command("remove")
@click.option("--id", "budget_id", type=int, required=True)
def budget_remove(budget_id):
    """Remove a budget."""
    _run(_budget_remove(budget_id))


async def _budget_remove(budget_id):
    db = Database()
    await db.init()
    await db.remove_budget(budget_id)
    await db.close()
    console.print(f"[green]Budget #{budget_id} removed.[/]")


# --- Cost ---

@cli.group()
def cost():
    """View cost breakdowns."""
    pass


@cost.command("by-tag")
@click.option("--timeframe", "-t", default="24h", type=click.Choice(["1h", "24h", "7d", "30d", "all"]))
def cost_by_tag(timeframe):
    """Cost breakdown by feature tag."""
    _run(_cost_by_tag(timeframe))


async def _cost_by_tag(timeframe):
    db = Database()
    await db.init()
    data = await db.cost_by_tag(timeframe)
    await db.close()

    table = Table(show_header=True, header_style="bold cyan", title=f"Cost by Tag ({timeframe})")
    table.add_column("Tag")
    table.add_column("Requests", justify="right")
    table.add_column("Total Cost", justify="right", style="yellow")
    table.add_column("Avg/Request", justify="right")

    for row in data:
        table.add_row(
            row["tag"],
            str(row["requests"]),
            f"${row['total_cost']:.4f}",
            f"${row['avg_cost']:.4f}",
        )
    console.print(table)


@cost.command("by-app")
@click.option("--timeframe", "-t", default="24h", type=click.Choice(["1h", "24h", "7d", "30d", "all"]))
def cost_by_app(timeframe):
    """Cost breakdown by source application."""
    _run(_cost_by_app(timeframe))


async def _cost_by_app(timeframe):
    db = Database()
    await db.init()
    data = await db.cost_by_app(timeframe)
    await db.close()

    table = Table(show_header=True, header_style="bold cyan", title=f"Cost by App ({timeframe})")
    table.add_column("App")
    table.add_column("Requests", justify="right")
    table.add_column("Total Cost", justify="right", style="yellow")
    table.add_column("Avg/Request", justify="right")

    for row in data:
        table.add_row(row["app"], str(row["requests"]), f"${row['total_cost']:.4f}", f"${row['avg_cost']:.4f}")
    console.print(table)


@cost.command("by-session")
@click.option("--top", default=20, help="Number of sessions to show")
def cost_by_session(top):
    """Most expensive conversations."""
    _run(_cost_by_session(top))


async def _cost_by_session(top):
    db = Database()
    await db.init()
    data = await db.cost_by_session(top)
    await db.close()

    table = Table(show_header=True, header_style="bold cyan", title=f"Top {top} Sessions by Cost")
    table.add_column("Session ID")
    table.add_column("Turns", justify="right")
    table.add_column("Cost", justify="right", style="yellow")
    table.add_column("Started")
    table.add_column("Ended")

    for row in data:
        table.add_row(
            row["session_id"][:20] + "..." if len(row.get("session_id", "")) > 20 else row.get("session_id", ""),
            str(row["turns"]),
            f"${row['conversation_cost']:.4f}",
            row.get("started", ""),
            row.get("ended", ""),
        )
    console.print(table)


@cost.command("forecast")
def cost_forecast():
    """Project future spending based on recent trends."""
    _run(_cost_forecast())


async def _cost_forecast():
    db = Database()
    await db.init()
    data = await db.cost_forecast()
    await db.close()

    console.print(f"\n[bold]Cost Forecast[/] (based on {data['data_points']} days)\n")
    console.print(f"  Daily average:      [yellow]${data['daily_avg']:.4f}[/]")
    console.print(f"  Monthly projection: [yellow]${data['monthly_projection']:.2f}[/]")
    console.print()


# --- Route ---

@cli.group()
def route():
    """Manage smart routing rules."""
    pass


@route.command("add")
@click.argument("name")
@click.option("--condition", type=click.Choice(["token_count", "source_app", "regex", "model", "time", "cost_today"]), required=True)
@click.option("--value", required=True, help="Condition value")
@click.option("--target", required=True, help="Target model")
@click.option("--priority", default=100, help="Rule priority (lower = first)")
@click.option("--upstream", default="", help="Override upstream URL")
def route_add(name, condition, value, target, priority, upstream):
    """Add a routing rule."""
    _run(_route_add(RoutingRule(
        rule_name=name, condition_type=condition, condition_value=value,
        target_model=target, priority=priority, target_upstream=upstream,
    )))


async def _route_add(rule):
    db = Database()
    await db.init()
    rule_id = await db.add_routing_rule(rule)
    await db.close()
    console.print(f"[green]Rule #{rule_id} created: {rule.rule_name} ({rule.condition_type}={rule.condition_value} -> {rule.target_model})[/]")


@route.command("list")
def route_list():
    """Show all routing rules."""
    _run(_route_list())


async def _route_list():
    db = Database()
    await db.init()
    rules = await db.get_routing_rules()
    await db.close()

    if not rules:
        console.print("[dim]No routing rules configured.[/]")
        return

    table = Table(show_header=True, header_style="bold cyan")
    table.add_column("ID", justify="right")
    table.add_column("Priority", justify="right")
    table.add_column("Name")
    table.add_column("Condition")
    table.add_column("Target Model")

    for r in rules:
        table.add_row(str(r.id), str(r.priority), r.rule_name, f"{r.condition_type}={r.condition_value}", r.target_model)
    console.print(table)


@route.command("disable")
@click.option("--id", "rule_id", type=int, required=True)
def route_disable(rule_id):
    """Disable a routing rule."""
    _run(_route_toggle(rule_id, False))


@route.command("enable")
@click.option("--id", "rule_id", type=int, required=True)
def route_enable(rule_id):
    """Enable a routing rule."""
    _run(_route_toggle(rule_id, True))


async def _route_toggle(rule_id, active):
    db = Database()
    await db.init()
    await db.set_routing_rule_active(rule_id, active)
    await db.close()
    state = "enabled" if active else "disabled"
    console.print(f"[green]Rule #{rule_id} {state}.[/]")


# --- Cache ---

@cli.group()
def cache():
    """Manage the semantic prompt cache."""
    pass


@cache.command("stats")
def cache_stats():
    """Show cache statistics."""
    _run(_cache_stats())


async def _cache_stats():
    db = Database()
    await db.init()
    data = await db.cache_stats()
    await db.close()

    console.print("\n[bold]Cache Stats[/]\n")
    console.print(f"  Total entries:  {data['entries']}")
    console.print(f"  Active entries: {data['active_entries']}")
    console.print(f"  Total hits:     {data['total_hits']}")
    console.print()


@cache.command("clear")
@click.option("--model", default=None, help="Only clear cache for this model")
@click.confirmation_option(prompt="Clear the prompt cache?")
def cache_clear(model):
    """Clear cached responses."""
    _run(_cache_clear(model))


async def _cache_clear(model):
    db = Database()
    await db.init()
    await db.cache_clear(model)
    await db.close()
    scope = f" for model={model}" if model else ""
    console.print(f"[green]Cache cleared{scope}.[/]")


# --- A/B Test ---

@cli.group()
def ab():
    """Manage A/B tests."""
    pass


@ab.command("create")
@click.argument("name")
@click.option("--model-a", required=True, help="Model A")
@click.option("--model-b", required=True, help="Model B")
@click.option("--split", default=50, help="Percentage of traffic to model A")
def ab_create(name, model_a, model_b, split):
    """Create an A/B test."""
    _run(_ab_create(ABTest(test_name=name, model_a=model_a, model_b=model_b, split_pct=split)))


async def _ab_create(test):
    db = Database()
    await db.init()
    test_id = await db.create_ab_test(test)
    await db.close()
    console.print(f"[green]A/B test #{test_id} created: {test.test_name} ({test.model_a} vs {test.model_b}, {test.split_pct}% split)[/]")


@ab.command("list")
def ab_list():
    """Show active A/B tests."""
    _run(_ab_list())


async def _ab_list():
    db = Database()
    await db.init()
    tests = await db.get_active_ab_tests()
    await db.close()

    if not tests:
        console.print("[dim]No active A/B tests.[/]")
        return

    table = Table(show_header=True, header_style="bold cyan")
    table.add_column("ID", justify="right")
    table.add_column("Name")
    table.add_column("Model A")
    table.add_column("Model B")
    table.add_column("Split", justify="right")
    table.add_column("Status")

    for t in tests:
        table.add_row(str(t.id), t.test_name, t.model_a, t.model_b, f"{t.split_pct}%", t.status)
    console.print(table)


@ab.command("report")
@click.argument("name")
def ab_report(name):
    """Show A/B test comparison report."""
    _run(_ab_report(name))


async def _ab_report(name):
    db = Database()
    await db.init()
    data = await db.get_ab_report(name)
    await db.close()

    if not data:
        console.print(f"[red]Test '{name}' not found.[/]")
        return

    console.print(f"\n[bold]A/B Test: {name}[/]\n")

    table = Table(show_header=True, header_style="bold cyan")
    table.add_column("Metric")
    table.add_column(data["model_a"], justify="right")
    table.add_column(data["model_b"], justify="right")

    for variant_name, variant_data in data.get("variants", {}).items():
        pass  # Display per-variant metrics

    variants = data.get("variants", {})
    metrics = ["requests", "avg_latency", "avg_output_tokens", "total_cost", "avg_cost", "error_rate"]
    labels = ["Requests", "Avg Latency (ms)", "Avg Output Tokens", "Total Cost", "Avg Cost", "Error Rate"]

    va = variants.get(data["model_a"], {})
    vb = variants.get(data["model_b"], {})

    for metric, label in zip(metrics, labels):
        val_a = va.get(metric, 0) or 0
        val_b = vb.get(metric, 0) or 0
        if "cost" in metric:
            table.add_row(label, f"${val_a:.4f}", f"${val_b:.4f}")
        elif "rate" in metric:
            table.add_row(label, f"{val_a:.2%}", f"{val_b:.2%}")
        else:
            table.add_row(label, f"{val_a:,.0f}", f"{val_b:,.0f}")

    console.print(table)


@ab.command("pause")
@click.argument("name")
def ab_pause(name):
    """Pause an A/B test."""
    _run(_ab_status_change(name, "paused"))


@ab.command("complete")
@click.argument("name")
def ab_complete(name):
    """Mark an A/B test as completed."""
    _run(_ab_status_change(name, "completed"))


async def _ab_status_change(name, status):
    db = Database()
    await db.init()
    await db.update_ab_test_status(name, status)
    await db.close()
    console.print(f"[green]A/B test '{name}' {status}.[/]")


# --- Replay ---

@cli.command()
@click.option("--from", "from_date", required=True, help="Start date (YYYY-MM-DD)")
@click.option("--to", "to_date", required=True, help="End date (YYYY-MM-DD)")
@click.option("--source-model", required=True, help="Original model")
@click.option("--target-model", required=True, help="Model to replay against")
@click.option("--concurrency", default=3, help="Max concurrent replays")
@click.option("--dry-run", is_flag=True, help="Estimate cost without sending")
def replay(from_date, to_date, source_model, target_model, concurrency, dry_run):
    """Replay stored prompts against a different model."""
    _run(_replay(from_date, to_date, source_model, target_model, concurrency, dry_run))


async def _replay(from_date, to_date, source_model, target_model, concurrency, dry_run):
    from .replay import run_replay
    db = Database()
    await db.init()
    result = await run_replay(db, source_model, target_model, from_date, to_date, concurrency, dry_run)
    await db.close()

    if result.get("error"):
        console.print(f"[red]{result['error']}[/]")
        return

    if result.get("dry_run"):
        console.print(f"\n[bold]Replay Dry Run[/]")
        console.print(f"  Prompts:        {result['prompt_count']}")
        console.print(f"  Target model:   {result['target_model']}")
        console.print(f"  Estimated cost: [yellow]${result['estimated_cost']:.4f}[/]")
        return

    console.print(f"\n[bold]Replay Complete[/]")
    console.print(f"  Prompts:   {result['prompt_count']}")
    console.print(f"  Source:    {result['source_model']}")
    console.print(f"  Target:    {result['target_model']}")
    console.print(f"  Successes: [green]{result.get('successes', 0)}[/]")
    console.print(f"  Errors:    [red]{result.get('errors', 0)}[/]")
    console.print()


# --- Upstream ---

@cli.group()
def upstream():
    """Manage upstream provider endpoints."""
    pass


@upstream.command("add")
@click.argument("api_type", type=click.Choice(["anthropic", "openai"]))
@click.argument("url")
@click.option("--priority", default=100, help="Priority (lower = preferred)")
def upstream_add(api_type, url, priority):
    """Add an upstream endpoint."""
    _run(_upstream_add(Upstream(api_type=api_type, base_url=url, priority=priority)))


async def _upstream_add(u):
    db = Database()
    await db.init()
    uid = await db.add_upstream(u)
    await db.close()
    console.print(f"[green]Upstream #{uid} added: {u.api_type} {u.base_url} (priority={u.priority})[/]")


@upstream.command("list")
def upstream_list():
    """Show all upstream endpoints."""
    _run(_upstream_list())


async def _upstream_list():
    db = Database()
    await db.init()
    upstreams = await db.get_upstreams()
    await db.close()

    if not upstreams:
        console.print("[dim]No upstreams configured (using defaults from config).[/]")
        return

    table = Table(show_header=True, header_style="bold cyan")
    table.add_column("ID", justify="right")
    table.add_column("Type")
    table.add_column("URL")
    table.add_column("Priority", justify="right")
    table.add_column("Health")
    table.add_column("Fails", justify="right")

    for u in upstreams:
        health = "[green]healthy[/]" if u.is_healthy else "[red]unhealthy[/]"
        table.add_row(str(u.id), u.api_type, u.base_url, str(u.priority), health, str(u.fail_count))
    console.print(table)


@upstream.command("remove")
@click.option("--id", "upstream_id", type=int, required=True)
def upstream_remove(upstream_id):
    """Remove an upstream endpoint."""
    _run(_upstream_remove(upstream_id))


async def _upstream_remove(upstream_id):
    db = Database()
    await db.init()
    await db.remove_upstream(upstream_id)
    await db.close()
    console.print(f"[green]Upstream #{upstream_id} removed.[/]")
