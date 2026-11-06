package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnendean/herdrkeys/internal/board"
	"github.com/johnendean/herdrkeys/internal/chord"
	"github.com/johnendean/herdrkeys/internal/config"
	"github.com/johnendean/herdrkeys/internal/microphone"
	"github.com/johnendean/herdrkeys/internal/model"
	"github.com/johnendean/herdrkeys/internal/state"
	"github.com/johnendean/herdrkeys/internal/usage"
)

// -- fakes -------------------------------------------------------------------

type fakeHerdr struct {
	mu        sync.Mutex
	snapshots []state.Snapshot // answered in turn; the last one repeats
	agents    func() ([]state.Pane, error)
	focused   []string
	subscribe func() (Stream, error)
}

func (h *fakeHerdr) Subscribe() (Stream, error) {
	if h.subscribe != nil {
		return h.subscribe()
	}
	return nil, errors.New("no herdr")
}

func (h *fakeHerdr) Snapshot() (state.Snapshot, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.snapshots) == 0 {
		return state.Snapshot{}, nil
	}
	s := h.snapshots[0]
	if len(h.snapshots) > 1 {
		h.snapshots = h.snapshots[1:]
	}
	return s, nil
}

func (h *fakeHerdr) Agents() ([]state.Pane, error) {
	if h.agents == nil {
		return nil, nil
	}
	return h.agents()
}

func (h *fakeHerdr) Focus(paneID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.focused = append(h.focused, paneID)
	return nil
}

func (h *fakeHerdr) Endpoint() string { return "fake" }

type quietStream struct{ closed chan struct{} }

func newQuietStream() *quietStream { return &quietStream{closed: make(chan struct{})} }
func (s *quietStream) Next() (state.Event, error) {
	<-s.closed
	return state.Event{}, io.EOF
}
func (s *quietStream) Close() error {
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
	return nil
}

type fakeHost struct {
	board.Fake
	allowed, allowedAfresh bool
	asked                  int
	holds                  []bool
}

func newHost() *fakeHost                { h := &fakeHost{Fake: *board.NewFake(), allowed: true}; return h }
func (h *fakeHost) CanType() bool       { return h.allowed }
func (h *fakeHost) CanTypeAfresh() bool { return h.allowedAfresh }
func (h *fakeHost) Ask()                { h.asked++ }
func (h *fakeHost) Who() string         { return "herdrkeys" }
func (h *fakeHost) Hold(on bool, c chord.Chord) error {
	h.holds = append(h.holds, on)
	return h.Fake.Hold(on, c)
}

type rig struct {
	d        *Daemon
	herdr    *fakeHerdr
	host     *fakeHost
	device   *board.Fake
	raised   int
	gitAsked []string
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newRig(t *testing.T) *rig {
	t.Helper()
	cfg := config.Defaults()
	cfg.SettleSeconds = 0
	r := &rig{herdr: &fakeHerdr{}, host: newHost(), device: board.NewFake()}
	r.d = New(cfg, r.herdr, r.host, quiet())
	r.d.Raise = func() { r.raised++ }
	r.d.PageOf = func(string) string { return "" }
	r.d.GoToGit = func(cwd string) bool { r.gitAsked = append(r.gitAsked, cwd); return cwd == "/code/herdrkeys" }
	r.d.Spawn = func(work func()) { work() }
	b := r.d.AddBoard("keypad", func(float64) (board.Device, error) { return r.device, nil })
	b.Device = r.device
	return r
}

func pane(id, status string, focused bool, cwd ...string) state.Pane {
	agent := "claude"
	p := state.Pane{PaneID: id, Agent: &agent, AgentStatus: status, Revision: 1, Focused: focused}
	if len(cwd) > 0 {
		p.Cwd = cwd[0]
	}
	return p
}

// load takes panes as truth, as a snapshot would, and settles them.
func (r *rig) load(focused string, panes ...state.Pane) {
	s := state.Snapshot{Panes: panes}
	if focused != "" {
		s.FocusedPaneID = &focused
	}
	r.d.State.ApplySnapshot(s)
	r.d.Render(0)
	r.d.Render(1) // a second pass so the settlers commit
}

func keys(f model.Frame) string { return f.KeyString() }

func (r *rig) press(dev *board.Fake, slot int, now float64, upAfter ...float64) error {
	b := r.boardOf(dev)
	if err := r.d.HandleKey(b, board.Key{Slot: slot, Down: true}, now); err != nil {
		return err
	}
	if len(upAfter) > 0 {
		return r.d.HandleKey(b, board.Key{Slot: slot, Down: false}, now+upAfter[0])
	}
	return nil
}

func (r *rig) boardOf(dev *board.Fake) *Board {
	for _, b := range r.d.Boards {
		if b.Device == board.Device(dev) {
			return b
		}
	}
	return &Board{Name: "gone"}
}

func (r *rig) second() *board.Fake {
	dev := board.NewFake()
	b := r.d.AddBoard("second", func(float64) (board.Device, error) { return dev, nil })
	b.Device = dev
	return dev
}

// -- presses -------------------------------------------------------------------

func TestPressingAKeyFocusesThatAgentAndRaisesTheTerminal(t *testing.T) {
	r := newRig(t)
	r.load("w1:p1", pane("w1:p1", "idle", true), pane("w2:p1", "working", false))
	r.d.HandlePress(1, 2)
	if !slices.Equal(r.herdr.focused, []string{"w2:p1"}) || r.raised != 1 {
		t.Fatalf("focused %v, raised %d: focusing what you cannot see is not focusing it", r.herdr.focused, r.raised)
	}
}

func TestPressingAnEmptyKeyDoesNothingAtAll(t *testing.T) {
	r := newRig(t)
	r.load("w1:p1", pane("w1:p1", "idle", true))
	r.d.HandlePress(9, 2)
	if len(r.herdr.focused) != 0 || len(r.device.Flashes) != 0 {
		t.Fatal("something happened")
	}
}

func TestTheFunctionKeyGoesToTheBlockedAgentOrFlashes(t *testing.T) {
	r := newRig(t)
	r.load("w1:p1", pane("w1:p1", "working", true), pane("w2:p1", "blocked", false), pane("w3:p1", "done", false))
	r.d.HandlePress(model.FnSlot, 2)
	if !slices.Equal(r.herdr.focused, []string{"w2:p1"}) {
		t.Fatalf("focused %v: blocked outranks done", r.herdr.focused)
	}

	r = newRig(t)
	r.load("w1:p1", pane("w1:p1", "working", true))
	r.d.HandlePress(model.FnSlot, 2)
	if len(r.herdr.focused) != 0 || !slices.Equal(r.device.Flashes, []int{board.FlashFunctionKey}) {
		t.Fatal("silence is ambiguous; a flash says 'heard you'")
	}
}

func TestActivationCanBeSwitchedOff(t *testing.T) {
	r := newRig(t)
	r.d.Config.ActivateTerminal = false
	r.load("w1:p1", pane("w1:p1", "idle", true))
	r.d.HandlePress(0, 2)
	if len(r.herdr.focused) != 1 || r.raised != 0 {
		t.Fatal("raised anyway")
	}
}

func TestNoFeatureKeyCanFocusAnAgent(t *testing.T) {
	r := newRig(t)
	var panes []state.Pane
	for i := 1; i <= 12; i++ {
		panes = append(panes, pane(fmt.Sprintf("w%d:p1", i), "idle", i == 1))
	}
	r.load("w1:p1", panes...)
	for _, slot := range []int{model.MicSlot, model.ReturnSlot, model.GitSlot} {
		r.press(r.device, slot, 2, 0.1)
	}
	if len(r.herdr.focused) != 0 {
		t.Fatalf("focused %v", r.herdr.focused)
	}
}

// -- the microphone ------------------------------------------------------------

func TestTheHostHoldsTheMicrophoneForABoardThatCannotType(t *testing.T) {
	r := newRig(t)
	r.load("w1:p1", pane("w1:p1", "idle", true))
	r.press(r.device, model.MicSlot, 2)
	if !r.host.Holding || keys(r.d.Render(2))[model.MicSlot] != model.CodeMicOpen {
		t.Fatal("every board shows the one microphone")
	}
	r.d.HandleKey(r.boardOf(r.device), board.Key{Slot: model.MicSlot}, 5)
	if r.host.Holding || keys(r.d.Render(5))[model.MicSlot] != model.CodeMic {
		t.Fatal("let go")
	}
	if len(r.herdr.focused) != 0 || len(r.device.Flashes) != 0 {
		t.Fatal("the microphone focuses nothing")
	}
}

func TestReturnOnABoardThatCannotTypeIsTypedByTheHost(t *testing.T) {
	r := newRig(t)
	r.press(r.device, model.ReturnSlot, 2, 0.1)
	if r.host.Returns != 1 || len(r.device.Flashes) != 0 {
		t.Fatal("not typed")
	}
}

func TestABoardThatCanTypeTypesForItself(t *testing.T) {
	r := newRig(t)
	r.device.TypesItself = true
	r.press(r.device, model.MicSlot, 2)
	if !r.device.Holding || r.host.Holding {
		t.Fatal("the board holds it")
	}
	r.press(r.device, model.ReturnSlot, 3)
	if r.device.Holding {
		t.Fatal("Return closes the microphone first")
	}
	r.press(r.device, model.ReturnSlot, 4)
	if r.device.Returns != 1 || r.host.Returns != 0 {
		t.Fatal("and the next one types")
	}
}

func TestAStartingDaemonLetsGoOfWhatACrashedOneHeld(t *testing.T) {
	// A crash leaves Flow listening -- measured, not supposed -- so starting
	// is the moment to let go (ADR 0017).
	host := newHost()
	New(config.Defaults(), &fakeHerdr{}, host, quiet())
	if !slices.Equal(host.holds, []bool{false}) {
		t.Fatalf("holds %v", host.holds)
	}
	host = newHost()
	host.allowed = false
	New(config.Defaults(), &fakeHerdr{}, host, quiet())
	if len(host.holds) != 0 {
		t.Fatal("without the permission, posting would make macOS ask before anyone has tried to dictate")
	}
}

func TestWithoutThePermissionAPressFlashesAndAsksOnce(t *testing.T) {
	r := newRig(t)
	r.host.allowed = false
	if r.host.asked != 0 {
		t.Fatal("nothing is asked for before it is wanted")
	}
	r.press(r.device, model.MicSlot, 2, 0.1)
	r.press(r.device, model.ReturnSlot, 3)
	r.press(r.device, model.MicSlot, 4, 0.1)
	if r.host.Holding || r.host.Returns != 0 {
		t.Fatal("pretended")
	}
	if keys(r.d.Render(5))[model.MicSlot] != model.CodeMic {
		t.Fatal("the frame must not claim a microphone that is not open")
	}
	if !slices.Equal(r.device.Flashes, []int{model.MicSlot, model.ReturnSlot, model.MicSlot}) || r.host.asked != 1 {
		t.Fatalf("flashes %v, asked %d: one dialog, not one per press", r.device.Flashes, r.host.asked)
	}
}

func TestAPermissionGrantedAfterwardsRestartsTheDaemonToUseIt(t *testing.T) {
	r := newRig(t)
	r.host.allowed = false
	r.press(r.device, model.MicSlot, 2, 0.1)
	r.host.allowedAfresh = true
	if err := r.press(r.device, model.MicSlot, 3); !errors.Is(err, ErrRestart) {
		t.Fatalf("err = %v", err)
	}
	if r.host.Holding {
		t.Fatal("nothing held across the restart")
	}
}

func TestABoardThatTypesForItselfNeedsNoPermission(t *testing.T) {
	r := newRig(t)
	r.device.TypesItself = true
	r.host.allowed = false
	r.press(r.device, model.MicSlot, 2)
	if !r.device.Holding || len(r.device.Flashes) != 0 {
		t.Fatal("asked anyway")
	}
}

func TestALatchTimesOutOnItsOwnAndTheLoopWakesForIt(t *testing.T) {
	r := newRig(t)
	r.press(r.device, model.MicSlot, 2, 0.1)
	if keys(r.d.Render(3))[model.MicSlot] != model.CodeMicLatched {
		t.Fatal("latched")
	}
	if r.d.Timeout(2.1+microphone.LatchTimeout-0.005) > 0.01 {
		t.Fatal("the loop must wake for the timeout")
	}
	r.d.Render(2.1 + microphone.LatchTimeout + 1)
	if r.host.Holding {
		t.Fatal("timed out")
	}
}

func TestClosingTheDaemonClosesTheMicrophoneAndLetsEveryBoardGo(t *testing.T) {
	r := newRig(t)
	second := r.second()
	r.press(r.device, model.MicSlot, 2, 0.1)
	r.d.Close()
	if r.host.Holding || !r.device.Closed || !second.Closed {
		t.Fatal("something held on")
	}
	for _, b := range r.d.Boards {
		if b.Device != nil {
			t.Fatal("a board is still connected")
		}
	}
}

// -- frames --------------------------------------------------------------------

func TestAFrameIsOnlyPushedWhenItChangesAndResentAsAHeartbeat(t *testing.T) {
	r := newRig(t)
	r.load("w1:p1", pane("w1:p1", "idle", true))
	r.d.Push(r.d.Render(10), 10)
	r.d.Push(r.d.Render(11), 11)
	if len(r.device.Frames) != 1 {
		t.Fatal("unchanged and not yet due")
	}
	r.d.Push(r.d.Render(12.5), 12.5)
	if len(r.device.Frames) != 2 || r.device.Frames[1] != r.device.Frames[0] {
		t.Fatal("unchanged but due: the board reads silence as the daemon having died")
	}
	blocked := pane("w1:p1", "blocked", true)
	blocked.Revision = 2
	r.d.State.ApplySnapshot(state.Snapshot{Panes: []state.Pane{blocked}})
	r.d.Push(r.d.Render(13), 13)
	if last, _ := r.device.LastFrame(); len(r.device.Frames) != 3 || keys(last)[0] != 'b' {
		t.Fatalf("frames %d", len(r.device.Frames))
	}
}

func TestDisconnectedFromHerdrIsVisibleOnTheFunctionKey(t *testing.T) {
	r := newRig(t)
	if keys(r.d.Render(0))[model.FnSlot] != model.CodeFnDisconnected {
		t.Fatal("dark keys must not mean two things")
	}
}

func TestTheHeartbeatIsFasterThanTheBoardsStalenessTimeout(t *testing.T) {
	// The board discards a frame nobody has refreshed; if these two ever
	// cross, a healthy session would flicker to "no host".
	source, err := os.ReadFile("../../device/code.py")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^HOST_TIMEOUT = ([\d.]+)`).FindSubmatch(source)
	if m == nil {
		t.Fatal("device/code.py must declare HOST_TIMEOUT")
	}
	timeout, _ := strconv.ParseFloat(string(m[1]), 64)
	if HeartbeatSeconds >= timeout/2 {
		t.Fatalf("heartbeat %v against a timeout of %v", HeartbeatSeconds, timeout)
	}
	r := newRig(t)
	r.d.Boards[0].lastPush = 100
	if r.d.Timeout(100) > HeartbeatSeconds {
		t.Fatal("the heartbeat keeps the loop awake")
	}
}

func TestBackoffWidensIsCappedAndResets(t *testing.T) {
	b := NewBackoff(0.5, 4)
	b.Failed(0)
	if b.Ready(0.4) || !b.Ready(0.5) {
		t.Fatal("first wait")
	}
	b.Failed(0.5)
	if b.Ready(1.4) || !b.Ready(1.5) {
		t.Fatal("doubled")
	}
	for range 10 {
		b.Failed(0)
	}
	if b.WaitFor(0) > 4 {
		t.Fatal("capped")
	}
	b.Succeeded()
	if !b.Ready(0) {
		t.Fatal("reset")
	}
}

// -- herdr -----------------------------------------------------------------------

func snapshot(panes ...state.Pane) state.Snapshot { return state.Snapshot{Panes: panes} }

func TestTheBacklogIsReconciledOnceTheStreamGoesQuiet(t *testing.T) {
	// The replay is rate limited, so a fixed delay would race it. The second
	// snapshot is what picks up anything that changed while it drained.
	r := newRig(t)
	idle, blocked := pane("w1:p1", "idle", false), pane("w1:p1", "blocked", false)
	idle.Revision, blocked.Revision = 36, 36
	r.herdr.snapshots = []state.Snapshot{snapshot(idle), snapshot(blocked)}
	r.herdr.subscribe = func() (Stream, error) { return newQuietStream(), nil }

	if err := r.d.ConnectHerdr(100); err != nil {
		t.Fatal(err)
	}
	if !r.d.backlogSettling || r.d.State.Panes["w1:p1"].State != model.Idle {
		t.Fatal("the connect snapshot is truth")
	}
	r.d.PumpHerdr(100.5, nil)
	if !r.d.backlogSettling {
		t.Fatal("quiet, but not long enough")
	}
	r.d.PumpHerdr(101, nil)
	if r.d.backlogSettling || r.d.State.Panes["w1:p1"].State != model.Blocked {
		t.Fatal("picks up what changed")
	}
}

func TestHistoryIsDiscardedRatherThanApplied(t *testing.T) {
	// Replaying the backlog would walk the session's past across the keys:
	// focus strobing between agents, agents appearing and vanishing.
	r := newRig(t)
	focused := "w1:p1"
	r.herdr.snapshots = []state.Snapshot{{Panes: []state.Pane{pane("w1:p1", "idle", true)}, FocusedPaneID: &focused}}
	r.herdr.subscribe = func() (Stream, error) { return newQuietStream(), nil }
	r.d.ConnectHerdr(0)
	r.d.Push(r.d.Render(0.1), 0.1)
	if len(r.device.Frames) != 1 {
		t.Fatal("the connect snapshot is truth; show it at once")
	}
	history := state.Event{Data: state.EventData{Type: "pane_focused", PaneID: "w2:p1"}}
	r.d.PumpHerdr(0.2, []state.Event{history})
	if r.d.State.FocusedPaneID != "w1:p1" {
		t.Fatal("history must not move focus")
	}
	r.d.PumpHerdr(1.5, nil)
	r.d.PumpHerdr(1.6, []state.Event{{Data: state.EventData{Type: "pane_focused", PaneID: "w9:p9"}}})
	if r.d.State.FocusedPaneID != "w9:p9" {
		t.Fatal("live events are applied normally")
	}
}

func TestABusySessionStillLightsUpEventually(t *testing.T) {
	r := newRig(t)
	r.d.backlogSettling, r.d.lastEventAt, r.d.backlogDeadline = true, 100, 100+BacklogMaxSeconds
	if r.d.BacklogDone(100.5) || !r.d.BacklogDone(100+BacklogMaxSeconds) {
		t.Fatal("waiting for quiet would wait forever")
	}
	r.d.DropHerdr(0, "gone")
	if r.d.backlogSettling {
		t.Fatal("losing Herdr clears the backlog wait")
	}
}

func TestStatusComesFromThePollWhenNoEventEverArrives(t *testing.T) {
	// Measured live: the stream replays its backlog and then goes quiet. See
	// ADR 0005.
	r := newRig(t)
	r.load("w1:p1", pane("w1:p1", "working", true))
	r.herdr.agents = func() ([]state.Pane, error) { return []state.Pane{pane("w1:p1", "blocked", true)}, nil }
	r.d.Stream = newQuietStream()
	r.d.PollStatus(10)
	r.d.Push(r.d.Render(10), 10)
	if last, _ := r.device.LastFrame(); keys(last)[0] != 'B' {
		t.Fatal("no event arrived, and the key changed anyway")
	}
	if r.d.nextStatusPoll != 10.5 {
		t.Fatal("the poll paces itself")
	}
}

func TestAnAgentLeavingFreesItsKeyAndTheRestMoveUp(t *testing.T) {
	r := newRig(t)
	r.load("w1:p1", pane("w1:p1", "idle", true), pane("w2:p1", "working", false), pane("w3:p1", "blocked", false))
	if keys(r.d.Render(1))[:3] != "Iwb" {
		t.Fatal(keys(r.d.Render(1)))
	}
	r.herdr.agents = func() ([]state.Pane, error) {
		return []state.Pane{pane("w1:p1", "idle", true), pane("w3:p1", "blocked", false)}, nil
	}
	r.d.PollStatus(10)
	r.d.Render(10)
	if got := keys(r.d.Render(10))[:3]; got != "Ib-" {
		t.Fatalf("%q: w3:p1 takes the key w2:p1 had", got)
	}
}

func TestTheOrderHoldsStillThroughASingleBadPoll(t *testing.T) {
	r := newRig(t)
	r.d.Order.SettleSeconds = 1
	r.load("w1:p1", pane("w1:p1", "idle", true), pane("w2:p1", "working", false), pane("w3:p1", "blocked", false))
	r.d.Render(2)
	rows := []state.Pane{pane("w1:p1", "idle", true), pane("w3:p1", "blocked", false)}
	r.herdr.agents = func() ([]state.Pane, error) { return rows, nil }
	r.d.PollStatus(10)
	if got := keys(r.d.Render(10))[:3]; got != "I-b" {
		t.Fatalf("%q: one missing row is not yet a departure", got)
	}
	if r.d.Timeout(10) > 1 {
		t.Fatal("the loop must wake to commit the new order")
	}
	rows = []state.Pane{pane("w1:p1", "idle", true), pane("w2:p1", "working", false), pane("w3:p1", "blocked", false)}
	r.d.PollStatus(10.2)
	if got := keys(r.d.Render(10.2))[:3]; got != "Iwb" {
		t.Fatalf("%q: the blip cost nobody a key", got)
	}
}

func TestTheKeysTakeTheOrderHerdrListsAgentsIn(t *testing.T) {
	r := newRig(t)
	r.load("w1:p1", pane("w1:p1", "idle", true), pane("w2:p1", "idle", false))
	r.herdr.agents = func() ([]state.Pane, error) {
		return []state.Pane{pane("w2:p1", "working", false), pane("w1:p1", "blocked", true)}, nil
	}
	r.d.PollStatus(10)
	r.d.Render(10)
	if got := keys(r.d.Render(10))[:2]; got != "wB" {
		t.Fatalf("%q", got)
	}
	r.d.HandlePress(0, 10)
	if !slices.Equal(r.herdr.focused, []string{"w2:p1"}) {
		t.Fatal("the key goes where the board says it does")
	}
}

func TestAFailedPollDropsHerdr(t *testing.T) {
	r := newRig(t)
	r.d.Stream = newQuietStream()
	r.herdr.agents = func() ([]state.Pane, error) { return nil, errors.New("agent.list: connection closed") }
	r.d.PollStatus(5)
	if r.d.Stream != nil {
		t.Fatal("the function key must show Herdr is gone")
	}
}

// -- the git key -----------------------------------------------------------------

func TestTheGitKeyFollowsFocus(t *testing.T) {
	r := newRig(t)
	r.load("w2:p1", pane("w1:p1", "idle", false, "/elsewhere"), pane("w2:p1", "idle", true, "/code/herdrkeys"))
	r.d.HandlePress(model.GitSlot, 2)
	if !slices.Equal(r.gitAsked, []string{"/code/herdrkeys"}) || len(r.device.Flashes) != 0 {
		t.Fatalf("asked %v, flashes %v", r.gitAsked, r.device.Flashes)
	}
}

func TestNowhereToGoFlashesTheGitKeyItself(t *testing.T) {
	// Not the function key: that would say "nothing wants your attention".
	for _, c := range []struct {
		why   string
		panes []state.Pane
		asks  int
	}{
		{"outside a repository", []state.Pane{pane("w1:p1", "idle", true, "/elsewhere")}, 1},
		{"no focused agent", []state.Pane{pane("w1:p1", "idle", false, "/code/herdrkeys")}, 0},
		{"no directory", []state.Pane{pane("w1:p1", "idle", true)}, 0},
	} {
		r := newRig(t)
		focused := ""
		if c.panes[0].Focused {
			focused = "w1:p1"
		}
		r.load(focused, c.panes...)
		r.d.HandlePress(model.GitSlot, 2)
		if len(r.gitAsked) != c.asks || !slices.Equal(r.device.Flashes, []int{model.GitSlot}) {
			t.Fatalf("%s: asked %v, flashes %v", c.why, r.gitAsked, r.device.Flashes)
		}
	}
}

func TestTheKeyShowsItIsLookingUntilTheLookupLands(t *testing.T) {
	r := newRig(t)
	var parked []func()
	r.d.Spawn = func(work func()) { parked = append(parked, work) }
	r.load("w1:p1", pane("w1:p1", "idle", true, "/elsewhere"))
	r.d.HandlePress(model.GitSlot, 2)
	if keys(r.d.Render(2))[model.GitSlot] != model.CodeGitLooking {
		t.Fatal("not looking")
	}
	r.d.HandlePress(model.GitSlot, 2.1)
	if len(parked) != 1 {
		t.Fatal("a second press while looking is ignored, not queued")
	}
	parked[0]()
	if len(r.device.Flashes) != 0 {
		t.Fatal("the flash comes from the loop, not the lookup")
	}
	r.d.CollectGitLookup(3)
	if keys(r.d.Render(3))[model.GitSlot] == model.CodeGitLooking || !slices.Equal(r.device.Flashes, []int{model.GitSlot}) {
		t.Fatal("landed")
	}
}

func TestALookupThatPanicsDoesNotLeaveTheKeyLooking(t *testing.T) {
	r := newRig(t)
	r.d.GoToGit = func(string) bool { panic("gh exploded") }
	r.load("w1:p1", pane("w1:p1", "idle", true, "/code/herdrkeys"))
	r.d.HandlePress(model.GitSlot, 2)
	if r.d.gitLookup != nil || !slices.Equal(r.device.Flashes, []int{model.GitSlot}) {
		t.Fatal("stuck looking")
	}
}

func TestTheFrameSaysWhetherTheFocusedAgentHasAPageAndWhere(t *testing.T) {
	r := newRig(t)
	pages := map[string]string{"/code/herdrkeys": "https://github.com/johnendean/herdrkeys", "/code/elsewhere": "https://git.example.com/team/repo"}
	r.d.PageOf = func(cwd string) string { return pages[cwd] }
	r.load("w1:p1", pane("w1:p1", "idle", true, "/code/herdrkeys"), pane("w2:p1", "idle", false, "/code/elsewhere"))
	if f := r.d.Render(2); f.Keys[model.GitSlot] != model.CodeGitPage || f.Forge != "github" {
		t.Fatalf("%q %q", f.Keys[model.GitSlot], f.Forge)
	}
	r.d.State.FocusedPaneID = "w2:p1"
	if f := r.d.Render(3); f.Keys[model.GitSlot] != model.CodeGitPage || f.Forge != "" {
		t.Fatal("a page on a host we cannot be sure of is a page, with no forge")
	}
	r.d.State.FocusedPaneID = ""
	if f := r.d.Render(4); f.Keys[model.GitSlot] != model.CodeGitNoPage {
		t.Fatal("no focus, no page")
	}
}

// -- more than one board -----------------------------------------------------------

func TestEveryBoardIsShownTheSameFrameAndAPressOnEitherMeansTheSame(t *testing.T) {
	r := newRig(t)
	second := r.second()
	r.load("", pane("w1:p1", "idle", false), pane("w2:p1", "idle", false))
	r.d.Push(r.d.Render(2), 2)
	a, _ := r.device.LastFrame()
	b, _ := second.LastFrame()
	if a != b {
		t.Fatal("different frames")
	}
	r.press(second, 1, 2)
	if !slices.Equal(r.herdr.focused, []string{"w2:p1"}) {
		t.Fatal("a press on the second board")
	}
	r.d.HandlePress(model.FnSlot, 3)
	if len(r.device.Flashes) != 1 || len(second.Flashes) != 1 {
		t.Fatal("a flash shows on every board")
	}
}

func TestLosingOneBoardLeavesTheOtherLit(t *testing.T) {
	r := newRig(t)
	second := r.second()
	r.load("", pane("w1:p1", "idle", false))
	r.device.Fail = errors.New("gone")
	r.d.Push(r.d.Render(2), 2)
	if r.d.Boards[0].Device != nil {
		t.Fatal("the failed board was kept")
	}
	if _, ok := second.LastFrame(); !ok {
		t.Fatal("the other board never noticed")
	}
}

func TestLosingTheBoardThatOpenedTheMicrophoneClosesIt(t *testing.T) {
	r := newRig(t)
	second := r.second()
	r.press(second, model.MicSlot, 2, 0.1)
	if !r.host.Holding {
		t.Fatal("not open")
	}
	r.d.DropBoard(r.d.Boards[1], 3, "unplugged")
	if r.host.Holding {
		t.Fatal("a CN001 unplugged mid-dictation must not leave Flow listening")
	}
}

func TestAKeybowThatCannotBeToldToTypeIsDroppedNotTheDaemon(t *testing.T) {
	r := newRig(t)
	r.device.TypesItself = true
	r.device.Fail = errors.New("gone")
	r.press(r.device, model.MicSlot, 2)
	if r.d.Boards[0].Device != nil {
		t.Fatal("kept")
	}
	if keys(r.d.Render(2))[model.MicSlot] != model.CodeMic {
		t.Fatal("a microphone nobody holds is not open")
	}
}

func TestAnAbsentBoardIsWaitedForNotAnError(t *testing.T) {
	r := newRig(t)
	b := &Board{Name: "absent", Connect: func(float64) (board.Device, error) { return nil, nil }, Backoff: NewBackoff(1, 1)}
	r.d.ConnectBoard(b, 5)
	if b.Device != nil || b.Backoff.Ready(5.5) {
		t.Fatal("absent")
	}
}

// -- provisioning ------------------------------------------------------------------

type fakeProvisioner struct {
	console         string
	drive           string
	mayWrite, found bool
	copied, reset   []string
	inspected       int
}

func (p *fakeProvisioner) ConsolePort() string { return p.console }
func (p *fakeProvisioner) Inspect() (string, bool, bool) {
	p.inspected++
	return p.drive, p.mayWrite, p.found
}
func (p *fakeProvisioner) CopyFirmware(d string) error { p.copied = append(p.copied, d); return nil }
func (p *fakeProvisioner) HardReset(c string) bool     { p.reset = append(p.reset, c); return true }

func TestOursOrEmptyIsProvisionedAndSomebodyElsesNever(t *testing.T) {
	r := newRig(t)
	p := &fakeProvisioner{console: "COM3", drive: `D:\`, mayWrite: true, found: true}
	r.d.Provisioner = p
	r.d.MaybeProvision(0)
	if !slices.Equal(p.copied, []string{`D:\`}) || !slices.Equal(p.reset, []string{"COM3"}) {
		t.Fatalf("%v %v: boot.py needs a re-enumeration to take effect", p.copied, p.reset)
	}

	var logged strings.Builder
	r = newRig(t)
	r.d.Log = slog.New(slog.NewTextHandler(&logged, nil))
	foreign := &fakeProvisioner{console: "COM3", drive: `D:\`, found: true}
	r.d.Provisioner = foreign
	for tick := 0; tick < 600; tick += 60 {
		r.d.provisionBackoff.Succeeded()
		r.d.MaybeProvision(float64(tick))
	}
	if len(foreign.copied) != 0 || len(foreign.reset) != 0 {
		t.Fatal("installing herdrkeys is not consent to destroy this")
	}
	if strings.Count(logged.String(), "leaving it alone") != 1 || !strings.Contains(logged.String(), "adopt") {
		t.Fatalf("%s: told once, and told how", logged.String())
	}
}

func TestProvisioningCanBeSwitchedOffAndNoKeybowIsNothing(t *testing.T) {
	r := newRig(t)
	p := &fakeProvisioner{console: "COM3", drive: `D:\`, mayWrite: true, found: true}
	r.d.Provisioner = p
	r.d.Config.Provision = false
	r.d.MaybeProvision(0)
	none := &fakeProvisioner{}
	r.d.Config.Provision = true
	r.d.Provisioner = none
	r.d.MaybeProvision(0)
	if len(p.copied) != 0 || none.inspected != 0 {
		t.Fatal("no board means nothing to inspect")
	}
}

// -- usage -------------------------------------------------------------------------

type stripBoard struct {
	*board.Fake
	shown []bool // stale, for each reading shown
}

func (s *stripBoard) ShowUsage(u *usage.Usage, stale bool) error {
	if u != nil {
		s.shown = append(s.shown, stale)
	}
	return nil
}

func TestUsageIsAskedForOnlyWithABoardToShowItOn(t *testing.T) {
	r := newRig(t)
	asked := 0
	r.d.Usage = &usage.Poller{Ask: func() (usage.Usage, error) { asked++; return usage.Usage{}, nil }}
	r.d.PollUsage(0)
	if asked != 0 {
		t.Fatal("asked with nowhere to show it")
	}
	strip := &stripBoard{Fake: board.NewFake()}
	b := r.d.AddBoard("cn001", func(float64) (board.Device, error) { return strip, nil })
	b.Device = strip
	r.d.PollUsage(0)
	r.d.Usage.Wait(0)
	r.d.Push(r.d.Render(0), 0)
	r.d.Push(r.d.Render(400), 400)
	if asked != 1 || !slices.Equal(strip.shown, []bool{false, true}) {
		t.Fatalf("asked %d, shown %v: five minutes without a reading is stale", asked, strip.shown)
	}
	r.d.Config.Usage = false
	r.d.Usage = &usage.Poller{Ask: func() (usage.Usage, error) { asked++; return usage.Usage{}, nil }}
	r.d.PollUsage(1000)
	if asked != 1 {
		t.Fatal("usage can be turned off")
	}
}

// -- the loop ----------------------------------------------------------------------

func TestTheLoopReadsPressesAndDropsAnUnpluggedBoard(t *testing.T) {
	r := newRig(t)
	r.d.Boards[0].Device = nil // the loop connects it
	focusedID := "w1:p1"
	r.herdr.snapshots = []state.Snapshot{{Panes: []state.Pane{pane("w1:p1", "working", true)}, FocusedPaneID: &focusedID}}
	r.herdr.agents = func() ([]state.Pane, error) { return []state.Pane{pane("w1:p1", "working", true)}, nil }
	r.herdr.subscribe = func() (Stream, error) { return newQuietStream(), nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.d.Run(ctx) }()

	waitFor(t, func() bool { _, ok := r.device.LastFrame(); return ok }, "a frame")
	time.Sleep(50 * time.Millisecond) // past the settle time, so the key is someone's
	r.device.Press(0, true)
	waitFor(t, func() bool { r.herdr.mu.Lock(); defer r.herdr.mu.Unlock(); return len(r.herdr.focused) == 1 }, "the press")

	r.device.Unplug(board.ErrGone)
	waitFor(t, func() bool { return r.device.Closed }, "the unplug")
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, ok func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
