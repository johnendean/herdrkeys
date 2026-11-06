package instance

import (
	"os"
	"os/signal"
	"strconv"

	"golang.org/x/sys/windows"
)

func lockFile(f *os.File) error {
	var o windows.Overlapped
	return windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &o)
}

func unlockFile(f *os.File) {
	var o windows.Overlapped
	windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &o)
}

// Windows has no SIGTERM, and killing the process would leave the chord held
// and the CN001 lit. So each daemon waits on an event named after its pid,
// and Stop sets it.
func eventName(pid int) string { return `Local\herdrkeys-stop-` + strconv.Itoa(pid) }

func requestStop(pid int) error {
	name, _ := windows.UTF16PtrFromString(eventName(pid))
	h, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.SetEvent(h)
}

func stopRequested() <-chan struct{} {
	done := make(chan struct{})
	name, _ := windows.UTF16PtrFromString(eventName(os.Getpid()))
	h, err := windows.CreateEvent(nil, 1, 0, name)
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	fired := make(chan struct{}, 2)
	if err == nil {
		go func() {
			windows.WaitForSingleObject(h, windows.INFINITE)
			fired <- struct{}{}
		}()
	}
	go func() {
		<-interrupts
		fired <- struct{}{}
	}()
	go func() {
		<-fired
		close(done)
	}()
	return done
}
