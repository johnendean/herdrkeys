package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnendean/herdrkeys/internal/config"
	"github.com/johnendean/herdrkeys/internal/daemon"
	"github.com/johnendean/herdrkeys/internal/instance"
)

const logName = "herdrkeys.log"

func me() instance.Info {
	self, _ := os.Executable()
	return instance.Info{PID: os.Getpid(), Version: Version, Path: self, Started: time.Now()}
}

func newLogger(w io.Writer, verbose bool) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}

// takeOver makes this build the one daemon: nothing to do if no daemon runs,
// nothing at all if this same build does, and otherwise the running one is
// asked to stop. False when this build should not run.
func takeOver(dir string, out io.Writer) (bool, error) {
	running, held := instance.Running(dir)
	if !held {
		return true, nil
	}
	if running.Same(me()) {
		fmt.Fprintf(out, "herdrkeys: already running, pid %d\n", running.PID)
		return false, nil
	}
	fmt.Fprintf(out, "herdrkeys: taking over from %s (pid %d, %s)\n", running.Version, running.PID, running.Path)
	stopped, err := instance.Stop(dir, 10*time.Second)
	if err != nil {
		return false, err
	}
	if !stopped {
		return false, fmt.Errorf("pid %d did not stop", running.PID)
	}
	return true, nil
}

// run is the daemon in the foreground. It never exits voluntarily: Herdr and
// each board reconnect on their own, and only a stop request ends it.
func run(verbose bool, stderr io.Writer) int {
	env := config.System()
	cfg := config.Load(env)
	log := newLogger(stderr, verbose)
	for _, problem := range cfg.Problems {
		log.Warn("config: " + problem)
	}

	dir := env.StateDir()
	ok, err := takeOver(dir, stderr)
	if err != nil {
		log.Error("cannot take over: " + err.Error())
		return 1
	}
	if !ok {
		return 0
	}
	lock, err := instance.Acquire(dir, me())
	if err != nil {
		log.Error("cannot hold the instance lock: " + err.Error())
		return 1
	}

	ctx, cancel := context.WithCancel(context.Background())
	stopRequested := instance.StopRequested()
	go func() {
		<-stopRequested
		log.Info("asked to stop")
		cancel()
	}()

	d := newDaemon(cfg, log)
	err = d.Run(ctx)
	lock.Release()
	if errors.Is(err, daemon.ErrRestart) {
		// Started over in place rather than exited, so whatever started this
		// sees nothing happen (ADR 0017).
		return restart(log)
	}
	if err != nil {
		log.Error(err.Error())
		return 1
	}
	return 0
}

// start starts the daemon detached, unless this build is already running.
//
// Herdr's startup hooks are one-shot and unsupervised, so this is what they
// call: it must outlive the hook that ran it, and must be idempotent, since
// Herdr runs it both at install and at every server start.
func start(stdout, stderr io.Writer) int {
	env := config.System()
	dir := env.StateDir()
	ok, err := takeOver(dir, stdout)
	if err != nil {
		fmt.Fprintln(stderr, "herdrkeys:", err)
		return 1
	}
	if !ok {
		return 0
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(stderr, "herdrkeys:", err)
		return 1
	}
	// Always the state directory, never the plugin's: the log must be in one
	// place whether Herdr started the daemon or a human did.
	logPath := filepath.Join(dir, logName)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintln(stderr, "herdrkeys:", err)
		return 1
	}
	defer logFile.Close()
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(stderr, "herdrkeys:", err)
		return 1
	}
	cmd := exec.Command(self, "run")
	cmd.Stdout, cmd.Stderr = logFile, logFile
	// Do not inherit the socket of whichever server ran this hook. There is
	// one set of boards, so there is one daemon, and it must not end up bound
	// to a named or throwaway session just because that server happened to
	// start first. The daemon resolves the default session instead; to follow
	// a named one, set socket_path in config.
	cmd.Env = withoutSession(os.Environ())
	if err := startDetached(cmd); err != nil {
		fmt.Fprintln(stderr, "herdrkeys: cannot start the daemon:", err)
		return 1
	}
	cmd.Process.Release()
	fmt.Fprintf(stdout, "herdrkeys: started, logging to %s\n", logPath)
	return 0
}

func withoutSession(env []string) []string {
	var out []string
	for _, kv := range env {
		if hasPrefixFold(kv, "HERDR_SOCKET_PATH=") || hasPrefixFold(kv, "HERDR_SESSION=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// stop asks the running daemon to stop cleanly, which blanks the CN001 and
// lets go of the dictation chord. Herdr has no uninstall hook, so this is the
// first of uninstall's two steps.
func stop(stdout io.Writer) int {
	dir := config.System().StateDir()
	running, held := instance.Running(dir)
	if !held {
		fmt.Fprintln(stdout, "herdrkeys: not running")
		return 0
	}
	stopped, err := instance.Stop(dir, 10*time.Second)
	if err != nil {
		fmt.Fprintln(stdout, "herdrkeys: cannot stop:", err)
		return 1
	}
	if !stopped {
		fmt.Fprintf(stdout, "herdrkeys: pid %d did not stop\n", running.PID)
		return 1
	}
	fmt.Fprintf(stdout, "herdrkeys: stopped pid %d\n", running.PID)
	return 0
}
