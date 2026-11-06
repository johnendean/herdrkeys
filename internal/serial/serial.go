// Package serial is a raw serial port: 8N1, no flow control, no translation.
//
// This is the whole of what herdrkeys needs from a serial library. Read
// blocks until at least one byte arrives and is meant to be called from a
// goroutine of its own; Close unblocks it, and from then on every call fails.
// Every failure is an error, including the one the OS raises once a USB
// device is unplugged mid-read.
package serial

import "errors"

// ErrClosed is what a port says once Close has been called.
var ErrClosed = errors.New("serial port closed")

// Port is an open serial port.
type Port interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	// Flush drops whatever has arrived and not been read, best effort.
	Flush()
	Close() error
}

// Open opens the port at path -- `/dev/cu.usbmodem…` on macOS, `COM4` on
// Windows -- raw, at the given baud rate.
func Open(path string, baud int) (Port, error) { return open(path, baud) }
