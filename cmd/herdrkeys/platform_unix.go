//go:build !windows

package main

import (
	"log/slog"
	"os"
	"os/exec"
	"syscall"

	"github.com/johnendean/herdrkeys/internal/host"
)

// startDetached starts cmd in a session of its own, so it outlives the hook
// that started it.
func startDetached(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}

// restart re-executes this binary in place. The process ID stays, so
// whatever started the daemon sees nothing happen, and the new image is one
// macOS tells about the permission granted since (ADR 0017).
func restart(log *slog.Logger) int {
	self, err := os.Executable()
	if err != nil {
		log.Error(err.Error())
		return 1
	}
	err = syscall.Exec(self, os.Args, os.Environ())
	log.Error("cannot restart: " + err.Error())
	return 1
}

// canTypeCommand reports whether this fresh process may post events.
func canTypeCommand() int {
	if (&host.Typist{}).CanType() {
		return 0
	}
	return 1
}
