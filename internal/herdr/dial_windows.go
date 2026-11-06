//go:build windows

package herdr

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// errorPipeBusy is ERROR_PIPE_BUSY: every instance of the pipe is serving
// someone else, which a moment's wait fixes.
const errorPipeBusy syscall.Errno = 231

// Endpoint is what dialling path actually opens, for doctor to name.
//
// On Windows Herdr listens on a named pipe called after the socket path, and
// the file at that path is only a marker holding `pid:start-time` (ADR 0021,
// checked against Herdr 0.9.5).
func Endpoint(path string) string { return `\\.\pipe\` + path }

// dialPlatform opens the pipe for overlapped I/O, so that the *os.File it
// becomes is driven by the runtime's I/O completion port and honours
// deadlines -- which is how a request times out and how Close unblocks a
// pending read.
func dialPlatform(path string, timeout time.Duration) (conn, error) {
	name := Endpoint(path)
	utf16, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	for {
		handle, err := syscall.CreateFile(
			utf16,
			syscall.GENERIC_READ|syscall.GENERIC_WRITE,
			0, nil,
			syscall.OPEN_EXISTING,
			syscall.FILE_FLAG_OVERLAPPED,
			0,
		)
		if err == nil {
			return os.NewFile(uintptr(handle), name), nil
		}
		if !errors.Is(err, errorPipeBusy) || time.Now().After(deadline) {
			return nil, &os.PathError{Op: "dial", Path: name, Err: err}
		}
		time.Sleep(10 * time.Millisecond)
	}
}
