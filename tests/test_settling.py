from herdrkeys.model import AgentState
from herdrkeys.settling import Settler


def test_nothing_is_renderable_until_it_settles():
    settler = Settler(0.3)
    settler.observe("p", AgentState.UNKNOWN, 0.0)
    settler.tick(0.1)
    assert settler.settled("p") is None, "a key stays dark rather than strobing"
    settler.tick(0.35)
    assert settler.settled("p") is AgentState.UNKNOWN


def test_agent_startup_flap_never_reaches_a_frame():
    # The real sequence Herdr emits while an agent boots, captured from a live
    # session: unknown -> unknown -> working inside ~2s.
    settler = Settler(0.3)
    settler.observe("p", AgentState.UNKNOWN, 0.0)
    settler.tick(0.1)
    settler.observe("p", AgentState.UNKNOWN, 0.1)
    settler.tick(0.2)
    settler.observe("p", AgentState.WORKING, 0.2)
    settler.tick(0.25)
    assert settler.settled("p") is None, "blue never appears on the way to amber"
    settler.tick(0.55)
    assert settler.settled("p") is AgentState.WORKING


def test_blocked_is_never_delayed():
    settler = Settler(0.3)
    settler.observe("p", AgentState.WORKING, 0.0)
    settler.tick(0.4)
    settler.observe("p", AgentState.BLOCKED, 0.5)
    assert settler.settled("p") is AgentState.BLOCKED, "a human is waiting on blocked"


def test_a_state_that_returns_to_committed_cancels_its_pending_change():
    settler = Settler(0.3)
    settler.observe("p", AgentState.IDLE, 0.0)
    settler.tick(0.4)
    settler.observe("p", AgentState.WORKING, 0.5)
    settler.observe("p", AgentState.IDLE, 0.6)
    settler.tick(0.9)
    assert settler.settled("p") is AgentState.IDLE
    assert settler.next_deadline(0.9) is None


def test_deadline_tells_the_loop_when_to_wake():
    settler = Settler(0.3)
    assert settler.next_deadline(0.0) is None
    settler.observe("p", AgentState.IDLE, 1.0)
    assert settler.next_deadline(1.1) == __import__("pytest").approx(0.2)


def test_retain_forgets_panes_that_are_gone():
    settler = Settler(0.3)
    settler.observe("gone", AgentState.IDLE, 0.0)
    settler.observe("here", AgentState.IDLE, 0.0)
    settler.tick(0.4)
    settler.retain({"here"})
    assert settler.settled("gone") is None
    assert settler.settled("here") is AgentState.IDLE
