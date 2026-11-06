package reducer

import (
	"fmt"
	"testing"

	"github.com/johnendean/herdrkeys/internal/model"
	"github.com/johnendean/herdrkeys/internal/settling"
)

func pane(paneID string, state model.AgentState, focused ...bool) model.AgentPane {
	return model.AgentPane{PaneID: paneID, Agent: "claude", State: state, Focused: len(focused) > 0 && focused[0]}
}

// panes is an agent list, as the state fold hands one over.
func panes(specs ...model.AgentPane) Panes {
	out := Panes{}
	for _, p := range specs {
		out[p.PaneID] = p
	}
	return out
}

// grid is a settled grid of panes with no project colour.
func grid(ids ...string) settling.Grid {
	out := make(settling.Grid, len(ids))
	for i, id := range ids {
		out[i] = settling.Slot{PaneID: id}
	}
	return out
}

// settled is a settler that has already committed everything it was shown.
func settled(live Panes) *settling.Settler {
	s := settling.NewSettler(0.0)
	for _, p := range live {
		s.Observe(p.PaneID, p.State, 0.0)
	}
	s.Tick(1.0)
	return s
}

var connected = Input{Features: model.Features{Connected: true}}

func keys(t *testing.T, frame model.Frame, upTo int, want string, why string) {
	t.Helper()
	if got := frame.KeyString()[:upTo]; got != want {
		t.Fatalf("keys[:%d] = %q, want %q: %s", upTo, got, want, why)
	}
}

func repeat(s string, n int) string {
	out := ""
	for range n {
		out += s
	}
	return out
}

func TestFrameIsOneCharacterPerKey(t *testing.T) {
	live := panes(pane("a", model.Working))
	frame := Render(live, grid("a"), settled(live), connected)
	if got, want := frame.KeyString(), "w"+repeat("-", 11)+"menf"; got != want {
		t.Fatalf("frame = %q, want %q", got, want)
	}
}

func TestFocusIsShownByCaseNotByADifferentState(t *testing.T) {
	live := panes(pane("a", model.Working, true))
	keys(t, Render(live, grid("a"), settled(live), connected), 1, "W", "upper case is focus")
}

func TestDisconnectedIsDistinguishableFromHavingNoAgents(t *testing.T) {
	up := Render(Panes{}, nil, settling.NewSettler(0.3), connected)
	down := Render(Panes{}, nil, settling.NewSettler(0.3), Input{})
	if up.Keys[model.FnSlot] != model.CodeFnConnected || down.Keys[model.FnSlot] != model.CodeFnDisconnected {
		t.Fatalf("fn keys = %q / %q", up.Keys[model.FnSlot], down.Keys[model.FnSlot])
	}
	if up == down {
		t.Fatal("sixteen dark keys must not mean two things")
	}
}

func TestUnsettledPaneRendersDark(t *testing.T) {
	live := panes(pane("a", model.Unknown))
	keys(t, Render(live, grid("a"), settling.NewSettler(0.3), connected), 1, "-", "not settled")
}

func TestAnAgentGoneFromTheListLeavesAHoleUntilTheOrderSettles(t *testing.T) {
	// The order is settled separately and lags by up to the settle time, so
	// for that moment it still names an agent that has gone. Blanking that one
	// key is right: shifting everything below it now, and again when the new
	// order commits, would move the survivors twice for one departure.
	live := panes(pane("a", model.Working), pane("c", model.Idle))
	keys(t, Render(live, grid("a", "b", "c"), settled(live), connected), 3, "w-i", "a hole")
}

// -- the order is the key numbering --------------------------------------

func TestKeysFollowHerdrsOrderNotTheOrderAgentsArrived(t *testing.T) {
	live := panes(pane("b", model.Working), pane("a", model.Idle))
	keys(t, Render(live, grid("b", "a"), settled(live), connected), 2, "wi",
		"first in the list is key 0, whenever it showed up")
}

func TestClosingAnAgentMovesTheOnesBelowItUp(t *testing.T) {
	before := panes(pane("a", model.Idle), pane("b", model.Working), pane("c", model.Done))
	keys(t, Render(before, grid("a", "b", "c"), settled(before), connected), 3, "iwd", "before")

	after := panes(pane("a", model.Idle), pane("c", model.Done))
	keys(t, Render(after, grid("a", "c"), settled(after), connected), 3, "id-",
		"c takes b's key rather than leaving a gap behind")
}

func TestAnAgentAppearingAbovePushesTheOthersDown(t *testing.T) {
	// The cost of mirroring, stated as a test so nobody mistakes it for a bug:
	// a new agent at the top of Herdr's list renumbers every key below it.
	after := panes(pane("new", model.Working), pane("a", model.Idle))
	keys(t, Render(after, grid("new", "a"), settled(after), connected), 2, "wi", "pushed down")
}

func fifteen(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("w%d:p1", i)
	}
	return ids
}

func TestTheGridHoldsTwelveAgentsAndNoMore(t *testing.T) {
	ids := fifteen(15)
	live := Panes{}
	for _, id := range ids {
		live[id] = pane(id, model.Blocked)
	}
	frame := Render(live, grid(ids...), settled(live), connected)
	keys(t, frame, model.GridSize, repeat("b", 12), "twelve blocked")
	for _, slot := range model.FeatureSlots {
		if frame.Keys[slot] == 'b' {
			t.Fatalf("slot %d: the feature row never holds an agent", slot)
		}
	}
	if onGrid := OnTheGrid(grid(ids...)); !onGrid.Equal(grid(ids[:12]...)) {
		t.Fatalf("on the grid = %v", onGrid)
	}
}

// -- which key focuses what ----------------------------------------------

func TestAKeyPressFocusesTheAgentAtThatPosition(t *testing.T) {
	live := panes(pane("a", model.Idle), pane("b", model.Idle))
	for slot, want := range []string{"a", "b", ""} {
		if got := PaneForSlot(slot, live, grid("a", "b")); got != want {
			t.Fatalf("slot %d = %q, want %q", slot, got, want)
		}
	}
}

func TestTheFeatureRowNeverFocusesAnAgent(t *testing.T) {
	ids := fifteen(15)
	live := Panes{}
	for _, id := range ids {
		live[id] = pane(id, model.Idle)
	}
	for _, slot := range model.FeatureSlots {
		if got := PaneForSlot(slot, live, grid(ids...)); got != "" {
			t.Fatalf("slot %d focuses %q", slot, got)
		}
	}
}

func TestAKeyWhoseAgentHasGoneFocusesNothing(t *testing.T) {
	live := panes(pane("a", model.Idle))
	if got := PaneForSlot(1, live, grid("a", "b")); got != "" {
		t.Fatalf("focuses %q", got)
	}
}

// -- the function key ----------------------------------------------------

func TestBlockedWinsOverDone(t *testing.T) {
	live := panes(pane("done", model.Done), pane("blocked", model.Blocked))
	if got := NextAttentionTarget(live, grid("done", "blocked"), settled(live)); got != "blocked" {
		t.Fatalf("target = %q", got)
	}
}

func TestNothingToGoToReturnsNone(t *testing.T) {
	live := panes(pane("a", model.Working), pane("b", model.Idle))
	if got := NextAttentionTarget(live, grid("a", "b"), settled(live)); got != "" {
		t.Fatalf("target = %q", got)
	}
}

func TestNoAgentsAtAllReturnsNone(t *testing.T) {
	if got := NextAttentionTarget(Panes{}, nil, settling.NewSettler(0.3)); got != "" {
		t.Fatalf("target = %q", got)
	}
}

func TestRepeatedPressesWalkTheQueueRatherThanSticking(t *testing.T) {
	order := grid("x", "y", "z")
	live := panes(pane("x", model.Blocked, true), pane("y", model.Blocked), pane("z", model.Blocked))
	for _, step := range []struct{ focused, want string }{{"x", "y"}, {"y", "z"}, {"z", "x"}} {
		for id := range live {
			live[id] = pane(id, model.Blocked, id == step.focused)
		}
		if got := NextAttentionTarget(live, order, settled(live)); got != step.want {
			t.Fatalf("from %s: target = %q, want %q", step.focused, got, step.want)
		}
	}
}

func TestTheQueueWrapsOverTheAgentsThereAreNotTheTwelveSlots(t *testing.T) {
	// Two agents, the second focused: the wrap must come back to the first
	// rather than walking ten empty slots and falling off the end.
	live := panes(pane("a", model.Blocked), pane("b", model.Blocked, true))
	if got := NextAttentionTarget(live, grid("a", "b"), settled(live)); got != "a" {
		t.Fatalf("target = %q", got)
	}
}

func TestAnAgentPastTheGridCannotBeJumpedTo(t *testing.T) {
	ids := fifteen(13)
	live := Panes{}
	for i, id := range ids {
		state := model.Working
		if i == 12 {
			state = model.Blocked
		}
		live[id] = pane(id, state)
	}
	if got := NextAttentionTarget(live, grid(ids...), settled(live)); got != "" {
		t.Fatalf("target = %q: the thirteenth agent has no key, so the function key cannot send you to it", got)
	}
}

// -- project colours ------------------------------------------------------

const (
	green = "#a6e3a1"
	blue  = "#89b4fa"
)

func TestAProjectColourRidesAlongWithItsKey(t *testing.T) {
	live := panes(pane("a", model.Idle), pane("b", model.Working))
	frame := Render(live, settling.Grid{{PaneID: "a", Colour: green}, {PaneID: "b", Colour: blue}}, settled(live), connected)
	if frame.Colours[0] != green || frame.Colours[1] != blue {
		t.Fatalf("colours = %q: sent for every state, because which ones wear it is the device's call", frame.Colours[:2])
	}
	keys(t, frame, 2, "iw", "and the meaning codes are untouched")
}

func TestAKeyWithNoProjectColourCarriesNone(t *testing.T) {
	live := panes(pane("a", model.Idle))
	frame := Render(live, grid("a"), settled(live), connected)
	if frame.Colours[0] != "" {
		t.Fatal("the fallback is an absence, not a colour")
	}
	keys(t, frame, 1, "i", "idle")
}

func TestTheFeatureRowNeverCarriesAColour(t *testing.T) {
	live := panes(pane("a", model.Idle))
	frame := Render(live, settling.Grid{{PaneID: "a", Colour: green}}, settled(live), connected)
	for _, slot := range model.FeatureSlots {
		if frame.Colours[slot] != "" {
			t.Fatalf("slot %d carries %q", slot, frame.Colours[slot])
		}
	}
}

func TestAKeyThatHasNotSettledCarriesNoColourEither(t *testing.T) {
	live := panes(pane("a", model.Idle))
	frame := Render(live, settling.Grid{{PaneID: "a", Colour: green}}, settling.NewSettler(0.3), connected)
	keys(t, frame, 1, "-", "dark")
	if frame.Colours[0] != "" {
		t.Fatal("a dark key must not be told what colour to be")
	}
}

func TestColoursArePartOfWhatMakesAFrameDifferent(t *testing.T) {
	// Frames are only pushed when they change, so a recolour that compared
	// equal would never reach the board.
	live := panes(pane("a", model.Idle))
	before := Render(live, settling.Grid{{PaneID: "a", Colour: green}}, settled(live), connected)
	after := Render(live, settling.Grid{{PaneID: "a", Colour: blue}}, settled(live), connected)
	if before.Keys != after.Keys || before == after {
		t.Fatal("same keys, different frames")
	}
}

func TestTheFrameCarriesTheForgeBesideTheGitKey(t *testing.T) {
	live := panes(pane("a", model.Idle, true))
	in := Input{Features: model.Features{Connected: true, GitPage: true}, Forge: "gitlab"}
	if frame := Render(live, grid("a"), settled(live), in); frame.Forge != "gitlab" {
		t.Fatalf("forge = %q", frame.Forge)
	}
}

func TestAKeyWithNoPageCarriesNoForge(t *testing.T) {
	// A forge belongs to a page, and only a page has one.
	live := panes(pane("a", model.Idle, true))
	in := Input{Features: model.Features{Connected: true}, Forge: "github"}
	frame := Render(live, grid("a"), settled(live), in)
	if frame.Keys[model.GitSlot] != model.CodeGitNoPage || frame.Forge != "" {
		t.Fatalf("git key %q, forge %q", frame.Keys[model.GitSlot], frame.Forge)
	}
}
