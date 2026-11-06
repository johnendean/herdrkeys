//go:build windows

package herdr

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

var (
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	procCreateNamedPipe = kernel32.NewProc("CreateNamedPipeW")
	procConnectPipe     = kernel32.NewProc("ConnectNamedPipe")
)

const (
	pipeAccessDuplex       = 0x00000003
	pipeTypeByte           = 0x00000000
	pipeUnlimitedInstances = 255
	errorPipeConnected     = syscall.Errno(535)
)

var pipeCounter atomic.Int64

// listen serves a named pipe the way Herdr does on Windows -- named after a
// socket path that is never a socket -- and returns that path.
func listen(t *testing.T, serve func(io.ReadWriteCloser)) string {
	t.Helper()
	path := fmt.Sprintf(`C:\herdrkeys-test\%d-%d\herdr.sock`, os.Getpid(), pipeCounter.Add(1))
	name, _ := syscall.UTF16PtrFromString(Endpoint(path))

	var stopped atomic.Bool
	// The first instance is made before returning, so a client that dials at
	// once finds the pipe there.
	next := func() (syscall.Handle, error) {
		h, _, err := procCreateNamedPipe.Call(
			uintptr(unsafe.Pointer(name)), pipeAccessDuplex, pipeTypeByte,
			pipeUnlimitedInstances, 65536, 65536, 0, 0)
		if syscall.Handle(h) == syscall.InvalidHandle {
			return 0, err
		}
		return syscall.Handle(h), nil
	}
	first, err := next()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		handle := first
		for {
			ok, _, err := procConnectPipe.Call(uintptr(handle), 0)
			if ok == 0 && err != errorPipeConnected {
				syscall.CloseHandle(handle)
				return
			}
			if stopped.Load() {
				syscall.CloseHandle(handle)
				return
			}
			go serve(os.NewFile(uintptr(handle), path))
			if handle, err = next(); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		// Wake the pending ConnectNamedPipe so the loop sees it is done.
		stopped.Store(true)
		if c, err := dialPlatform(path, time.Second); err == nil {
			c.Close()
		}
	})
	return path
}

func missingPath(t *testing.T) string {
	return filepath.Join(t.TempDir(), "nobody.sock")
}

func samePath(got, base string, parts ...string) bool {
	return got == filepath.Join(append([]string{filepath.Dir(base)}, append(parts, filepath.Base(base))...)...)
}
