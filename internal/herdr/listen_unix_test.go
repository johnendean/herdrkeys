//go:build !windows

package herdr

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// listen serves a Unix socket and returns its path. The directory is made
// under /tmp rather than t.TempDir: a socket path is capped near 104 bytes and
// macOS's per-test temp paths overrun it.
func listen(t *testing.T, serve func(io.ReadWriteCloser)) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "hk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "herdr.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			go serve(c)
		}
	}()
	return path
}

func missingPath(t *testing.T) string {
	return filepath.Join(t.TempDir(), "nobody.sock")
}

func samePath(got, base string, parts ...string) bool {
	return got == filepath.Join(append([]string{filepath.Dir(base)}, append(parts, filepath.Base(base))...)...) ||
		strings.HasSuffix(got, filepath.Join(append(parts, "herdr.sock")...))
}
