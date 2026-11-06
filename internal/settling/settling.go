// Package settling holds changes briefly so agent-startup flapping never
// reaches the keys.
//
// Two things settle, for the same reason and on the same clock: what a key
// shows (Settler) and which key an agent is (OrderSettler).
//
// Herdr really emits this while an agent boots (captured from a live session):
//
//	revision 8  -> agent_status "unknown", no agent field
//	revision 9  -> agent "claude", "unknown"
//	revision 10 -> agent "claude", "working"
//
// ...inside about two seconds. Rendered directly, that is a blue strobe on
// every agent start.
//
// Settling is asymmetric: a transition into `blocked` commits immediately,
// because a human is waiting on it. Everything else waits for the state to
// hold still.
//
// Pure: the clock is injected, as seconds on any monotonic scale.
package settling

import "github.com/johnendean/herdrkeys/internal/model"

// DefaultSettleSeconds is how long a change must hold before it is shown.
const DefaultSettleSeconds = 0.3

// Slot is one key of the settled grid: the agent on it, and the colour it
// wears when idle. A pair rather than a bare pane ID so that a recolour is
// settled by the same mechanism as a reorder -- see OrderSettler.
type Slot struct {
	PaneID string
	Colour string
}

// Grid is the agents on the keys, in key order.
type Grid []Slot

// Equal reports whether two grids put the same agents, in the same colours,
// on the same keys.
func (g Grid) Equal(other Grid) bool {
	if len(g) != len(other) {
		return false
	}
	for i := range g {
		if g[i] != other[i] {
			return false
		}
	}
	return true
}

type pendingState struct {
	state model.AgentState
	since float64
}

// Settler settles what each key shows.
type Settler struct {
	SettleSeconds float64
	committed     map[string]model.AgentState
	pending       map[string]pendingState
}

// NewSettler makes a Settler that commits a state once it has held for
// settleSeconds.
func NewSettler(settleSeconds float64) *Settler {
	return &Settler{
		SettleSeconds: settleSeconds,
		committed:     map[string]model.AgentState{},
		pending:       map[string]pendingState{},
	}
}

// Observe records what Herdr says a pane is doing now.
func (s *Settler) Observe(paneID string, state model.AgentState, now float64) {
	if committed, ok := s.committed[paneID]; ok && committed == state {
		delete(s.pending, paneID)
		return
	}
	if state == model.Blocked {
		s.committed[paneID] = state
		delete(s.pending, paneID)
		return
	}
	if p, ok := s.pending[paneID]; !ok || p.state != state {
		s.pending[paneID] = pendingState{state, now}
	}
}

// Tick commits anything that has held still long enough.
func (s *Settler) Tick(now float64) {
	for paneID, p := range s.pending {
		if now-p.since >= s.SettleSeconds {
			s.committed[paneID] = p.state
			delete(s.pending, paneID)
		}
	}
}

// Settled is the state safe to render, and false if nothing has settled yet.
//
// A pane that has just appeared has no settled state, so its key stays dark
// for at most SettleSeconds rather than strobing through the states Herdr
// emits while the agent boots.
func (s *Settler) Settled(paneID string) (model.AgentState, bool) {
	state, ok := s.committed[paneID]
	return state, ok
}

// Forget drops everything known about a pane.
func (s *Settler) Forget(paneID string) {
	delete(s.committed, paneID)
	delete(s.pending, paneID)
}

// Retain forgets every pane not in keep.
func (s *Settler) Retain(keep map[string]bool) {
	for paneID := range s.committed {
		if !keep[paneID] {
			delete(s.committed, paneID)
		}
	}
	for paneID := range s.pending {
		if !keep[paneID] {
			delete(s.pending, paneID)
		}
	}
}

// NextDeadline is how long until the event loop must wake to commit a pending
// change, and false when nothing is pending.
func (s *Settler) NextDeadline(now float64) (float64, bool) {
	if len(s.pending) == 0 {
		return 0, false
	}
	first := true
	var soonest float64
	for _, p := range s.pending {
		if first || p.since < soonest {
			soonest, first = p.since, false
		}
	}
	return max(0, soonest+s.SettleSeconds-now), true
}

// OrderSettler holds a change in the grid until it stops moving.
//
// What settles here is the ordered list of (pane, colour) pairs: which agent
// is on which key, and what colour that key wears when idle. Colour rides with
// the order rather than settling on its own because herdrcolor recolours a
// project when a different project appears or goes away, so a colour change
// is something an unrelated agent did -- the same shape of event as a
// reorder, and it must not flicker for the same reason (ADR 0010).
//
// The keys mirror the order `agent.list` returns (ADR 0009), so a single poll
// that omits an agent does not just darken one key -- it renumbers every key
// below it. The list is polled twice a second and an absent row releases an
// agent at once, so one blip would shuffle the board and the next poll would
// shuffle it back.
//
// What this does not do is protect a restart in place. Quitting an agent and
// starting another takes seconds, not milliseconds, so the keys below it will
// move and move back. That is the price of mirroring, paid knowingly: see
// ADR 0009.
//
// The whole order is one value, committed or not. Settling each agent's
// membership separately would be more precise and buy nothing, since an agent
// flapping in and out moves everything below it either way.
type OrderSettler struct {
	SettleSeconds float64
	committed     Grid
	pending       Grid
	pendingSince  float64
	hasPending    bool
}

// NewOrderSettler makes an OrderSettler on the given settle time.
func NewOrderSettler(settleSeconds float64) *OrderSettler {
	return &OrderSettler{SettleSeconds: settleSeconds}
}

// Observe records the agent list as Herdr gives it now, in its order.
func (o *OrderSettler) Observe(panes []model.AgentPane, now float64) {
	candidate := make(Grid, len(panes))
	for i, pane := range panes {
		candidate[i] = Slot{pane.PaneID, pane.Colour}
	}
	if candidate.Equal(o.committed) {
		o.hasPending = false
		o.pending = nil
		return
	}
	if !o.hasPending || !o.pending.Equal(candidate) {
		o.pending, o.pendingSince, o.hasPending = candidate, now, true
	}
}

// Tick commits the pending order once it has held still long enough.
func (o *OrderSettler) Tick(now float64) {
	if !o.hasPending {
		return
	}
	if now-o.pendingSince >= o.SettleSeconds {
		o.committed = o.pending
		o.pending, o.hasPending = nil, false
	}
}

// Settled is the grid safe to render. Empty until the first one has held
// still.
func (o *OrderSettler) Settled() Grid { return o.committed }

// NextDeadline is how long until a pending order commits, and false when
// nothing is pending.
func (o *OrderSettler) NextDeadline(now float64) (float64, bool) {
	if !o.hasPending {
		return 0, false
	}
	return max(0, o.pendingSince+o.SettleSeconds-now), true
}
