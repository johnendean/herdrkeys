package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"runtime"

	"github.com/johnendean/herdrkeys/internal/board"
	"github.com/johnendean/herdrkeys/internal/chord"
	"github.com/johnendean/herdrkeys/internal/config"
	"github.com/johnendean/herdrkeys/internal/daemon"
	"github.com/johnendean/herdrkeys/internal/herdr"
	"github.com/johnendean/herdrkeys/internal/host"
	"github.com/johnendean/herdrkeys/internal/instance"
	"github.com/johnendean/herdrkeys/internal/keybow"
	"github.com/johnendean/herdrkeys/internal/state"
	"github.com/johnendean/herdrkeys/internal/usage"
)

// line prints one doctor line: a name, then what was found.
func line(w io.Writer, name, format string, args ...any) {
	fmt.Fprintf(w, "%-16s %s\n", name, fmt.Sprintf(format, args...))
}

// doctor reports on everything the daemon depends on, without changing
// anything. Exit 1 when something it needs is missing.
func doctor(w io.Writer) int {
	ok := true
	env := config.System()
	cfg := config.Load(env)
	running, daemonUp := instance.Running(env.StateDir())

	absent := ""
	if !cfg.Location.Exists {
		absent = "  (absent, using defaults)"
	}
	line(w, "config file", "%s%s", cfg.Location.Path, absent)
	for _, ignored := range cfg.Location.Shadowed {
		ok = false
		line(w, "", "IGNORING %s -- two config files, only the first is read", ignored)
	}
	for _, problem := range cfg.Problems {
		ok = false
		line(w, "", "PROBLEM  %s", problem)
	}

	path := socketPath(cfg)
	snapshot, err := herdr.GetSnapshot(path, herdr.DefaultTimeout)
	var agents []state.Pane
	if err != nil {
		ok = false
		line(w, "herdr", "%s  UNREACHABLE  (%v)", herdr.Endpoint(path), err)
	} else {
		for _, p := range snapshot.Panes {
			if p.Agent != nil {
				agents = append(agents, p)
			}
		}
		line(w, "herdr", "%s  ok  (%d panes, %d agent panes)", herdr.Endpoint(path), len(snapshot.Panes), len(agents))
		// A key showing its state colour and a key whose agent has no
		// project colour look identical on the board, by design -- so this
		// is the only place the difference can be seen. Not a failure either
		// way: no colours is what herdrkeys did before herdrcolor existed.
		line(w, "colours", "%s", colourReport(agents))
	}

	ports := keybow.FindPorts()
	cn001Present, cn001OK, cn001Line := cn001Report(daemonUp, running.PID)
	switch {
	case len(ports) == 0:
		// One board is enough: a missing Keybow is a failure only when there
		// is no other board to show anything on, decided below.
		line(w, "keybow", "NOT FOUND  (no %04x:%04x serial ports; is it plugged in?)", keybow.VendorID, keybow.ProductID)
	case len(ports) == 1:
		ok = false
		line(w, "keybow", "%s  console only, no data channel", ports[0].Path)
		if d, found := keybow.Inspect(); !found {
			line(w, "", "CIRCUITPY not mounted; cannot provision it")
		} else if d.MayWrite() {
			line(w, "", "%s is %s; the daemon will provision it", d.Path, d.State)
		} else {
			line(w, "", "%s carries other firmware -- run 'herdrkeys adopt' to save it and take over", d.Path)
		}
	default:
		port := cfg.SerialPort
		if port == "" {
			port = keybow.DataPort(ports)
		}
		where := fmt.Sprintf("%s  (console %s)", port, keybow.ConsolePort(ports))
		if daemonUp {
			// Probing now would take the port from under the daemon, or get
			// no reply because the daemon read it. Neither proves anything.
			line(w, "keybow", "%s  in use by the running daemon (pid %d)", where, running.PID)
		} else if dev, err := keybow.Open(port); err != nil {
			ok = false
			line(w, "keybow", "%s  NO HANDSHAKE  (%v)", where, err)
		} else {
			line(w, "keybow", "%s  ok  (firmware %s, protocol %d)", where, dev.Firmware(), keybow.Protocol)
			dev.Close()
		}
	}

	line(w, "cn001", "%s", cn001Line)
	if cn001Present {
		line(w, "usage", "%s", usageReport(cfg))
		ok = ok && cn001OK
	} else if len(ports) == 0 {
		ok = false
	}

	line(w, "dictation chord", "%s%s", cfg.DictationChord, flowReport(cfg.DictationChord))
	typist := &host.Typist{}
	if !typist.CanType() {
		line(w, "", "the cn001 cannot type yet: allow %s", typist.Who())
	}

	if daemonUp {
		line(w, "daemon", "running, pid %d  (%s, %s)", running.PID, running.Version, running.Path)
	} else {
		line(w, "daemon", "not running  (herdrkeys start)")
	}
	line(w, "log", "%s", filepath.Join(env.StateDir(), logName))

	if !cfg.ActivateTerminal {
		line(w, "terminal", "disabled in config")
	} else if what := (&host.Raiser{Override: cfg.TerminalApp}).Describe(); what != "" {
		line(w, "terminal", "%s", what)
	} else if runtime.GOOS == "windows" {
		line(w, "terminal", "UNDETECTED  (no Herdr client running in a terminal)")
	} else {
		line(w, "terminal", "UNDETECTED  (set terminal_app in config.toml)")
	}
	if ok {
		return 0
	}
	return 1
}

func colourReport(agents []state.Pane) string {
	if len(agents) == 0 {
		return "no agents to colour"
	}
	s := state.New()
	s.ApplyAgents(agents)
	coloured := 0
	for _, p := range s.AgentPanes() {
		if p.Colour != "" {
			coloured++
		}
	}
	switch coloured {
	case 0:
		return fmt.Sprintf("none of %d agents; keys show agent state alone (install herdrcolor, or run its sync)", len(agents))
	case len(agents):
		return fmt.Sprintf("all %d agents; their keys wear their project colour", len(agents))
	}
	return fmt.Sprintf("%d of %d agents; the rest fall back to agent state (herdrcolor may not have synced since they appeared)", coloured, len(agents))
}

// usageReport is what the CN001's strip would show (ADR 0019). Never a
// failure: the boards work without it.
func usageReport(cfg config.Config) string {
	if !cfg.Usage {
		return "off in config"
	}
	now, err := usage.Current()
	if err != nil {
		return fmt.Sprintf("UNKNOWN  (%v; credential: %s)", err, usage.CredentialSource())
	}
	describe := func(name string, l *usage.Limit) string {
		if l == nil {
			return name + " none"
		}
		return fmt.Sprintf("%s %g%%", name, l.Percent)
	}
	return fmt.Sprintf("ok  (%s, %s; credential: %s)", describe("session", now.Session), describe("weekly", now.Weekly), usage.CredentialSource())
}

// flowReport checks the chord against the push-to-talk binding Wispr Flow
// has recorded, where it can be read (ADR 0017).
func flowReport(c chord.Chord) string {
	binding, found := flowBinding()
	if !found {
		return ""
	}
	if binding.matches(c) {
		return "  (matches Wispr Flow's push-to-talk)"
	}
	return "  MISMATCH: Wispr Flow's push-to-talk is " + binding.describe() + " -- set dictation_chord, or rebind Flow"
}

// adopt takes over a Keybow carrying somebody else's firmware, saving it
// first (ADR 0004).
func adopt(w io.Writer) int {
	cfg := config.Load(config.System())
	d, found := keybow.Inspect()
	if !found {
		fmt.Fprintln(w, "no Keybow 2040 found -- plug it in and check its CIRCUITPY drive is mounted")
		return 1
	}
	line(w, "board", "%s  (%s)", d.Path, d.State)
	if d.State == keybow.Ours {
		fmt.Fprintln(w, "already running herdrkeys; nothing to salvage")
	} else {
		saved, err := keybow.Salvage(d.Path, cfg.SalvageDir)
		if err != nil {
			fmt.Fprintf(w, "could not save what is on the board, so it is left alone: %v\n", err)
			return 1
		}
		if len(saved) == 0 {
			fmt.Fprintln(w, "nothing to salvage; the board is empty")
		} else {
			line(w, "saved", "%d file(s) to %s", len(saved), cfg.SalvageDir)
			for _, p := range saved {
				line(w, "", "%s", filepath.Base(p))
			}
		}
	}
	return writeAndReset(w, d.Path)
}

// deploy writes the firmware to a board already running herdrkeys, or
// carrying nothing. What `make deploy` was, for a checkout on any platform.
func deploy(w io.Writer) int {
	d, found := keybow.Inspect()
	if !found {
		fmt.Fprintln(w, "no Keybow 2040 found -- plug it in and check its CIRCUITPY drive is mounted")
		return 1
	}
	if !d.MayWrite() {
		fmt.Fprintf(w, "%s carries other firmware -- run 'herdrkeys adopt' to save it and take over\n", d.Path)
		return 1
	}
	return writeAndReset(w, d.Path)
}

func writeAndReset(w io.Writer, drive string) int {
	if _, err := keybow.CopyFirmware(drive); err != nil {
		fmt.Fprintf(w, "could not write the firmware: %v\n", err)
		return 1
	}
	line(w, "wrote", "boot.py, code.py  (protocol %d)", keybow.Protocol)
	console := keybow.ConsolePort(keybow.FindPorts())
	if console != "" && keybow.HardReset(console) {
		line(w, "reset", "board re-enumerating; it will reconnect in a few seconds")
		return 0
	}
	line(w, "reset", "FAILED -- unplug and replug the board to finish")
	return 1
}

// runTUI runs the whole daemon against a keypad drawn in the terminal.
func runTUI(verbose bool, stderr io.Writer) int {
	dev, err := openTUI()
	if err != nil {
		fmt.Fprintln(stderr, "herdrkeys: the tui needs a terminal:", err)
		return 1
	}
	cfg := config.Load(config.System())
	d := daemon.New(cfg, herdrClient{socketPath(cfg)}, &host.Typist{}, newLogger(io.Discard, verbose))
	d.AddBoard("tui", func(float64) (board.Device, error) { return dev, nil })
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-instance.StopRequested(); cancel() }()
	d.Run(ctx)
	dev.Close()
	return 0
}
