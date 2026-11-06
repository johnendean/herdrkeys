// Package board is what the daemon needs from a keypad, implemented by the
// Keybow, the CN001 and a fake alike.
//
// Every board is shown the same frame and every board's press means the same
// thing (CONTEXT.md, "Board").
package board

import (
	"errors"

	"github.com/johnendean/herdrkeys/internal/chord"
	"github.com/johnendean/herdrkeys/internal/model"
)

// FlashFunctionKey asks a board to flash the function key, which is what a
// flash meant before a flash could name its key.
const FlashFunctionKey = -1

// Key is one edge of one key: down when pressed, up when let go.
type Key struct {
	Slot int
	Down bool
}

// Typist is whatever types for a board: the board itself where it can, the
// host where it cannot (ADR 0017).
type Typist interface {
	// Hold holds the dictation chord down, or lets go of it.
	Hold(on bool, c chord.Chord) error
	TypeReturn() error
}

// Device is a connected keypad.
type Device interface {
	// Firmware names what is running on the board, for the log.
	Firmware() string
	// Typist is the board itself if it presses keys of its own, else nil:
	// a board that cannot type has the host type for it.
	Typist() Typist
	SendFrame(model.Frame) error
	// Flash flashes one key, or the function key for FlashFunctionKey.
	Flash(slot int) error
	// Keys delivers key edges as they happen. It is closed when the board
	// goes away, and Err then says why.
	Keys() <-chan Key
	Err() error
	Close() error
	// Animate redraws a board the host draws (ADR 0014); one that animates
	// itself does nothing. NextChange says how soon it next must be called.
	Animate(now float64) error
	NextChange(now float64) (float64, bool)
}

// ErrGone is how a board that has been unplugged or closed reports itself.
var ErrGone = errors.New("board gone")
