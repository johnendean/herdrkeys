// Package daemon wires Herdr on one side and the boards on the other, with the
// reducer in between.
//
// Every side fails independently and routinely -- you unplug a keypad, Herdr
// restarts on update -- so the daemon never exits voluntarily. Herdr and each
// board have their own reconnect loop with capped backoff. Cycling the process
// for a routine reconnect would drop the connection to Herdr for no reason,
// and the keys go dark while it is gone.
//
// The methods here are the steps of one pass of the loop, so each can be
// driven directly by a test; Run is the loop that strings them together.
package daemon

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/johnendean/herdrkeys/internal/board"
	"github.com/johnendean/herdrkeys/internal/config"
	"github.com/johnendean/herdrkeys/internal/microphone"
	"github.com/johnendean/herdrkeys/internal/model"
	"github.com/johnendean/herdrkeys/internal/reducer"
	"github.com/johnendean/herdrkeys/internal/remote"
	"github.com/johnendean/herdrkeys/internal/settling"
	"github.com/johnendean/herdrkeys/internal/state"
	"github.com/johnendean/herdrkeys/internal/usage"
)

const (
	// IdleTimeout is the longest the loop sleeps with nothing due.
	IdleTimeout = 1.0
	// HeartbeatSeconds: frames are only pushed when they change, so a steady
	// session can go minutes without one. The board treats silence as the
	// daemon having died, so the current frame is resent this often to prove
	// otherwise. Must be comfortably shorter than HOST_TIMEOUT in
	// device/code.py.
	HeartbeatSeconds = 2.0
	// Herdr replays a backlog on subscribe, and a replayed event can carry the
	// same per-pane revision as the snapshot taken alongside it -- Herdr does
	// not bump `revision` for every status change. The revision guard drops
	// strictly-older updates but cannot drop those, so history folded after a
	// snapshot overwrites it and the grid shows a state the agent was in
	// minutes ago.
	//
	// The replay is also rate-limited: 94 backlog events took about four
	// seconds to arrive on a real session, and how many there are depends on
	// how long that session has been alive. So the corrective reconcile waits
	// for the stream to go quiet rather than for a fixed delay, which would
	// race a longer backlog.
	BacklogQuietSeconds = 0.75
	// ...but a busy session might never fall quiet, and the grid has to light
	// up eventually. Reconcile regardless once this long has passed since
	// connecting.
	BacklogMaxSeconds = 10.0
)

// ErrRestart is the daemon wanting to start over as a fresh process.
//
// Returned once it can see that the permission to type has been granted:
// macOS tells a running process about a grant only if it had one when it
// started, so the grant made in the dialog the daemon raised is one only a new
// process can use (ADR 0017).
var ErrRestart = errors.New("restart to use the permission to type")

// Herdr is what the daemon asks of Herdr.
type Herdr interface {
	Subscribe() (Stream, error)
	Snapshot() (state.Snapshot, error)
	Agents() ([]state.Pane, error)
	Focus(paneID string) error
	// Where it is, for the log.
	Endpoint() string
}

// Stream is a subscribed event stream.
type Stream interface {
	Next() (state.Event, error)
	Close() error
}

// HostTypist types for a board that cannot type for itself.
type HostTypist interface {
	board.Typist
	// CanType reports whether this process may type.
	CanType() bool
	// CanTypeAfresh reports whether a process started now could.
	CanTypeAfresh() bool
	// Ask asks the OS for the permission, once.
	Ask()
	// Who names what the permission must be given to, for the log.
	Who() string
}

// StripBoard is a board with somewhere to show usage, like the CN001.
type StripBoard interface {
	ShowUsage(u *usage.Usage, stale bool) error
}

// Provisioner gets firmware onto a Keybow. See package keybow.
type Provisioner interface {
	// ConsolePort is the Keybow's console port, or "" with none attached.
	ConsolePort() string
	// Inspect finds its drive; false when none is mounted.
	Inspect() (path string, mayWrite bool, found bool)
	CopyFirmware(drive string) error
	HardReset(consolePort string) bool
}

// Board is one keypad: how to reach it, and what it was last shown.
//
// Every board is shown the same frame and every board's press means the same
// thing, so the daemon holds one of these per kind of keypad and treats them
// alike (CONTEXT.md, "Board"). Absent is a normal state for any of them.
type Board struct {
	Name string
	// Connect returns the board, or nil and no error when no such board is
	// attached. An error means one is attached and will not talk.
	Connect   func(now float64) (board.Device, error)
	Backoff   *Backoff
	Device    board.Device
	lastFrame *model.Frame
	lastPush  float64
	lastError string
}

// Backoff paces reconnects.
type Backoff struct {
	Min, Max    float64
	nextAttempt float64
	delay       float64
}

// NewBackoff starts at min and doubles up to max.
func NewBackoff(min, max float64) *Backoff { return &Backoff{Min: min, Max: max, delay: min} }

func (b *Backoff) Ready(now float64) bool { return now >= b.nextAttempt }

func (b *Backoff) Failed(now float64) {
	b.nextAttempt = now + b.delay
	b.delay = min(b.Max, b.delay*2)
}

func (b *Backoff) Succeeded() { b.delay, b.nextAttempt = b.Min, 0 }

func (b *Backoff) WaitFor(now float64) float64 { return max(0, b.nextAttempt-now) }

// Daemon is the daemon's state.
type Daemon struct {
	Config      config.Config
	Herdr       Herdr
	Boards      []*Board
	HostTypist  HostTypist
	Provisioner Provisioner // nil: no Keybow to provision
	Usage       *usage.Poller
	Log         *slog.Logger
	// Raise brings the terminal hosting Herdr to the front. Best effort.
	Raise func()
	// GoToGit is the git key's whole journey for a directory, run off the
	// loop. False if there was nowhere to go.
	GoToGit func(cwd string) bool
	// Spawn runs work off the loop.
	Spawn func(work func())
	// PageOf is a directory's page, read cheaply enough for every frame.
	PageOf func(cwd string) string

	State    *state.State
	Settler  *settling.Settler
	Order    *settling.OrderSettler
	Mic      *microphone.Microphone
	Stream   Stream
	streamID int

	herdrBackoff     *Backoff
	provisionBackoff *Backoff
	nextReconcile    float64
	nextStatusPoll   float64
	backlogSettling  bool
	lastEventAt      float64
	backlogDeadline  float64
	refused          string
	askedToType      bool
	gitLookup        chan bool // the git key's lookup, while one runs
}

// New is a daemon with no connections yet.
func New(cfg config.Config, herdr Herdr, host HostTypist, log *slog.Logger) *Daemon {
	if log == nil {
		log = slog.Default()
	}
	d := &Daemon{
		Config:           cfg,
		Herdr:            herdr,
		HostTypist:       host,
		Log:              log,
		State:            state.New(),
		Settler:          settling.NewSettler(cfg.SettleSeconds),
		Order:            settling.NewOrderSettler(cfg.SettleSeconds),
		Mic:              microphone.New(cfg.DictationChord),
		Usage:            usage.NewPoller(log),
		herdrBackoff:     NewBackoff(cfg.ReconnectMinSeconds, cfg.ReconnectMaxSeconds),
		provisionBackoff: NewBackoff(5, 60),
		Raise:            func() {},
		GoToGit:          GoToGit,
		Spawn:            func(work func()) { go work() },
		PageOf:           remote.PageFromConfig,
	}
	// A daemon that crashed holding the chord left Flow listening, and
	// starting again is what brings us here: let go before anything else. A
	// release with nothing held does nothing, so this costs nothing on an
	// ordinary start. Unless it cannot type: then nothing it posts lands, and
	// posting anyway makes macOS ask for the permission before anyone has
	// tried to dictate.
	if host.CanType() {
		d.quietly(host.Hold(false, cfg.DictationChord))
	}
	return d
}

// AddBoard adds a kind of keypad for the daemon to look for.
func (d *Daemon) AddBoard(name string, connect func(now float64) (board.Device, error)) *Board {
	b := &Board{Name: name, Connect: connect,
		Backoff: NewBackoff(d.Config.ReconnectMinSeconds, d.Config.ReconnectMaxSeconds)}
	d.Boards = append(d.Boards, b)
	return b
}

// GoToGit opens the most specific place a directory's repository has: its
// branch's pull request, the open ones, or the repository's page (ADR 0013).
func GoToGit(cwd string) bool {
	page := remote.PageFor(cwd)
	if page == "" {
		return false
	}
	pulls, err := remote.OpenPullRequests(page)
	if err != nil {
		// The page it is, as the key always did -- but say why, since
		// nothing on the board can.
		slog.Info("pull requests unknown, so opening the page: " + err.Error())
	}
	url := remote.Destination(page, remote.CurrentBranch(cwd), pulls)
	slog.Info("opening " + url)
	remote.OpenURL(url)
	return true
}

func (d *Daemon) quietly(err error) {
	if err != nil {
		d.Log.Warn(fmt.Sprintf("could not type: %v", err))
	}
}

// -- connections -------------------------------------------------------------

// ConnectHerdr subscribes, and takes a snapshot as truth.
func (d *Daemon) ConnectHerdr(now float64) error {
	stream, err := d.Herdr.Subscribe()
	if err == nil {
		d.Stream = stream
		d.streamID++
		err = d.reconcile(now)
	}
	if err != nil {
		if d.Stream != nil {
			d.Stream.Close()
		}
		d.Stream = nil
		d.herdrBackoff.Failed(now)
		d.Log.Debug(fmt.Sprintf("herdr connect failed: %v", err))
		return err
	}
	d.backlogSettling = true
	d.lastEventAt = now
	d.backlogDeadline = now + BacklogMaxSeconds
	d.herdrBackoff.Succeeded()
	d.Log.Info("connected to herdr at " + d.Herdr.Endpoint())
	return nil
}

// DropHerdr lets go of Herdr and forgets everything it said.
func (d *Daemon) DropHerdr(now float64, reason string) {
	d.Log.Warn("herdr connection lost: " + reason)
	if d.Stream != nil {
		d.Stream.Close()
	}
	d.Stream = nil
	d.State = state.New()
	d.backlogSettling = false
	d.herdrBackoff.Failed(now)
}

// ConnectBoard tries one board.
func (d *Daemon) ConnectBoard(b *Board, now float64) {
	device, err := b.Connect(now)
	if err != nil {
		b.Device = nil
		b.Backoff.Failed(now)
		// Once per distinct reason: a board that will not talk usually keeps
		// not talking for the same reason -- another app holding the CN001,
		// say -- and a line every backoff says nothing new.
		if err.Error() != b.lastError {
			b.lastError = err.Error()
			d.Log.Warn(fmt.Sprintf("%s connect failed: %v", b.Name, err))
		}
		return
	}
	if device == nil {
		b.Backoff.Failed(now)
		return
	}
	b.Device = device
	b.Backoff.Succeeded()
	b.lastError = ""
	b.lastFrame = nil // force a full frame onto a fresh board
	d.Log.Info(fmt.Sprintf("connected to %s (%s)", b.Name, device.Firmware()))
}

// MaybeProvision puts the firmware on a Keybow that has no data channel yet.
//
// Only a board already running herdrkeys, or carrying nothing, is written to.
// Somebody else's firmware is left alone: it may be the only copy they have,
// and installing herdrkeys is not consent to destroy it days later when a
// different board gets plugged in (ADR 0004).
func (d *Daemon) MaybeProvision(now float64) {
	p := d.Provisioner
	if p == nil || !d.Config.Provision || !d.provisionBackoff.Ready(now) {
		return
	}
	console := p.ConsolePort()
	if console == "" {
		return // no Keybow attached at all
	}
	drive, mayWrite, found := p.Inspect()
	if !found {
		d.provisionBackoff.Failed(now)
		d.Log.Warn("keybow present but its CIRCUITPY drive is not mounted")
		return
	}
	if !mayWrite {
		d.provisionBackoff.Failed(now)
		if d.refused != drive {
			d.refused = drive
			d.Log.Warn(drive + " carries firmware that is not herdrkeys; leaving it alone. " +
				"Run 'herdrkeys adopt' to save it and take the board over.")
		}
		return
	}
	d.refused = ""
	if err := p.CopyFirmware(drive); err != nil {
		d.provisionBackoff.Failed(now)
		d.Log.Warn(fmt.Sprintf("could not write firmware to %s: %v", drive, err))
		return
	}
	d.Log.Info("wrote firmware to " + drive + "; resetting the board")
	if !p.HardReset(console) {
		d.Log.Warn("could not reset the board; unplug and replug it to finish")
	}
	d.provisionBackoff.Failed(now)
}

// DropBoard lets go of a board that has gone.
func (d *Daemon) DropBoard(b *Board, now float64, reason string) {
	if b.Device == nil {
		return
	}
	d.Log.Warn(fmt.Sprintf("%s connection lost: %s", b.Name, reason))
	// A CN001 unplugged mid-dictation would otherwise leave the host holding
	// the chord for a board that is no longer there.
	d.quietly(d.Mic.Lost(d.typist(b.Device)))
	b.Device.Close()
	b.Device = nil
	b.lastFrame = nil
	b.Backoff.Failed(now)
}

func (d *Daemon) connected() []*Board {
	var out []*Board
	for _, b := range d.Boards {
		if b.Device != nil {
			out = append(out, b)
		}
	}
	return out
}

// flash flashes a key on every board: a flash is part of what the boards show.
func (d *Daemon) flash(slot int, now float64) {
	for _, b := range d.connected() {
		if err := b.Device.Flash(slot); err != nil {
			d.DropBoard(b, now, err.Error())
		}
	}
}

// Close lets every board go. A CN001 blanks itself; a Keybow notices silence.
func (d *Daemon) Close() {
	d.quietly(d.Mic.Close())
	for _, b := range d.connected() {
		b.Device.Close()
		b.Device = nil
	}
	if d.Stream != nil {
		d.Stream.Close()
		d.Stream = nil
	}
}

// -- herdr ingest ------------------------------------------------------------

// reconcile takes a snapshot as truth.
//
// Needed on every reconnect, and periodically: the backlog Herdr replays on
// subscribe is not guaranteed to reach back to session start, so folding
// events alone could leave a pane the daemon never heard about.
func (d *Daemon) reconcile(now float64) error {
	snapshot, err := d.Herdr.Snapshot()
	if err != nil {
		return err
	}
	d.State.ApplySnapshot(snapshot)
	d.nextReconcile = now + d.Config.ReconcileSeconds
	return nil
}

// PollStatus asks Herdr what every agent is doing.
//
// The event stream cannot be relied on for this. Measured against a live
// session: `events.subscribe` replays its backlog at about ten events a second
// and then goes quiet, delivering nothing for the next two minutes while five
// real status changes happened. Rendering what the stream says therefore
// means rendering the last reconcile, up to thirty seconds stale. Asking
// costs 0.22ms, so ask often and treat the stream as a bonus.
func (d *Daemon) PollStatus(now float64) {
	rows, err := d.Herdr.Agents()
	if err != nil {
		d.DropHerdr(now, err.Error())
		return
	}
	d.State.ApplyAgents(rows)
	d.nextStatusPoll = now + d.Config.StatusPollSeconds
}

// PumpHerdr folds the events that have arrived.
func (d *Daemon) PumpHerdr(now float64, events []state.Event) {
	if d.Stream == nil {
		return
	}
	if len(events) > 0 {
		d.lastEventAt = now
	}
	if d.backlogSettling {
		// Herdr is replaying history. Applying it animates the session's
		// past across the keys -- focus strobing between agents, agents
		// appearing and vanishing -- so drop it. The snapshot taken at connect
		// is already truth, and another one follows once the replay stops, so
		// nothing is lost by ignoring the stream until then.
		events = nil
	}
	for _, event := range events {
		d.State.ApplyEvent(event)
	}
	if d.BacklogDone(now) {
		d.settleBacklog(now)
	}
}

// BacklogDone reports whether the replay has stopped arriving.
func (d *Daemon) BacklogDone(now float64) bool {
	if !d.backlogSettling {
		return false
	}
	return now-d.lastEventAt >= BacklogQuietSeconds || now >= d.backlogDeadline
}

// settleBacklog re-applies truth once the replayed history has stopped.
func (d *Daemon) settleBacklog(now float64) {
	d.backlogSettling = false
	if err := d.reconcile(now); err != nil {
		d.DropHerdr(now, err.Error())
	}
}

// -- key presses ---------------------------------------------------------------

func (d *Daemon) focus(paneID string) {
	if err := d.Herdr.Focus(paneID); err != nil {
		d.Log.Warn(fmt.Sprintf("focus %s failed: %v", paneID, err))
		return
	}
	if d.Config.ActivateTerminal {
		d.Raise()
	}
}

func (d *Daemon) focusedPane() (model.AgentPane, bool) {
	for _, p := range d.State.AgentPanes() {
		if p.Focused {
			return p, true
		}
	}
	return model.AgentPane{}, false
}

// openGit opens the most specific place the focused agent's repository has.
//
// Git and GitHub are asked here and nowhere else. Resolving either on the
// render path would put a subprocess -- and for GitHub a network round trip --
// between every status poll and the keys, to answer a question nothing is
// asking until a finger lands on the key.
//
// Even here they are asked off the loop. Half a second is what GitHub takes
// on a good day, and the loop is what animates every other key; a press must
// not freeze the board while it waits (ADR 0013). A second press while one is
// still looking is ignored rather than queued.
//
// A flash covers every way this can come to nothing -- no focused agent, no
// directory, not a repository, no `origin`, a remote that names no web host --
// because the key does the same thing in all of them.
func (d *Daemon) openGit(now float64) {
	if d.gitLookup != nil {
		return
	}
	focused, ok := d.focusedPane()
	if !ok || focused.Cwd == "" {
		d.flash(model.GitSlot, now)
		return
	}
	lookup := make(chan bool, 1)
	d.gitLookup = lookup
	cwd, goToGit, log := focused.Cwd, d.GoToGit, d.Log
	d.Spawn(func() {
		found := false
		// A goroutine that dies leaves the key stuck looking, and a panic
		// would take the daemon with it.
		defer func() {
			if r := recover(); r != nil {
				log.Warn(fmt.Sprintf("git key lookup failed: %v", r))
			}
			lookup <- found
		}()
		found = goToGit(cwd)
	})
	d.CollectGitLookup(now)
}

// CollectGitLookup lands a git key lookup that has finished. False is
// answered with a flash, which writes to the boards, so it is done here on the
// loop rather than from the lookup.
func (d *Daemon) CollectGitLookup(now float64) {
	if d.gitLookup == nil {
		return
	}
	select {
	case found := <-d.gitLookup:
		d.landGitLookup(found, now)
	default:
	}
}

func (d *Daemon) landGitLookup(found bool, now float64) {
	d.gitLookup = nil
	if !found {
		// This key, not the function key: flashing that would say
		// "nothing wants your attention", a different statement.
		d.flash(model.GitSlot, now)
	}
}

// typist is whatever types for this board: the board itself where it can.
func (d *Daemon) typist(dev board.Device) board.Typist {
	if t := dev.Typist(); t != nil {
		return t
	}
	return d.HostTypist
}

// canType: a board types for itself without asking anyone; only the host
// needs the permission.
func (d *Daemon) canType(t board.Typist) bool {
	return t != board.Typist(d.HostTypist) || d.HostTypist.CanType()
}

// askToType asks, once a run, to let the daemon type for a board that cannot.
//
// From here and nowhere else: macOS gives the permission to whichever process
// asks, so a command run in a terminal would grant the terminal, not the
// daemon. And only on a press: someone who has just tried to dictate has asked
// for it, which is the consent ADR 0004 wants.
func (d *Daemon) askToType() {
	if d.askedToType {
		return
	}
	d.askedToType = true
	d.Log.Warn("the cn001 cannot type: allow " + d.HostTypist.Who())
	d.HostTypist.Ask()
}

// HandleKey handles one key edge from one board. ErrRestart is the only error.
func (d *Daemon) HandleKey(b *Board, key board.Key, now float64) error {
	dev := b.Device
	if dev == nil {
		return nil
	}
	if (key.Slot == model.MicSlot || key.Slot == model.ReturnSlot) && key.Down {
		// Without the permission the OS drops what is posted without a word,
		// so a press that cannot type says so on the key that was pressed
		// rather than lighting a microphone nobody is listening to.
		if !d.canType(d.typist(dev)) {
			// However it was granted -- in the dialog the daemon raised, one
			// the OS raised, or in its settings -- only a new process can use
			// it.
			if d.HostTypist.CanTypeAfresh() {
				d.Log.Info("allowed to type now; restarting to use it")
				return ErrRestart
			}
			d.askToType()
			if err := dev.Flash(key.Slot); err != nil {
				d.DropBoard(b, now, err.Error())
			}
			return nil
		}
	}
	var err error
	switch {
	case key.Slot == model.MicSlot && key.Down:
		err = d.Mic.Down(d.typist(dev), now)
	case key.Slot == model.MicSlot:
		err = d.Mic.Up(d.typist(dev), now)
	case !key.Down:
		return nil // every other key acts as it goes down
	case key.Slot == model.ReturnSlot:
		err = d.Mic.PressReturn(d.typist(dev))
	default:
		d.HandlePress(key.Slot, now)
		return nil
	}
	if err != nil {
		// A Keybow told to type, unplugged under the telling.
		if d.typist(dev) == board.Typist(d.HostTypist) {
			d.quietly(err)
		} else {
			d.DropBoard(b, now, err.Error())
		}
	}
	return nil
}

// HandlePress handles a press of any key that acts on a press.
func (d *Daemon) HandlePress(slot int, now float64) {
	panes := d.panes()
	// The settled grid, which is the one the keys were drawn from. Reading
	// the live order here would mean a press landing on an agent that is not
	// yet under that key.
	grid := d.Order.Settled()
	switch slot {
	case model.GitSlot:
		d.openGit(now)
	case model.FnSlot:
		target := reducer.NextAttentionTarget(panes, grid, d.Settler)
		if target == "" {
			d.flash(board.FlashFunctionKey, now) // heard you; nothing wants you
			return
		}
		d.focus(target)
	default:
		if paneID := reducer.PaneForSlot(slot, panes, grid); paneID != "" {
			d.focus(paneID)
		}
	}
}

func (d *Daemon) panes() reducer.Panes {
	out := reducer.Panes{}
	for _, p := range d.State.AgentPanes() {
		out[p.PaneID] = p
	}
	return out
}

// -- rendering -----------------------------------------------------------------

// Render draws the frame every board is shown now.
func (d *Daemon) Render(now float64) model.Frame {
	d.quietly(d.Mic.Tick(now))
	agentPanes := d.State.AgentPanes()
	for _, p := range agentPanes {
		d.Settler.Observe(p.PaneID, p.State, now)
	}
	d.Settler.Retain(d.State.LivePaneIDs())
	d.Settler.Tick(now)
	// Which key each agent is and what colour it wears, settled on the same
	// clock as what each key shows: the order is Herdr's list order, so one
	// poll that omits an agent would otherwise renumber every key below it and
	// renumber them back half a second later.
	d.Order.Observe(agentPanes, now)
	d.Order.Tick(now)
	cwd := ""
	if focused, ok := d.focusedPane(); ok {
		cwd = focused.Cwd
	}
	// Read from `.git/config`, never `git` itself: this runs on every pass of
	// the loop, and a subprocess here would sit between every status poll and
	// the keys. The forge comes out of the same read, so it costs nothing more.
	page := d.PageOf(cwd)
	return reducer.Render(d.panes(), d.Order.Settled(), d.Settler, reducer.Input{
		Features: model.Features{
			Connected:  d.Stream != nil,
			GitPage:    page != "",
			GitLooking: d.gitLookup != nil,
			Mic:        d.Mic.State(),
		},
		Forge: remote.Forge(page),
	})
}

func (d *Daemon) showsUsage(dev board.Device) (StripBoard, bool) {
	strip, ok := dev.(StripBoard)
	return strip, ok && d.Config.Usage
}

// PollUsage keeps usage fresh, but only while a board has somewhere to show
// it.
func (d *Daemon) PollUsage(now float64) {
	wanted := false
	for _, b := range d.connected() {
		if _, ok := d.showsUsage(b.Device); ok {
			wanted = true
		}
	}
	d.Usage.Tick(now, wanted)
}

// Push shows the frame on every board that needs it.
func (d *Daemon) Push(frame model.Frame, now float64) {
	for _, b := range d.connected() {
		due := now-b.lastPush >= HeartbeatSeconds
		if err := d.pushOne(b, frame, now, due); err != nil {
			d.DropBoard(b, now, err.Error())
		}
	}
}

func (d *Daemon) pushOne(b *Board, frame model.Frame, now float64, due bool) error {
	if strip, ok := d.showsUsage(b.Device); ok {
		if err := strip.ShowUsage(d.Usage.Reading, d.Usage.Stale(now)); err != nil {
			return err
		}
	}
	if b.lastFrame == nil || *b.lastFrame != frame || due {
		if err := b.Device.SendFrame(frame); err != nil {
			return err
		}
		b.lastFrame = &frame
		b.lastPush = now
	}
	// Boards the host draws move only when redrawn: a blink is this, every
	// pass, not a new frame.
	return b.Device.Animate(now)
}

// Timeout is how long the loop may sleep before something is due.
func (d *Daemon) Timeout(now float64) float64 {
	soonest := IdleTimeout
	consider := func(v float64) { soonest = min(soonest, v) }
	for _, b := range d.Boards {
		if b.Device == nil {
			consider(b.Backoff.WaitFor(now))
			continue
		}
		consider(max(0, b.lastPush+HeartbeatSeconds-now))
		if change, ok := b.Device.NextChange(now); ok {
			consider(change)
		}
	}
	if v, ok := d.Settler.NextDeadline(now); ok {
		consider(v)
	}
	if v, ok := d.Order.NextDeadline(now); ok {
		consider(v)
	}
	if d.Stream == nil {
		consider(d.herdrBackoff.WaitFor(now))
	} else {
		consider(max(0, d.nextStatusPoll-now))
		consider(max(0, d.nextReconcile-now))
		if d.backlogSettling {
			consider(max(0, d.lastEventAt+BacklogQuietSeconds-now))
			consider(max(0, d.backlogDeadline-now))
		}
	}
	if v, ok := d.Mic.NextChange(now); ok {
		consider(v)
	}
	return max(0.01, soonest)
}

// Step is everything one pass of the loop does before it waits: connect what
// is due, ask Herdr what is due, and draw. Rendering comes before the wait,
// never after: rendering is what starts a state settling, so doing it on the
// far side of the wait would add a whole wait's delay to everything the poll
// learned. It is also what puts the settle deadline into Timeout.
func (d *Daemon) Step(now float64) {
	if d.Stream == nil && d.herdrBackoff.Ready(now) {
		d.ConnectHerdr(now)
	}
	for _, b := range d.Boards {
		if b.Device == nil && b.Backoff.Ready(now) {
			d.ConnectBoard(b, now)
		}
	}
	if d.Stream != nil && now >= d.nextStatusPoll {
		d.PollStatus(now)
	}
	if d.Stream != nil && now >= d.nextReconcile {
		if err := d.reconcile(now); err != nil {
			d.DropHerdr(now, err.Error())
		}
	}
	if d.Stream != nil && d.BacklogDone(now) {
		d.settleBacklog(now)
	}
	d.CollectGitLookup(now)
	d.PollUsage(now)
	d.Push(d.Render(now), now)
}
