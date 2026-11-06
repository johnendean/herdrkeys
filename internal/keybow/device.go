package keybow

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/johnendean/herdrkeys/internal/board"
	"github.com/johnendean/herdrkeys/internal/chord"
	"github.com/johnendean/herdrkeys/internal/model"
	"github.com/johnendean/herdrkeys/internal/serial"
)

// HandshakeTimeout is how long a board has to say hello.
const HandshakeTimeout = 3 * time.Second

// Error is a Keybow that is attached but will not talk.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

// Device is a Keybow on the other end of a CDC data port.
type Device struct {
	port     serial.Port
	path     string
	firmware string

	writing sync.Mutex
	keys    chan board.Key
	mu      sync.Mutex
	err     error
}

// Open connects to the board on the data port at path and checks that it
// runs herdrkeys firmware on this protocol.
func Open(path string) (*Device, error) {
	port, err := serial.Open(path, 115200)
	if err != nil {
		return nil, err
	}
	d, err := handshake(port, path, HandshakeTimeout)
	if err != nil {
		port.Close()
		return nil, err
	}
	return d, nil
}

// handshake confirms a herdrkeys firmware is listening, and that its protocol
// matches, then starts reading keys.
func handshake(port serial.Port, path string, timeout time.Duration) (*Device, error) {
	port.Flush()
	if _, err := port.Write(EncodeHello()); err != nil {
		return nil, err
	}
	lines := make(chan []byte, 16)
	failed := make(chan error, 1)
	go readLines(port, lines, failed)

	deadline := time.After(timeout)
	for {
		select {
		case raw := <-lines:
			var m message
			if json.Unmarshal(raw, &m) != nil || m.T != "hello" {
				continue // debug prints, and anything older than our hello
			}
			if m.Proto == nil || *m.Proto != Protocol {
				speaks := "none"
				if m.Proto != nil {
					speaks = fmt.Sprint(*m.Proto)
				}
				return nil, &Error{fmt.Sprintf(
					"%s: firmware speaks protocol %s, herdrkeys speaks %d -- the firmware needs updating",
					path, speaks, Protocol)}
			}
			firmware := "unknown"
			if fw, ok := m.FW.(string); ok && fw != "" {
				firmware = fw
			}
			d := &Device{port: port, path: path, firmware: firmware, keys: make(chan board.Key, 64)}
			go d.pump(lines, failed)
			return d, nil
		case err := <-failed:
			return nil, fmt.Errorf("%s: %w", path, err)
		case <-deadline:
			return nil, &Error{path + ": no hello from the keypad -- is herdrkeys firmware on it?"}
		}
	}
}

// readLines splits what the port says into lines until it fails.
func readLines(port serial.Port, lines chan<- []byte, failed chan<- error) {
	reader := bufio.NewReader(readerFunc(port.Read))
	for {
		raw, err := reader.ReadBytes('\n')
		if err != nil {
			failed <- err
			return
		}
		if raw = bytes.TrimSpace(raw); len(raw) > 0 {
			lines <- raw
		}
	}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

func (d *Device) pump(lines <-chan []byte, failed <-chan error) {
	defer close(d.keys)
	for {
		select {
		case raw := <-lines:
			var m message
			if json.Unmarshal(raw, &m) != nil {
				continue // device debug prints are not our problem
			}
			if key, ok := decodeKey(m); ok {
				d.keys <- key
			}
		case err := <-failed:
			d.mu.Lock()
			if errors.Is(err, serial.ErrClosed) {
				err = board.ErrGone
			}
			d.err = fmt.Errorf("%s: %w", d.path, err)
			d.mu.Unlock()
			return
		}
	}
}

func (d *Device) write(payload []byte) error {
	d.writing.Lock()
	defer d.writing.Unlock()
	if _, err := d.port.Write(payload); err != nil {
		return fmt.Errorf("%s: %w", d.path, err)
	}
	return nil
}

// Path is the data port the board is on.
func (d *Device) Path() string { return d.path }

func (d *Device) Firmware() string { return d.firmware }

// Typist is the board itself: the Keybow types for itself (ADR 0006), and
// only what it is told to, because the microphone is decided on the host.
func (d *Device) Typist() board.Typist { return d }

func (d *Device) SendFrame(frame model.Frame) error { return d.write(EncodeFrame(frame)) }
func (d *Device) Flash(slot int) error              { return d.write(EncodeFlash(slot)) }
func (d *Device) Hold(on bool, c chord.Chord) error { return d.write(EncodeHold(on, c)) }
func (d *Device) TypeReturn() error                 { return d.write(EncodeReturn()) }
func (d *Device) Keys() <-chan board.Key            { return d.keys }

func (d *Device) Err() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.err
}

func (d *Device) Close() error { return d.port.Close() }

// The Keybow animates itself.
func (d *Device) Animate(now float64) error              { return nil }
func (d *Device) NextChange(now float64) (float64, bool) { return 0, false }
