"""Turn Herdr's view of the world into a frame, and resolve the function key.

Pure. This is where every interesting decision lives, which is why it takes
plain data and returns plain data -- no hardware and no running Herdr needed to
test any of it.

Keys are positions in Herdr's agent list, not bindings an agent holds: the first
agent Herdr lists is key 0, the second key 1, and so on (ADR 0009). Nothing here
remembers anything -- the order is an argument, so the whole grid is a function
of what Herdr says right now.
"""

from __future__ import annotations

from collections.abc import Iterable, Sequence

from .model import (
    AGENT_SLOTS,
    AgentPane,
    AgentState,
    Frame,
    feature_keys,
    state_code,
)
from .settling import Settler

# Attention order for the function key: `blocked` has a human waiting on it,
# `done` finished unseen, everything else does not want you.
ATTENTION_ORDER = (AgentState.BLOCKED, AgentState.DONE)


def on_the_grid(order: Iterable[str]) -> list[str]:
    """The prefix of the agent list the grid has room for.

    Past twelve agents the rest are simply not on the board, and not reachable
    by the function key either. That is what the old scheme did too -- the
    thirteenth agent to appear got no key and could not be jumped to -- so
    nothing regresses here; it is just now the last in Herdr's order that misses
    out rather than the last to arrive.
    """
    return list(order)[: len(AGENT_SLOTS)]


def render(
    agent_panes: dict[str, AgentPane],
    order: Sequence[str],
    settler: Settler,
    *,
    connected: bool,
    repo_page: bool = False,
) -> Frame:
    """`repo_page` is passed in rather than worked out here.

    Deciding it means touching the filesystem, and this module stays pure: it
    is the one place every interesting decision can be tested without a running
    Herdr, a keypad, or a checkout on disk.
    """
    keys = feature_keys(connected=connected, repo_page=repo_page)
    for slot, pane_id in enumerate(on_the_grid(order)):
        pane = agent_panes.get(pane_id)
        if pane is None:
            # In the settled order but no longer an agent: the order has not
            # caught up yet. Leave a hole for the moment rather than shifting
            # every key below it twice.
            continue
        state = settler.settled(pane_id)
        if state is None:
            continue  # not settled yet: stay dark rather than strobe
        keys[slot] = state_code(state, focused=pane.focused)
    return Frame("".join(keys))


def next_attention_target(
    agent_panes: dict[str, AgentPane],
    order: Sequence[str],
    settler: Settler,
) -> str | None:
    """The pane the function key should focus, or None if nothing wants you.

    Blocked agents first, then done, each in key order wrapping from whichever
    key is focused now, so repeated presses walk the queue instead of sticking.

    The wrap is over the agents that are actually on the grid, not over the
    twelve slots: with no holes left to skip, the two are the same walk, and
    counting agents means the queue is right however few there are.
    """
    on_grid = on_the_grid(order)
    if not on_grid:
        return None

    start = 0
    for slot, pane_id in enumerate(on_grid):
        pane = agent_panes.get(pane_id)
        if pane is not None and pane.focused:
            start = slot + 1
            break

    rotated = [on_grid[(start + offset) % len(on_grid)] for offset in range(len(on_grid))]
    for wanted in ATTENTION_ORDER:
        for pane_id in rotated:
            pane = agent_panes.get(pane_id)
            if pane is None or pane.focused:
                continue
            if settler.settled(pane_id) is wanted:
                return pane_id
    return None


def pane_for_slot(
    slot: int, agent_panes: dict[str, AgentPane], order: Sequence[str]
) -> str | None:
    """The pane a key press should focus, or None if that key holds nothing.

    A feature-row slot falls out of this naturally: the grid is at most twelve
    long, so 12 to 15 are always past its end.
    """
    on_grid = on_the_grid(order)
    if slot < 0 or slot >= len(on_grid):
        return None
    pane_id = on_grid[slot]
    return pane_id if pane_id in agent_panes else None
