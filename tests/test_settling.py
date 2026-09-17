import pytest

from herdrkeys.model import AgentState
from herdrkeys.settling import OrderSettler, Settler


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


# -- the order the keys are numbered in -----------------------------------


def test_the_order_is_not_renderable_until_it_holds_still():
    order = OrderSettler(0.3)
    order.observe(["a", "b"], 0.0)
    assert order.settled() == (), "no keys at all rather than keys that are about to move"
    order.tick(0.1)
    assert order.settled() == ()
    order.tick(0.35)
    assert order.settled() == ("a", "b")


def test_one_poll_that_omits_an_agent_does_not_renumber_the_board():
    # `agent.list` is polled twice a second and an absent row releases an agent
    # immediately, so a single blip would shuffle every key below it and shuffle
    # them back on the next poll.
    order = OrderSettler(0.3)
    order.observe(["a", "b", "c"], 0.0)
    order.tick(0.3)
    assert order.settled() == ("a", "b", "c")

    order.observe(["a", "c"], 0.5)  # the blip
    order.tick(0.6)
    order.observe(["a", "b", "c"], 1.0)  # and gone again
    order.tick(1.1)
    assert order.settled() == ("a", "b", "c"), "nobody moved"


def test_a_real_departure_commits_once_it_stops_moving():
    order = OrderSettler(0.3)
    order.observe(["a", "b", "c"], 0.0)
    order.tick(0.3)
    order.observe(["a", "c"], 0.5)
    order.tick(0.9)
    assert order.settled() == ("a", "c")


def test_returning_to_the_committed_order_cancels_the_wait():
    order = OrderSettler(0.3)
    order.observe(["a", "b"], 0.0)
    order.tick(0.3)
    order.observe(["b", "a"], 0.4)
    order.observe(["a", "b"], 0.5)
    assert order.next_deadline(0.5) is None, "nothing pending, so nothing to wake for"
    order.tick(0.9)
    assert order.settled() == ("a", "b")


def test_the_loop_is_told_when_to_wake_for_a_pending_order():
    order = OrderSettler(0.3)
    assert order.next_deadline(0.0) is None
    order.observe(["a"], 1.0)
    assert order.next_deadline(1.1) == pytest.approx(0.2)
