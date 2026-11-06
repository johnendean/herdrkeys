package keybow

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnendean/herdrkeys/internal/board"
	"github.com/johnendean/herdrkeys/internal/chord"
	"github.com/johnendean/herdrkeys/internal/model"
	"github.com/johnendean/herdrkeys/internal/serial"
)

func decoded(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	if !bytes.HasSuffix(raw, []byte("\n")) {
		t.Fatalf("%q: newline framing, like Herdr's own", raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func frameOf(keys string, colours ...string) model.Frame {
	var f model.Frame
	copy(f.Keys[:], keys)
	copy(f.Colours[:], colours)
	return f
}

// -- the protocol ----------------------------------------------------------

func TestFramesAreAbsoluteSoADroppedByteSelfHeals(t *testing.T) {
	got := decoded(t, EncodeFrame(frameOf("iwB------------f")))
	want := map[string]any{"v": float64(Protocol), "t": "frame", "k": "iwB------------f"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
}

func TestControlMessages(t *testing.T) {
	if m := decoded(t, EncodeFlash(board.FlashFunctionKey)); m["t"] != "flash" || m["k"] != nil {
		t.Fatalf("function key flash = %v: no slot, so older firmware still flashes something", m)
	}
	if m := decoded(t, EncodeFlash(13)); m["k"] != float64(13) {
		t.Fatalf("slot flash = %v", m)
	}
	if m := decoded(t, EncodeHello()); m["t"] != "hello" || m["v"] != float64(3) {
		t.Fatalf("hello = %v", m)
	}
}

func TestTheHostTellsTheKeybowWhatToHold(t *testing.T) {
	// Protocol 3 names the chord on every hold, so the board keeps no
	// setting of its own.
	c, _ := chord.Parse("ctrl+win")
	on := decoded(t, EncodeHold(true, c))
	if on["on"] != true || !reflect.DeepEqual(on["keys"], []any{"LEFT_CONTROL", "LEFT_GUI"}) {
		t.Fatalf("hold = %v", on)
	}
	off := decoded(t, EncodeHold(false, c))
	if off["on"] != false || off["keys"] != nil {
		t.Fatalf("release = %v: it lets go of whatever it holds", off)
	}
	if m := decoded(t, EncodeReturn()); !reflect.DeepEqual(m, map[string]any{"v": float64(3), "t": "return"}) {
		t.Fatalf("return = %v", m)
	}
}

func TestAFrameWithNoColoursIsByteForByteWhatItAlwaysWas(t *testing.T) {
	// Without herdrcolor there is nothing to send: the field is absent, not
	// null, so older firmware sees what it always saw.
	if raw := EncodeFrame(frameOf("i-----------menf")); bytes.Contains(raw, []byte(`"c"`)) {
		t.Fatalf("%s", raw)
	}
}

func TestColoursRideOnTheFrameItself(t *testing.T) {
	m := decoded(t, EncodeFrame(frameOf("i-----------menf", "#a6e3a1")))
	colours, _ := m["c"].([]any)
	if len(colours) != 16 || colours[0] != "#a6e3a1" || colours[1] != nil {
		t.Fatalf("c = %v: one entry per key, so a frame stays absolute", m["c"])
	}
}

func TestTheKeybowNeverHearsAboutTheForge(t *testing.T) {
	// Only the CN001 can draw a logo (ADR 0018).
	plain := frameOf("------------mer" + "f")
	withForge := plain
	withForge.Forge = "github"
	if !bytes.Equal(EncodeFrame(plain), EncodeFrame(withForge)) {
		t.Fatal("the forge reached the wire")
	}
}

func TestOnlyWellFormedKeyMessagesCount(t *testing.T) {
	var got []board.Key
	for _, raw := range []string{
		`{"t":"key","k":3}`, `{"t":"key"}`, `{"t":"key","k":"3"}`, `{"t":"hello","proto":1}`,
		`{"t":"up","k":3}`, `{"t":"up","k":null}`, `{"t":"key","k":3.5}`, `{"t":"key","k":15}`,
	} {
		var m message
		json.Unmarshal([]byte(raw), &m)
		if key, ok := decodeKey(m); ok {
			got = append(got, key)
		}
	}
	want := []board.Key{{Slot: 3, Down: true}, {Slot: 3, Down: false}, {Slot: 15, Down: true}}
	if !slices.Equal(got, want) {
		t.Fatalf("%v", got)
	}
}

// -- the handshake, against a board in memory ------------------------------

// pipePort is a serial port whose other end is a fake board.
type pipePort struct {
	in     *io.PipeReader // what the board says
	toHost *io.PipeWriter
	mu     sync.Mutex
	wrote  bytes.Buffer
	answer func(line string) string // the board's reply to each host line
	closed bool
}

func newPipePort(answer func(string) string) *pipePort {
	r, w := io.Pipe()
	return &pipePort{in: r, toHost: w, answer: answer}
}

func (p *pipePort) Read(b []byte) (int, error) {
	n, err := p.in.Read(b)
	if err != nil {
		return n, serial.ErrClosed
	}
	return n, nil
}

func (p *pipePort) Write(b []byte) (int, error) {
	p.mu.Lock()
	p.wrote.Write(b)
	p.mu.Unlock()
	if reply := p.answer(string(b)); reply != "" {
		go io.WriteString(p.toHost, reply)
	}
	return len(b), nil
}

func (p *pipePort) say(line string) { io.WriteString(p.toHost, line+"\n") }
func (p *pipePort) Flush()          {}
func (p *pipePort) Close() error {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	p.toHost.Close()
	return nil
}

func helloWith(proto string) func(string) string {
	return func(line string) string {
		if strings.Contains(line, `"hello"`) {
			return "Traceback: a debug print first\n" +
				`{"v":3,"t":"hello","proto":` + proto + `,"fw":"herdrkeys-device/test"}` + "\n"
		}
		return ""
	}
}

func TestAHerdrkeysBoardIsTakenOnAndItsKeysRead(t *testing.T) {
	port := newPipePort(helloWith("3"))
	d, err := handshake(port, "COM9", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if d.Firmware() != "herdrkeys-device/test" {
		t.Fatalf("firmware = %q", d.Firmware())
	}
	port.say(`{"v":3,"t":"key","k":12}`)
	port.say(`not json`)
	port.say(`{"v":3,"t":"up","k":12}`)
	for _, want := range []board.Key{{Slot: 12, Down: true}, {Slot: 12, Down: false}} {
		select {
		case got := <-d.Keys():
			if got != want {
				t.Fatalf("key = %v, want %v", got, want)
			}
		case <-time.After(time.Second):
			t.Fatal("no key")
		}
	}

	// Unplugged: the key stream ends, and says the board is gone.
	port.Close()
	select {
	case _, open := <-d.Keys():
		if open {
			t.Fatal("a key after the unplug")
		}
	case <-time.After(time.Second):
		t.Fatal("the key stream did not end")
	}
	if !errors.Is(d.Err(), board.ErrGone) {
		t.Fatalf("err = %v", d.Err())
	}
}

func TestABoardOnAnotherProtocolIsRefusedWithTheReason(t *testing.T) {
	_, err := handshake(newPipePort(helloWith("2")), "COM9", time.Second)
	var refused *Error
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), "protocol 2") {
		t.Fatalf("err = %v", err)
	}
}

func TestABoardThatSaysNothingIsRefused(t *testing.T) {
	_, err := handshake(newPipePort(func(string) string { return "" }), "COM9", 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "no hello") {
		t.Fatalf("err = %v", err)
	}
}

// -- discovery -------------------------------------------------------------

const ioregSample = `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><array>
 <dict>
  <key>idVendor</key><integer>5840</integer><key>idProduct</key><integer>2246</integer>
  <key>IORegistryEntryChildren</key><array>
   <dict><key>bInterfaceNumber</key><integer>2</integer>
    <key>IORegistryEntryChildren</key><array>
     <dict><key>IOCalloutDevice</key><string>/dev/cu.usbmodem11403</string></dict>
    </array></dict>
   <dict><key>bInterfaceNumber</key><integer>0</integer>
    <key>IORegistryEntryChildren</key><array>
     <dict><key>IOCalloutDevice</key><string>/dev/cu.usbmodem11401</string><key>Flag</key><true/></dict>
    </array></dict>
  </array>
 </dict>
 <dict>
  <key>idVendor</key><integer>1234</integer><key>idProduct</key><integer>2246</integer>
  <key>IORegistryEntryChildren</key><array>
   <dict><key>IOCalloutDevice</key><string>/dev/cu.someone-else</string></dict>
  </array>
 </dict>
</array></plist>`

func TestTheKeybowsPortsAreFoundInTheIORegistry(t *testing.T) {
	got := portsFromIOReg([]byte(ioregSample))
	slices.SortFunc(got, func(a, b Port) int { return a.Interface - b.Interface })
	want := []Port{{Path: "/dev/cu.usbmodem11401", Interface: 0}, {Path: "/dev/cu.usbmodem11403", Interface: 2}}
	if !slices.Equal(got, want) {
		t.Fatalf("ports = %v", got)
	}
	if DataPort(want) != "/dev/cu.usbmodem11403" || ConsolePort(want) != "/dev/cu.usbmodem11401" {
		t.Fatal("the data channel is the higher interface")
	}
}

func TestOnePortMeansBootPyHasNotBeenDeployed(t *testing.T) {
	if DataPort([]Port{{Path: "/dev/cu.usbmodem1", Interface: 0}}) != "" {
		t.Fatal("frames into the console would feed JSON to the REPL")
	}
}

func TestALiveKeybowIsFoundAndAnswers(t *testing.T) {
	// Opt-in: a Keybow attached to this machine.
	if os.Getenv("HERDRKEYS_LIVE_KEYBOW") == "" {
		t.Skip("set HERDRKEYS_LIVE_KEYBOW=1 with a Keybow attached")
	}
	ports := FindPorts()
	t.Logf("ports: %v", ports)
	data := DataPort(ports)
	if data == "" {
		t.Fatal("no data port")
	}
	d, err := Open(data)
	if err != nil {
		t.Logf("handshake on %s: %v", data, err) // a board on older firmware is refused, with the reason
		var refused *Error
		if !errors.As(err, &refused) {
			t.Fatal(err)
		}
		return
	}
	defer d.Close()
	t.Logf("%s runs %s", data, d.Firmware())
	if err := d.SendFrame(model.Blank(true, false)); err != nil {
		t.Fatal(err)
	}
}

// -- provisioning ----------------------------------------------------------

const bootOut = "Adafruit CircuitPython 10.3.0 on 2026-08-31; Pimoroni Keybow 2040 with rp2040\nBoard ID:pimoroni_keybow2040\n"

func makeDrive(t *testing.T, root, name string, boardID bool, code *string) string {
	t.Helper()
	drive := filepath.Join(root, name)
	if err := os.MkdirAll(drive, 0o755); err != nil {
		t.Fatal(err)
	}
	if boardID {
		os.WriteFile(filepath.Join(drive, "boot_out.txt"), []byte(bootOut), 0o644)
	}
	if code != nil {
		os.WriteFile(filepath.Join(drive, "code.py"), []byte(*code), 0o644)
	}
	return drive
}

func ptr(s string) *string { return &s }

func TestAKeybowIsFoundByBoardIDNotVolumeName(t *testing.T) {
	// Owners rename volumes, and the name says nothing about what board it is.
	root := t.TempDir()
	other := makeDrive(t, root, "Backup Disk", false, nil)
	os.WriteFile(filepath.Join(other, "boot_out.txt"), []byte("Board ID:raspberry_pi_pico\n"), 0o644)
	mine := makeDrive(t, root, "MY KEYPAD", true, nil)
	if got := FindDrive([]string{other, mine}); got != mine {
		t.Fatalf("drive = %q", got)
	}
	if FindDrive([]string{other, filepath.Join(root, "nope")}) != "" {
		t.Fatal("other drives are ignored, and a missing one is not an error")
	}
}

func TestWhetherWeMayWriteIsAConsentQuestion(t *testing.T) {
	root := t.TempDir()
	for _, c := range []struct {
		code  *string
		state DriveState
	}{
		{ptr(`FIRMWARE = "herdrkeys-device/0.1.0"` + "\n"), Ours},
		{nil, Blank},
		{ptr("   \n"), Blank},
		// The board this project was built on arrived carrying a four-layer
		// HID keyboard its owner had no other copy of.
		{ptr("from pmk import PMK  # someone's macro pad\n"), Foreign},
	} {
		drive := makeDrive(t, root, string(rune('a'+len(c.state)))+string(c.state)+filepath.Base(t.TempDir()), true, c.code)
		d, ok := InspectAmong([]string{drive})
		if !ok || d.State != c.state {
			t.Fatalf("%v: state = %v", c.code, d.State)
		}
		if d.MayWrite() != (c.state != Foreign) {
			t.Fatalf("%v: may write = %v: installing herdrkeys is not consent to destroy this", c.code, d.MayWrite())
		}
	}
}

func TestSalvageKeepsEveryCopy(t *testing.T) {
	root := t.TempDir()
	drive := makeDrive(t, root, "CIRCUITPY", true, ptr("original\n"))
	os.WriteFile(filepath.Join(drive, "boot.py"), []byte("import usb_hid\n"), 0o644)
	destination := filepath.Join(root, "salvage")

	first, err := Salvage(drive, destination)
	if err != nil || len(first) != 3 {
		t.Fatalf("%v, %v", first, err)
	}
	// A second salvage, even in the same second, must not clobber the
	// first: it may be the only copy.
	os.WriteFile(filepath.Join(drive, "code.py"), []byte("changed\n"), 0o644)
	second, err := Salvage(drive, destination)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range append(first, second...) {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s is gone", p)
		}
	}
	if data, _ := os.ReadFile(first[0]); string(data) != "original\n" {
		t.Fatalf("first copy = %q", data)
	}
}

func TestSalvagingAnEmptyBoardSavesNothing(t *testing.T) {
	root := t.TempDir()
	saved, err := Salvage(makeDrive(t, root, "CIRCUITPY", false, nil), filepath.Join(root, "salvage"))
	if err != nil || len(saved) != 0 {
		t.Fatalf("%v, %v", saved, err)
	}
}

func TestTheFirmwareThatShipsIsTheFirmwareWeCheckFor(t *testing.T) {
	// Classify looks for the marker; if code.py stopped declaring it, every
	// board would look foreign and nothing would ever provision.
	files := FirmwareFiles()
	if !bytes.Contains(files["code.py"], []byte(FirmwareMarker)) || len(files["boot.py"]) == 0 {
		t.Fatal("the embedded firmware is not ours")
	}
	// And it speaks the protocol this host does.
	if !bytes.Contains(files["code.py"], []byte("PROTOCOL = 3\n")) {
		t.Fatal("the embedded firmware is on another protocol")
	}
	if bytes.Contains(files["code.py"], []byte("\r\n")) {
		t.Fatal("the firmware is embedded with CRLF: see .gitattributes")
	}
}

func TestCopyFirmwareWritesBothFiles(t *testing.T) {
	drive := makeDrive(t, t.TempDir(), "CIRCUITPY", true, nil)
	written, err := CopyFirmware(drive)
	if err != nil || !slices.Equal(written, []string{"boot.py", "code.py"}) {
		t.Fatalf("%v, %v", written, err)
	}
	if Classify(drive) != Ours {
		t.Fatal("and the result is recognised as ours")
	}
}
