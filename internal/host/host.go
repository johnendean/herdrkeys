// Package host is what the daemon does to the machine itself: type for a
// board that cannot (ADR 0017), and bring the terminal hosting Herdr to the
// front, because focusing an agent you cannot see is not focusing it.
//
// Each platform does both its own way; this file is what they share.
package host

import (
	"sync"

	"github.com/johnendean/herdrkeys/internal/chord"
)

// Typist presses keys from the daemon itself. It remembers what it is
// holding, so that letting go releases exactly that, whatever the chord is by
// then.
type Typist struct {
	mu   sync.Mutex
	held chord.Chord
	// asker, on macOS, is how a fresh process is asked whether it could
	// type: the path of this binary.
	Self string
}

// Hold holds the chord down, or lets go of what is held.
func (t *Typist) Hold(on bool, c chord.Chord) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if on {
		if len(t.held) > 0 {
			return nil
		}
		if err := press(c, true); err != nil {
			return err
		}
		t.held = c
		return nil
	}
	if len(t.held) == 0 {
		// Let go of the chord anyway: a daemon that crashed holding it is
		// exactly what a release at start-up is for, and releasing a key that
		// is up does nothing.
		return press(c, false)
	}
	held := t.held
	t.held = nil
	return press(held, false)
}

// TypeReturn types Return.
func (t *Typist) TypeReturn() error { return typeReturn() }
