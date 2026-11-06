package main

import (
	"log/slog"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// startDetached starts cmd with no console and in a process group of its
// own, so it outlives the hook that started it and no window appears. It
// breaks away from the hook's job object where it may, since a job can take
// its children with it when it ends.
func startDetached(cmd *exec.Cmd) error {
	flags := uint32(windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags | windows.CREATE_BREAKAWAY_FROM_JOB, HideWindow: true}
	if err := cmd.Start(); err == nil {
		return nil
	}
	// The job does not allow breaking away: start inside it.
	again := exec.Command(cmd.Path, cmd.Args[1:]...)
	again.Stdout, again.Stderr, again.Env = cmd.Stdout, cmd.Stderr, cmd.Env
	again.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags, HideWindow: true}
	*cmd = *again
	return cmd.Start()
}

// restart starts this daemon over as a new process. Windows needs no
// permission to type, so nothing asks for this there; it is here so the
// daemon's answer to ErrRestart is the same on every platform.
func restart(log *slog.Logger) int {
	self, err := os.Executable()
	if err != nil {
		log.Error(err.Error())
		return 1
	}
	cmd := exec.Command(self, os.Args[1:]...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := startDetached(cmd); err != nil {
		log.Error("cannot restart: " + err.Error())
		return 1
	}
	return 0
}

// canTypeCommand: Windows needs no permission to type.
func canTypeCommand() int { return 0 }
