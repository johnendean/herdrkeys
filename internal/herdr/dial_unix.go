//go:build !windows

package herdr

import (
	"net"
	"time"
)

func dialPlatform(path string, timeout time.Duration) (conn, error) {
	return net.DialTimeout("unix", path, timeout)
}

// Endpoint is what dialling path actually opens, for doctor to name.
func Endpoint(path string) string { return path }
