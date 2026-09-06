"""Fold Herdr's event stream into a view of the panes.

Pure. Feed it snapshots and events; ask it for agent panes.

Two things about Herdr's stream shape drive this design:

* `events.subscribe` replays a backlog on connect, so events can arrive that are
  older than a snapshot taken afterwards. Every `pane_updated` carries a
  monotonic per-pane `revision`, so stale updates are discarded by revision.
* The stream cannot be trusted to keep arriving, so agent status is polled with
  `agent.list` and folded by `apply_agents`. `pane_updated` carries the whole
  pane object, status included, so events still count -- they just are not the
  thing being waited on. See ADR 0005.
"""

from __future__ import annotations

from dataclasses import dataclass, replace
from typing import Any

from .model import AgentPane, AgentState


@dataclass(frozen=True)
class PaneRecord:
    pane_id: str
    agent: str | None
    state: AgentState
    revision: int

    @property
    def is_agent_pane(self) -> bool:
        return self.agent is not None


def _state_of(raw: Any) -> AgentState:
    try:
        return AgentState(raw)
    except ValueError:
        return AgentState.UNKNOWN


def _record_from_pane(pane: dict[str, Any]) -> PaneRecord:
    return PaneRecord(
        pane_id=pane["pane_id"],
        agent=pane.get("agent"),
        state=_state_of(pane.get("agent_status")),
        revision=int(pane.get("revision", 0)),
    )


class HerdrState:
    def __init__(self) -> None:
        self.panes: dict[str, PaneRecord] = {}
        self.focused_pane_id: str | None = None

    # -- ingest ----------------------------------------------------------

    def apply_snapshot(self, snapshot: dict[str, Any]) -> None:
        """Replace everything. The snapshot is authoritative."""
        self.panes = {}
        for pane in snapshot.get("panes") or []:
            record = _record_from_pane(pane)
            self.panes[record.pane_id] = record
        self.focused_pane_id = snapshot.get("focused_pane_id")

    def apply_agents(self, rows: list[dict[str, Any]]) -> None:
        """Fold a status poll: authoritative about state, silent about lifetime.

        `agent.list` answers "which panes have an agent, and what is it doing",
        so a pane it does not mention either has no agent or is not a pane at
        all. Panes are therefore updated and released here, never forgotten --
        forgetting stays with the snapshot and `pane_closed`, so a slot outlives
        the agent that was in it (ADR 0002).
        """
        seen = set()
        focused, unfocused = None, set()
        for row in rows:
            if not row.get("pane_id"):
                continue
            record = _record_from_pane(row)
            seen.add(record.pane_id)
            existing = self.panes.get(record.pane_id)
            if existing is not None:
                # Hold the high-water revision, so a `pane_updated` replayed
                # after this poll cannot overwrite it through the guard below.
                record = replace(record, revision=max(record.revision, existing.revision))
            self.panes[record.pane_id] = record
            if row.get("focused"):
                focused = record.pane_id
            else:
                unfocused.add(record.pane_id)
        if focused is not None:
            self.focused_pane_id = focused
        elif self.focused_pane_id in unfocused:
            # Focus has moved to a pane with no agent. `agent.list` cannot say
            # which one, but it can say it is no longer this one, and a key left
            # bright is a key claiming focus it does not have.
            self.focused_pane_id = None
        for pane_id, record in list(self.panes.items()):
            if record.is_agent_pane and pane_id not in seen:
                # The agent left the pane; the pane itself survives.
                self.panes[pane_id] = replace(record, agent=None, state=AgentState.UNKNOWN)

    def apply_event(self, event: dict[str, Any]) -> None:
        """Fold one event. Unknown or stale events are ignored."""
        data = event.get("data") or {}
        kind = data.get("type") or event.get("event")

        if kind in ("pane_updated", "pane_created"):
            self._upsert(data.get("pane") or {})
        elif kind == "pane_moved":
            # The pane keeps its identity but gets a new workspace-qualified ID.
            previous = data.get("previous_pane_id")
            if previous:
                self.panes.pop(previous, None)
                if self.focused_pane_id == previous:
                    self.focused_pane_id = None
            self._upsert(data.get("pane") or {}, force=True)
        elif kind in ("pane_closed", "pane_exited"):
            pane_id = data.get("pane_id")
            if pane_id:
                self.panes.pop(pane_id, None)
                if self.focused_pane_id == pane_id:
                    self.focused_pane_id = None
        elif kind == "pane_focused":
            self.focused_pane_id = data.get("pane_id")
        elif kind == "pane_agent_detected":
            self._agent_detected(data)

    def _upsert(self, pane: dict[str, Any], *, force: bool = False) -> None:
        if not pane.get("pane_id"):
            return
        record = _record_from_pane(pane)
        existing = self.panes.get(record.pane_id)
        if existing is not None and not force and record.revision < existing.revision:
            return  # backlog replay: an update older than what we already hold
        self.panes[record.pane_id] = record
        if pane.get("focused"):
            self.focused_pane_id = record.pane_id

    def _agent_detected(self, data: dict[str, Any]) -> None:
        pane_id = data.get("pane_id")
        if not pane_id:
            return
        existing = self.panes.get(pane_id)
        if data.get("released"):
            # The agent left the pane; the pane itself survives.
            if existing is not None:
                self.panes[pane_id] = replace(
                    existing, agent=None, state=AgentState.UNKNOWN
                )
            return
        agent = data.get("agent")
        if existing is None:
            self.panes[pane_id] = PaneRecord(
                pane_id=pane_id, agent=agent, state=AgentState.UNKNOWN, revision=0
            )
        else:
            self.panes[pane_id] = replace(existing, agent=agent)

    # -- queries ---------------------------------------------------------

    def agent_panes(self) -> dict[str, AgentPane]:
        return {
            record.pane_id: AgentPane(
                pane_id=record.pane_id,
                agent=record.agent or "",
                state=record.state,
                focused=record.pane_id == self.focused_pane_id,
            )
            for record in self.panes.values()
            if record.is_agent_pane
        }

    def live_pane_ids(self) -> set[str]:
        return set(self.panes)
