// Package chord is the dictation chord: the keys Wispr Flow treats as
// push-to-talk, which whatever types for a board holds down while the
// microphone is open (CONTEXT.md).
//
// It is a setting of the host, not of a board. Each key is named once here and
// carries its name in each of the three places it is typed from: adafruit_hid
// on the Keybow, a virtual-key code for SendInput on Windows, and a key code
// for CGEvent on macOS.
package chord

import (
	"fmt"
	"runtime"
	"strings"
)

// Key is one key a chord may hold.
type Key struct {
	// Name is the adafruit_hid Keycode attribute, which is also what the
	// Keybow is sent (protocol 3).
	Name string
	// VK is the Windows virtual-key code.
	VK uint16
	// MacCode is the macOS virtual key code, and MacFlag the CGEventFlags bit
	// a modifier sets while it is down (0 for a key that is not a modifier).
	MacCode uint16
	MacFlag uint64
	// MacDevice is the NX_DEVICE* bit saying which of a pair of modifiers it
	// was: Flow tells Right Option from Left by it.
	MacDevice uint64
}

// Modifier flags, from CGEventTypes.h.
const (
	macShift   = 0x00020000
	macControl = 0x00040000
	macOption  = 0x00080000
	macCommand = 0x00100000
)

// keys are the keys a chord may be made of. Modifiers, because a chord of
// ordinary keys would type into whatever has focus while dictation runs; and
// F13-F15, which no keyboard has and every OS passes through.
var keys = []Key{
	{"LEFT_CONTROL", 0xA2, 0x3B, macControl, 0x0001},
	{"RIGHT_CONTROL", 0xA3, 0x3E, macControl, 0x2000},
	{"LEFT_SHIFT", 0xA0, 0x38, macShift, 0x0002},
	{"RIGHT_SHIFT", 0xA1, 0x3C, macShift, 0x0004},
	{"LEFT_ALT", 0xA4, 0x3A, macOption, 0x0020},
	{"RIGHT_ALT", 0xA5, 0x3D, macOption, 0x0040},
	{"LEFT_GUI", 0x5B, 0x37, macCommand, 0x0008},
	{"RIGHT_GUI", 0x5C, 0x36, macCommand, 0x0010},
	{"F13", 0x7C, 0x69, 0, 0},
	{"F14", 0x7D, 0x6B, 0, 0},
	{"F15", 0x7E, 0x71, 0, 0},
}

// aliases are the names a person might write in config.toml for each key.
// Bare modifiers mean the left one, as a keyboard shortcut usually does.
var aliases = map[string]string{
	"ctrl": "LEFT_CONTROL", "control": "LEFT_CONTROL", "left_ctrl": "LEFT_CONTROL",
	"right_ctrl": "RIGHT_CONTROL",
	"shift":      "LEFT_SHIFT",
	"alt":        "LEFT_ALT", "option": "LEFT_ALT", "left_option": "LEFT_ALT",
	"right_option": "RIGHT_ALT", "altgr": "RIGHT_ALT",
	"win": "LEFT_GUI", "windows": "LEFT_GUI", "cmd": "LEFT_GUI", "command": "LEFT_GUI",
	"super": "LEFT_GUI", "gui": "LEFT_GUI", "meta": "LEFT_GUI",
	"right_win": "RIGHT_GUI", "right_cmd": "RIGHT_GUI", "right_command": "RIGHT_GUI",
}

// Chord is the keys held together, in the order they go down.
type Chord []Key

// String is the chord as config.toml would spell it.
func (c Chord) String() string {
	names := make([]string, len(c))
	for i, k := range c {
		names[i] = strings.ToLower(k.Name)
	}
	return strings.Join(names, "+")
}

// Names are the adafruit_hid names, as the Keybow is sent them.
func (c Chord) Names() []string {
	names := make([]string, len(c))
	for i, k := range c {
		names[i] = k.Name
	}
	return names
}

// Default is the chord for this platform: Right Option on macOS, which is
// what this project has always held, and Ctrl+Win on Windows, since Right Alt
// is AltGr on many layouts and Flow rejects a bare Ctrl (ADR 0017).
func Default() Chord { return DefaultFor(runtime.GOOS) }

// DefaultFor is the default chord on the platform goos names.
func DefaultFor(goos string) Chord {
	if goos == "windows" {
		return Chord{lookup("LEFT_CONTROL"), lookup("LEFT_GUI")}
	}
	return Chord{lookup("RIGHT_ALT")}
}

func lookup(name string) Key {
	for _, k := range keys {
		if k.Name == name {
			return k
		}
	}
	panic("chord: no key " + name)
}

// Parse reads a chord as config.toml spells it: key names joined by `+`, any
// case, such as "ctrl+win" or "right_alt". An empty string is the default.
func Parse(text string) (Chord, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Default(), nil
	}
	var out Chord
	seen := map[string]bool{}
	for _, part := range strings.Split(text, "+") {
		name := strings.ToLower(strings.TrimSpace(part))
		if canonical, ok := aliases[name]; ok {
			name = canonical
		}
		name = strings.ToUpper(name)
		var key *Key
		for i := range keys {
			if keys[i].Name == name {
				key = &keys[i]
			}
		}
		if key == nil {
			return nil, fmt.Errorf("dictation chord %q: %q is not a key a chord can hold", text, strings.TrimSpace(part))
		}
		if seen[key.Name] {
			return nil, fmt.Errorf("dictation chord %q: %s twice", text, strings.TrimSpace(part))
		}
		seen[key.Name] = true
		out = append(out, *key)
	}
	return out, nil
}
