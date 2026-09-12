"""Turn Herdr's view of the world into a frame, and resolve the function key.

Pure. This is where every interesting decision lives, which is why it takes
plain data and returns plain data -- no hardware and no running Herdr needed to
test any of it.
"""

from __future__ import annotations

from .model import (
    AGENT_SLOTS,
    AgentPane,
    AgentState,
    Frame,
    feature_keys,
    state_code,
)
from .settling import Settler
from .slots import SlotMap

# Attention order for the function key: `blocked` has a human waiting on it,
# `done` finished unseen, everything else does not want you.
ATTENTION_ORDER = (AgentState.BLOCKED, AgentState.DONE)


def render(
    agent_panes: dict[str, AgentPane],
    slots: SlotMap,
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
    for slot in AGENT_SLOTS:
        pane_id = slots.pane_at(slot)
        if pane_id is None:
            continue
        pane = agent_panes.get(pane_id)
        if pane is None:
            continue  # slot still reserved for a pane whose agent has left
        state = settler.settled(pane_id)
        if state is None:
            continue  # not settled yet: stay dark rather than strobe
        keys[slot] = state_code(state, focused=pane.focused)
    return Frame("".join(keys))


def sync_slots(agent_panes: dict[str, AgentPane], slots: SlotMap, live_pane_ids: set[str]) -> bool:
    """Assign slots to new agent panes and release closed panes.

    A slot is released when the *pane* goes away, not when its agent exits, so
    restarting an agent in place keeps its key. Returns whether anything moved.
    """
    changed = False
    for pane_id in sorted(agent_panes):
        if slots.slot_of(pane_id) is None:
            if slots.assign(pane_id) is not None:
                changed = True
    for _slot, pane_id in list(slots.items()):
        if pane_id not in live_pane_ids:
            slots.release(pane_id)
            changed = True
    return changed


def next_attention_target(
    agent_panes: dict[str, AgentPane],
    slots: SlotMap,
    settler: Settler,
) -> str | None:
    """The pane the function key should focus, or None if nothing wants you.

    Blocked agents first, then done, each in slot order wrapping from whichever
    slot is focused now, so repeated presses walk the queue instead of sticking.
    """
    start = 0
    for slot, pane_id in slots.items():
        pane = agent_panes.get(pane_id)
        if pane is not None and pane.focused:
            start = slot + 1
            break

    ordered = sorted(AGENT_SLOTS, key=lambda s: (s - start) % len(AGENT_SLOTS))
    for wanted in ATTENTION_ORDER:
        for slot in ordered:
            pane_id = slots.pane_at(slot)
            if pane_id is None:
                continue
            pane = agent_panes.get(pane_id)
            if pane is None or pane.focused:
                continue
            if settler.settled(pane_id) is wanted:
                return pane_id
    return None


def pane_for_slot(slot: int, agent_panes: dict[str, AgentPane], slots: SlotMap) -> str | None:
    """The pane a key press should focus, or None if that key holds nothing."""
    if slot not in AGENT_SLOTS:
        return None
    pane_id = slots.pane_at(slot)
    if pane_id is None or pane_id not in agent_panes:
        return None
    return pane_id
