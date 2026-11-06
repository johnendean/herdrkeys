package host

import (
	"os"
	"testing"
	"unsafe"
)

func TestAnInputIsTheSizeWindowsExpects(t *testing.T) {
	// SendInput is told the size of each INPUT and refuses any other: 40
	// bytes on 64-bit Windows.
	if size := unsafe.Sizeof(keybdInput{}); size != 40 {
		t.Fatalf("INPUT is %d bytes", size)
	}
}

func TestTheWindowHostingHerdrIsFound(t *testing.T) {
	// Opt-in: with a Herdr client running in a terminal on this machine.
	if os.Getenv("HERDRKEYS_LIVE") == "" {
		t.Skip("set HERDRKEYS_LIVE=1 with Herdr running")
	}
	r := &Raiser{}
	what := r.Describe()
	if what == "" {
		t.Fatalf("no window found for clients %v (server %d)", herdrClients(), serverPID())
	}
	t.Logf("would raise %s", what)
}
