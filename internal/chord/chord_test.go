package chord

import (
	"slices"
	"testing"
)

func TestEachPlatformHasTheChordADR0017Names(t *testing.T) {
	if got := DefaultFor("darwin").Names(); !slices.Equal(got, []string{"RIGHT_ALT"}) {
		t.Fatalf("macOS = %v: Right Option, as it has always been", got)
	}
	// Right Alt is AltGr on many layouts, and Flow rejects a bare Ctrl.
	if got := DefaultFor("windows").Names(); !slices.Equal(got, []string{"LEFT_CONTROL", "LEFT_GUI"}) {
		t.Fatalf("Windows = %v", got)
	}
}

func TestAChordIsWrittenTheWayAPersonWouldWriteIt(t *testing.T) {
	for text, want := range map[string][]string{
		"ctrl+win":            {"LEFT_CONTROL", "LEFT_GUI"},
		" Right_Option ":      {"RIGHT_ALT"},
		"cmd + shift + f13":   {"LEFT_GUI", "LEFT_SHIFT", "F13"},
		"RIGHT_CONTROL+altgr": {"RIGHT_CONTROL", "RIGHT_ALT"},
	} {
		got, err := Parse(text)
		if err != nil {
			t.Fatalf("%q: %v", text, err)
		}
		if !slices.Equal(got.Names(), want) {
			t.Fatalf("%q = %v, want %v", text, got.Names(), want)
		}
	}
}

func TestAnEmptyChordIsTheDefault(t *testing.T) {
	got, err := Parse("")
	if err != nil || !slices.Equal(got.Names(), Default().Names()) {
		t.Fatalf("%v, %v", got, err)
	}
}

func TestAChordOfOrdinaryKeysIsRefused(t *testing.T) {
	// It would type into whatever has focus for as long as dictation runs.
	for _, text := range []string{"a", "ctrl+space", "ctrl+", "ctrl+ctrl"} {
		if _, err := Parse(text); err == nil {
			t.Fatalf("%q was accepted", text)
		}
	}
}

func TestAChordReadsBackAsItWasWritten(t *testing.T) {
	c, _ := Parse("ctrl+win")
	if c.String() != "left_control+left_gui" {
		t.Fatalf("%q", c.String())
	}
	if again, _ := Parse(c.String()); !slices.Equal(again.Names(), c.Names()) {
		t.Fatal("does not round-trip")
	}
}
