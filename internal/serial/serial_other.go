//go:build !darwin && !windows

package serial

import (
	"fmt"
	"runtime"
)

// Linux is not a platform herdrkeys claims (ADR 0020): its boards need udev
// rules before an ordinary user can open them.
func open(path string, baud int) (Port, error) {
	return nil, fmt.Errorf("%s: serial ports are not supported on %s", path, runtime.GOOS)
}
