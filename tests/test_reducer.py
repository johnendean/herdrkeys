import pytest

from herdrkeys.model import (
    EMPTY,
    FEATURE_SLOTS,
    FN_CONNECTED,
    FN_DISCONNECTED,
    MIC,
    MIC_SLOT,
    AgentPane,
    AgentState,
)
from herdrkeys.reducer import (
    next_attention_target,
    pane_for_slot,
    render,
    sync_slots,
)
from herdrkeys.settling import Settler
from herdrkeys.slots import SlotMap


def pane(pane_id, state, focused=False):
    return AgentPane(pane_id=pane_id, agent="claude", state=state, focused=focused)


def settled(*panes):
    """A settler that has already committed everything it was shown."""
    settler = Settler(0.0)
    for p in panes:
        settler.observe(p.pane_id, p.state, 0.0)
    settler.tick(1.0)
    return settler


def test_frame_is_one_character_per_key():
    panes = {"a": pane("a", AgentState.WORKING)}
    slots = SlotMap({0: "a"})
    frame = render(panes, slots, settled(*panes.values()), connected=True)
    assert frame.keys == "w" + EMPTY * 11 + MIC + EMPTY * 2 + FN_CONNECTED


def test_focus_is_shown_by_case_not_by_a_different_state():
    panes = {"a": pane("a", AgentState.WORKING, focused=True)}
    frame = render(panes, SlotMap({0: "a"}), settled(*panes.values()), connected=True)
    assert frame.keys[0] == "W"


def test_disconnected_is_distinguishable_from_having_no_agents():
    empty_connected = render({}, SlotMap(), Settler(), connected=True)
    empty_disconnected = render({}, SlotMap(), Settler(), connected=False)
    assert empty_connected.keys[-1] == FN_CONNECTED
    assert empty_disconnected.keys[-1] == FN_DISCONNECTED
    assert empty_connected != empty_disconnected, "sixteen dark keys must not mean two things"


def test_a_slot_whose_agent_has_left_goes_dark_but_stays_reserved():
    slots = SlotMap({0: "a"})
    frame = render({}, slots, Settler(), connected=True)
    assert frame.keys[0] == EMPTY
    assert slots.slot_of("a") == 0, "the pane still exists, so it keeps its key"


def test_unsettled_pane_renders_dark():
    panes = {"a": pane("a", AgentState.UNKNOWN)}
    frame = render(panes, SlotMap({0: "a"}), Settler(0.3), connected=True)
    assert frame.keys[0] == EMPTY


# -- slot syncing --------------------------------------------------------


def test_sync_assigns_new_agent_panes_and_releases_closed_ones():
    slots = SlotMap()
    panes = {"a": pane("a", AgentState.IDLE), "b": pane("b", AgentState.IDLE)}
    assert sync_slots(panes, slots, {"a", "b"}) is True
    assert slots.slot_of("a") == 0 and slots.slot_of("b") == 1
    assert sync_slots(panes, slots, {"a", "b"}) is False, "steady state does not churn"


def test_restarting_an_agent_in_place_keeps_its_key():
    # The pane survives; only its agent came and went. Verified against Herdr:
    # pane 4 went agent=None -> Claude -> None -> Claude keeping its ID.
    slots = SlotMap()
    sync_slots({"w1:p1": pane("w1:p1", AgentState.IDLE)}, slots, {"w1:p1"})
    sync_slots({}, slots, {"w1:p1"})  # agent exited, pane still open
    assert slots.slot_of("w1:p1") == 0
    sync_slots({"w1:p1": pane("w1:p1", AgentState.WORKING)}, slots, {"w1:p1"})
    assert slots.slot_of("w1:p1") == 0


def test_closing_the_pane_is_what_frees_the_key():
    slots = SlotMap()
    sync_slots({"w1:p1": pane("w1:p1", AgentState.IDLE)}, slots, {"w1:p1"})
    assert sync_slots({}, slots, set()) is True
    assert slots.slot_of("w1:p1") is None


# -- the function key ----------------------------------------------------


def test_blocked_wins_over_done():
    panes = {
        "done": pane("done", AgentState.DONE),
        "blocked": pane("blocked", AgentState.BLOCKED),
    }
    slots = SlotMap({0: "done", 1: "blocked"})
    assert next_attention_target(panes, slots, settled(*panes.values())) == "blocked"


def test_nothing_to_go_to_returns_none():
    panes = {"a": pane("a", AgentState.WORKING), "b": pane("b", AgentState.IDLE)}
    slots = SlotMap({0: "a", 1: "b"})
    assert next_attention_target(panes, slots, settled(*panes.values())) is None


def test_repeated_presses_walk_the_queue_rather_than_sticking():
    panes = {
        "x": pane("x", AgentState.BLOCKED, focused=True),
        "y": pane("y", AgentState.BLOCKED),
        "z": pane("z", AgentState.BLOCKED),
    }
    slots = SlotMap({0: "x", 1: "y", 2: "z"})
    assert next_attention_target(panes, slots, settled(*panes.values())) == "y"

    panes["x"] = pane("x", AgentState.BLOCKED)
    panes["y"] = pane("y", AgentState.BLOCKED, focused=True)
    assert next_attention_target(panes, slots, settled(*panes.values())) == "z"

    panes["y"] = pane("y", AgentState.BLOCKED)
    panes["z"] = pane("z", AgentState.BLOCKED, focused=True)
    assert next_attention_target(panes, slots, settled(*panes.values())) == "x", "wraps"


def test_blocked_is_targetable_at_once_despite_a_long_settle_window():
    panes = {"a": pane("a", AgentState.BLOCKED)}
    slots = SlotMap({0: "a"})
    settler = Settler(999)
    settler.observe("a", AgentState.BLOCKED, 0.0)  # no tick: nothing has settled
    assert next_attention_target(panes, slots, settler) == "a"


def test_a_pane_that_has_not_settled_yet_is_not_a_target():
    panes = {"a": pane("a", AgentState.DONE)}
    slots = SlotMap({0: "a"})
    settler = Settler(999)
    settler.observe("a", AgentState.DONE, 0.0)
    assert next_attention_target(panes, slots, settler) is None


# -- key presses ---------------------------------------------------------


@pytest.mark.parametrize("slot", [0, 7, 11])
def test_press_resolves_to_the_pane_in_that_slot(slot):
    panes = {"a": pane("a", AgentState.IDLE)}
    assert pane_for_slot(slot, panes, SlotMap({slot: "a"})) == "a"


def test_press_on_an_empty_or_stale_slot_resolves_to_nothing():
    assert pane_for_slot(3, {}, SlotMap()) is None
    assert pane_for_slot(3, {}, SlotMap({3: "gone"})) is None


@pytest.mark.parametrize("slot", FEATURE_SLOTS)
def test_no_feature_key_is_an_agent_slot(slot):
    # Even a slot map that somehow binds one -- an older state file, a hand edit
    # -- must not turn a feature key into an agent key under a finger.
    assert pane_for_slot(slot, {"a": pane("a", AgentState.IDLE)}, SlotMap({slot: "a"})) is None


def test_the_feature_row_is_in_every_frame():
    # The mic key is not a status light: it is there whether or not Herdr is
    # reachable, because dictation has nothing to do with Herdr.
    for connected in (True, False):
        frame = render({}, SlotMap(), Settler(), connected=connected)
        assert frame.keys[MIC_SLOT] == MIC
        assert frame.keys[13] == frame.keys[14] == EMPTY, "13 and 14 are spare, and dark"
