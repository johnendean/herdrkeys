// Package keybow drives a Pimoroni Keybow 2040 running herdrkeys firmware.
//
// Wire protocol, newline-delimited JSON in both directions -- the same
// framing Herdr itself uses, so a serial terminal on the data port at 115200
// is a live debugger.
//
//	host -> device  {"v":3,"t":"hello"}
//	                {"v":3,"t":"frame","k":"iwB------------f"}
//	                {"v":3,"t":"flash"}          flashes the function key
//	                {"v":3,"t":"flash","k":13}   flashes that key instead
//	                {"v":3,"t":"hold","on":true,"keys":["LEFT_CONTROL","LEFT_GUI"]}
//	                {"v":3,"t":"hold","on":false}
//	                {"v":3,"t":"return"}         types Return
//	device -> host  {"v":3,"t":"hello","proto":3,"fw":"..."}
//	                {"v":3,"t":"key","k":3}      key 3 went down
//	                {"v":3,"t":"up","k":3}       ...and came up
//
// Frames are absolute, never deltas: a dropped byte or a board reset
// self-heals on the next frame instead of leaving a stale LED lit forever.
// Key presses fire on press, not release; releases are reported too, because
// the microphone is decided on the host and a hold is told from a tap by when
// the key comes up (ADR 0017).
//
// Protocol 3 carries the dictation chord on every hold, rather than once in
// the handshake, so the board keeps no setting of its own: what it holds is
// what the message it is answering said (ADR 0017, as amended for ADR 0020).
package keybow

import (
	"encoding/json"

	"github.com/johnendean/herdrkeys/internal/board"
	"github.com/johnendean/herdrkeys/internal/chord"
	"github.com/johnendean/herdrkeys/internal/model"
)

// Protocol is the wire protocol this host speaks. A board on any other is
// refused at the handshake.
const Protocol = 3

func line(message any) []byte {
	out, err := json.Marshal(message)
	if err != nil {
		panic(err) // every message here is built from plain values
	}
	return append(out, '\n')
}

// EncodeFrame is a frame as the Keybow is sent it.
//
// `c` is omitted entirely when no key has a colour, so a board sees exactly
// the bytes it saw before herdrcolor existed whenever that plugin is absent
// (ADR 0010). The forge is never sent: only a board that can draw a logo
// reads it (ADR 0018).
func EncodeFrame(frame model.Frame) []byte {
	message := struct {
		V int       `json:"v"`
		T string    `json:"t"`
		K string    `json:"k"`
		C []*string `json:"c,omitempty"`
	}{V: Protocol, T: "frame", K: frame.KeyString()}
	coloured := false
	colours := make([]*string, model.SlotCount)
	for i, colour := range frame.Colours {
		if colour != "" {
			coloured = true
			colours[i] = &frame.Colours[i]
		}
	}
	if coloured {
		message.C = colours
	}
	return line(message)
}

// EncodeFlash asks the board to flash a key. FlashFunctionKey leaves the slot
// out, so that firmware which knows nothing of slots still flashes something.
func EncodeFlash(slot int) []byte {
	message := struct {
		V int    `json:"v"`
		T string `json:"t"`
		K *int   `json:"k,omitempty"`
	}{V: Protocol, T: "flash"}
	if slot != board.FlashFunctionKey {
		message.K = &slot
	}
	return line(message)
}

// EncodeHello opens the handshake.
func EncodeHello() []byte {
	return line(struct {
		V int    `json:"v"`
		T string `json:"t"`
	}{Protocol, "hello"})
}

// EncodeHold holds the chord down, or lets go of whatever is held.
func EncodeHold(on bool, c chord.Chord) []byte {
	message := struct {
		V    int      `json:"v"`
		T    string   `json:"t"`
		On   bool     `json:"on"`
		Keys []string `json:"keys,omitempty"`
	}{V: Protocol, T: "hold", On: on}
	if on {
		message.Keys = c.Names()
	}
	return line(message)
}

// EncodeReturn types Return.
func EncodeReturn() []byte {
	return line(struct {
		V int    `json:"v"`
		T string `json:"t"`
	}{Protocol, "return"})
}

// message is anything the board sends.
type message struct {
	T     string          `json:"t"`
	K     json.RawMessage `json:"k"`
	Proto *int            `json:"proto"`
	FW    any             `json:"fw"`
}

// decodeKey is the key edge in one message, if it is a well-formed one.
func decodeKey(m message) (board.Key, bool) {
	var down bool
	switch m.T {
	case "key":
		down = true
	case "up":
		down = false
	default:
		return board.Key{}, false
	}
	var slot int
	if len(m.K) == 0 || json.Unmarshal(m.K, &slot) != nil {
		return board.Key{}, false
	}
	// An integer, not a number that happens to be whole: "3" and 3.0 are not
	// what the firmware sends.
	var exact json.Number
	if json.Unmarshal(m.K, &exact) != nil || exact.String() != string(m.K) {
		return board.Key{}, false
	}
	return board.Key{Slot: slot, Down: down}, true
}
