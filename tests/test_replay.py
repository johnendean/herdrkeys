"""Replay a real Herdr event stream.

`tests/fixtures/session-events.jsonl` is 99 events captured verbatim from a live
Herdr 0.8.2 session by subscribing over the socket. It is the only fixture here
that nobody invented, so it is the one that catches wrong assumptions.
"""

import json
from pathlib import Path

from herdrkeys.herdr_state import HerdrState
from herdrkeys.model import AgentState
from herdrkeys.reducer import render, sync_slots
from herdrkeys.settling import Settler
from herdrkeys.slots import SlotMap

FIXTURE = Path(__file__).parent / "fixtures" / "session-events.jsonl"


def events():
    with FIXTURE.open() as handle:
        return [json.loads(line) for line in handle if line.strip()]


def folded():
    state = HerdrState()
    for event in events():
        state.apply_event(event)
    return state


def test_folding_a_real_stream_finds_the_agent_panes():
    state = folded()
    assert sorted(state.agent_panes()) == ["w1:p1", "w2:p1"]
    assert state.focused_pane_id == "w2:p1"


def test_shell_panes_never_become_agent_panes():
    state = folded()
    assert "w1:p2" in state.panes, "the shell pane is tracked"
    assert "w1:p2" not in state.agent_panes(), "but it earns no key"


def test_a_pane_keeps_its_identity_across_agent_restarts():
    # w1:p1 really did go unknown/None -> claude -> unknown/None -> claude four
    # times in this capture, always as w1:p1. The whole sticky-slot design rests
    # on this, so assert it against the recording rather than trusting the docs.
    restarts = 0
    had_agent = False
    for event in events():
        pane = (event.get("data") or {}).get("pane") or {}
        if pane.get("pane_id") != "w1:p1":
            continue
        has_agent = pane.get("agent") is not None
        if has_agent and not had_agent:
            restarts += 1
        had_agent = has_agent
    assert restarts >= 3
    assert folded().panes["w1:p1"].agent == "claude"


def test_the_backlog_alone_is_not_a_trustworthy_picture_of_the_world():
    # Herdr replays a backlog on subscribe, but it is bounded: this capture
    # contains zero pane_exited/pane_closed events even though a pane was
    # closed during the session, leaving w1:p3 as a phantom. This is precisely
    # why the daemon reconciles against session.snapshot rather than trusting
    # the fold -- if this assertion ever starts failing, that reconciliation
    # has become cheaper, not unnecessary.
    kinds = {(event.get("data") or {}).get("type") for event in events()}
    assert "pane_closed" not in kinds and "pane_exited" not in kinds
    assert "w1:p3" in folded().panes, "phantom pane survives the fold"


def test_a_snapshot_evicts_phantoms_that_the_fold_kept():
    state = folded()
    slots = SlotMap()
    sync_slots(state.agent_panes(), slots, state.live_pane_ids())
    assert slots.slot_of("w1:p1") == 0 and slots.slot_of("w2:p1") == 1

    state.apply_snapshot({"panes": [{"pane_id": "w2:p1", "agent": "claude",
                                     "agent_status": "working", "revision": 10,
                                     "focused": True}],
                          "focused_pane_id": "w2:p1"})
    slots.gc(state.live_pane_ids())
    assert slots.slot_of("w1:p1") is None
    assert slots.slot_of("w2:p1") == 1, "the survivor does not move"


def test_stale_backlog_updates_never_overwrite_newer_state():
    state = HerdrState()
    state.apply_snapshot({"panes": [{"pane_id": "w1:p1", "agent": "claude",
                                     "agent_status": "idle", "revision": 36,
                                     "focused": False}],
                          "focused_pane_id": None})
    for event in events():  # the whole backlog, all of it older than revision 36
        state.apply_event(event)
    assert state.panes["w1:p1"].revision == 36


def test_end_to_end_from_recording_to_frame():
    state = folded()
    panes = state.agent_panes()
    slots = SlotMap()
    sync_slots(panes, slots, state.live_pane_ids())

    settler = Settler(0.3)
    for pane_id, pane in panes.items():
        settler.observe(pane_id, pane.state, 0.0)
    settler.tick(0.0)
    assert render(panes, slots, settler, connected=True).keys == "-" * 12 + "mr-f", (
        "nothing has settled yet, so the grid is still dark"
    )

    settler.tick(1.0)
    frame = render(panes, slots, settler, connected=True)
    assert frame.keys == "wW" + "-" * 10 + "mr-f", (
        "w1:p1 working in slot 0, w2:p1 working and focused in slot 1"
    )
    assert state.agent_panes()["w2:p1"].state is AgentState.WORKING
