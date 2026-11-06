package main

import (
	"fmt"
	"strings"

	"github.com/johnendean/herdrkeys/internal/board"
	"github.com/johnendean/herdrkeys/internal/cn001"
	"github.com/johnendean/herdrkeys/internal/config"
	"github.com/johnendean/herdrkeys/internal/daemon"
	"github.com/johnendean/herdrkeys/internal/hid"
)

// addCN001 adds the Soomfon CN001 to the boards the daemon looks for.
func addCN001(d *daemon.Daemon, cfg config.Config) {
	d.AddBoard("cn001", func(float64) (board.Device, error) {
		dev, err := cn001.Open(cfg.CN001Brightness, d.HostTypist.CanType)
		if err != nil || dev == nil {
			return nil, err
		}
		return dev, nil
	})
}

// cn001Report is (attached, healthy, what to print) for the CN001. Absent is
// not a failure on its own; doctor decides that once it knows whether there
// is a Keybow instead.
func cn001Report(daemonUp bool, pid int) (bool, bool, string) {
	found, err := hid.Find(cn001.VendorID, cn001.ProductID, cn001.UsagePage)
	if err != nil {
		return false, false, fmt.Sprintf("UNAVAILABLE  (%v)", err)
	}
	if len(found) == 0 {
		return false, true, fmt.Sprintf("not attached  (%04x:%04x)", cn001.VendorID, cn001.ProductID)
	}
	where := "attached"
	if found[0].Serial != "" {
		where = "serial " + found[0].Serial
	}
	// Probed even while the daemon runs: a running daemon does not mean it
	// got the board -- another app may hold it, and the daemon only retries.
	// The open never seizes the device, so it succeeds alongside the
	// daemon's.
	dev, err := hid.Open(found[0])
	if err != nil {
		hint := ""
		if strings.Contains(err.Error(), "another app") || strings.Contains(err.Error(), "Exclusive") {
			hint = "  -- quit the Soomfon Stream Controller app"
		}
		return true, false, fmt.Sprintf("%s  WILL NOT OPEN  (%v)%s", where, err, hint)
	}
	dev.Close()
	if daemonUp {
		return true, true, fmt.Sprintf("%s  ok, opens alongside the running daemon (pid %d)", where, pid)
	}
	return true, true, where + "  ok"
}
