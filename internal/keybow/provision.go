package keybow

// Getting the firmware onto a board, and deciding whether we may.
//
// Copying two files is the easy part. The hard parts are that `boot.py` only
// takes effect on a USB re-enumeration, and that the board we found might not
// be ours to overwrite.
//
// The re-enumeration turns out to be automatable: breaking into the
// CircuitPython REPL over the console serial and calling
// `microcontroller.reset()` performs a genuine hard reset. Measured on a
// Keybow 2040: the USB nodes disappear entirely and are back about two seconds
// later, with `boot.py` applied.
//
// The consent question is the more important one. A Keybow that shows up on
// this machine may be carrying firmware its owner cares about and has no copy
// of, so herdrkeys provisions only a board that is already running herdrkeys
// or is carrying nothing at all. Anything else is left alone until someone
// asks for it by running `herdrkeys adopt`, which salvages what is there
// first (ADR 0004).

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/johnendean/herdrkeys/device"
	"github.com/johnendean/herdrkeys/internal/serial"
)

const (
	boardID = "pimoroni_keybow2040"
	// FirmwareMarker is what herdrkeys firmware declares about itself.
	FirmwareMarker = "herdrkeys-device/"
	replTimeout    = 5 * time.Second
	resetSettle    = 500 * time.Millisecond
)

// DriveState is what is on the board, and therefore whether we may write to
// it.
type DriveState string

const (
	Ours    DriveState = "ours"    // already running herdrkeys; safe to update
	Blank   DriveState = "blank"   // no code.py; nothing to lose
	Foreign DriveState = "foreign" // somebody else's firmware; needs consent
)

// Drive is a Keybow's CIRCUITPY drive and what is on it.
type Drive struct {
	Path  string
	State DriveState
}

// MayWrite reports whether provisioning may write to the drive unasked.
func (d Drive) MayWrite() bool { return d.State == Ours || d.State == Blank }

// FindDrive is the mounted CIRCUITPY drive of a Keybow 2040, identified by
// boot_out.txt, among the given mount points -- or "".
//
// Matched on board ID rather than volume name, which the owner may have
// renamed and which says nothing about what board it is.
func FindDrive(candidates []string) string {
	for _, candidate := range candidates {
		marker, err := os.ReadFile(filepath.Join(candidate, "boot_out.txt"))
		if err == nil && bytes.Contains(marker, []byte(boardID)) {
			return candidate
		}
	}
	return ""
}

// Classify says what is on a drive.
func Classify(drive string) DriveState {
	code, err := os.ReadFile(filepath.Join(drive, "code.py"))
	if errors.Is(err, fs.ErrNotExist) {
		return Blank
	}
	if err != nil {
		return Foreign // unreadable: assume it matters
	}
	if bytes.Contains(code, []byte(FirmwareMarker)) {
		return Ours
	}
	if len(bytes.TrimSpace(code)) == 0 {
		return Blank
	}
	return Foreign
}

// Inspect finds the Keybow's drive among this machine's mounts. False when
// none is mounted.
func Inspect() (Drive, bool) { return InspectAmong(mountPoints()) }

// InspectAmong is Inspect over the given mount points.
func InspectAmong(candidates []string) (Drive, bool) {
	drive := FindDrive(candidates)
	if drive == "" {
		return Drive{}, false
	}
	return Drive{Path: drive, State: Classify(drive)}, true
}

// Salvage copies whatever firmware is on the board into destination. Never
// overwrites: every copy is stamped, since it may be the only one.
func Salvage(drive, destination string) ([]string, error) {
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return nil, err
	}
	stamp := time.Now().Format("20060102-150405")
	var saved []string
	for _, name := range []string{"code.py", "boot.py", "boot_out.txt"} {
		data, err := os.ReadFile(filepath.Join(drive, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return saved, err
		}
		target := uniquePath(filepath.Join(destination, stamp+"-"+name))
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return saved, err
		}
		saved = append(saved, target)
	}
	return saved, nil
}

// uniquePath is path, or path with a counter in it if that is already taken:
// two salvages in the same second must not clobber each other.
func uniquePath(path string) string {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return path
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for n := 2; ; n++ {
		candidate := base + "-" + strconv.Itoa(n) + ext
		if _, err := os.Stat(candidate); errors.Is(err, fs.ErrNotExist) {
			return candidate
		}
	}
}

// FirmwareFiles are the files provisioning writes, with what goes in each.
func FirmwareFiles() map[string][]byte {
	return map[string][]byte{"boot.py": device.Boot, "code.py": device.Code}
}

// CopyFirmware writes boot.py and code.py to the drive and flushes them, so
// the reset that follows finds them there.
func CopyFirmware(drive string) ([]string, error) {
	var written []string
	for _, name := range []string{"boot.py", "code.py"} {
		if err := writeSynced(filepath.Join(drive, name), FirmwareFiles()[name]); err != nil {
			return written, err
		}
		written = append(written, name)
	}
	return written, nil
}

func writeSynced(path string, data []byte) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// HardReset re-enumerates the board so a new boot.py takes effect.
//
// Interrupts the running code.py to reach the REPL, then resets. Waits for
// the prompt rather than guessing a delay -- a fixed one-second wait was too
// short on a real board, which left it sitting at a dead REPL with the LEDs
// off.
func HardReset(consolePort string) bool {
	port, err := serial.Open(consolePort, 115200)
	if err != nil {
		return false
	}
	defer port.Close()
	port.Flush()
	if _, err := port.Write([]byte{0x03}); err != nil { // ctrl-c: interrupt code.py
		return false
	}
	prompt := make(chan bool, 1)
	go func() {
		var seen []byte
		buf := make([]byte, 256)
		for {
			n, err := port.Read(buf)
			if err != nil {
				prompt <- false
				return
			}
			seen = append(seen, buf[:n]...)
			if bytes.Contains(seen, []byte(">>> ")) {
				prompt <- true
				return
			}
		}
	}()
	select {
	case ok := <-prompt:
		if !ok {
			return false
		}
	case <-time.After(replTimeout):
		return false
	}
	if _, err := port.Write([]byte("import microcontroller; microcontroller.reset()\r\n")); err != nil {
		return false
	}
	time.Sleep(resetSettle)
	return true
}
