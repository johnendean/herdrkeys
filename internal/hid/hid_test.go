package hid

import (
	"os"
	"testing"
)

// Opt-in: a Soomfon CN001 attached to this machine.
func TestALiveCN001IsFoundAndOpens(t *testing.T) {
	if os.Getenv("HERDRKEYS_LIVE_CN001") == "" {
		t.Skip("set HERDRKEYS_LIVE_CN001=1 with a CN001 attached")
	}
	found, err := Find(0x1500, 0x3003, 0xFFA0)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) == 0 {
		t.Fatal("no CN001")
	}
	t.Logf("found %+v", found[0])
	d, err := Open(found[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
}
