package tui

import (
	"strings"
	"testing"

	"github.com/johnendean/herdrkeys/internal/model"
)

func frame(keys string, colours ...string) model.Frame {
	var f model.Frame
	copy(f.Keys[:], keys)
	copy(f.Colours[:], colours)
	return f
}

func TestTheGridIsDrawnKeyZeroBottomLeft(t *testing.T) {
	lines := strings.Split(Draw(frame("wB----------menf")), "\n")
	if len(lines) != 4 {
		t.Fatalf("%d rows", len(lines))
	}
	if !strings.Contains(lines[3], "[ 0 w working ]") || !strings.Contains(lines[3], "[ 1 B blocked*]") {
		t.Fatalf("bottom row: %q", lines[3])
	}
	if !strings.Contains(lines[0], "[15 f fn") || !strings.Contains(lines[0], "[12 m mic") {
		t.Fatalf("top row: %q", lines[0])
	}
}

func TestIdleAndDoneWearTheirProjectColour(t *testing.T) {
	drawn := Draw(frame("iw----------menf", "#89b4fa", "#a6e3a1"))
	if !strings.Contains(drawn, "\x1b[38;2;137;180;250m[ 0 i") {
		t.Fatal("idle wears its project's colour")
	}
	if strings.Contains(drawn, "\x1b[38;2;166;227;161m") {
		t.Fatal("working does not: it is amber, whoever it is")
	}
}
