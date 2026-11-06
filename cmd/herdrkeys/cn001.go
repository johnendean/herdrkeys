package main

import (
	"github.com/johnendean/herdrkeys/internal/config"
	"github.com/johnendean/herdrkeys/internal/daemon"
)

// addCN001 adds the Soomfon CN001 to the boards the daemon looks for.
// Ported next; until then the daemon drives the Keybow alone.
func addCN001(d *daemon.Daemon, cfg config.Config) {}

// cn001Report is (attached, healthy, what to print) for the CN001.
func cn001Report(daemonUp bool, pid int) (bool, bool, string) {
	return false, true, "not yet supported by this build"
}
