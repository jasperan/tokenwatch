"""WebSocket connection manager for real-time dashboard updates."""

import asyncio
import json
import logging

from fastapi import WebSocket

logger = logging.getLogger("tokenwatch")


class ConnectionManager:
    def __init__(self):
        self.active_connections: list[WebSocket] = []

    async def connect(self, websocket: WebSocket):
        await websocket.accept()
        self.active_connections.append(websocket)

    def disconnect(self, websocket: WebSocket):
        if websocket in self.active_connections:
            self.active_connections.remove(websocket)

    async def broadcast(self, data: dict):
        if not self.active_connections:
            return

        message = json.dumps(data, default=str)
        connections = list(self.active_connections)
        results = await asyncio.gather(
            *(connection.send_text(message) for connection in connections),
            return_exceptions=True,
        )
        for connection, result in zip(connections, results):
            if isinstance(result, Exception) and connection in self.active_connections:
                self.active_connections.remove(connection)
