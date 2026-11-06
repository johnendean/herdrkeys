package cn001

// What each meaning looks like.
//
// The same rules as device/code.py where an LED and an LCD can share them,
// and not where they cannot (ADRs 0014 and 0016). Motion means this agent
// wants you: `done` and `blocked` blink to black, at the same rate, and
// nothing else on the grid moves.
//
// What differs is that this board can draw. On the grid, colour only ever
// says whose: every key is its project's colour, as the sidebar shows it.
// What the agent is doing is its mark, drawn on a dark badge in a strong
// colour of its own -- so a pastel always means whose, a saturated colour
// always means what, and a red project's `done` never matches `blocked`.
// Focus is a border, which leaves brightness meaning nothing at all.

import (
	"math"
	"strconv"

	"github.com/johnendean/herdrkeys/internal/model"
)

// RGB is a colour.
type RGB [3]uint8

// Look is everything that decides a key's pixels, and so what its image is
// cached by. Comparable.
type Look struct {
	Background RGB
	Mark       byte // the state code whose mark it wears; 0 for none
	Focused    bool
	Muted      bool   // its mark greyed: a key this board cannot act on
	Logo       string // the forge whose logo it wears: the git key only
}

var (
	off  = RGB{0, 0, 0}
	Dark = Look{Background: off} // a key with nothing on it is cleared, not drawn

	neutral = RGB{108, 108, 108} // behind a mark whose agent has no project colour
	badge   = RGB{22, 22, 22}
	border  = RGB{240, 240, 240} // focus

	markColour = map[byte]RGB{
		'w': {240, 140, 0},  // working   amber
		'b': {224, 0, 0},    // blocked   red
		'd': {0, 200, 0},    // done      green
		'u': {40, 100, 255}, // unknown   blue
	}

	fnIdle     = RGB{90, 104, 90}   // function key, daemon connected
	fnOffline  = RGB{200, 0, 0}     // daemon cannot see Herdr; pulses
	flash      = RGB{230, 230, 230} // "heard you"
	gitPage    = RGB{108, 0, 168}   // the focused agent's directory has a page
	gitNoPage  = RGB{20, 0, 32}     // ...and this one does not
	gitLooking = RGB{170, 0, 255}   // a press is finding out where to go

	// A page on a known forge wears that forge's logo in white on its brand
	// colour instead of the violet (ADR 0018). The feature row was never
	// bound by "a saturated colour means what": GitLab's orange is not
	// `working` here.
	forgeColour = map[string]RGB{
		"github":    {0x18, 0x17, 0x17},
		"gitlab":    {0xFC, 0x6D, 0x26},
		"bitbucket": {0x00, 0x52, 0xCC},
		"codeberg":  {0x21, 0x85, 0xD0},
	}
	logoColour = RGB{255, 255, 255}

	// The microphone and return keys (ADR 0017), in the Keybow's cyan: the
	// one hue no agent state uses. Each draws its symbol, and the key behind
	// it says how the one microphone is -- dim closed, bright open, breathing
	// latched. Return is the dimmer of the two, so the microphone stays the
	// key the eye finds first.
	micIdle    = RGB{0, 52, 62}
	micOpen    = RGB{0, 150, 180}
	returnIdle = RGB{0, 30, 36}
	symbol     = RGB{0, 210, 240} // the symbols themselves
	muted      = RGB{96, 96, 96}  // ...greyed, when this board cannot type
)

const (
	micPulseSeconds  = 0.9
	blinkHz          = 1.4
	pulseSeconds     = 2.0
	pulseStepSeconds = 0.125 // the panel is redrawn, not dimmed, so a pulse is stepped
	FlashSeconds     = 0.35
)

// parseHex is a `#rrggbb` colour, digit by digit: a numeric parse would also
// take a sign, and a channel of -1 would fail the encoder rather than fall
// back to neutral.
func parseHex(colour string) (RGB, bool) {
	if len(colour) != 7 || colour[0] != '#' {
		return RGB{}, false
	}
	for _, c := range colour[1:] {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F') {
			return RGB{}, false
		}
	}
	var out RGB
	for i := range out {
		v, _ := strconv.ParseUint(colour[1+2*i:3+2*i], 16, 8)
		out[i] = uint8(v)
	}
	return out, true
}

func scaled(c RGB, gain float64) RGB {
	var out RGB
	for i, v := range c {
		out[i] = uint8(max(0, min(255, math.Round(float64(v)*gain))))
	}
	return out
}

func blinkOff(now float64) bool { return int(now*blinkHz*2)%2 == 0 }

func stepped(now float64) float64 { return now - math.Mod(now, pulseStepSeconds) }

func pulse(now float64) float64 {
	phase := math.Mod(stepped(now), pulseSeconds) / pulseSeconds
	return 0.25 + 0.75*(1-math.Abs(phase*2-1))
}

func isMark(code byte) bool {
	_, ok := markColour[code]
	return ok
}

// LookFor is what a key should be showing at now.
//
// canType is whether a press on this board can type. Without it the
// microphone and return keys grey their symbols -- but still show the one
// microphone, which another board may have opened.
//
// forge is the git key's, and only matters there. One this board has no logo
// for is no forge at all.
func LookFor(code byte, colour string, now float64, flashing, canType bool, forge string) Look {
	switch code {
	case model.CodeEmpty:
		return Dark
	case model.CodeMic, model.CodeMicOpen, model.CodeMicLatched, model.CodeReturn:
		if flashing {
			return Look{Background: flash}
		}
		mark := byte('m')
		if code == model.CodeReturn {
			mark = 'e'
		}
		return Look{Background: dictationBackground(code, now), Mark: mark, Muted: !canType}
	case model.CodeFnConnected:
		if flashing {
			return Look{Background: flash}
		}
		return Look{Background: fnIdle}
	case model.CodeFnDisconnected:
		return Look{Background: scaled(fnOffline, pulse(now))}
	}
	known := ""
	if _, ok := forgeColour[forge]; ok && hasLogo(forge) {
		known = forge
	}
	switch code {
	case model.CodeGitLooking:
		// Looking is the focus border, held steady: motion means an agent
		// wants you (ADR 0012), and this is the board acknowledging you.
		if known != "" {
			return Look{Background: forgeColour[known], Logo: known, Focused: true}
		}
		return Look{Background: gitLooking}
	case model.CodeGitPage, model.CodeGitNoPage:
		switch {
		case flashing:
			return Look{Background: flash}
		case code == model.CodeGitNoPage:
			return Look{Background: gitNoPage}
		case known != "":
			return Look{Background: forgeColour[known], Logo: known}
		}
		return Look{Background: gitPage}
	}
	state := code | 0x20 // lower case
	if state != 'i' && !isMark(state) {
		return Dark // a meaning this board does not show
	}
	if (state == 'b' || state == 'd') && blinkOff(now) {
		return Dark
	}
	background, ok := parseHex(colour)
	if !ok {
		background = neutral
	}
	look := Look{Background: background, Focused: code != state}
	if isMark(state) {
		look.Mark = state
	}
	return look
}

func dictationBackground(code byte, now float64) RGB {
	switch code {
	case model.CodeReturn:
		return returnIdle
	case model.CodeMicOpen:
		return micOpen
	case model.CodeMicLatched:
		// Stepped like every pulse here: the panel is redrawn, not dimmed.
		phase := math.Mod(stepped(now), micPulseSeconds) / micPulseSeconds
		return scaled(micOpen, 0.3+0.7*(1-math.Abs(phase*2-1)))
	}
	return micIdle
}

// NextChange is how long until some key on this board would look different,
// and false if nothing ever will.
func NextChange(keys [model.SlotCount]byte, now, flashUntil float64) (float64, bool) {
	soonest, any := math.Inf(1), false
	consider := func(v float64) { soonest, any = min(soonest, v), true }
	blinks, pulses := false, false
	for _, slot := range slotsInOrder {
		switch keys[slot] | 0x20 {
		case 'b', 'd':
			blinks = true
		}
		if keys[slot] == model.CodeFnDisconnected || keys[slot] == model.CodeMicLatched {
			pulses = true
		}
	}
	if blinks {
		period := 1 / (blinkHz * 2)
		consider(period - math.Mod(now, period))
	}
	if pulses {
		consider(pulseStepSeconds - math.Mod(now, pulseStepSeconds))
	}
	if flashUntil > now {
		consider(flashUntil - now)
	}
	return soonest, any
}
