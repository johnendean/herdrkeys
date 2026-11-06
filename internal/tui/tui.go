// Package tui is a keypad made of text.
//
// The same board interface as the real ones, so the whole daemon can be
// driven with no hardware attached: frames are drawn as a 4x4 grid and typing
// a hex digit (0-9, a-f) counts as a key press.
//
// The grid mirrors the physical board: PMK numbers keys row-major with key 0
// bottom-left, so slot 15 is top-right.
package tui

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/johnendean/herdrkeys/internal/board"
	"github.com/johnendean/herdrkeys/internal/model"
)

const reset = "\x1b[0m"

type swatch struct{ colour, label string }

// Terminal stand-ins for the palette the firmware owns.
var swatches = map[byte]swatch{
	'i': {"\x1b[38;5;22m", "idle"}, 'I': {"\x1b[38;5;46m", "idle*"},
	'w': {"\x1b[38;5;130m", "working"}, 'W': {"\x1b[38;5;214m", "working*"},
	'b': {"\x1b[38;5;88m", "blocked"}, 'B': {"\x1b[38;5;196m", "blocked*"},
	'd': {"\x1b[38;5;28m", "done"}, 'D': {"\x1b[38;5;82m", "done*"},
	'u': {"\x1b[38;5;17m", "unknown"}, 'U': {"\x1b[38;5;33m", "unknown*"},
	model.CodeEmpty:          {"\x1b[38;5;236m", ""},
	model.CodeFnConnected:    {"\x1b[38;5;245m", "fn"},
	model.CodeFnDisconnected: {"\x1b[38;5;124m", "offline"},
	model.CodeMic:            {"\x1b[38;5;44m", "mic"},
	model.CodeMicOpen:        {"\x1b[38;5;51m", "mic open"},
	model.CodeMicLatched:     {"\x1b[38;5;51m", "latched"},
	model.CodeReturn:         {"\x1b[38;5;30m", "return"},
	model.CodeGitPage:        {"\x1b[38;5;250m", "git"},
	model.CodeGitNoPage:      {"\x1b[38;5;240m", "no git"},
	model.CodeGitLooking:     {"\x1b[38;5;255m", "git..."},
}

// identity is a `#rrggbb` project colour as 24-bit ANSI, or "".
//
// Shown raw, without the saturation the firmware applies: a terminal is not
// an LED, and pretending otherwise would make this a worse preview, not a
// better one. What the TUI can show is which key wears which colour; only the
// board can settle whether two of them are far enough apart.
func identity(colour string) string {
	if len(colour) != 7 || colour[0] != '#' {
		return ""
	}
	v, err := strconv.ParseUint(colour[1:], 16, 32)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", v>>16&0xff, v>>8&0xff, v&0xff)
}

// Draw is the frame as a 4x4 grid, key 0 bottom-left, numbering along each
// row.
func Draw(frame model.Frame) string {
	var lines []string
	for fromTop := 0; fromTop < 4; fromTop++ {
		row := 3 - fromTop
		var cells []string
		for column := 0; column < 4; column++ {
			slot := row*4 + column
			code := frame.Keys[slot]
			s, ok := swatches[code]
			if !ok {
				s = swatch{"", string(code)}
			}
			// `idle` and `done` wear a project colour, exactly as on the
			// board. What the preview cannot show is the blink that tells
			// those two apart, so the state letter does that work here.
			if lower := code | 0x20; lower == 'i' || lower == 'd' {
				if c := identity(frame.Colours[slot]); c != "" {
					s.colour = c
				}
			}
			cells = append(cells, fmt.Sprintf("%s[%2d %c %-8s]%s", s.colour, slot, code, s.label, reset))
		}
		lines = append(lines, strings.Join(cells, " "))
	}
	return strings.Join(lines, "\n")
}

// Device draws frames to the terminal; hex digits typed are key presses.
type Device struct {
	out     io.Writer
	mu      sync.Mutex
	keys    chan board.Key
	restore func()
}

// Open takes over the terminal: unbuffered input, no echo.
func Open() (*Device, error) {
	restore, err := rawMode()
	if err != nil {
		return nil, err
	}
	d := &Device{out: os.Stdout, keys: make(chan board.Key, 64), restore: restore}
	go d.read(os.Stdin)
	return d, nil
}

func (d *Device) read(in io.Reader) {
	defer close(d.keys)
	buf := make([]byte, 1)
	for {
		if n, err := in.Read(buf); err != nil || n == 0 {
			return
		}
		// A terminal reports a keystroke, not a key going down and coming
		// up, so every press here is a tap -- which latches the microphone.
		if slot, err := strconv.ParseUint(string(buf), 16, 8); err == nil && slot < model.SlotCount {
			d.keys <- board.Key{Slot: int(slot), Down: true}
			d.keys <- board.Key{Slot: int(slot), Down: false}
		}
	}
}

func (d *Device) Firmware() string     { return "tui" }
func (d *Device) Typist() board.Typist { return nil }

func (d *Device) SendFrame(frame model.Frame) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	fmt.Fprint(d.out, "\x1b[H\x1b[2J")
	fmt.Fprint(d.out, "herdrkeys - press 0-9 a-f to simulate a key, ctrl-c to quit\r\n\r\n")
	fmt.Fprint(d.out, strings.ReplaceAll(Draw(frame), "\n", "\r\n")+"\r\n\r\n")
	fmt.Fprintf(d.out, "frame: %s\r\n", frame.KeyString())
	return nil
}

func (d *Device) Flash(slot int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if slot == board.FlashFunctionKey {
		fmt.Fprint(d.out, "\a[flash] nothing wants your attention\r\n")
	} else {
		fmt.Fprintf(d.out, "\a[flash] key %d\r\n", slot)
	}
	return nil
}

func (d *Device) Keys() <-chan board.Key { return d.keys }
func (d *Device) Err() error             { return board.ErrGone }

// Close gives the terminal back as it was.
func (d *Device) Close() error {
	if d.restore != nil {
		d.restore()
		d.restore = nil
	}
	return nil
}

// Redrawn per frame; see ADR 0011 on what the preview cannot show.
func (d *Device) Animate(now float64) error              { return nil }
func (d *Device) NextChange(now float64) (float64, bool) { return 0, false }
