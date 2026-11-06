package model

import (
	"os"
	"regexp"
	"testing"
)

// The firmware is CircuitPython and the host is Go, so nothing makes them
// agree on a frame's characters but this: it reads what device/code.py
// declares, and holds it to what the host sends.
func TestTheFirmwareSpeaksTheHostsCodes(t *testing.T) {
	source, err := os.ReadFile("../../device/code.py")
	if err != nil {
		t.Fatal(err)
	}
	declared := func(name string) string {
		m := regexp.MustCompile(`(?m)^` + name + ` = (.+?)\s*(#.*)?$`).FindSubmatch(source)
		if m == nil {
			t.Fatalf("device/code.py does not declare %s", name)
		}
		return string(m[1])
	}
	for name, want := range map[string]string{
		"MIC_CODES":        `("` + string(CodeMic) + `", "` + string(CodeMicOpen) + `", "` + string(CodeMicLatched) + `")`,
		"RETURN_CODE":      `"` + string(CodeReturn) + `"`,
		"GIT_CODE":         `"` + string(CodeGitPage) + `"`,
		"GIT_NONE_CODE":    `"` + string(CodeGitNoPage) + `"`,
		"GIT_LOOKING_CODE": `"` + string(CodeGitLooking) + `"`,
	} {
		if got := declared(name); got != want {
			t.Errorf("device/code.py has %s = %s; the host sends %s", name, got, want)
		}
	}
}

func TestEveryBlankFrameCarriesTheFeatureRow(t *testing.T) {
	f := Blank(true, false)
	if got := f.KeyString(); got != "------------menf" {
		t.Fatalf("%q", got)
	}
	if Blank(false, true).KeyString() != "------------merx" {
		t.Fatal("disconnected, with a page")
	}
}

func TestFocusIsUpperCase(t *testing.T) {
	if StateCode(Working, true) != 'W' || StateCode(Working, false) != 'w' || StateCode("nonsense", false) != 'u' {
		t.Fatal("codes")
	}
	if ParseState("thinking") != Unknown || ParseState("blocked") != Blocked {
		t.Fatal("a state Herdr cannot classify is unknown")
	}
}
