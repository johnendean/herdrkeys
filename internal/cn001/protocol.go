// Package cn001 drives the Soomfon CN001, a board that cannot run our code.
//
// Fifteen keys behind one LCD panel, three rows by five, plus a narrow strip
// of three more displays on the right. It speaks Mirabox's "v3" vendor HID
// protocol: every output report is 1024 bytes beginning `CRT\0\0` and a
// command; every input report is 512 bytes beginning `ACK\0\0OK\0\0`, then the
// key and whether it went down or up.
//
// There is no firmware of ours on it, so the host decides what each meaning
// looks like -- this package is to the CN001 what device/code.py is to the
// Keybow. See ADR 0014.
//
// Commands, report parsing, where each slot sits and what each meaning looks
// like are pure; Device is the only part that touches the board.
package cn001

import (
	"bytes"

	"github.com/johnendean/herdrkeys/internal/model"
)

const (
	VendorID  = 0x1500
	ProductID = 0x3003
	UsagePage = 0xFFA0 // the vendor interface; its boot keyboard is never opened

	ReportSize = 1024
	InputSize  = 512
	KeyPixels  = 96
)

// The hardware numbers its keys 1-15 column by column, starting at the top of
// the rightmost column; the strip is 16-18. The same number addresses a key's
// image and names it in a press.
//
//	13 10  7  4  1 | 16
//	14 11  8  5  2 | 17
//	15 12  9  6  3 | 18
const (
	rows    = 3
	columns = 5
)

// HardwareKey is the key at a position: row 0 the top, column 0 the left.
func HardwareKey(row, column int) int { return (columns-1-column)*rows + row + 1 }

// SlotToKey: agents read like the agents pane beside it, the first ten left to
// right across the top two rows. The feature row is the bottom row in the
// Keybow's order: the microphone and return keys at its left, the git and
// function keys at its right, and the one between them holds nothing (ADR
// 0017).
var SlotToKey = func() map[int]int {
	m := map[int]int{}
	for slot := 0; slot < 10; slot++ {
		m[slot] = HardwareKey(slot/columns, slot%columns)
	}
	m[model.MicSlot] = HardwareKey(2, 0)
	m[model.ReturnSlot] = HardwareKey(2, 1)
	m[model.GitSlot] = HardwareKey(2, 3)
	m[model.FnSlot] = HardwareKey(2, 4)
	return m
}()

// KeyToSlot is SlotToKey the other way round.
var KeyToSlot = func() map[int]int {
	m := map[int]int{}
	for slot, key := range SlotToKey {
		m[key] = slot
	}
	return m
}()

// slotsInOrder is every slot this board shows, in slot order, so redraws go
// out in a stable order.
var slotsInOrder = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, model.MicSlot, model.ReturnSlot, model.GitSlot, model.FnSlot}

func command(name string, args ...byte) []byte {
	out := make([]byte, ReportSize)
	n := copy(out, "CRT\x00\x00"+name)
	copy(out[n:], args)
	return out
}

func Wake() []byte      { return command("DIS") }
func Commit() []byte    { return command("STP") } // nothing drawn or cleared appears until this
func Heartbeat() []byte { return command("CONNECT") }

// Brightness is the panel's brightness, 0-100.
func Brightness(percent int) []byte {
	return command("LIG", 0, 0, byte(max(0, min(100, percent))))
}

// ClearAll blanks every key and the strip.
const ClearAll = 0xFF

// Clear blanks one key, or every key and the strip.
func Clear(key int) []byte { return command("CLE", 0, 0, 0, byte(key)) }

// Image is the reports that put jpeg on key: a header, then the bytes in
// chunks.
func Image(key int, jpeg []byte) [][]byte {
	size := len(jpeg)
	reports := [][]byte{command("BAT", byte(size>>24), byte(size>>16), byte(size>>8), byte(size), byte(key))}
	for start := 0; start < size; start += ReportSize {
		chunk := make([]byte, ReportSize)
		copy(chunk, jpeg[start:min(size, start+ReportSize)])
		reports = append(reports, chunk)
	}
	return reports
}

// ParseInput is the hardware key and whether it went down, for a key report.
func ParseInput(report []byte) (key int, down, ok bool) {
	if len(report) < 11 || !bytes.HasPrefix(report, []byte("ACK\x00\x00OK\x00\x00")) {
		return 0, false, false
	}
	if report[9] == 0 {
		return 0, false, false
	}
	return int(report[9]), report[10] == 1, true
}
