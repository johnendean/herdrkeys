package cn001

import (
	"os"
	"testing"
	"time"

	"github.com/johnendean/herdrkeys/internal/model"
	"github.com/johnendean/herdrkeys/internal/usage"
)

// Opt-in: draws on a CN001 attached to this machine, then blanks it.
func TestALiveCN001DrawsAFrame(t *testing.T) {
	if os.Getenv("HERDRKEYS_LIVE_CN001") == "" {
		t.Skip("set HERDRKEYS_LIVE_CN001=1 with a CN001 attached")
	}
	d, err := Open(80, func() bool { return true })
	if err != nil || d == nil {
		t.Fatalf("%v %v", d, err)
	}
	defer d.Close()
	var f model.Frame
	copy(f.Keys[:], "iWbdu---------mer"[:12]+"menf")
	f.Colours[0], f.Colours[1] = "#89b4fa", "#a6e3a1"
	f.Forge = "github"
	f.Keys[model.GitSlot] = model.CodeGitPage
	session := usage.Limit{Percent: 42, ResetsAt: time.Now().Add(95 * time.Minute)}
	weekly := usage.Limit{Percent: 85}
	d.ShowUsage(&usage.Usage{Session: &session, Weekly: &weekly}, false)
	start := time.Now()
	if err := d.SendFrame(f); err != nil {
		t.Fatal(err)
	}
	t.Logf("drew a full panel in %v", time.Since(start))
	for i := 0; i < 20; i++ {
		if err := d.Animate(0); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if hold := os.Getenv("HERDRKEYS_LIVE_HOLD"); hold != "" {
		wait, _ := time.ParseDuration(hold)
		time.Sleep(wait)
	}
}
