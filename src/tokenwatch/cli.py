"""CLI interface for TokenWatch."""

import asyncio
import logging
import signal
import sys

import click
import uvicorn
from rich.console import Console
from rich.live import Live
from rich.table import Table

from . import __version__
from .config import DASHBOARD_PORT, DB_PATH, PROXY_PORT
from .db import Database

console = Console()


@click.group()
@click.version_option(__version__)
def cli():
    """TokenWatch - Real-time API token usage proxy."""
    pass


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
    console.print(f"  Database:  {DB_PATH}")
    console.print()
    console.print("[dim]Configure your apps to use:[/]")
    console.print(f"[dim]  Anthropic: http://localhost:{proxy_port}/anthropic[/dim]")
    console.print(f"[dim]  OpenAI:    http://localhost:{proxy_port}/openai[/dim]")
    console.print()

    import threading

    # Start dashboard in a thread
    def run_dashboard():
        from .dashboard_app import create_dashboard_app

        dash_app = create_dashboard_app()
        uvicorn.run(dash_app, host="0.0.0.0", port=dashboard_port, log_level=log_level)

    dash_thread = threading.Thread(target=run_dashboard, daemon=True)
    dash_thread.start()

    # Run proxy in main thread
    uvicorn.run(
        "tokenwatch.proxy:app",
        host="0.0.0.0",
        port=proxy_port,
        log_level=log_level,
    )


@cli.command()
@click.option("--timeframe", "-t", default="24h", type=click.Choice(["1h", "24h", "7d", "30d", "all"]))
def stats(timeframe):
    """Show token usage statistics."""
    asyncio.run(_show_stats(timeframe))


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
    console.print()


@cli.command()
@click.option("--limit", "-n", default=20, help="Number of recent requests to show")
def tail(limit):
    """Show recent requests (live updating)."""
    asyncio.run(_tail(limit))


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
        for r in rows:
            api_style = "blue" if r["api_type"] == "anthropic" else "green"
            t.add_row(
                r["created_at"].split("T")[-1][:8] if "T" in r.get("created_at", "") else str(r.get("created_at", "")),
                f"[{api_style}]{r['api_type']}[/]",
                r["model"],
                f"{r['input_tokens']:,}",
                f"{r['output_tokens']:,}",
                f"{r['latency_ms']}ms",
                f"${r['estimated_cost']:.4f}" if r.get("estimated_cost") else "-",
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


@cli.command()
@click.confirmation_option(prompt="Are you sure you want to clear all usage data?")
def reset():
    """Clear all usage data from the database."""
    asyncio.run(_reset())


async def _reset():
    db = Database()
    await db.init()
    await db.reset()
    await db.close()
    console.print("[green]Database cleared.[/]")
