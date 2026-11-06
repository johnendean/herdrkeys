// Package microphone is the microphone: one, however many boards are
// attached (ADR 0017).
//
// Pure: no I/O and no clock of its own. What it decides reaches the machine
// through a typist -- the board itself where it can type, the daemon where it
// cannot -- and whichever board opened the microphone is the one that holds
// it.
package microphone

import (
	"github.com/johnendean/herdrkeys/internal/board"
	"github.com/johnendean/herdrkeys/internal/chord"
	"github.com/johnendean/herdrkeys/internal/model"
)

// LatchTapSeconds: a press shorter than this latches the microphone open
// instead of closing it with your finger; anything longer is an ordinary
// hold. Long enough not to fire on a deliberate short phrase, short enough
// that a tap never feels like a wait.
const LatchTapSeconds = 0.4

// LatchTimeout: a latch does not last forever. Nothing here can tell whether
// Wispr Flow is still listening, and a chord held down for an afternoon turns
// every keystroke into a shortcut.
const LatchTimeout = 300.0

// Microphone is the one microphone.
type Microphone struct {
	Chord     chord.Chord
	holder    board.Typist
	pressedAt float64
	latched   bool
	latchedAt float64
}

// New is a closed microphone that holds the given chord.
func New(c chord.Chord) *Microphone { return &Microphone{Chord: c} }

// State is what the microphone key shows.
func (m *Microphone) State() model.Microphone {
	switch {
	case m.holder == nil:
		return model.MicClosed
	case m.latched:
		return model.MicLatched
	}
	return model.MicOpen
}

// Down is the microphone key going down: open it, or end a latch.
func (m *Microphone) Down(t board.Typist, now float64) error {
	if m.latched {
		return m.close()
	}
	if m.holder != nil {
		return nil // held by another board's finger; a second holder is a second thing to forget
	}
	m.holder, m.pressedAt = t, now
	return t.Hold(true, m.Chord)
}

// Up is the key coming up: a tap latches what a hold would have ended.
func (m *Microphone) Up(t board.Typist, now float64) error {
	if m.holder != t || m.latched {
		return nil
	}
	if now-m.pressedAt < LatchTapSeconds {
		m.latched, m.latchedAt = true, now
		return nil
	}
	return m.close()
}

// PressReturn closes an open microphone, or types Return. Never both.
//
// While the microphone is open Flow has not yet inserted what it heard, so a
// Return now would send the prompt without it -- and with the chord still down
// it would not even be a Return. Closing it is the useful half; the next
// press, once the text is there, sends it.
func (m *Microphone) PressReturn(t board.Typist) error {
	if m.holder != nil {
		return m.close()
	}
	return t.TypeReturn()
}

// Lost is a board going away. If it was holding the microphone, it closes.
func (m *Microphone) Lost(t board.Typist) error {
	if m.holder != nil && m.holder == t {
		return m.close()
	}
	return nil
}

// Tick ends a latch that has run out.
func (m *Microphone) Tick(now float64) error {
	if m.latched && now-m.latchedAt > LatchTimeout {
		return m.close()
	}
	return nil
}

// NextChange is how long until a latch times out, so the loop wakes for it.
func (m *Microphone) NextChange(now float64) (float64, bool) {
	if !m.latched {
		return 0, false
	}
	return max(0, m.latchedAt+LatchTimeout-now), true
}

// Close lets go, whoever is holding it. Safe to call when nothing is.
func (m *Microphone) Close() error { return m.close() }

func (m *Microphone) close() error {
	if m.holder == nil {
		return nil
	}
	holder := m.holder
	m.holder, m.latched = nil, false
	return holder.Hold(false, m.Chord)
}
