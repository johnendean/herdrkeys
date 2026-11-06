package serial

import (
	"fmt"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/unix"
)

// darwinPort reads with VTIME, not through kqueue.
//
// A daemon that had outlived a week of unplugs was found still writing
// frames while kqueue had quietly stopped reporting its descriptor readable,
// so every press went unread (ADR 0007). Here the descriptor is blocking with
// a tenth-of-a-second read timeout, so a read returns at least that often and
// nothing waits on readiness at all.
type darwinPort struct {
	fd      int
	path    string
	closed  atomic.Bool
	reading sync.Mutex // held for the length of each read, so Close never races one
	writing sync.Mutex
}

// fread is FREAD from <sys/fcntl.h>: TIOCFLUSH with it drops the input queue.
const fread = 0x0001

var speeds = map[int]uint64{
	9600: unix.B9600, 19200: unix.B19200, 38400: unix.B38400,
	57600: unix.B57600, 115200: unix.B115200,
}

func open(path string, baud int) (Port, error) {
	speed, ok := speeds[baud]
	if !ok {
		return nil, fmt.Errorf("unsupported baud rate %d", baud)
	}
	// Opened non-blocking, so that a port waiting for carrier cannot hang the
	// open, then made blocking once CLOCAL says carrier does not matter.
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := configure(fd, speed); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &darwinPort{fd: fd, path: path}, nil
}

func configure(fd int, speed uint64) error {
	t, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return err
	}
	t.Iflag = 0 // no translation, no flow control
	t.Oflag = 0 // no post-processing
	t.Cflag = unix.CS8 | unix.CREAD | unix.CLOCAL
	t.Lflag = 0 // no canonical mode, no echo, no signals
	t.Ispeed, t.Ospeed = speed, speed
	t.Cc[unix.VMIN] = 0
	t.Cc[unix.VTIME] = 1 // tenths of a second
	if err := unix.IoctlSetTermios(fd, unix.TIOCSETA, t); err != nil {
		return err
	}
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		return err
	}
	_, err = unix.FcntlInt(uintptr(fd), unix.F_SETFL, flags&^unix.O_NONBLOCK)
	return err
}

func (p *darwinPort) Read(buf []byte) (int, error) {
	for {
		p.reading.Lock()
		if p.closed.Load() {
			p.reading.Unlock()
			return 0, ErrClosed
		}
		n, err := unix.Read(p.fd, buf)
		p.reading.Unlock()
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("%s: %w", p.path, err)
		}
		if n > 0 {
			return n, nil
		}
		// VTIME expired with nothing waiting: go round, checking for Close.
	}
}

func (p *darwinPort) Write(buf []byte) (int, error) {
	p.writing.Lock()
	defer p.writing.Unlock()
	written := 0
	for written < len(buf) {
		if p.closed.Load() {
			return written, ErrClosed
		}
		n, err := unix.Write(p.fd, buf[written:])
		if err == unix.EINTR || err == unix.EAGAIN {
			continue
		}
		if err != nil {
			return written, fmt.Errorf("%s: %w", p.path, err)
		}
		written += n
	}
	return written, nil
}

func (p *darwinPort) Flush() {
	if !p.closed.Load() {
		unix.IoctlSetPointerInt(p.fd, unix.TIOCFLUSH, fread) // tcflush(TCIFLUSH)
	}
}

func (p *darwinPort) Close() error {
	if p.closed.Swap(true) {
		return nil
	}
	// Wait out a read in flight -- at most one VTIME -- so the descriptor is
	// never closed, and its number reused, under a read.
	p.reading.Lock()
	p.writing.Lock()
	defer p.reading.Unlock()
	defer p.writing.Unlock()
	return unix.Close(p.fd)
}
