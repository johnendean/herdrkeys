//go:build darwin

package tui

import (
	"os"

	"golang.org/x/sys/unix"
)

// rawMode puts the terminal into cbreak: a key at a time, no echo, signals
// still delivered so ctrl-c stops the daemon.
func rawMode() (func(), error) {
	fd := int(os.Stdin.Fd())
	saved, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return nil, err
	}
	raw := *saved
	raw.Lflag &^= unix.ICANON | unix.ECHO
	raw.Cc[unix.VMIN], raw.Cc[unix.VTIME] = 1, 0
	if err := unix.IoctlSetTermios(fd, unix.TIOCSETA, &raw); err != nil {
		return nil, err
	}
	return func() { unix.IoctlSetTermios(fd, unix.TIOCSETA, saved) }, nil
}
