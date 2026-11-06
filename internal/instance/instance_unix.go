//go:build !windows

package instance

import (
	"os"
	"os/signal"
	"syscall"
)

func lockFile(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) }
func unlockFile(f *os.File)     { syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }

// `kill`, launchd and Stop all send SIGTERM, which the daemon takes as the
// cue to stop cleanly -- the only chance there is to blank the CN001, whose
// panel keeps whatever it was last sent, indefinitely.
func requestStop(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

func stopRequested() <-chan struct{} {
	done := make(chan struct{})
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, os.Interrupt)
	go func() {
		<-signals
		close(done)
	}()
	return done
}
