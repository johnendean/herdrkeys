package cn001

// The strip.
//
// Three displays right of the keys, top to bottom: the 5-hour session, the
// weekly limit, and how long until you can work again (ADR 0019). They take a
// key's 96 pixels and its quarter turn, but crop a few pixels at the edges, so
// nothing is drawn within stripMargin of one. A saturated colour means what
// here too: a gauge is light grey, amber when nearly spent, red once it is,
// and the countdown is red while you are stopped. Nothing on the strip moves.

import (
	"fmt"
	"math"
	"time"

	"github.com/johnendean/herdrkeys/internal/usage"
)

// StripKeys are the strip's displays, top to bottom.
var StripKeys = [3]int{16, 17, 18}

const (
	stripMargin = 8
	amberFrom   = 80 // percent

	gaugeLeft, gaugeRight, gaugeTop, gaugeBottom = 12, 84, 32, 86 // [left, right) x [top, bottom)
	fontHeight                                   = 7
)

var (
	gaugeColour = RGB{210, 210, 210}
	labelColour = RGB{150, 150, 150}
	track       = RGB{44, 44, 44}
	staleTrack  = RGB{28, 28, 28}
	staleLabel  = RGB{70, 70, 70}
)

// Panel is everything that decides one strip display's pixels. Comparable.
type Panel struct {
	Label, Text string
	Colour      RGB
	Fill        float64 // a gauge's share filled, 0-1
	Gauge       bool    // whether it is a gauge at all
	Stale       bool
}

func gauge(label string, limit *usage.Limit, stale bool) Panel {
	percent := 0.0
	if limit != nil {
		percent = limit.Percent
	}
	colour := gaugeColour
	switch {
	case stale:
		colour = muted
	case percent >= 100:
		colour = markColour['b']
	case percent >= amberFrom:
		colour = markColour['w']
	}
	// Rounded down: 99.6% is not yet spent, and must not read as if it were.
	shown := max(0, min(100, int(math.Floor(percent))))
	return Panel{label, fmt.Sprintf("%d%%", shown), colour, max(0, min(1, percent/100)), true, stale}
}

// Countdown is a wait, to the minute, rounded up: "42M", "3:07", "2D5H".
func Countdown(seconds float64) string {
	minutes := max(0, int(math.Ceil(seconds/60)))
	if minutes < 60 {
		return fmt.Sprintf("%dM", minutes)
	}
	hours, minutes := minutes/60, minutes%60
	if hours < 24 {
		return fmt.Sprintf("%d:%02d", hours, minutes)
	}
	return fmt.Sprintf("%dD%dH", hours/24, hours%24)
}

// StripPanels is what each strip display shows at wall; nil while usage has
// never been known.
func StripPanels(reading *usage.Usage, wall time.Time, stale bool) map[int]*Panel {
	out := map[int]*Panel{StripKeys[0]: nil, StripKeys[1]: nil, StripKeys[2]: nil}
	if reading == nil {
		return out
	}
	now := reading.At(wall)
	free := reading.FreeAt(wall)
	colour := gaugeColour
	if stale {
		colour = muted
	} else if reading.Limited(wall) {
		colour = markColour['b']
	}
	session, week := gauge("5H", now.Session, stale), gauge("WEEK", now.Weekly, stale)
	wait := "-"
	if !free.IsZero() {
		wait = Countdown(free.Sub(wall).Seconds())
	}
	reset := Panel{Label: "RESET", Text: wait, Colour: colour, Stale: stale}
	out[StripKeys[0]], out[StripKeys[1]], out[StripKeys[2]] = &session, &week, &reset
	return out
}

// font is a 5x7 font: just the characters the strip says.
var font = map[rune][]string{
	'0': {"01110", "10001", "10011", "10101", "11001", "10001", "01110"},
	'1': {"00100", "01100", "00100", "00100", "00100", "00100", "01110"},
	'2': {"01110", "10001", "00001", "00010", "00100", "01000", "11111"},
	'3': {"11110", "00001", "00001", "01110", "00001", "00001", "11110"},
	'4': {"00010", "00110", "01010", "10010", "11111", "00010", "00010"},
	'5': {"11111", "10000", "11110", "00001", "00001", "10001", "01110"},
	'6': {"00110", "01000", "10000", "11110", "10001", "10001", "01110"},
	'7': {"11111", "00001", "00010", "00100", "01000", "01000", "01000"},
	'8': {"01110", "10001", "10001", "01110", "10001", "10001", "01110"},
	'9': {"01110", "10001", "10001", "01111", "00001", "00010", "01100"},
	'%': {"11001", "11010", "00010", "00100", "01000", "01011", "10011"},
	':': {"0", "1", "1", "0", "1", "1", "0"},
	'-': {"000", "000", "000", "111", "000", "000", "000"},
	'D': {"11110", "10001", "10001", "10001", "10001", "10001", "11110"},
	'E': {"11111", "10000", "10000", "11110", "10000", "10000", "11111"},
	'H': {"10001", "10001", "10001", "11111", "10001", "10001", "10001"},
	'K': {"10001", "10010", "10100", "11000", "10100", "10010", "10001"},
	'M': {"10001", "11011", "10101", "10101", "10001", "10001", "10001"},
	'R': {"11110", "10001", "10001", "11110", "10100", "10010", "10001"},
	'S': {"01111", "10000", "10000", "01110", "00001", "00001", "11110"},
	'T': {"11111", "00100", "00100", "00100", "00100", "00100", "00100"},
	'W': {"10001", "10001", "10001", "10101", "10101", "10101", "01010"},
}

// TextWidth is how wide text is drawn at scale.
func TextWidth(text string, scale int) int {
	width, count := 0, 0
	for _, c := range text {
		width += len(font[c][0]) * scale
		count++
	}
	return width + scale*max(0, count-1)
}

// textPixels calls each with every pixel text covers, drawn at scale from
// (left, top).
func textPixels(text string, scale, left, top int, each func(x, y int)) {
	x := left
	for _, c := range text {
		glyph := font[c]
		for row, line := range glyph {
			for column, bit := range line {
				if bit != '1' {
					continue
				}
				for dy := 0; dy < scale; dy++ {
					for dx := 0; dx < scale; dx++ {
						each(x+column*scale+dx, top+row*scale+dy)
					}
				}
			}
		}
		x += (len(glyph[0]) + 1) * scale
	}
}

func centred(text string, scale int) int { return (KeyPixels - TextWidth(text, scale)) / 2 }

// ShownPanel is a strip display as it should appear.
func ShownPanel(panel Panel) *Pixels {
	var p Pixels
	set := func(x, y int, c RGB) {
		if 0 <= x && x < KeyPixels && 0 <= y && y < KeyPixels {
			p[y][x] = c
		}
	}
	label := labelColour
	if panel.Stale {
		label = staleLabel
	}
	textPixels(panel.Label, 2, centred(panel.Label, 2), stripMargin+2, func(x, y int) { set(x, y, label) })
	middle := (gaugeTop + gaugeBottom) / 2
	if !panel.Gauge {
		widest := KeyPixels - 2*stripMargin
		scale := 2
		for _, s := range []int{4, 3} {
			if TextWidth(panel.Text, s) <= widest {
				scale = s
				break
			}
		}
		top := middle - fontHeight*scale/2
		textPixels(panel.Text, scale, centred(panel.Text, scale), top, func(x, y int) { set(x, y, panel.Colour) })
		return &p
	}
	filledFrom := gaugeBottom - int(math.Round(panel.Fill*(gaugeBottom-gaugeTop)))
	behind := track
	if panel.Stale {
		behind = staleTrack
	}
	for y := gaugeTop; y < gaugeBottom; y++ {
		for x := gaugeLeft; x < gaugeRight; x++ {
			if y >= filledFrom {
				p[y][x] = panel.Colour
			} else {
				p[y][x] = behind
			}
		}
	}
	// The number reads over the gauge: dark where the fill is, light above.
	top := middle - fontHeight*3/2
	textPixels(panel.Text, 3, centred(panel.Text, 3), top, func(x, y int) {
		if y >= filledFrom && gaugeLeft <= x && x < gaugeRight {
			set(x, y, badge)
		} else {
			set(x, y, panel.Colour)
		}
	})
	return &p
}

// RenderPanel is a strip display's JPEG, turned for the panel like a key's.
func RenderPanel(panel Panel) ([]byte, error) { return Encode(Turned(ShownPanel(panel))) }
