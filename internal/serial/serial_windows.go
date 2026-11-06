package serial

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

// windowsPort is a COM port opened for overlapped I/O, so the *os.File it
// becomes is driven by the runtime's completion port: a pending read does not
// pin a thread, a write is not queued behind it, and Close cancels it.
type windowsPort struct {
	file   *os.File
	handle windows.Handle
	closed atomic.Bool
}

const (
	maxDword = 0xFFFFFFFF
	// DCB flag bits, from the Win32 bitfield.
	dcbBinary    = 1 << 0
	dcbDtrEnable = 1 << 4  // fDtrControl = DTR_CONTROL_ENABLE
	dcbRtsEnable = 1 << 12 // fRtsControl = RTS_CONTROL_ENABLE
	purgeRxClear = 0x0008
	writeTimeout = 2000 // ms: a board that will not take a frame in two seconds is gone
)

func open(path string, baud int) (Port, error) {
	name := path
	if !strings.HasPrefix(name, `\\.\`) {
		// COM10 and above are only reachable through the device namespace,
		// and the prefix is harmless below it.
		name = `\\.\` + name
	}
	utf16, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(utf16,
		windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := configure(handle, baud); err != nil {
		windows.CloseHandle(handle)
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &windowsPort{file: os.NewFile(uintptr(handle), path), handle: handle}, nil
}

func configure(handle windows.Handle, baud int) error {
	var dcb windows.DCB
	dcb.DCBlength = uint32(unsafe.Sizeof(dcb))
	if err := windows.GetCommState(handle, &dcb); err != nil {
		return err
	}
	dcb.BaudRate = uint32(baud)
	dcb.ByteSize = 8
	dcb.Parity = 0   // NOPARITY
	dcb.StopBits = 0 // ONESTOPBIT
	// DTR asserted, as macOS asserts it on open: CircuitPython reports a CDC
	// channel as connected only while the host holds DTR, and its REPL does
	// not answer without it.
	dcb.Flags = dcbBinary | dcbDtrEnable | dcbRtsEnable
	if err := windows.SetCommState(handle, &dcb); err != nil {
		return err
	}
	// Return as soon as anything has arrived, and otherwise wait for it --
	// "forever" being the largest timeout Windows takes short of none. A read
	// that came back empty would read as end of file.
	return windows.SetCommTimeouts(handle, &windows.CommTimeouts{
		ReadIntervalTimeout:        maxDword,
		ReadTotalTimeoutMultiplier: maxDword,
		ReadTotalTimeoutConstant:   maxDword - 1,
		WriteTotalTimeoutConstant:  writeTimeout,
	})
}

func (p *windowsPort) Read(buf []byte) (int, error) {
	for {
		if p.closed.Load() {
			return 0, ErrClosed
		}
		n, err := p.file.Read(buf)
		if p.closed.Load() {
			return 0, ErrClosed
		}
		if err != nil {
			return n, fmt.Errorf("%s: %w", p.file.Name(), err)
		}
		if n > 0 {
			return n, nil
		}
	}
}

func (p *windowsPort) Write(buf []byte) (int, error) {
	if p.closed.Load() {
		return 0, ErrClosed
	}
	n, err := p.file.Write(buf)
	if err != nil {
		if errors.Is(err, os.ErrClosed) {
			return n, ErrClosed
		}
		return n, fmt.Errorf("%s: %w", p.file.Name(), err)
	}
	return n, nil
}

func (p *windowsPort) Flush() {
	if !p.closed.Load() {
		windows.PurgeComm(p.handle, purgeRxClear)
	}
}

func (p *windowsPort) Close() error {
	if p.closed.Swap(true) {
		return nil
	}
	return p.file.Close()
}
