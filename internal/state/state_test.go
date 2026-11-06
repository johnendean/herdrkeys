package state

import (
	"bufio"
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/johnendean/herdrkeys/internal/model"
	"github.com/johnendean/herdrkeys/internal/reducer"
	"github.com/johnendean/herdrkeys/internal/settling"
)

// fixture is 99 events captured verbatim from a live Herdr 0.8.2 session by
// subscribing over the socket. It is the only fixture here that nobody
// invented, so it is the one that catches wrong assumptions. Shared with the
// Python suite until the cutover in ADR 0020.
const fixture = "../../tests/fixtures/session-events.jsonl"

// rawEvents is the recording as plain JSON, for the assertions about the
// recording itself rather than about the fold.
func rawEvents(t *testing.T) []map[string]any {
	t.Helper()
	f, err := os.Open(fixture)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []map[string]any
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		if len(scanner.Bytes()) == 0 {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		out = append(out, event)
	}
	if len(out) != 99 {
		t.Fatalf("the recording has %d events, want 99", len(out))
	}
	return out
}

func events(t *testing.T) []Event {
	t.Helper()
	var out []Event
	for _, raw := range rawEvents(t) {
		line, _ := json.Marshal(raw)
		var event Event
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatal(err)
		}
		out = append(out, event)
	}
	return out
}

func folded(t *testing.T) *State {
	t.Helper()
	s := New()
	for _, event := range events(t) {
		s.ApplyEvent(event)
	}
	return s
}

func paneIDs(panes []model.AgentPane) []string {
	out := []string{}
	for _, p := range panes {
		out = append(out, p.PaneID)
	}
	return out
}

func sortedIDs(panes []model.AgentPane) []string {
	out := paneIDs(panes)
	slices.Sort(out)
	return out
}

// decode builds a payload the way Herdr would send it.
func decode[T any](t *testing.T, raw string) T {
	t.Helper()
	var out T
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func find(panes []model.AgentPane, paneID string) (model.AgentPane, bool) {
	for _, p := range panes {
		if p.PaneID == paneID {
			return p, true
		}
	}
	return model.AgentPane{}, false
}

func TestFoldingARealStreamFindsTheAgentPanes(t *testing.T) {
	s := folded(t)
	if got := sortedIDs(s.AgentPanes()); !slices.Equal(got, []string{"w1:p1", "w2:p1"}) {
		t.Fatalf("agent panes = %v", got)
	}
	if s.FocusedPaneID != "w2:p1" {
		t.Fatalf("focused = %q", s.FocusedPaneID)
	}
}

func TestShellPanesNeverBecomeAgentPanes(t *testing.T) {
	s := folded(t)
	if _, ok := s.Panes["w1:p2"]; !ok {
		t.Fatal("the shell pane is tracked")
	}
	if _, ok := find(s.AgentPanes(), "w1:p2"); ok {
		t.Fatal("but it earns no key")
	}
}

func TestAPaneKeepsItsIdentityAcrossAgentRestarts(t *testing.T) {
	// w1:p1 really did go unknown/None -> claude -> unknown/None -> claude
	// four times in this capture, always as w1:p1. Keys no longer rest on
	// this (ADR 0009 numbers them by position in Herdr's list), but the
	// recording is the only evidence anybody has for it, so the assertion
	// stays: it is what ADR 0002's reasoning was built on and what would have
	// to be re-checked if keys were ever pinned to panes again.
	restarts, hadAgent := 0, false
	for _, event := range rawEvents(t) {
		data, _ := event["data"].(map[string]any)
		pane, _ := data["pane"].(map[string]any)
		if pane["pane_id"] != "w1:p1" {
			continue
		}
		hasAgent := pane["agent"] != nil
		if hasAgent && !hadAgent {
			restarts++
		}
		hadAgent = hasAgent
	}
	if restarts < 3 {
		t.Fatalf("restarts = %d", restarts)
	}
	if agent := folded(t).Panes["w1:p1"].Agent; agent == nil || *agent != "claude" {
		t.Fatalf("agent = %v", agent)
	}
}

func TestTheBacklogAloneIsNotATrustworthyPictureOfTheWorld(t *testing.T) {
	// Herdr replays a backlog on subscribe, but it is bounded: this capture
	// contains zero pane_exited/pane_closed events even though a pane was
	// closed during the session, leaving w1:p3 as a phantom. This is
	// precisely why the daemon reconciles against session.snapshot rather
	// than trusting the fold -- if this assertion ever starts failing, that
	// reconciliation has become cheaper, not unnecessary.
	for _, event := range events(t) {
		if k := event.Data.Type; k == "pane_closed" || k == "pane_exited" {
			t.Fatalf("the recording has a %s", k)
		}
	}
	if _, ok := folded(t).Panes["w1:p3"]; !ok {
		t.Fatal("phantom pane survives the fold")
	}
}

func TestASnapshotEvictsPhantomsThatTheFoldKept(t *testing.T) {
	s := folded(t)
	if got := paneIDs(s.AgentPanes()); !slices.Equal(got, []string{"w1:p1", "w2:p1"}) {
		t.Fatalf("before = %v", got)
	}
	s.ApplySnapshot(decode[Snapshot](t, `{"panes": [{"pane_id": "w2:p1", "agent": "claude",
		"agent_status": "working", "revision": 10, "focused": true}],
		"focused_pane_id": "w2:p1"}`))
	if got := paneIDs(s.AgentPanes()); !slices.Equal(got, []string{"w2:p1"}) {
		t.Fatalf("after = %v: the phantom is gone", got)
	}
	// Under ADR 0002 the survivor would have stayed on key 1 and key 0 would
	// have gone dark. It moves up now: that is the whole point of the change.
}

func TestStaleBacklogUpdatesNeverOverwriteNewerState(t *testing.T) {
	s := New()
	s.ApplySnapshot(decode[Snapshot](t, `{"panes": [{"pane_id": "w1:p1", "agent": "claude",
		"agent_status": "idle", "revision": 36, "focused": false}], "focused_pane_id": null}`))
	for _, event := range events(t) { // the whole backlog, all older than revision 36
		s.ApplyEvent(event)
	}
	if got := s.Panes["w1:p1"].Revision; got != 36 {
		t.Fatalf("revision = %d", got)
	}
}

func TestEndToEndFromRecordingToFrame(t *testing.T) {
	s := folded(t)
	agentPanes := s.AgentPanes()
	panes := reducer.Panes{}
	var grid settling.Grid
	for _, p := range agentPanes {
		panes[p.PaneID] = p
		grid = append(grid, settling.Slot{PaneID: p.PaneID, Colour: p.Colour})
	}
	connected := reducer.Input{Features: model.Features{Connected: true}}

	settler := settling.NewSettler(0.3)
	for _, p := range agentPanes {
		settler.Observe(p.PaneID, p.State, 0.0)
	}
	settler.Tick(0.0)
	if got := reducer.Render(panes, grid, settler, connected).KeyString(); got != "------------menf" {
		t.Fatalf("frame = %q: nothing has settled yet, so the grid is still dark", got)
	}

	settler.Tick(1.0)
	if got := reducer.Render(panes, grid, settler, connected).KeyString(); got != "wW----------menf" {
		t.Fatalf("frame = %q: w1:p1 working in slot 0, w2:p1 working and focused in slot 1", got)
	}
	if p, _ := find(s.AgentPanes(), "w2:p1"); p.State != model.Working {
		t.Fatalf("w2:p1 is %q", p.State)
	}
}

func TestTheSnapshotCarriesHerdrsOwnAgentOrder(t *testing.T) {
	// A live session answers `session.snapshot` with an `agents` array that
	// is the same list, in the same order, as `agent.list`. Taking the order
	// from there means a reconnect does not renumber the keys for the
	// half-second until the first poll lands.
	s := New()
	s.ApplySnapshot(decode[Snapshot](t, `{
		"panes": [
			{"pane_id": "w8:p1", "agent": "claude", "agent_status": "working", "revision": 1},
			{"pane_id": "w6:p1", "agent": "claude", "agent_status": "idle", "revision": 1}
		],
		"agents": [{"pane_id": "w6:p1"}, {"pane_id": "w8:p1"}],
		"focused_pane_id": "w8:p1"}`))
	if got := paneIDs(s.AgentPanes()); !slices.Equal(got, []string{"w6:p1", "w8:p1"}) {
		t.Fatalf("order = %v: Herdr's order, not the order the panes happen to be listed in", got)
	}
}

func TestASnapshotWithoutAnAgentsArrayLeavesTheOrderAlone(t *testing.T) {
	s := New()
	s.ApplyAgents(decode[[]Pane](t, `[{"pane_id": "w6:p1", "agent": "claude", "agent_status": "idle"},
		{"pane_id": "w8:p1", "agent": "claude", "agent_status": "idle"}]`))
	s.ApplySnapshot(decode[Snapshot](t, `{"panes": [
		{"pane_id": "w8:p1", "agent": "claude", "agent_status": "idle", "revision": 2},
		{"pane_id": "w6:p1", "agent": "claude", "agent_status": "idle", "revision": 2}
	], "focused_pane_id": null}`))
	if got := paneIDs(s.AgentPanes()); !slices.Equal(got, []string{"w6:p1", "w8:p1"}) {
		t.Fatalf("order = %v", got)
	}
}

func TestAnAgentHerdrHasNotListedYetStillGetsAKey(t *testing.T) {
	// `pane_agent_detected` can arrive between two polls. Ordering it last, by
	// pane ID, is arbitrary but deterministic -- and it beats no key at all
	// until the next poll.
	s := New()
	s.ApplyAgents(decode[[]Pane](t, `[{"pane_id": "w6:p1", "agent": "claude", "agent_status": "idle"}]`))
	s.ApplyEvent(decode[Event](t, `{"data": {"type": "pane_agent_detected", "pane_id": "w2:p9", "agent": "claude"}}`))
	if got := paneIDs(s.AgentPanes()); !slices.Equal(got, []string{"w6:p1", "w2:p9"}) {
		t.Fatalf("order = %v: listed agents first, in Herdr's order", got)
	}
}

func TestAProjectColourIsReadOffThePanesHerdrAlreadySends(t *testing.T) {
	// herdrcolor publishes it as `tokens.color`, which rides along on payloads
	// herdrkeys already fetches -- so no extra call, and no dependency on that
	// plugin beyond the name of the field.
	s := New()
	s.ApplyAgents(decode[[]Pane](t, `[{"pane_id": "w6:p1", "agent": "claude", "agent_status": "idle",
		"tokens": {"c4": "sevenbyseven", "color": "#89B4FA"}}]`))
	if p, _ := find(s.AgentPanes(), "w6:p1"); p.Colour != "#89b4fa" {
		t.Fatalf("colour = %q", p.Colour)
	}
}

func TestAPanePayloadWithoutTokensKeepsTheColourItHad(t *testing.T) {
	// Herdr omits `tokens` from some payloads, exactly as it omits `cwd`.
	// Reading that as a retraction would drop the key back to green until the
	// next payload that happened to carry it.
	s := New()
	s.ApplyAgents(decode[[]Pane](t, `[{"pane_id": "w6:p1", "agent": "claude", "agent_status": "idle",
		"tokens": {"color": "#89b4fa"}}]`))
	s.ApplyEvent(decode[Event](t, `{"data": {"type": "pane_updated", "pane": {
		"pane_id": "w6:p1", "agent": "claude", "agent_status": "working", "revision": 9}}}`))
	if p, _ := find(s.AgentPanes(), "w6:p1"); p.Colour != "#89b4fa" {
		t.Fatalf("colour = %q", p.Colour)
	}
}

func TestAColourThatIsNotOneIsTreatedAsAbsent(t *testing.T) {
	// A numeric parse takes a sign, a 0x and underscores, so "#-1fff" and
	// "#0x1fff" would otherwise pass as colours with impossible channels.
	for _, tokens := range []string{
		`{"color": "green"}`, `{"color": "#12345"}`, `{"color": 5}`, `{}`, `null`, `"#89b4fa"`,
		`{"color": "#-1ffff"}`, `{"color": "#+1ffff"}`, `{"color": "#0x1fff"}`,
		`{"color": "#1_ffff"}`, `{"color": "# 1ffff"}`,
	} {
		s := New()
		s.ApplyAgents(decode[[]Pane](t, `[{"pane_id": "w6:p1", "agent": "claude",
			"agent_status": "idle", "tokens": `+tokens+`}]`))
		if p, _ := find(s.AgentPanes(), "w6:p1"); p.Colour != "" {
			t.Fatalf("tokens %s gave colour %q", tokens, p.Colour)
		}
	}
}

func TestSameRevisionUpdatesAreNotDiscarded(t *testing.T) {
	// Herdr does not bump `revision` for every status change: a pane observed
	// at revision 36 was `working` in the event backlog and `idle` in a
	// snapshot taken later. Filtering on `<=` would drop the newer value.
	s := New()
	s.ApplySnapshot(decode[Snapshot](t, `{"panes": [{"pane_id": "w1:p1", "agent": "claude",
		"agent_status": "working", "revision": 36}], "focused_pane_id": null}`))
	s.ApplyEvent(decode[Event](t, `{"data": {"type": "pane_updated", "pane": {"pane_id": "w1:p1",
		"agent": "claude", "agent_status": "idle", "revision": 36}}}`))
	if got := s.Panes["w1:p1"].State; got != model.Idle {
		t.Fatalf("state = %q", got)
	}
}

func TestAReplayedEventCannotUndoAPoll(t *testing.T) {
	// The revision guard drops updates older than what is held, so the poll
	// has to leave the high-water revision behind it -- a row carrying a
	// lower one would otherwise let stale history win the moment the replay
	// resumed.
	s := New()
	s.ApplySnapshot(decode[Snapshot](t, `{"panes": [{"pane_id": "w1:p1", "agent": "claude",
		"agent_status": "working", "revision": 36}], "focused_pane_id": null}`))
	s.ApplyAgents(decode[[]Pane](t, `[{"pane_id": "w1:p1", "agent": "claude", "agent_status": "blocked", "revision": 0}]`))
	s.ApplyEvent(decode[Event](t, `{"data": {"type": "pane_updated", "pane": {"pane_id": "w1:p1",
		"agent": "claude", "agent_status": "idle", "revision": 12}}}`))
	if got := s.Panes["w1:p1"].State; got != model.Blocked {
		t.Fatalf("state = %q", got)
	}
}

func TestThePollReleasesFocusItCanSeeHasMoved(t *testing.T) {
	// Focus moving to a pane with no agent leaves every row unfocused.
	// `agent.list` cannot say where focus went, but a key left bright would
	// claim focus it does not have.
	s := New()
	s.ApplyAgents(decode[[]Pane](t, `[{"pane_id": "w1:p1", "agent": "claude", "agent_status": "idle", "focused": true}]`))
	s.ApplyAgents(decode[[]Pane](t, `[{"pane_id": "w1:p1", "agent": "claude", "agent_status": "idle", "focused": false}]`))
	if s.FocusedPaneID != "" {
		t.Fatalf("focused = %q", s.FocusedPaneID)
	}
}

func TestAPollThatOmitsAnAgentReleasesItButKeepsThePane(t *testing.T) {
	s := New()
	s.ApplyAgents(decode[[]Pane](t, `[{"pane_id": "w1:p1", "agent": "claude", "agent_status": "idle", "cwd": "/x"}]`))
	s.ApplyAgents(nil)
	if len(s.AgentPanes()) != 0 {
		t.Fatal("the key goes")
	}
	if r, ok := s.Panes["w1:p1"]; !ok || r.Cwd != "/x" {
		t.Fatal("the pane survives losing its agent")
	}
}
