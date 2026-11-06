//go:build !darwin && !windows

package host

import (
	"errors"

	"github.com/johnendean/herdrkeys/internal/chord"
)

var errUnsupported = errors.New("typing is not supported on this platform")

func press(chord.Chord, bool) error { return errUnsupported }
func typeReturn() error             { return errUnsupported }

func (t *Typist) CanType() bool       { return false }
func (t *Typist) CanTypeAfresh() bool { return false }
func (t *Typist) Ask()                {}
func (t *Typist) Who() string         { return "nothing: this platform is not supported" }

// Raiser does nothing here.
type Raiser struct{ Override string }

func (r *Raiser) Raise()           {}
func (r *Raiser) Describe() string { return "" }
