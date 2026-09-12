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
# requires a specific pane_id, so it cannot be subscribed to globally.
#
# None of these is trusted for agent status. The stream replays a backlog and
# then goes quiet (ADR 0005), so status is polled with `agent.list` and these
# events only shorten the wait when they do arrive.
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


def agents(path: Path, *, timeout: float = 5.0) -> list[dict[str, Any]]:
    """Every agent pane and the state it is in, right now.

    The status source. `agent.list` is cheap enough to poll -- a round trip
    measured at 0.22ms against a live session -- and unlike the event stream it
    cannot fall behind: it is a question, not a feed. See ADR 0005.
    """
    return request(path, "agent.list", timeout=timeout).get("agents") or []


def focus_agent(path: Path, pane_id: str, *, timeout: float = 5.0) -> None:
    """Focus the agent in a pane, by focusing the pane.

    `pane.focus` is what moves the viewport, and it is the only part of this
    that must succeed.

    Not `agent.focus`, which ADR 0001 chose and which no longer works. Herdr
    0.9.0 moved the terminal UI into each client, and `agent.focus` was left
    behind: it still updates session focus, still logs `workspace focused` and
    `tab focused`, still answers `ok` -- and never reaches a surface-active
    client, so nothing on screen moves. Its sibling focus calls were not
    affected; `pane.focus` still propagates, and lands on the exact pane even
    when the hop crosses a workspace. Upstream is herdr#3760, fixed on master
    and unreleased as of 0.9.0, so this cannot simply be waited out.

    `agent.focus` is then sent anyway, and its failure ignored. Marking an agent
    seen -- what turns `done` back into `idle` -- is the reason ADR 0001 went
    through the agent surface, and the server keeps a seen state of its own that
    `agent.list` reports and the keys are drawn from. Going through the agent
    surface is the documented way to set it, it still works server-side, and it
    costs a fraction of a millisecond. It is sent second because it is the
    expendable one: if the agent has left the pane since the frame was drawn it
    raises `agent_not_found`, which must not cost us the navigation we already
    did.
    """
    request(path, "pane.focus", {"pane_id": pane_id}, timeout=timeout)
    try:
        request(path, "agent.focus", {"target": pane_id}, timeout=timeout)
    except HerdrError:
        pass


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
