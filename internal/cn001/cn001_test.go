package cn001

import (
	"bytes"
	"errors"
	"image/jpeg"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/johnendean/herdrkeys/internal/board"
	"github.com/johnendean/herdrkeys/internal/model"
	"github.com/johnendean/herdrkeys/internal/usage"
)

// -- where each slot sits -----------------------------------------------------

func TestTheHardwareNumbersKeysDownEachColumnFromTheRight(t *testing.T) {
	if HardwareKey(0, 4) != 1 || HardwareKey(2, 4) != 3 || HardwareKey(0, 0) != 13 || HardwareKey(2, 0) != 15 {
		t.Fatal("13 10 7 4 1 / 14 11 8 5 2 / 15 12 9 6 3")
	}
}

func TestAgentsReadLeftToRightAcrossTheTopTwoRows(t *testing.T) {
	want := []int{13, 10, 7, 4, 1, 14, 11, 8, 5, 2}
	for slot, key := range want {
		if SlotToKey[slot] != key {
			t.Fatalf("slot %d is key %d, want %d", slot, SlotToKey[slot], key)
		}
	}
	if _, ok := SlotToKey[10]; ok {
		t.Fatal("the eleventh and twelfth agents are on the Keybow only")
	}
	if SlotToKey[model.MicSlot] != 15 || SlotToKey[model.ReturnSlot] != 12 ||
		SlotToKey[model.GitSlot] != 6 || SlotToKey[model.FnSlot] != 3 {
		t.Fatal("the feature row is the bottom row in the Keybow's order")
	}
	if _, ok := KeyToSlot[9]; ok {
		t.Fatal("the key between return and git holds nothing")
	}
}

// -- the protocol ---------------------------------------------------------------

func TestEveryCommandIsOneFullReport(t *testing.T) {
	for _, r := range [][]byte{Wake(), Commit(), Heartbeat(), Brightness(50), Clear(3)} {
		if len(r) != ReportSize || !bytes.HasPrefix(r, []byte("CRT\x00\x00")) {
			t.Fatalf("%q", r[:16])
		}
	}
	if Brightness(140)[10] != 100 || Brightness(-5)[10] != 0 || Brightness(80)[10] != 80 {
		t.Fatal("brightness is clamped, and sits at offset ten")
	}
}

func TestAnImageIsAHeaderNamingItsSizeAndKeyThenItsBytesInChunks(t *testing.T) {
	jpegBytes := bytes.Repeat([]byte{7}, 2500)
	reports := Image(5, jpegBytes)
	if len(reports) != 4 {
		t.Fatalf("%d reports", len(reports))
	}
	header := reports[0]
	if string(header[5:8]) != "BAT" || header[8] != 0 || header[9] != 0 || header[10] != 0x09 || header[11] != 0xC4 || header[12] != 5 {
		t.Fatalf("header % x", header[:13])
	}
	if reports[3][2500-2048-1] != 7 || reports[3][2500-2048] != 0 {
		t.Fatal("the last chunk is padded")
	}
}

func TestOnlyKeyReportsArePresses(t *testing.T) {
	report := func(key, state byte) []byte {
		return append([]byte("ACK\x00\x00OK\x00\x00"), key, state)
	}
	if key, down, ok := ParseInput(report(7, 1)); !ok || key != 7 || !down {
		t.Fatal("a press")
	}
	if key, down, ok := ParseInput(report(7, 0)); !ok || key != 7 || down {
		t.Fatal("a release")
	}
	for _, r := range [][]byte{report(0, 1), []byte("ACK\x00\x00OK"), []byte("NAK\x00\x00OK\x00\x00\x07\x01")} {
		if _, _, ok := ParseInput(r); ok {
			t.Fatalf("%q counted", r)
		}
	}
}

// -- what each meaning looks like -------------------------------------------------

const blue = "#89b4fa"

// steady is a moment when nothing blinks dark.
const steady = 1.5 / (blinkHz * 2) // the second half-period: blinks are lit

func look(code byte, colour string) Look { return LookFor(code, colour, steady, false, true, "") }

func TestEveryStateWearsItsProjectColourAndItsOwnMark(t *testing.T) {
	want := RGB{0x89, 0xb4, 0xfa}
	for _, code := range []byte("iwbdu") {
		l := look(code, blue)
		if l.Background != want {
			t.Fatalf("%c: background %v", code, l.Background)
		}
		if (code == 'i') != (l.Mark == 0) {
			t.Fatalf("%c: mark %q: each state has a mark of its own, and idle has none", code, l.Mark)
		}
	}
	if look('w', "").Background != neutral || look('w', "#-1ffff").Background != neutral {
		t.Fatal("no colour, or an impossible one, is neutral behind its mark")
	}
}

func TestFocusIsABorderNotBrightness(t *testing.T) {
	plain, focused := look('w', blue), look('W', blue)
	if !focused.Focused || plain.Focused || focused.Background != plain.Background {
		t.Fatal("focus")
	}
	p := Shown(focused)
	if p[0][0] != border || p[KeyPixels/2][2] != border || p[KeyPixels/2][borderWidth] != badge {
		t.Fatal("a light border, with a dark keyline inside it")
	}
}

func TestDoneAndBlockedBlinkToBlackTogetherAndNothingElseMoves(t *testing.T) {
	dark := 0.0 // a moment when the blink is off
	for _, code := range []byte("bd") {
		if LookFor(code, blue, dark, false, true, "") != Dark || LookFor(code, blue, steady, false, true, "") == Dark {
			t.Fatalf("%c does not blink", code)
		}
	}
	for _, code := range []byte("iwu") {
		if LookFor(code, blue, dark, false, true, "") != LookFor(code, blue, steady, false, true, "") {
			t.Fatalf("%c moved", code)
		}
	}
}

func TestARedProjectsDoneNeverLooksLikeBlocked(t *testing.T) {
	red := "#f38ba8"
	if look('d', red) == look('b', red) {
		t.Fatal("the same look")
	}
	if markColour['d'] == markColour['b'] {
		t.Fatal("the marks share a colour")
	}
}

func TestAMarkIsDrawnUprightOnTheDarkBadge(t *testing.T) {
	p := Shown(look('b', blue))
	centre := KeyPixels / 2
	if p[centre][centre-badgeRadius+3] != badge && p[centre][centre-badgeRadius+3] != markColour['b'] {
		t.Fatalf("badge %v", p[centre][centre-badgeRadius+3])
	}
	// The exclamation mark's dot is below its stroke: y points down.
	if p[centre+13][centre] != markColour['b'] || p[centre-10][centre] != markColour['b'] || p[centre+7][centre] != badge {
		t.Fatal("the mark is not upright")
	}
	if p[2][2] != look('b', blue).Background {
		t.Fatal("framed by the project colour")
	}
}

func TestTheImageIsTurnedAQuarterAnticlockwiseForThePanel(t *testing.T) {
	// Measured on the board: a corner drawn top-left appears top-right.
	var p Pixels
	p[0][0] = RGB{255, 0, 0}
	img := Turned(&p)
	if r, _, _, _ := img.At(0, KeyPixels-1).RGBA(); r>>8 != 255 {
		t.Fatal("what shows at (0, 0) is drawn at (0, 95)")
	}
}

func TestTheFeatureRow(t *testing.T) {
	if look('f', "").Background != fnIdle || LookFor('f', "", steady, true, true, "").Background != flash {
		t.Fatal("the function key")
	}
	if LookFor('x', "", 0, false, true, "") == LookFor('x', "", pulseSeconds/2, false, true, "") {
		t.Fatal("offline pulses")
	}
	mic := look(model.CodeMic, "")
	open := look(model.CodeMicOpen, "")
	ret := look(model.CodeReturn, "")
	if mic.Mark != 'm' || ret.Mark != 'e' || mic.Background == open.Background {
		t.Fatal("closed and open are told apart")
	}
	if int(ret.Background[1])+int(ret.Background[2]) >= int(mic.Background[1])+int(mic.Background[2]) {
		t.Fatal("return is the dimmer of the two")
	}
	if l := LookFor(model.CodeMic, "", steady, false, false, ""); !l.Muted || l.Mark != 'm' {
		t.Fatal("without the permission the symbol is greyed, and the microphone still shows")
	}
	latched := func(at float64) Look { return LookFor(model.CodeMicLatched, "", at, false, true, "") }
	if latched(0) == latched(micPulseSeconds/2) {
		t.Fatal("only latched moves")
	}
}

func TestTheGitKeyWearsItsForge(t *testing.T) {
	for forge := range forgeColour {
		l := LookFor(model.CodeGitPage, "", steady, false, true, forge)
		if l.Logo != forge || l.Background != forgeColour[forge] {
			t.Fatalf("%s: %+v", forge, l)
		}
		if looking := LookFor(model.CodeGitLooking, "", steady, false, true, forge); !looking.Focused || looking.Logo != forge {
			t.Fatal("looking keeps the logo and adds the border, without moving")
		}
	}
	if l := LookFor(model.CodeGitPage, "", steady, false, true, "sourcehut"); l.Background != gitPage || l.Logo != "" {
		t.Fatal("an unknown forge keeps the violet")
	}
	if LookFor(model.CodeGitNoPage, "", steady, false, true, "github").Background != gitNoPage {
		t.Fatal("no page is dim violet, whatever the frame says")
	}
	if LookFor(model.CodeGitPage, "", steady, true, true, "github").Background != flash {
		t.Fatal("a press still flashes over a logo")
	}
	p := Shown(LookFor(model.CodeGitPage, "", steady, false, true, "github"))
	if p[0][0] != forgeColour["github"] || p[KeyPixels/2][2] != forgeColour["github"] {
		t.Fatal("the logo is clear of the edges")
	}
	white := 0
	for y := range p {
		for _, c := range p[y] {
			if c == logoColour {
				white++
			}
		}
	}
	if white < 500 {
		t.Fatalf("%d white pixels: the logo is white on its brand colour", white)
	}
}

func TestTheBoardWakesOnlyWhileSomethingMoves(t *testing.T) {
	var keys [model.SlotCount]byte
	copy(keys[:], "iiw---------menf")
	if _, ok := NextChange(keys, 0, 0); ok {
		t.Fatal("nothing moves")
	}
	keys[0] = 'b'
	if d, ok := NextChange(keys, 0.1, 0); !ok || d <= 0 {
		t.Fatal("blocked blinks")
	}
	keys[0], keys[model.MicSlot] = 'i', model.CodeMicLatched
	if _, ok := NextChange(keys, 0, 0); !ok {
		t.Fatal("a latched microphone pulses")
	}
	keys[model.MicSlot] = model.CodeMic
	if d, ok := NextChange(keys, 1, 1.2); !ok || d < 0.19 || d > 0.21 {
		t.Fatal("a flash ends")
	}
	keys[10] = 'b'
	if _, ok := NextChange(keys, 0, 0); ok {
		t.Fatal("the eleventh agent is not on this board, so its blink wakes nothing")
	}
}

func TestEveryKeyEncodesAsAJPEGOfItsSize(t *testing.T) {
	for _, l := range []Look{look('w', blue), look('B', ""), look(model.CodeMic, ""), LookFor(model.CodeGitPage, "", steady, false, true, "gitlab")} {
		data, err := RenderKey(l)
		if err != nil {
			t.Fatal(err)
		}
		img, err := jpeg.Decode(bytes.NewReader(data))
		if err != nil || img.Bounds().Dx() != KeyPixels || img.Bounds().Dy() != KeyPixels {
			t.Fatalf("%v %v", img.Bounds(), err)
		}
	}
}

// -- the strip ------------------------------------------------------------------

var wall = time.Date(2026, 11, 5, 17, 0, 0, 0, time.UTC)

func limit(percent float64, resets time.Duration) *usage.Limit {
	l := &usage.Limit{Percent: percent}
	if resets != 0 {
		l.ResetsAt = wall.Add(resets)
	}
	return l
}

func TestTheStripIsDarkUntilUsageHasBeenKnown(t *testing.T) {
	for _, p := range StripPanels(nil, wall, false) {
		if p != nil {
			t.Fatal("drawn before anything was known")
		}
	}
}

func TestTopIsTheSessionMiddleTheWeekBottomTheWait(t *testing.T) {
	panels := StripPanels(&usage.Usage{Session: limit(7, 42*time.Minute), Weekly: limit(9.6, 0)}, wall, false)
	top, middle, bottom := panels[16], panels[17], panels[18]
	if top.Label != "5H" || top.Text != "7%" || middle.Label != "WEEK" || middle.Text != "9%" {
		t.Fatalf("%+v %+v", top, middle)
	}
	if bottom.Label != "RESET" || bottom.Text != "42M" || bottom.Gauge {
		t.Fatalf("%+v", bottom)
	}
}

func TestAGaugeIsNeutralThenAmberThenRedOnlyOnceSpent(t *testing.T) {
	colour := func(p float64) RGB {
		return StripPanels(&usage.Usage{Session: limit(p, time.Hour)}, wall, false)[16].Colour
	}
	if colour(50) != gaugeColour || colour(85) != markColour['w'] || colour(99.6) != markColour['w'] || colour(100) != markColour['b'] {
		t.Fatal("colours")
	}
	if StripPanels(&usage.Usage{Session: limit(99.6, time.Hour)}, wall, false)[16].Text != "99%" {
		t.Fatal("nearly spent never reads as spent")
	}
}

func TestTheCountdownTurnsRedAndFollowsWhicheverLimitStoppedYou(t *testing.T) {
	panels := StripPanels(&usage.Usage{Session: limit(40, time.Hour), Weekly: limit(100, 50*time.Hour)}, wall, false)
	if panels[18].Text != "2D2H" || panels[18].Colour != markColour['b'] {
		t.Fatalf("%+v", panels[18])
	}
}

func TestASessionWhoseResetHasPassedIsShownReset(t *testing.T) {
	panels := StripPanels(&usage.Usage{Session: limit(100, time.Minute)}, wall.Add(2*time.Minute), false)
	if panels[16].Text != "0%" || panels[18].Text != "-" {
		t.Fatalf("%+v %+v", panels[16], panels[18])
	}
}

func TestAStaleReadingIsKeptButDrawnGrey(t *testing.T) {
	panels := StripPanels(&usage.Usage{Session: limit(100, time.Hour)}, wall, true)
	if panels[16].Text != "100%" || panels[16].Colour != muted || panels[18].Colour != muted {
		t.Fatal("stale")
	}
}

func TestAWaitIsToTheMinuteRoundedUp(t *testing.T) {
	for seconds, want := range map[float64]string{0: "0M", 1: "1M", 59*60 + 1: "1:00", 3*3600 + 7*60: "3:07", 53 * 3600: "2D5H"} {
		if got := Countdown(seconds); got != want {
			t.Errorf("%v = %q, want %q", seconds, got, want)
		}
	}
}

func TestEverythingTheStripCanSayFitsInsideTheMargin(t *testing.T) {
	for _, text := range []string{"100%", "23:59", "6D23H", "RESET", "WEEK"} {
		if TextWidth(text, 2) > KeyPixels-2*stripMargin {
			t.Fatalf("%q overflows", text)
		}
	}
	p := ShownPanel(*StripPanels(&usage.Usage{Session: limit(100, 47*time.Hour), Weekly: limit(100, 0)}, wall, false)[18])
	for y := range p {
		for x := range p[y] {
			if (x < stripMargin || y < stripMargin || x >= KeyPixels-stripMargin || y >= KeyPixels-stripMargin) && p[y][x] != off {
				t.Fatalf("drawn at (%d, %d), where the strip crops", x, y)
			}
		}
	}
}

func TestAGaugeFillsUpwardsAndItsNumberIsDarkOnTheFill(t *testing.T) {
	p := ShownPanel(*StripPanels(&usage.Usage{Session: limit(50, time.Hour)}, wall, false)[16])
	if p[gaugeBottom-1][gaugeLeft] != gaugeColour || p[gaugeTop][gaugeLeft] != track {
		t.Fatal("it fills from the bottom")
	}
	full := ShownPanel(*StripPanels(&usage.Usage{Session: limit(100, time.Hour)}, wall, false)[16])
	dark := 0
	for y := gaugeTop; y < gaugeBottom; y++ {
		for x := gaugeLeft; x < gaugeRight; x++ {
			if full[y][x] == badge {
				dark++
			}
		}
	}
	if dark == 0 {
		t.Fatal("a full gauge's number is drawn dark on it, so it fits inside it")
	}
}

// -- the board ------------------------------------------------------------------

type fakeHID struct {
	mu      sync.Mutex
	written [][]byte
	fail    error
	reports chan []byte
	closed  bool
}

func newFakeHID() *fakeHID { return &fakeHID{reports: make(chan []byte, 8)} }

func (f *fakeHID) Write(r []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.written = append(f.written, r)
	return nil
}
func (f *fakeHID) Reports() <-chan []byte { return f.reports }
func (f *fakeHID) Err() error             { return errors.New("unplugged") }
func (f *fakeHID) Close() error           { f.closed = true; return nil }

func (f *fakeHID) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.written {
		if bytes.HasPrefix(r, []byte("CRT\x00\x00")) {
			name := string(bytes.TrimRight(r[5:12], "\x00"))
			if len(name) > 3 && name != "CONNECT" {
				name = name[:3]
			}
			out = append(out, name)
		}
	}
	return out
}

func (f *fakeHID) reset() {
	f.mu.Lock()
	f.written = nil
	f.mu.Unlock()
}

func frameOf(keys string) model.Frame {
	var f model.Frame
	copy(f.Keys[:], keys)
	return f
}

func newBoard(t *testing.T) (*Device, *fakeHID, *float64) {
	t.Helper()
	h := newFakeHID()
	d, err := NewDevice(h, 80, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	now := steady
	d.clock = func() float64 { return now }
	d.wall = func() time.Time { return wall }
	return d, h, &now
}

func TestOpeningWakesThePanelAndBlanksIt(t *testing.T) {
	_, h, _ := newBoard(t)
	if got := h.commands(); !slices.Equal(got, []string{"CONNECT", "DIS", "LIG", "CLE", "STP"}) {
		t.Fatalf("%v", got)
	}
}

func TestAnUnchangedFrameIsAHeartbeatNotARedraw(t *testing.T) {
	d, h, _ := newBoard(t)
	d.SendFrame(frameOf("i-----------menf"))
	h.reset()
	d.SendFrame(frameOf("i-----------menf"))
	if got := h.commands(); !slices.Equal(got, []string{"CONNECT"}) {
		t.Fatalf("%v: it proves the board is still there", got)
	}
}

func TestABlinkRedrawsOnlyTheBlinkingKey(t *testing.T) {
	d, h, now := newBoard(t)
	d.SendFrame(frameOf("bi----------menf"))
	h.reset()
	*now = 0 // the blink is off
	d.Animate(0)
	if got := h.commands(); !slices.Equal(got, []string{"CLE", "STP"}) {
		t.Fatalf("%v", got)
	}
}

func TestABareFlashIsTheFunctionKeysAndANamedOneIsThatKeys(t *testing.T) {
	d, _, _ := newBoard(t)
	d.SendFrame(frameOf("i-----------menf"))
	d.Flash(board.FlashFunctionKey)
	if d.drawn[SlotToKey[model.FnSlot]] != (Look{Background: flash}) {
		t.Fatal("the function key")
	}
	d.Flash(model.GitSlot)
	if d.drawn[SlotToKey[model.GitSlot]] != (Look{Background: flash}) || d.drawn[SlotToKey[model.FnSlot]] == (Look{Background: flash}) {
		t.Fatal("the git key, and only it")
	}
}

func TestKeysReportBothEdgesAndNameSlots(t *testing.T) {
	d, h, _ := newBoard(t)
	ack := []byte("ACK\x00\x00OK\x00\x00")
	h.reports <- append(append([]byte{}, ack...), 15, 1)
	h.reports <- append(append([]byte{}, ack...), 9, 1) // between return and git: nothing
	h.reports <- append(append([]byte{}, ack...), 15, 0)
	close(h.reports)
	var got []board.Key
	for k := range d.Keys() {
		got = append(got, k)
	}
	if !slices.Equal(got, []board.Key{{Slot: model.MicSlot, Down: true}, {Slot: model.MicSlot, Down: false}}) {
		t.Fatalf("%v", got)
	}
	if d.Err() == nil {
		t.Fatal("an unplugged board says so")
	}
}

func TestAnUnpluggedBoardFailsItsWrites(t *testing.T) {
	d, h, _ := newBoard(t)
	h.fail = errors.New("gone")
	if err := d.SendFrame(frameOf("i-----------menf")); err == nil {
		t.Fatal("a write to an unplugged board succeeded")
	}
}

func TestClosingBlanksThePanelBecauseNothingElseWill(t *testing.T) {
	d, h, _ := newBoard(t)
	d.SendFrame(frameOf("i-----------menf"))
	h.reset()
	d.Close()
	if got := h.commands(); !slices.Equal(got, []string{"CLE", "STP"}) || !h.closed {
		t.Fatalf("%v", got)
	}
}

func TestTheBoardDrawsTheStripItIsToldAndOnlyRedrawsOnChange(t *testing.T) {
	d, h, _ := newBoard(t)
	d.SendFrame(frameOf("i-----------menf"))
	h.reset()
	d.ShowUsage(&usage.Usage{Session: limit(7, time.Hour)}, false)
	d.Animate(0)
	bats := 0
	for _, c := range h.commands() {
		if c == "BAT" {
			bats++
		}
	}
	if bats != 3 {
		t.Fatalf("%d strip images", bats)
	}
	h.reset()
	d.Animate(0)
	if len(h.commands()) != 0 {
		t.Fatal("redrawn without a change")
	}
}
