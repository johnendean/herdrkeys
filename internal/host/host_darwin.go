package host

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ebitengine/purego"

	"github.com/johnendean/herdrkeys/internal/chord"
)

// -- typing for a board that cannot (ADR 0017) ------------------------------
//
// Posted at the HID tap, so they arrive as if from a keyboard: Wispr Flow was
// measured opening on a synthetic Right Option in 0.21s and closing on its
// release. Posting needs Accessibility permission; without it macOS drops the
// events without a word, so CanType is asked before anything is posted.

const (
	hidTap       = 0  // kCGHIDEventTap
	flagsChanged = 12 // kCGEventFlagsChanged
	macReturn    = 36
	nonCoalesced = 0x100 // kCGEventFlagMaskNonCoalesced
)

var (
	loadOnce sync.Once
	loadErr  error

	cgPreflight      func() bool
	cgRequest        func() bool
	cgCreateKeyboard func(source uintptr, keycode uint16, down bool) uintptr
	cgSetType        func(event uintptr, kind uint32)
	cgSetFlags       func(event uintptr, flags uint64)
	cgPost           func(tap uint32, event uintptr)
	cfRelease        func(ref uintptr)
)

func load() error {
	loadOnce.Do(func() {
		cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = err
			return
		}
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = err
			return
		}
		purego.RegisterLibFunc(&cgPreflight, cg, "CGPreflightPostEventAccess")
		purego.RegisterLibFunc(&cgRequest, cg, "CGRequestPostEventAccess")
		purego.RegisterLibFunc(&cgCreateKeyboard, cg, "CGEventCreateKeyboardEvent")
		purego.RegisterLibFunc(&cgSetType, cg, "CGEventSetType")
		purego.RegisterLibFunc(&cgSetFlags, cg, "CGEventSetFlags")
		purego.RegisterLibFunc(&cgPost, cg, "CGEventPost")
		purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
	})
	return loadErr
}

func post(keycode uint16, down bool, kind uint32, flags uint64) error {
	if err := load(); err != nil {
		return err
	}
	event := cgCreateKeyboard(0, keycode, down)
	if event == 0 {
		return errors.New("could not create a keyboard event")
	}
	defer cfRelease(event)
	if kind != 0 {
		cgSetType(event, kind)
	}
	cgSetFlags(event, flags)
	cgPost(hidTap, event)
	return nil
}

// press posts the chord going down in order, or coming up in reverse. A
// modifier is a flags-changed event carrying every modifier still down,
// including the device bit that says which of a pair it is -- which is how
// Flow tells Right Option from Left.
func press(c chord.Chord, down bool) error {
	var flags uint64
	order := c
	if !down {
		order = make(chord.Chord, len(c))
		for i := range c {
			order[i] = c[len(c)-1-i]
		}
		for _, k := range c {
			flags |= k.MacFlag | k.MacDevice
		}
	}
	for _, k := range order {
		if k.MacFlag == 0 {
			if err := post(k.MacCode, down, 0, flags|nonCoalesced); err != nil {
				return err
			}
			continue
		}
		if down {
			flags |= k.MacFlag | k.MacDevice
		} else {
			flags &^= k.MacDevice
			if !stillHeld(order, k) {
				flags &^= k.MacFlag
			}
		}
		if err := post(k.MacCode, down, flagsChanged, flags|nonCoalesced); err != nil {
			return err
		}
	}
	return nil
}

// stillHeld reports whether a modifier sharing k's flag is still down after
// k comes up -- left and right Shift held together, say.
func stillHeld(releasing chord.Chord, k chord.Key) bool {
	after := false
	for _, other := range releasing {
		if other.Name == k.Name {
			after = true
			continue
		}
		if after && other.MacFlag == k.MacFlag {
			return true
		}
	}
	return false
}

func typeReturn() error {
	if err := post(macReturn, true, 0, nonCoalesced); err != nil {
		return err
	}
	return post(macReturn, false, 0, nonCoalesced)
}

// CanType reports whether this process may post events.
func (t *Typist) CanType() bool { return load() == nil && cgPreflight() }

// CanTypeAfresh reports whether a process started now could type.
//
// Not the same question as CanType: a process that started without the
// permission is not told when it is granted, so only a new one can say. The
// new one is this binary, asked with `can-type`.
func (t *Typist) CanTypeAfresh() bool {
	self := t.Self
	if self == "" {
		var err error
		if self, err = os.Executable(); err != nil {
			return false
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, self, "can-type").Run() == nil
}

// Ask asks macOS for the permission, which shows the user a dialog.
func (t *Typist) Ask() {
	if load() == nil {
		cgRequest()
	}
}

// Who is what to allow in System Settings.
func (t *Typist) Who() string {
	self, _ := os.Executable()
	return self + " in System Settings > Privacy & Security > Accessibility"
}

// -- raising the terminal ------------------------------------------------------
//
// The app is found by walking up from a Herdr client process to the first
// ancestor that names an `/Applications/*.app` -- the server is detached with
// ppid 1 and has no terminal above it, so it is useless for this. Verified as
//
//	herdr -> zsh -> login -> iTermServer-3.6.11 -> /Applications/iTerm.app
//
// and then, from iTerm 3.7, as
//
//	herdr -> zsh -> login -f ... /Applications/iTerm.app/.../ShellLauncher
//	      -> ~/Library/Application Support/iTerm2/iTermServer-3.7.2 -> launchd
//
// where the app is no ancestor at all, only an argument to `login`. So the
// bundle is looked for anywhere in an ancestor's command line.
//
// The walk can legitimately fail (a detached session, `--remote`, or no client
// attached at all), which is why config can name the app outright.

var appPath = regexp.MustCompile(`(/Applications/[^/]+?\.app)/`)

// Raiser brings the terminal app hosting Herdr to the front.
type Raiser struct {
	// Override is terminal_app from config: the .app to raise, skipping the
	// search.
	Override string
	found    string
}

type process struct {
	ppid    int
	command string
}

func processes() map[int]process {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-Ao", "pid=,ppid=,command=").Output()
	if err != nil {
		return nil
	}
	rows := map[int]process{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 == nil && err2 == nil {
			rows[pid] = process{ppid, strings.Join(fields[2:], " ")}
		}
	}
	return rows
}

// App is the .app bundle hosting a Herdr client, or "".
func (r *Raiser) App() string {
	if r.Override != "" {
		return r.Override
	}
	if r.found != "" {
		return r.found
	}
	rows := processes()
	for pid, p := range rows {
		program := strings.Fields(p.command)[0]
		if !(strings.HasSuffix(strings.TrimSpace(p.command), "herdr") || strings.Contains(program, "/herdr")) ||
			strings.Contains(p.command, " server") {
			continue
		}
		seen := map[int]bool{}
		for cursor, hops := pid, 0; hops < 12 && cursor > 1 && !seen[cursor]; hops++ {
			seen[cursor] = true
			entry, ok := rows[cursor]
			if !ok {
				break
			}
			if m := appPath.FindStringSubmatch(entry.command); m != nil {
				r.found = m[1]
				return r.found
			}
			cursor = entry.ppid
		}
	}
	return ""
}

// Raise brings the terminal app forward. Best effort: never worth failing a
// key press.
func (r *Raiser) Raise() {
	if app := r.App(); app != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		exec.CommandContext(ctx, "open", "-a", app).Run()
	}
}

// Describe says what Raise would bring forward, for doctor.
func (r *Raiser) Describe() string { return r.App() }
