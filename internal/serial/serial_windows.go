package serial

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

// windowsPort is a COM port driven with overlapped I/O by hand.
//
// Not an *os.File: Go lets only one read or write on a file run at a time,
// overlapped handle or not, so a read waiting for a key press would hold up
// every frame written meanwhile. Here a read and a write each have an
// operation of their own, and Close cancels both.
type windowsPort struct {
	handle  windows.Handle
	path    string
	reading sync.Mutex
	writing sync.Mutex
	readEv  windows.Handle
	writeEv windows.Handle
	closed  atomic.Bool
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
	p := &windowsPort{handle: handle, path: path}
	if err := p.setUp(baud); err != nil {
		p.release()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

func (p *windowsPort) setUp(baud int) error {
	var dcb windows.DCB
	dcb.DCBlength = uint32(unsafe.Sizeof(dcb))
	if err := windows.GetCommState(p.handle, &dcb); err != nil {
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
	if err := windows.SetCommState(p.handle, &dcb); err != nil {
		return err
	}
	// Return as soon as anything has arrived, and otherwise wait for it --
	// "forever" being the largest timeout Windows takes short of none.
	if err := windows.SetCommTimeouts(p.handle, &windows.CommTimeouts{
		ReadIntervalTimeout:        maxDword,
		ReadTotalTimeoutMultiplier: maxDword,
		ReadTotalTimeoutConstant:   maxDword - 1,
		WriteTotalTimeoutConstant:  writeTimeout,
	}); err != nil {
		return err
	}
	var err error
	if p.readEv, err = windows.CreateEvent(nil, 1, 0, nil); err != nil {
		return err
	}
	p.writeEv, err = windows.CreateEvent(nil, 1, 0, nil)
	return err
}

// io runs one overlapped operation to completion.
func (p *windowsPort) io(event windows.Handle, op func(*uint32, *windows.Overlapped) error) (int, error) {
	if p.closed.Load() {
		return 0, ErrClosed
	}
	var done uint32
	o := windows.Overlapped{HEvent: event}
	err := op(&done, &o)
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		err = windows.GetOverlappedResult(p.handle, &o, &done, true)
	}
	if p.closed.Load() {
		return int(done), ErrClosed
	}
	if err != nil {
		return int(done), fmt.Errorf("%s: %w", p.path, err)
	}
	return int(done), nil
}

func (p *windowsPort) Read(buf []byte) (int, error) {
	p.reading.Lock()
	defer p.reading.Unlock()
	for {
		n, err := p.io(p.readEv, func(done *uint32, o *windows.Overlapped) error {
			return windows.ReadFile(p.handle, buf, done, o)
		})
		if err != nil || n > 0 {
			return n, err
		}
	}
}

func (p *windowsPort) Write(buf []byte) (int, error) {
	p.writing.Lock()
	defer p.writing.Unlock()
	written := 0
	for written < len(buf) {
		n, err := p.io(p.writeEv, func(done *uint32, o *windows.Overlapped) error {
			return windows.WriteFile(p.handle, buf[written:], done, o)
		})
		written += n
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, fmt.Errorf("%s: write timed out", p.path)
		}
	}
	return written, nil
}

func (p *windowsPort) Flush() {
	if !p.closed.Load() {
		windows.PurgeComm(p.handle, purgeRxClear)
	}
}

// Close cancels a read or write in flight, waits for it to give up, and
// lets the port go.
func (p *windowsPort) Close() error {
	if p.closed.Swap(true) {
		return nil
	}
	windows.CancelIoEx(p.handle, nil)
	p.reading.Lock()
	p.writing.Lock()
	defer p.reading.Unlock()
	defer p.writing.Unlock()
	return p.release()
}

func (p *windowsPort) release() error {
	if p.readEv != 0 {
		windows.CloseHandle(p.readEv)
	}
	if p.writeEv != 0 {
		windows.CloseHandle(p.writeEv)
	}
	return windows.CloseHandle(p.handle)
}
