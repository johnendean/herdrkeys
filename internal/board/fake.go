package board

import (
	"sync"

	"github.com/johnendean/herdrkeys/internal/chord"
	"github.com/johnendean/herdrkeys/internal/model"
)

// Fake records what the daemon would have shown, instead of lighting LEDs.
// Lets the daemon be tested with no hardware.
type Fake struct {
	mu      sync.Mutex
	Frames  []model.Frame
	Flashes []int
	Closed  bool
	// TypesItself makes the fake a board that presses keys of its own, like
	// a Keybow. By default it is one that cannot, like a CN001.
	TypesItself bool
	Holding     bool
	Held        chord.Chord
	Returns     int
	// Fail makes every write fail, as an unplugged board's would.
	Fail error

	keys chan Key
	err  error
}

// NewFake is a fake board with nothing pressed.
func NewFake() *Fake { return &Fake{keys: make(chan Key, 64)} }

// Press queues a key edge, as a finger on the board would.
func (f *Fake) Press(slot int, down bool) { f.keys <- Key{slot, down} }

// Unplug ends the key stream, as pulling the cable would.
func (f *Fake) Unplug(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
	close(f.keys)
}

// LastFrame is the frame the board shows now.
func (f *Fake) LastFrame() (model.Frame, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.Frames) == 0 {
		return model.Frame{}, false
	}
	return f.Frames[len(f.Frames)-1], true
}

func (f *Fake) Firmware() string { return "fake" }

func (f *Fake) Typist() Typist {
	if f.TypesItself {
		return f
	}
	return nil
}

func (f *Fake) SendFrame(frame model.Frame) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Fail != nil {
		return f.Fail
	}
	f.Frames = append(f.Frames, frame)
	return nil
}

func (f *Fake) Flash(slot int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Fail != nil {
		return f.Fail
	}
	f.Flashes = append(f.Flashes, slot)
	return nil
}

func (f *Fake) Keys() <-chan Key { return f.keys }

func (f *Fake) Err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

func (f *Fake) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Closed = true
	return nil
}

func (f *Fake) Hold(on bool, c chord.Chord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Fail != nil {
		return f.Fail
	}
	f.Holding = on
	if on {
		f.Held = c
	}
	return nil
}

func (f *Fake) TypeReturn() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Fail != nil {
		return f.Fail
	}
	f.Returns++
	return nil
}

func (f *Fake) Animate(now float64) error              { return nil }
func (f *Fake) NextChange(now float64) (float64, bool) { return 0, false }
