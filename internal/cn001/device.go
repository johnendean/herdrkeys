package cn001

import (
	"fmt"
	"sync"
	"time"

	"github.com/johnendean/herdrkeys/internal/board"
	"github.com/johnendean/herdrkeys/internal/hid"
	"github.com/johnendean/herdrkeys/internal/model"
	"github.com/johnendean/herdrkeys/internal/usage"
)

// Device is a CN001 as the daemon sees it: a board, like the Keybow.
//
// It differs in one way the daemon has to know about. The Keybow animates
// itself; this board has to be redrawn for anything on it to move, so the
// daemon calls Animate every pass and asks NextChange how soon it must.
//
// Only keys whose look changed are redrawn, and each look is encoded once, so
// a blink costs a couple of reports per key rather than a panel's worth.
type Device struct {
	hid   hid.Device
	clock func() float64
	wall  func() time.Time // the strip counts down to moments on the calendar
	// CanType is asked on every draw rather than once, so a permission
	// switched off or back on shows on the keys straight away.
	canType func() bool

	mu         sync.Mutex
	frame      *model.Frame
	flashUntil float64
	flashSlot  int
	drawn      map[int]any // Look or Panel
	jpegs      map[Look][]byte
	panels     map[Panel][]byte
	usage      *usage.Usage
	stale      bool

	keys chan board.Key
	err  error
}

var started = time.Now()

func monotonic() float64 { return time.Since(started).Seconds() }

// NewDevice takes over an opened CN001: wakes it, sets its brightness, and
// blanks it.
func NewDevice(h hid.Device, brightness int, canType func() bool) (*Device, error) {
	d := &Device{hid: h, clock: monotonic, wall: time.Now, canType: canType,
		flashSlot: board.FlashFunctionKey, drawn: map[int]any{},
		jpegs: map[Look][]byte{}, panels: map[Panel][]byte{}, keys: make(chan board.Key, 64)}
	for _, report := range [][]byte{Heartbeat(), Wake(), Brightness(brightness), Clear(ClearAll), Commit()} {
		if err := d.write(report); err != nil {
			return nil, err
		}
	}
	go d.pump()
	return d, nil
}

// Open is the attached CN001, opened and blank; nil when there is none.
func Open(brightness int, canType func() bool) (*Device, error) {
	found, err := hid.Find(VendorID, ProductID, UsagePage)
	if err != nil {
		return nil, fmt.Errorf("cn001: %w", err)
	}
	if len(found) == 0 {
		return nil, nil
	}
	h, err := hid.Open(found[0])
	if err != nil {
		return nil, fmt.Errorf("cn001: %w", err)
	}
	d, err := NewDevice(h, brightness, canType)
	if err != nil {
		h.Close()
		return nil, err
	}
	return d, nil
}

func (d *Device) write(report []byte) error {
	if err := d.hid.Write(report); err != nil {
		return fmt.Errorf("cn001: %w", err)
	}
	return nil
}

// pump turns key reports into key edges: both edges, as on the Keybow. A key
// that holds no slot -- the one between return and git -- does nothing.
func (d *Device) pump() {
	defer close(d.keys)
	for report := range d.hid.Reports() {
		if key, down, ok := ParseInput(report); ok {
			if slot, ok := KeyToSlot[key]; ok {
				d.keys <- board.Key{Slot: slot, Down: down}
			}
		}
	}
	d.mu.Lock()
	d.err = fmt.Errorf("cn001: %w", d.hid.Err())
	d.mu.Unlock()
}

func (d *Device) jpeg(look Look) ([]byte, error) {
	if j, ok := d.jpegs[look]; ok {
		return j, nil
	}
	j, err := RenderKey(look)
	if err != nil {
		// This board's failure, not the daemon's: it drops the CN001 and
		// leaves the Keybow running.
		return nil, fmt.Errorf("cn001: %w", err)
	}
	d.jpegs[look] = j
	return j, nil
}

func (d *Device) panelJPEG(p Panel) ([]byte, error) {
	if j, ok := d.panels[p]; ok {
		return j, nil
	}
	// The countdown makes a new panel every minute, so unlike the keys'
	// looks these are not a small set: keep only recent ones.
	if len(d.panels) >= 64 {
		d.panels = map[Panel][]byte{}
	}
	j, err := RenderPanel(p)
	if err != nil {
		return nil, fmt.Errorf("cn001: %w", err)
	}
	d.panels[p] = j
	return j, nil
}

// ShowUsage is what the strip should say from now on; drawn on the next
// redraw (ADR 0019).
func (d *Device) ShowUsage(u *usage.Usage, stale bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.usage, d.stale = u, stale
	return nil
}

func (d *Device) wanted(now float64) map[int]any {
	wanted := map[int]any{}
	for key := 1; key <= rows*columns; key++ {
		wanted[key] = Dark
	}
	for key, panel := range StripPanels(d.usage, d.wall(), d.stale) {
		if panel == nil {
			wanted[key] = Dark
		} else {
			wanted[key] = *panel
		}
	}
	if d.frame == nil {
		return wanted
	}
	// A flash names its key; one that names none is the function key's.
	flashing := d.flashSlot
	if flashing == board.FlashFunctionKey {
		flashing = model.FnSlot
	}
	canType := d.canType()
	for _, slot := range slotsInOrder {
		forge := ""
		if slot == model.GitSlot {
			forge = d.frame.Forge
		}
		wanted[SlotToKey[slot]] = LookFor(d.frame.Keys[slot], d.frame.Colours[slot], now,
			now < d.flashUntil && slot == flashing, canType, forge)
	}
	return wanted
}

// redraw draws what changed, in key order. False if nothing had.
func (d *Device) redraw(now float64) (bool, error) {
	wanted := d.wanted(now)
	changed := false
	for key := 1; key <= rows*columns+len(StripKeys); key++ {
		look := wanted[key]
		if previous, ok := d.drawn[key]; ok && previous == look {
			continue
		}
		changed = true
		var reports [][]byte
		switch l := look.(type) {
		case Look:
			if l == Dark {
				reports = [][]byte{Clear(key)}
			} else {
				j, err := d.jpeg(l)
				if err != nil {
					return false, err
				}
				reports = Image(key, j)
			}
		case Panel:
			j, err := d.panelJPEG(l)
			if err != nil {
				return false, err
			}
			reports = Image(key, j)
		}
		for _, r := range reports {
			if err := d.write(r); err != nil {
				return false, err
			}
		}
		d.drawn[key] = look
	}
	if !changed {
		return false, nil
	}
	return true, d.write(Commit())
}

func (d *Device) Firmware() string { return "Soomfon CN001" }

// Typist is nil: the manufacturer's firmware types nothing we ask it to.
func (d *Device) Typist() board.Typist { return nil }

// SendFrame draws frame. Sent again unchanged, it proves the board is still
// there: the daemon resends its frame every few seconds whether or not
// anything changed, and here that is a heartbeat rather than a redraw -- what
// notices an unplugged board, since nothing else is written while it is quiet.
func (d *Device) SendFrame(frame model.Frame) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.frame = &frame
	drew, err := d.redraw(d.clock())
	if err != nil || drew {
		return err
	}
	return d.write(Heartbeat())
}

func (d *Device) Flash(slot int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.flashUntil = d.clock() + FlashSeconds
	d.flashSlot = slot
	_, err := d.redraw(d.clock())
	return err
}

// Animate redraws whatever has moved. now is on the daemon's clock, which is
// not this board's: only the difference between passes matters, and that is
// read here.
func (d *Device) Animate(float64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.redraw(d.clock())
	return err
}

func (d *Device) NextChange(float64) (float64, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.frame == nil {
		return 0, false
	}
	return NextChange(d.frame.Keys, d.clock(), d.flashUntil)
}

func (d *Device) Keys() <-chan board.Key { return d.keys }

func (d *Device) Err() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.err
}

// Close blanks the panel. A crash cannot, and the panel never gives up by
// itself.
func (d *Device) Close() error {
	d.mu.Lock()
	d.write(Clear(ClearAll))
	d.write(Commit())
	d.mu.Unlock()
	return d.hid.Close()
}
