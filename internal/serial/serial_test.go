package serial

import (
	"bytes"
	"os"
	"testing"
	"time"
)

func TestAPortThatIsNotThereFailsToOpen(t *testing.T) {
	if _, err := Open("/dev/cu.herdrkeys-nobody", 115200); err == nil {
		t.Fatal("opened a port that does not exist")
	}
}

// Opt-in, against a Keybow running herdrkeys firmware: HERDRKEYS_KEYBOW_PORT
// names its data port (COM4, /dev/cu.usbmodem…). Nothing else can stand in for
// a real CDC port, and DTR, timeouts and cancellation are exactly what
// differ between the platforms.
func TestALiveKeybowAnswersAndClosingUnblocksARead(t *testing.T) {
	path := os.Getenv("HERDRKEYS_KEYBOW_PORT")
	if path == "" {
		t.Skip("set HERDRKEYS_KEYBOW_PORT to a Keybow's data port")
	}
	port, err := Open(path, 115200)
	if err != nil {
		t.Fatal(err)
	}
	port.Flush()
	if _, err := port.Write([]byte(`{"v":2,"t":"hello"}` + "\n")); err != nil {
		t.Fatal(err)
	}

	got := make(chan []byte, 1)
	failed := make(chan error, 1)
	go func() {
		var all []byte
		buf := make([]byte, 256)
		for {
			n, err := port.Read(buf)
			if err != nil {
				failed <- err
				return
			}
			all = append(all, buf[:n]...)
			if bytes.Contains(all, []byte(`"hello"`)) && bytes.Contains(all, []byte("\n")) {
				got <- all
				// Keep reading: this read is the one Close must unblock.
			}
		}
	}()
	select {
	case reply := <-got:
		t.Logf("%s answered: %s", path, bytes.TrimSpace(reply))
	case err := <-failed:
		t.Fatal(err)
	case <-time.After(3 * time.Second):
		t.Fatal("no hello from the keypad")
	}

	start := time.Now()
	port.Close()
	select {
	case err := <-failed:
		if err != ErrClosed {
			t.Fatalf("a closed port said %v", err)
		}
		t.Logf("close unblocked the read in %v", time.Since(start))
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not unblock Read")
	}
}
