package herdr

// What a key press actually puts on the socket.
//
// Every signal around this call can report success while the screen does not
// move, which is how herdr#3760 cost a day: `agent.focus` answered `ok`, the
// server logged `workspace focused`, and the viewport stayed where it was. The
// method name is therefore load-bearing in a way no other call here is, and
// nothing above this level can catch it being wrong.
//
// So these drive a real socket -- a Unix socket, or on Windows a named pipe --
// and assert on the bytes.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
	"sync"
	"testing"
	"time"
)

type request struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// fakeHerdr records requests and answers them however the test says.
type fakeHerdr struct {
	path    string
	mu      sync.Mutex
	got     []request
	replies map[string]string // method -> JSON fields merged into the reply
	// events are sent, one per line, after a subscribe is acknowledged.
	events []string
}

func (f *fakeHerdr) serve(c io.ReadWriteCloser) {
	defer c.Close()
	reader := bufio.NewReader(c)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return
	}
	var req request
	if json.Unmarshal(line, &req) != nil {
		return
	}
	f.mu.Lock()
	f.got = append(f.got, req)
	fields, ok := f.replies[req.Method]
	events := f.events
	f.mu.Unlock()
	if !ok {
		fields = `"result": {}`
	}
	// One line, however the test wrote it: the wire is newline-delimited.
	var out bytes.Buffer
	if json.Compact(&out, []byte(`{"id": "`+req.ID+`", `+fields+"}")) != nil {
		panic("a fake reply that is not JSON: " + fields)
	}
	c.Write(append(out.Bytes(), '\n'))
	if req.Method == "events.subscribe" {
		for _, event := range events {
			io.WriteString(c, event+"\n")
		}
		io.Copy(io.Discard, reader) // hold the stream open until the client goes
	}
}

func (f *fakeHerdr) methods() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.got {
		out = append(out, r.Method)
	}
	return out
}

func (f *fakeHerdr) params(i int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var compact map[string]any
	json.Unmarshal(f.got[i].Params, &compact)
	out, _ := json.Marshal(compact)
	return string(out)
}

func newFake(t *testing.T) *fakeHerdr {
	t.Helper()
	f := &fakeHerdr{replies: map[string]string{}}
	f.path = listen(t, f.serve) // per platform: see listen_*_test.go
	return f
}

func TestFocusMovesTheViewportWithPaneFocus(t *testing.T) {
	// The whole point. `agent.focus` stopped reaching a surface-active client
	// in herdr 0.9.0, so focusing through it alone moves nothing on screen.
	f := newFake(t)
	if err := FocusAgent(f.path, "w2:p1", time.Second); err != nil {
		t.Fatal(err)
	}
	if m := f.methods(); len(m) == 0 || m[0] != "pane.focus" {
		t.Fatalf("methods = %v: pane.focus is what moves the viewport, and it goes first", m)
	}
	if p := f.params(0); p != `{"pane_id":"w2:p1"}` {
		t.Fatalf("params = %s", p)
	}
}

func TestFocusAlsoMarksTheAgentSeen(t *testing.T) {
	// Going through the agent surface is what collapses `done` back to `idle`.
	f := newFake(t)
	if err := FocusAgent(f.path, "w2:p1", time.Second); err != nil {
		t.Fatal(err)
	}
	if m := f.methods(); !slices.Equal(m, []string{"pane.focus", "agent.focus"}) {
		t.Fatalf("methods = %v", m)
	}
	if p := f.params(1); p != `{"target":"w2:p1"}` {
		t.Fatalf("params = %s", p)
	}
}

func TestADepartedAgentDoesNotCostUsTheNavigation(t *testing.T) {
	// A pane outlives the agents that occupy it, so the frame can name a pane
	// whose agent has since gone. The viewport must still move.
	f := newFake(t)
	f.replies["agent.focus"] = `"error": {"code": "agent_not_found", "message": "agent target null not found"}`
	if err := FocusAgent(f.path, "w2:p1", time.Second); err != nil {
		t.Fatal(err)
	}
	if m := f.methods(); !slices.Equal(m, []string{"pane.focus", "agent.focus"}) {
		t.Fatalf("methods = %v", m)
	}
}

func TestAPaneThatHasClosedIsReported(t *testing.T) {
	// Unlike a missing agent, a missing pane means the press did nothing at
	// all, and the daemon logs it rather than swallowing it.
	f := newFake(t)
	f.replies["pane.focus"] = `"error": {"code": "pane_not_found", "message": "pane w9:p1 not found"}`
	err := FocusAgent(f.path, "w9:p1", time.Second)
	var herdrErr *Error
	if !errors.As(err, &herdrErr) || herdrErr.Code != "pane_not_found" {
		t.Fatalf("err = %v", err)
	}
}

func TestAgentsAreReadOffTheList(t *testing.T) {
	f := newFake(t)
	f.replies["agent.list"] = `"result": {"type": "agent_list", "agents": [
		{"pane_id": "w1:p1", "agent": "claude", "agent_status": "blocked", "focused": true}]}`
	agents, err := Agents(f.path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 || agents[0].PaneID != "w1:p1" || agents[0].AgentStatus != "blocked" || !agents[0].Focused {
		t.Fatalf("agents = %+v", agents)
	}
}

func TestTheStreamDeliversEventsAfterTheAcknowledgement(t *testing.T) {
	f := newFake(t)
	f.events = []string{
		`{"event": "pane_focused", "data": {"type": "pane_focused", "pane_id": "w1:p1"}}`,
		`not json, skipped`,
		`{"event": "pane_closed", "data": {"type": "pane_closed", "pane_id": "w1:p2"}}`,
	}
	stream, err := Subscribe(f.path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	for _, want := range []string{"w1:p1", "w1:p2"} {
		event, err := stream.Next()
		if err != nil {
			t.Fatal(err)
		}
		if event.Data.PaneID != want {
			t.Fatalf("event = %+v, want pane %s", event, want)
		}
	}
	if m := f.methods(); !slices.Equal(m, []string{"events.subscribe"}) {
		t.Fatalf("methods = %v", m)
	}
}

func TestClosingTheStreamUnblocksAPendingRead(t *testing.T) {
	// The daemon reads events on a goroutine of their own, and has to be able
	// to drop the connection from outside it.
	f := newFake(t)
	stream, err := Subscribe(f.path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := stream.Next(); done <- err }()
	time.Sleep(50 * time.Millisecond)
	stream.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a closed stream must say so")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not unblock Next")
	}
}

func TestAHerdrThatIsNotThereFailsQuickly(t *testing.T) {
	start := time.Now()
	_, err := Agents(missingPath(t), 500*time.Millisecond)
	if err == nil {
		t.Fatal("no Herdr, no agents")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
}

func TestTheSocketPathFollowsTheEnvironment(t *testing.T) {
	t.Setenv("HERDR_SOCKET_PATH", "")
	t.Setenv("HERDR_SESSION", "")
	base := DefaultSocketPath()
	t.Setenv("HERDR_SESSION", "work")
	if got := DefaultSocketPath(); got == base || !samePath(got, base, "sessions", "work") {
		t.Fatalf("named session = %q (default %q)", got, base)
	}
	t.Setenv("HERDR_SOCKET_PATH", "/explicit/herdr.sock")
	if got := DefaultSocketPath(); got != "/explicit/herdr.sock" {
		t.Fatalf("explicit = %q", got)
	}
}

func TestALiveHerdrAnswers(t *testing.T) {
	// Opt-in: run against the Herdr on this machine, to prove the transport
	// rather than the fake.
	if os.Getenv("HERDRKEYS_LIVE") == "" {
		t.Skip("set HERDRKEYS_LIVE=1 to talk to the running Herdr")
	}
	path := DefaultSocketPath()
	agents, err := Agents(path, 2*time.Second)
	if err != nil {
		t.Fatalf("%s: %v", Endpoint(path), err)
	}
	snapshot, err := GetSnapshot(path, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: %d agents, %d panes", Endpoint(path), len(agents), len(snapshot.Panes))
}
