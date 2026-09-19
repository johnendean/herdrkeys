"""Hold changes briefly so agent-startup flapping never reaches the LEDs.

Two things settle, for the same reason and on the same clock: what a key
*shows* (`Settler`) and which key an agent *is* (`OrderSettler`).

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

from collections.abc import Iterable

from .model import AgentPane, AgentState

DEFAULT_SETTLE_SECONDS = 0.3

# The settled grid: the agents on the keys, in key order, each with the colour
# it wears when idle. A pair rather than a bare pane ID so that a recolour is
# settled by the same mechanism as a reorder -- see `OrderSettler`.
Grid = tuple[tuple[str, "str | None"], ...]


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


class OrderSettler:
    """Hold a change in the grid until it stops moving.

    What settles here is the ordered list of `(pane_id, colour)` pairs: which
    agent is on which key, and what colour that key wears when idle. Colour
    rides with the order rather than settling on its own because herdrcolor
    recolours a project when a *different* project appears or goes away, so a
    colour change is something an unrelated agent did -- the same shape of
    event as a reorder, and it must not flicker for the same reason (ADR 0010).

    The keys mirror the order `agent.list` returns (ADR 0009), so a single poll
    that omits an agent does not just darken one key -- it renumbers every key
    below it. The list is polled twice a second and `apply_agents` drops an
    agent the moment one poll leaves it out, so one blip would shuffle the board
    and the next poll would shuffle it back.

    What this does *not* do is protect a restart in place. Quitting an agent and
    starting another takes seconds, not milliseconds, so the keys below it will
    move and move back. That is the price of mirroring, paid knowingly: see
    ADR 0009.

    The whole order is one value, committed or not. Settling each agent's
    membership separately would be more precise and buy nothing, since an agent
    flapping in and out moves everything below it either way.

    Pure: the clock is injected.
    """

    def __init__(self, settle_seconds: float = DEFAULT_SETTLE_SECONDS) -> None:
        self.settle_seconds = settle_seconds
        self._committed: Grid = ()
        self._pending: tuple[Grid, float] | None = None

    def observe(self, panes: Iterable[AgentPane], now: float) -> None:
        candidate = tuple((pane.pane_id, pane.colour) for pane in panes)
        if candidate == self._committed:
            self._pending = None
            return
        if self._pending is None or self._pending[0] != candidate:
            self._pending = (candidate, now)

    def tick(self, now: float) -> None:
        if self._pending is None:
            return
        candidate, since = self._pending
        if now - since >= self.settle_seconds:
            self._committed = candidate
            self._pending = None

    def settled(self) -> Grid:
        """The grid safe to render. Empty until the first one has held still."""
        return self._committed

    def next_deadline(self, now: float) -> float | None:
        if self._pending is None:
            return None
        return max(0.0, self._pending[1] + self.settle_seconds - now)
