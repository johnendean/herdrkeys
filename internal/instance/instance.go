// Package instance keeps herdrkeys to one daemon, and lets a newer build take
// over from an older one (ADR 0020).
//
// There is one set of boards, so there is one daemon. The running daemon
// holds an OS lock on a file in its state directory for as long as it runs --
// released by the OS however it ends -- and records beside it which build it
// is. Every `herdr plugin update` installs into a fresh directory, so a start
// from a different build asks the running one to stop cleanly, then runs
// itself.
package instance

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Info is which daemon holds the lock.
type Info struct {
	PID     int       `json:"pid"`
	Version string    `json:"version"`
	Path    string    `json:"path"` // the binary it runs from
	Started time.Time `json:"started"`
}

// Same reports whether two daemons are the same build.
func (i Info) Same(other Info) bool {
	return i.Version == other.Version && samePath(i.Path, other.Path)
}

func samePath(a, b string) bool {
	if a == b {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// ErrHeld is the lock being held by another running daemon.
var ErrHeld = errors.New("another herdrkeys daemon is running")

// Lock is the lock, held.
type Lock struct {
	file *os.File
	dir  string
}

func lockPath(dir string) string { return filepath.Join(dir, "daemon.lock") }
func infoPath(dir string) string { return filepath.Join(dir, "daemon.json") }

// Acquire takes the lock in dir and records me as holding it, or returns
// ErrHeld.
func Acquire(dir string, me Info) (*Lock, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lockPath(dir), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, ErrHeld
	}
	data, _ := json.MarshalIndent(me, "", "  ")
	if err := os.WriteFile(infoPath(dir), data, 0o644); err != nil {
		f.Close()
		return nil, err
	}
	return &Lock{file: f, dir: dir}, nil
}

// Release lets the lock go.
func (l *Lock) Release() {
	os.Remove(infoPath(l.dir))
	unlockFile(l.file)
	l.file.Close()
}

// Running is the daemon holding the lock in dir, and false when none does.
func Running(dir string) (Info, bool) {
	f, err := os.OpenFile(lockPath(dir), os.O_RDWR, 0o644)
	if err != nil {
		return Info{}, false
	}
	defer f.Close()
	if lockFile(f) == nil {
		unlockFile(f)
		return Info{}, false // nobody holds it
	}
	var info Info
	data, err := os.ReadFile(infoPath(dir))
	if err == nil {
		json.Unmarshal(data, &info)
	}
	return info, true
}

// Stop asks the daemon holding the lock in dir to stop cleanly, and waits up
// to timeout for it to let go. False if it is still running then.
func Stop(dir string, timeout time.Duration) (bool, error) {
	info, running := Running(dir)
	if !running {
		return true, nil
	}
	if info.PID == 0 {
		return false, fmt.Errorf("the running daemon did not say who it is (%s)", infoPath(dir))
	}
	if err := requestStop(info.PID); err != nil {
		return false, err
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, still := Running(dir); !still {
			return true, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false, nil
}

// StopRequested is closed when this process is asked to stop -- by Stop from
// another process, or by an interrupt.
func StopRequested() <-chan struct{} { return stopRequested() }
