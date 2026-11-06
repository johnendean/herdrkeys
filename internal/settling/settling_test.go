package settling

import (
	"math"
	"testing"

	"github.com/johnendean/herdrkeys/internal/model"
)

// panes are idle agent panes for the settler, one per slot given.
func panes(specs ...Slot) []model.AgentPane {
	out := make([]model.AgentPane, len(specs))
	for i, s := range specs {
		out[i] = model.AgentPane{PaneID: s.PaneID, Agent: "claude", State: model.Idle, Colour: s.Colour}
	}
	return out
}

func ids(names ...string) []Slot {
	out := make([]Slot, len(names))
	for i, name := range names {
		out[i] = Slot{PaneID: name}
	}
	return out
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func wantState(t *testing.T, s *Settler, paneID string, want model.AgentState) {
	t.Helper()
	got, ok := s.Settled(paneID)
	if !ok || got != want {
		t.Fatalf("settled(%q) = %q, %v; want %q", paneID, got, ok, want)
	}
}

func wantNothing(t *testing.T, s *Settler, paneID, why string) {
	t.Helper()
	if got, ok := s.Settled(paneID); ok {
		t.Fatalf("settled(%q) = %q; want nothing: %s", paneID, got, why)
	}
}

func wantGrid(t *testing.T, o *OrderSettler, want Grid, why string) {
	t.Helper()
	if !o.Settled().Equal(want) {
		t.Fatalf("settled order = %v; want %v: %s", o.Settled(), want, why)
	}
}

func TestNothingIsRenderableUntilItSettles(t *testing.T) {
	s := NewSettler(0.3)
	s.Observe("p", model.Unknown, 0.0)
	s.Tick(0.1)
	wantNothing(t, s, "p", "a key stays dark rather than strobing")
	s.Tick(0.35)
	wantState(t, s, "p", model.Unknown)
}

func TestAgentStartupFlapNeverReachesAFrame(t *testing.T) {
	// The real sequence Herdr emits while an agent boots, captured from a live
	// session: unknown -> unknown -> working inside ~2s.
	s := NewSettler(0.3)
	s.Observe("p", model.Unknown, 0.0)
	s.Tick(0.1)
	s.Observe("p", model.Unknown, 0.1)
	s.Tick(0.2)
	s.Observe("p", model.Working, 0.2)
	s.Tick(0.25)
	wantNothing(t, s, "p", "blue never appears on the way to amber")
	s.Tick(0.55)
	wantState(t, s, "p", model.Working)
}

func TestBlockedIsNeverDelayed(t *testing.T) {
	s := NewSettler(0.3)
	s.Observe("p", model.Working, 0.0)
	s.Tick(0.4)
	s.Observe("p", model.Blocked, 0.5)
	wantState(t, s, "p", model.Blocked) // a human is waiting on blocked
}

func TestAStateThatReturnsToCommittedCancelsItsPendingChange(t *testing.T) {
	s := NewSettler(0.3)
	s.Observe("p", model.Idle, 0.0)
	s.Tick(0.4)
	s.Observe("p", model.Working, 0.5)
	s.Observe("p", model.Idle, 0.6)
	s.Tick(0.9)
	wantState(t, s, "p", model.Idle)
	if _, ok := s.NextDeadline(0.9); ok {
		t.Fatal("nothing pending, so nothing to wake for")
	}
}

func TestDeadlineTellsTheLoopWhenToWake(t *testing.T) {
	s := NewSettler(0.3)
	if _, ok := s.NextDeadline(0.0); ok {
		t.Fatal("no deadline with nothing pending")
	}
	s.Observe("p", model.Idle, 1.0)
	if d, ok := s.NextDeadline(1.1); !ok || !near(d, 0.2) {
		t.Fatalf("deadline = %v, %v; want 0.2", d, ok)
	}
}

func TestRetainForgetsPanesThatAreGone(t *testing.T) {
	s := NewSettler(0.3)
	s.Observe("gone", model.Idle, 0.0)
	s.Observe("here", model.Idle, 0.0)
	s.Tick(0.4)
	s.Retain(map[string]bool{"here": true})
	wantNothing(t, s, "gone", "it was not retained")
	wantState(t, s, "here", model.Idle)
}

// -- the order the keys are numbered in -----------------------------------

func TestTheOrderIsNotRenderableUntilItHoldsStill(t *testing.T) {
	o := NewOrderSettler(0.3)
	o.Observe(panes(ids("a", "b")...), 0.0)
	wantGrid(t, o, nil, "no keys at all rather than keys that are about to move")
	o.Tick(0.1)
	wantGrid(t, o, nil, "still moving")
	o.Tick(0.35)
	wantGrid(t, o, ids("a", "b"), "held still")
}

func TestOnePollThatOmitsAnAgentDoesNotRenumberTheBoard(t *testing.T) {
	// `agent.list` is polled twice a second and an absent row releases an
	// agent immediately, so a single blip would shuffle every key below it
	// and shuffle them back on the next poll.
	o := NewOrderSettler(0.3)
	o.Observe(panes(ids("a", "b", "c")...), 0.0)
	o.Tick(0.3)
	wantGrid(t, o, ids("a", "b", "c"), "committed")

	o.Observe(panes(ids("a", "c")...), 0.5) // the blip
	o.Tick(0.6)
	o.Observe(panes(ids("a", "b", "c")...), 1.0) // and gone again
	o.Tick(1.1)
	wantGrid(t, o, ids("a", "b", "c"), "nobody moved")
}

func TestARealDepartureCommitsOnceItStopsMoving(t *testing.T) {
	o := NewOrderSettler(0.3)
	o.Observe(panes(ids("a", "b", "c")...), 0.0)
	o.Tick(0.3)
	o.Observe(panes(ids("a", "c")...), 0.5)
	o.Tick(0.9)
	wantGrid(t, o, ids("a", "c"), "the departure held")
}

func TestReturningToTheCommittedOrderCancelsTheWait(t *testing.T) {
	o := NewOrderSettler(0.3)
	o.Observe(panes(ids("a", "b")...), 0.0)
	o.Tick(0.3)
	o.Observe(panes(ids("b", "a")...), 0.4)
	o.Observe(panes(ids("a", "b")...), 0.5)
	if _, ok := o.NextDeadline(0.5); ok {
		t.Fatal("nothing pending, so nothing to wake for")
	}
	o.Tick(0.9)
	wantGrid(t, o, ids("a", "b"), "unchanged")
}

func TestTheLoopIsToldWhenToWakeForAPendingOrder(t *testing.T) {
	o := NewOrderSettler(0.3)
	if _, ok := o.NextDeadline(0.0); ok {
		t.Fatal("no deadline with nothing pending")
	}
	o.Observe(panes(ids("a")...), 1.0)
	if d, ok := o.NextDeadline(1.1); !ok || !near(d, 0.2) {
		t.Fatalf("deadline = %v, %v; want 0.2", d, ok)
	}
}

func TestARecolourSettlesLikeAReorder(t *testing.T) {
	// herdrcolor recolours a project when a colliding project appears or goes
	// away, so a key can change colour because of an agent somewhere else.
	green := Grid{{"a", "#a6e3a1"}, {"b", ""}}
	blue := Grid{{"a", "#89b4fa"}, {"b", ""}}
	o := NewOrderSettler(0.3)
	o.Observe(panes(green...), 0.0)
	o.Tick(0.3)
	wantGrid(t, o, green, "committed")

	o.Observe(panes(blue...), 0.5)
	wantGrid(t, o, green, "not yet")
	o.Tick(0.9)
	wantGrid(t, o, blue, "recoloured")
}

func TestAColourThatFlickersNeverReachesTheBoard(t *testing.T) {
	green := Grid{{"a", "#a6e3a1"}}
	o := NewOrderSettler(0.3)
	o.Observe(panes(green...), 0.0)
	o.Tick(0.3)
	o.Observe(panes(Slot{"a", "#89b4fa"}), 0.5)
	o.Tick(0.6)
	o.Observe(panes(green...), 0.7)
	o.Tick(1.2)
	wantGrid(t, o, green, "nothing moved")
}
