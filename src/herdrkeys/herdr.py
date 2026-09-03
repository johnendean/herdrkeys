"""Talking to Herdr over its unix socket.

Newline-delimited JSON. Two connection styles, deliberately:

* a long-lived connection that stays open after `events.subscribe` and receives
  pushed events, and
* a fresh short-lived connection per request.

Requests are rare (one per key press) and mixing them into the subscription
connection would mean demultiplexing responses out of the event stream for no
benefit.
"""

from __future__ import annotations

import json
import os
import socket
from pathlib import Path
from typing import Any

# Global subscriptions. `pane.agent_status_changed` is deliberately absent: it
# requires a specific pane_id, so it cannot be subscribed to globally. The
# `pane_updated` payload carries `agent` and `agent_status` anyway.
SUBSCRIPTIONS = [
    {"type": "pane.created"},
    {"type": "pane.updated"},
    {"type": "pane.closed"},
    {"type": "pane.exited"},
    {"type": "pane.focused"},
    {"type": "pane.moved"},
    {"type": "pane.agent_detected"},
]


class HerdrError(RuntimeError):
    pass


def default_socket_path() -> Path:
    explicit = os.environ.get("HERDR_SOCKET_PATH")
    if explicit:
        return Path(explicit)
    base = Path.home() / ".config" / "herdr"
    session = os.environ.get("HERDR_SESSION")
    if session:
        return base / "sessions" / session / "herdr.sock"
    return base / "herdr.sock"


def _connect(path: Path, timeout: float) -> socket.socket:
    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    sock.settimeout(timeout)
    sock.connect(str(path))
    return sock


def request(path: Path, method: str, params: dict[str, Any] | None = None, *, timeout: float = 5.0) -> dict[str, Any]:
    """One request on its own connection. Raises HerdrError on an error reply."""
    payload = {"id": f"herdrkeys:{method}", "method": method, "params": params or {}}
    with _connect(path, timeout) as sock:
        sock.sendall((json.dumps(payload) + "\n").encode())
        buffer = b""
        while b"\n" not in buffer:
            chunk = sock.recv(65536)
            if not chunk:
                raise HerdrError(f"{method}: connection closed before a reply")
            buffer += chunk
    reply = json.loads(buffer.split(b"\n", 1)[0])
    if "error" in reply:
        error = reply["error"]
        raise HerdrError(f"{method}: {error.get('code')}: {error.get('message')}")
    return reply.get("result") or {}


def snapshot(path: Path, *, timeout: float = 5.0) -> dict[str, Any]:
    return request(path, "session.snapshot", timeout=timeout).get("snapshot") or {}


def focus_agent(path: Path, pane_id: str, *, timeout: float = 5.0) -> None:
    """Focus the agent in a pane.

    `agent.focus` rather than `pane.focus` because focusing through the agent
    surface marks the agent seen, which is what turns `done` back into `idle`.
    It accepts a pane ID as its target. If the agent has left the pane since the
    frame was drawn, fall back to focusing the pane itself.
    """
    try:
        request(path, "agent.focus", {"target": pane_id}, timeout=timeout)
    except HerdrError:
        request(path, "pane.focus", {"pane_id": pane_id}, timeout=timeout)


class EventStream:
    """A subscribed connection. Non-blocking; poll it with `fileno()`.

    Herdr replays a backlog of past events on subscribe, so the first events off
    this stream are history, not news. `HerdrState` discards stale updates by
    per-pane revision, and the daemon reconciles against `session.snapshot`
    periodically, so neither a truncated nor a replayed backlog can strand the
    view.
    """

    def __init__(self, path: Path, *, timeout: float = 5.0) -> None:
        self._sock = _connect(path, timeout)
        payload = {
            "id": "herdrkeys:subscribe",
            "method": "events.subscribe",
            "params": {"subscriptions": SUBSCRIPTIONS},
        }
        self._sock.sendall((json.dumps(payload) + "\n").encode())
        self._buffer = b""
        self._await_ack()
        self._sock.setblocking(False)

    def _await_ack(self) -> None:
        while b"\n" not in self._buffer:
            chunk = self._sock.recv(65536)
            if not chunk:
                raise HerdrError("subscribe: connection closed before acknowledgement")
            self._buffer += chunk
        line, self._buffer = self._buffer.split(b"\n", 1)
        reply = json.loads(line)
        if "error" in reply:
            raise HerdrError(f"subscribe: {reply['error'].get('message')}")

    def fileno(self) -> int:
        return self._sock.fileno()

    def read_events(self) -> list[dict[str, Any]]:
        """Drain whatever has arrived. Raises HerdrError when Herdr goes away."""
        try:
            chunk = self._sock.recv(65536)
        except BlockingIOError:
            return []
        if not chunk:
            raise HerdrError("event stream closed")
        self._buffer += chunk
        events = []
        while b"\n" in self._buffer:
            line, self._buffer = self._buffer.split(b"\n", 1)
            if not line.strip():
                continue
            try:
                events.append(json.loads(line))
            except ValueError:
                continue
        return events

    def close(self) -> None:
        try:
            self._sock.close()
        except OSError:
            pass
