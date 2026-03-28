"""Tests for the WebSocket connection manager."""

import asyncio
import json

import pytest

from tokenwatch.ws import ConnectionManager


class FakeWebSocket:
    def __init__(self, should_fail=False):
        self.should_fail = should_fail
        self.accepted = False
        self.messages = []

    async def accept(self):
        self.accepted = True

    async def send_text(self, message):
        if self.should_fail:
            raise RuntimeError("socket closed")
        self.messages.append(message)


@pytest.mark.asyncio
async def test_connection_manager_broadcasts_and_prunes_disconnected_clients():
    manager = ConnectionManager()
    healthy = FakeWebSocket()
    broken = FakeWebSocket(should_fail=True)

    await manager.connect(healthy)
    await manager.connect(broken)
    await manager.broadcast({"type": "ping", "data": {"ok": True}})

    assert healthy.accepted is True
    assert json.loads(healthy.messages[0]) == {"type": "ping", "data": {"ok": True}}
    assert manager.active_connections == [healthy]


@pytest.mark.asyncio
async def test_connection_manager_disconnect_is_idempotent():
    manager = ConnectionManager()
    websocket = FakeWebSocket()

    await manager.connect(websocket)
    manager.disconnect(websocket)
    manager.disconnect(websocket)

    assert manager.active_connections == []


@pytest.mark.asyncio
async def test_connection_manager_handles_concurrent_broadcasts():
    manager = ConnectionManager()
    websocket = FakeWebSocket()

    await manager.connect(websocket)
    await asyncio.gather(
        manager.broadcast({"type": "first"}),
        manager.broadcast({"type": "second"}),
    )

    assert sorted(json.loads(message)["type"] for message in websocket.messages) == ["first", "second"]
