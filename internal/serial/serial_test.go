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
//
// The read starts first and the writes follow, because that is how the
// daemon uses a port: a read always waiting for a key press, and frames
// written past it. A port that serialised the two hung on the first write.
func TestALiveKeybowAnswersWhileAReadIsWaiting(t *testing.T) {
	path := os.Getenv("HERDRKEYS_KEYBOW_PORT")
	if path == "" {
		t.Skip("set HERDRKEYS_KEYBOW_PORT to a Keybow's data port")
	}
	port, err := Open(path, 115200)
	if err != nil {
		t.Fatal(err)
	}
	port.Flush()

	lines := make(chan []byte, 16)
	failed := make(chan error, 1)
	go func() {
		var pending []byte
		buf := make([]byte, 256)
		for {
			n, err := port.Read(buf)
			if err != nil {
				failed <- err
				return
			}
			pending = append(pending, buf[:n]...)
			for {
				i := bytes.IndexByte(pending, '\n')
				if i < 0 {
					break
				}
				lines <- bytes.TrimSpace(pending[:i])
				pending = pending[i+1:]
			}
		}
	}()
	time.Sleep(200 * time.Millisecond) // the read is now waiting

	for i := 0; i < 3; i++ {
		start := time.Now()
		if _, err := port.Write([]byte(`{"v":3,"t":"hello"}` + "\n")); err != nil {
			t.Fatal(err)
		}
		if took := time.Since(start); took > time.Second {
			t.Fatalf("a write took %v behind a waiting read", took)
		}
		select {
		case reply := <-lines:
			if !bytes.Contains(reply, []byte(`"hello"`)) {
				t.Fatalf("answered %s", reply)
			}
			if i == 0 {
				t.Logf("%s answered: %s", path, reply)
			}
		case err := <-failed:
			t.Fatal(err)
		case <-time.After(3 * time.Second):
			t.Fatal("no hello from the keypad")
		}
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
