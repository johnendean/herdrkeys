import pytest

from herdrkeys.model import (
    AGENT_SLOTS,
    EMPTY,
    FEATURE_SLOTS,
    FN_CONNECTED,
    FN_DISCONNECTED,
    MIC,
    REPO_NO_PAGE,
    REPO_SLOT,
    AgentPane,
    AgentState,
)
from herdrkeys.reducer import (
    next_attention_target,
    on_the_grid,
    pane_for_slot,
    pane_ids,
    render,
)
from herdrkeys.settling import Settler


def pane(pane_id, state, focused=False):
    return AgentPane(pane_id=pane_id, agent="claude", state=state, focused=focused)


def panes(*specs):
    """An ordered agent list, exactly as `HerdrState.agent_panes` hands one over."""
    return {p.pane_id: p for p in specs}


def grid(*specs):
    """A settled grid: each key's pane, and the colour it wears when idle.

    Written `"a"` for a pane with no project colour, `("a", "#a6e3a1")` for one
    with.
    """
    return tuple(spec if isinstance(spec, tuple) else (spec, None) for spec in specs)


def settled(*agent_panes):
    """A settler that has already committed everything it was shown."""
    settler = Settler(0.0)
    for p in agent_panes:
        settler.observe(p.pane_id, p.state, 0.0)
    settler.tick(1.0)
    return settler


def test_frame_is_one_character_per_key():
    live = panes(pane("a", AgentState.WORKING))
    frame = render(live, grid("a"), settled(*live.values()), connected=True)
    assert frame.keys == "w" + EMPTY * 11 + MIC + REPO_NO_PAGE + EMPTY + FN_CONNECTED


def test_focus_is_shown_by_case_not_by_a_different_state():
    live = panes(pane("a", AgentState.WORKING, focused=True))
    frame = render(live, grid("a"), settled(*live.values()), connected=True)
    assert frame.keys[0] == "W"


def test_disconnected_is_distinguishable_from_having_no_agents():
    empty_connected = render({}, grid(), Settler(), connected=True)
    empty_disconnected = render({}, grid(), Settler(), connected=False)
    assert empty_connected.keys[-1] == FN_CONNECTED
    assert empty_disconnected.keys[-1] == FN_DISCONNECTED
    assert empty_connected != empty_disconnected, "sixteen dark keys must not mean two things"


def test_unsettled_pane_renders_dark():
    live = panes(pane("a", AgentState.UNKNOWN))
    frame = render(live, grid("a"), Settler(0.3), connected=True)
    assert frame.keys[0] == EMPTY


def test_an_agent_gone_from_the_list_leaves_a_hole_until_the_order_settles():
    # The order is settled separately and lags by up to settle_seconds, so for
    # that moment it still names an agent that has gone. Blanking that one key
    # is right: shifting everything below it now, and again when the new order
    # commits, would move the survivors twice for one departure.
    live = panes(pane("a", AgentState.WORKING), pane("c", AgentState.IDLE))
    frame = render(live, grid("a", "b", "c"), settled(*live.values()), connected=True)
    assert frame.keys[:3] == "w" + EMPTY + "i"


# -- the order is the key numbering --------------------------------------


def test_keys_follow_herdrs_order_not_the_order_agents_arrived():
    live = panes(pane("b", AgentState.WORKING), pane("a", AgentState.IDLE))
    frame = render(live, grid("b", "a"), settled(*live.values()), connected=True)
    assert frame.keys[:2] == "wi", "first in the list is key 0, whenever it showed up"


def test_closing_an_agent_moves_the_ones_below_it_up():
    before = panes(
        pane("a", AgentState.IDLE), pane("b", AgentState.WORKING), pane("c", AgentState.DONE)
    )
    assert render(before, grid("a", "b", "c"), settled(*before.values()), connected=True).keys[:3] == "iwd"

    after = panes(pane("a", AgentState.IDLE), pane("c", AgentState.DONE))
    assert render(after, grid("a", "c"), settled(*after.values()), connected=True).keys[:3] == "id-", (
        "c takes b's key rather than leaving a gap behind"
    )


def test_an_agent_appearing_above_pushes_the_others_down():
    # The cost of mirroring, stated as a test so nobody mistakes it for a bug:
    # a new agent at the top of Herdr's list renumbers every key below it.
    after = panes(pane("new", AgentState.WORKING), pane("a", AgentState.IDLE))
    frame = render(after, grid("new", "a"), settled(*after.values()), connected=True)
    assert frame.keys[:2] == "wi"


def test_the_grid_holds_twelve_agents_and_no_more():
    ids = [f"w{n}:p1" for n in range(15)]
    live = panes(*(pane(i, AgentState.BLOCKED) for i in ids))
    frame = render(live, grid(*ids), settled(*live.values()), connected=True)
    assert frame.keys[: len(AGENT_SLOTS)] == "b" * 12
    for slot in FEATURE_SLOTS:
        assert frame.keys[slot] != "b", "the feature row never holds an agent"
    assert pane_ids(on_the_grid(grid(*ids))) == ids[:12]


# -- which key focuses what ----------------------------------------------


def test_a_key_press_focuses_the_agent_at_that_position():
    live = panes(pane("a", AgentState.IDLE), pane("b", AgentState.IDLE))
    assert pane_for_slot(0, live, grid("a", "b")) == "a"
    assert pane_for_slot(1, live, grid("a", "b")) == "b"
    assert pane_for_slot(2, live, grid("a", "b")) is None, "past the end of the list"


@pytest.mark.parametrize("slot", FEATURE_SLOTS)
def test_the_feature_row_never_focuses_an_agent(slot):
    ids = [f"w{n}:p1" for n in range(15)]
    live = panes(*(pane(i, AgentState.IDLE) for i in ids))
    assert pane_for_slot(slot, live, grid(*ids)) is None


def test_a_key_whose_agent_has_gone_focuses_nothing():
    live = panes(pane("a", AgentState.IDLE))
    assert pane_for_slot(1, live, grid("a", "b")) is None


# -- the function key ----------------------------------------------------


def test_blocked_wins_over_done():
    live = panes(pane("done", AgentState.DONE), pane("blocked", AgentState.BLOCKED))
    assert next_attention_target(live, grid("done", "blocked"), settled(*live.values())) == "blocked"


def test_nothing_to_go_to_returns_none():
    live = panes(pane("a", AgentState.WORKING), pane("b", AgentState.IDLE))
    assert next_attention_target(live, grid("a", "b"), settled(*live.values())) is None


def test_no_agents_at_all_returns_none():
    assert next_attention_target({}, grid(), Settler()) is None


def test_repeated_presses_walk_the_queue_rather_than_sticking():
    order = grid("x", "y", "z")
    live = panes(
        pane("x", AgentState.BLOCKED, focused=True),
        pane("y", AgentState.BLOCKED),
        pane("z", AgentState.BLOCKED),
    )
    assert next_attention_target(live, order, settled(*live.values())) == "y"

    live["x"] = pane("x", AgentState.BLOCKED)
    live["y"] = pane("y", AgentState.BLOCKED, focused=True)
    assert next_attention_target(live, order, settled(*live.values())) == "z"

    live["y"] = pane("y", AgentState.BLOCKED)
    live["z"] = pane("z", AgentState.BLOCKED, focused=True)
    assert next_attention_target(live, order, settled(*live.values())) == "x", "wraps"


def test_the_queue_wraps_over_the_agents_there_are_not_the_twelve_slots():
    # Two agents, the second focused: the wrap must come back to the first
    # rather than walking ten empty slots and falling off the end.
    order = grid("a", "b")
    live = panes(pane("a", AgentState.BLOCKED), pane("b", AgentState.BLOCKED, focused=True))
    assert next_attention_target(live, order, settled(*live.values())) == "a"


def test_an_agent_past_the_grid_cannot_be_jumped_to():
    ids = [f"w{n}:p1" for n in range(13)]
    states = [AgentState.WORKING] * 12 + [AgentState.BLOCKED]
    live = panes(*(pane(i, s) for i, s in zip(ids, states)))
    assert next_attention_target(live, grid(*ids), settled(*live.values())) is None, (
        "the thirteenth agent has no key, so the function key cannot send you to it"
    )


# -- project colours ------------------------------------------------------


GREEN = "#a6e3a1"
BLUE = "#89b4fa"


def test_a_project_colour_rides_along_with_its_key():
    live = panes(pane("a", AgentState.IDLE), pane("b", AgentState.WORKING))
    frame = render(live, grid(("a", GREEN), ("b", BLUE)), settled(*live.values()), connected=True)
    assert frame.colours[0] == GREEN
    assert frame.colours[1] == BLUE, (
        "sent for every state, because which ones wear it is the device's call"
    )
    assert frame.keys[:2] == "iw", "and the meaning codes are untouched"


def test_a_key_with_no_project_colour_carries_none():
    live = panes(pane("a", AgentState.IDLE))
    frame = render(live, grid("a"), settled(*live.values()), connected=True)
    assert frame.colours[0] is None, "the fallback is an absence, not a colour"
    assert frame.keys[0] == "i"


def test_the_feature_row_never_carries_a_colour():
    live = panes(pane("a", AgentState.IDLE))
    frame = render(live, grid(("a", GREEN)), settled(*live.values()), connected=True)
    for slot in FEATURE_SLOTS:
        assert frame.colours[slot] is None


def test_a_key_that_has_not_settled_carries_no_colour_either():
    live = panes(pane("a", AgentState.IDLE))
    frame = render(live, grid(("a", GREEN)), Settler(0.3), connected=True)
    assert frame.keys[0] == EMPTY
    assert frame.colours[0] is None, "a dark key must not be told what colour to be"


def test_colours_are_part_of_what_makes_a_frame_different():
    # Frames are only pushed when they change, so a recolour that compared
    # equal would never reach the board.
    live = panes(pane("a", AgentState.IDLE))
    before = render(live, grid(("a", GREEN)), settled(*live.values()), connected=True)
    after = render(live, grid(("a", BLUE)), settled(*live.values()), connected=True)
    assert before.keys == after.keys
    assert before != after
