"""Hold state changes briefly so agent-startup flapping never reaches the LEDs.

Herdr really emits this while an agent boots (captured from a live session):

    revision 8  -> agent_status "unknown", no agent field
    revision 9  -> agent "claude", "unknown"
    revision 10 -> agent "claude", "working"

...inside about two seconds. Rendered directly, that is a blue strobe on every
agent start.

Settling is asymmetric: a transition into `blocked` commits immediately, because
a human is waiting on it. Everything else waits for the state to hold still.

Pure: the clock is injected.
"""

from __future__ import annotations

from .model import AgentState

DEFAULT_SETTLE_SECONDS = 0.3


class Settler:
    def __init__(self, settle_seconds: float = DEFAULT_SETTLE_SECONDS) -> None:
        self.settle_seconds = settle_seconds
        self._committed: dict[str, AgentState] = {}
        self._pending: dict[str, tuple[AgentState, float]] = {}

    def observe(self, pane_id: str, state: AgentState, now: float) -> None:
        if self._committed.get(pane_id) == state:
            self._pending.pop(pane_id, None)
            return
        if state is AgentState.BLOCKED:
            self._committed[pane_id] = state
            self._pending.pop(pane_id, None)
            return
        pending = self._pending.get(pane_id)
        if pending is None or pending[0] != state:
            self._pending[pane_id] = (state, now)

    def tick(self, now: float) -> None:
        """Commit anything that has held still long enough."""
        for pane_id, (state, since) in list(self._pending.items()):
            if now - since >= self.settle_seconds:
                self._committed[pane_id] = state
                del self._pending[pane_id]

    def settled(self, pane_id: str) -> AgentState | None:
        """The state safe to render, or None if nothing has settled yet.

        A pane that has just appeared has no settled state, so its key stays
        dark for at most `settle_seconds` rather than strobing through the
        states Herdr emits while the agent boots.
        """
        return self._committed.get(pane_id)

    def forget(self, pane_id: str) -> None:
        self._committed.pop(pane_id, None)
        self._pending.pop(pane_id, None)

    def retain(self, pane_ids: set[str]) -> None:
        for pane_id in list(self._committed):
            if pane_id not in pane_ids:
                del self._committed[pane_id]
        for pane_id in list(self._pending):
            if pane_id not in pane_ids:
                del self._pending[pane_id]

    def next_deadline(self, now: float) -> float | None:
        """When the event loop must wake to commit a pending change."""
        if not self._pending:
            return None
        soonest = min(since for _, since in self._pending.values())
        return max(0.0, soonest + self.settle_seconds - now)
