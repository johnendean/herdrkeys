package main

import (
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/johnendean/herdrkeys/internal/board"
	"github.com/johnendean/herdrkeys/internal/config"
	"github.com/johnendean/herdrkeys/internal/daemon"
	"github.com/johnendean/herdrkeys/internal/herdr"
	"github.com/johnendean/herdrkeys/internal/host"
	"github.com/johnendean/herdrkeys/internal/keybow"
	"github.com/johnendean/herdrkeys/internal/state"
)

// herdrClient is Herdr's socket, as the daemon asks it things.
type herdrClient struct{ path string }

func (h herdrClient) Subscribe() (daemon.Stream, error) {
	return herdr.Subscribe(h.path, herdr.DefaultTimeout)
}
func (h herdrClient) Snapshot() (state.Snapshot, error) {
	return herdr.GetSnapshot(h.path, herdr.DefaultTimeout)
}
func (h herdrClient) Agents() ([]state.Pane, error) {
	return herdr.Agents(h.path, herdr.DefaultTimeout)
}
func (h herdrClient) Focus(paneID string) error {
	return herdr.FocusAgent(h.path, paneID, herdr.DefaultTimeout)
}
func (h herdrClient) Endpoint() string { return herdr.Endpoint(h.path) }

func socketPath(cfg config.Config) string {
	if cfg.SocketPath != "" {
		return cfg.SocketPath
	}
	return herdr.DefaultSocketPath()
}

// provisioner is package keybow, as the daemon provisions with it.
type provisioner struct{}

func (provisioner) ConsolePort() string { return keybow.ConsolePort(keybow.FindPorts()) }
func (provisioner) Inspect() (string, bool, bool) {
	d, ok := keybow.Inspect()
	return d.Path, d.MayWrite(), ok
}
func (provisioner) CopyFirmware(drive string) error { _, err := keybow.CopyFirmware(drive); return err }
func (provisioner) HardReset(console string) bool   { return keybow.HardReset(console) }

// newDaemon is the daemon with every real board and every real side effect.
func newDaemon(cfg config.Config, log *slog.Logger) *daemon.Daemon {
	self, _ := os.Executable()
	typist := &host.Typist{Self: self}
	d := daemon.New(cfg, herdrClient{socketPath(cfg)}, typist, log)
	raiser := &host.Raiser{Override: cfg.TerminalApp}
	d.Raise = raiser.Raise
	d.Provisioner = provisioner{}

	d.AddBoard("keybow", func(now float64) (board.Device, error) {
		port := cfg.SerialPort
		if port == "" {
			port = keybow.DataPort(keybow.FindPorts())
		}
		if port == "" {
			d.MaybeProvision(now)
			return nil, nil
		}
		dev, err := keybow.Open(port)
		var refused *keybow.Error
		if errors.As(err, &refused) {
			// Herdrkeys firmware on another protocol -- an update moved the
			// protocol on. The board is ours (ADR 0004), so it is brought up
			// to date rather than left refused until someone redeploys.
			d.MaybeProvision(now)
		}
		if err != nil {
			return nil, err
		}
		return dev, nil
	})
	addCN001(d, cfg)
	return d
}

// nowSeconds is a monotonic clock in seconds, for commands that drive a
// daemon step by step.
var started = time.Now()

func nowSeconds() float64 { return time.Since(started).Seconds() }
