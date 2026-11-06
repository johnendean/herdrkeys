// Package model holds the core domain types and the frame encoding.
//
// Pure: no I/O, no clock, no serial, no sockets.
package model

// SlotCount is how many positions a frame has.
const SlotCount = 16

// One row of four holds no agents. Twelve keys is more grid than the agents
// have ever asked for, so that row is free for keys that do something else.
// Which row it is physically depends on ROTATION, which only the device knows.
const (
	MicSlot    = 12
	ReturnSlot = 13 // beside the microphone, because it sends what was just dictated
	GitSlot    = 14
	FnSlot     = 15

	// GridSize is how many slots hold agents: 0..11, contiguous, so slot n
	// is the nth agent.
	GridSize = 12
)

// FeatureSlots are the slots that never hold an agent.
var FeatureSlots = [...]int{MicSlot, ReturnSlot, GitSlot, FnSlot}

// ProtocolVersion is the Keybow wire protocol this host speaks.
const ProtocolVersion = 2

// AgentState is Herdr's agent lifecycle state. A closed set: see CONTEXT.md.
type AgentState string

const (
	Idle    AgentState = "idle"
	Working AgentState = "working"
	Blocked AgentState = "blocked"
	Done    AgentState = "done"
	Unknown AgentState = "unknown"
)

// ParseState reads Herdr's `agent_status`. Anything outside the closed set is
// Unknown, which is what Herdr means by a state it cannot classify.
func ParseState(raw string) AgentState {
	switch s := AgentState(raw); s {
	case Idle, Working, Blocked, Done, Unknown:
		return s
	}
	return Unknown
}

// A frame is one character per key. Lowercase is unfocused, uppercase focused.
// The device owns the palette; these codes carry meaning, never colour.
const (
	CodeEmpty          = '-'
	CodeFnConnected    = 'f'
	CodeFnDisconnected = 'x'
	// The microphone key, in the three states of the one microphone (ADR
	// 0017). Every board shows the same one; which board holds it is not in
	// the frame.
	CodeMic        = 'm'
	CodeMicOpen    = 'o'
	CodeMicLatched = 'l'
	CodeReturn     = 'e'
	// The git key, in its two states: the focused agent's directory has a
	// page, or it does not. Both are lit -- dark would read as a board with no
	// daemon behind it -- and the device decides how far apart they look.
	CodeGitPage   = 'r'
	CodeGitNoPage = 'n'
	// ...and a third, while a press is finding out where to go. The lookup
	// asks GitHub, so it can take a second, and a key that does nothing for a
	// second reads as a key that did not hear you.
	CodeGitLooking = 'g'
)

// Microphone is the state of the one microphone.
type Microphone string

const (
	MicClosed  Microphone = "closed"
	MicOpen    Microphone = "open"
	MicLatched Microphone = "latched"
)

func micCode(m Microphone) byte {
	switch m {
	case MicOpen:
		return CodeMicOpen
	case MicLatched:
		return CodeMicLatched
	}
	return CodeMic
}

var stateCode = map[AgentState]byte{
	Idle:    'i',
	Working: 'w',
	Blocked: 'b',
	Done:    'd',
	Unknown: 'u',
}

// StateCode is a state's frame character, upper case when focused.
func StateCode(state AgentState, focused bool) byte {
	code, ok := stateCode[state]
	if !ok {
		code = stateCode[Unknown]
	}
	if focused {
		return code - 'a' + 'A'
	}
	return code
}

// Features is what the feature row shows.
type Features struct {
	Connected  bool
	GitPage    bool
	GitLooking bool
	Mic        Microphone // "" means closed
}

// FeatureKeys is an empty frame with the feature row already filled in.
//
// One place decides what the non-agent keys show, so a frame and a blank frame
// can never disagree about them.
func FeatureKeys(f Features) [SlotCount]byte {
	var keys [SlotCount]byte
	for i := range keys {
		keys[i] = CodeEmpty
	}
	keys[MicSlot] = micCode(f.Mic)
	keys[ReturnSlot] = CodeReturn
	switch {
	case f.GitLooking:
		keys[GitSlot] = CodeGitLooking
	case f.GitPage:
		keys[GitSlot] = CodeGitPage
	default:
		keys[GitSlot] = CodeGitNoPage
	}
	if f.Connected {
		keys[FnSlot] = CodeFnConnected
	} else {
		keys[FnSlot] = CodeFnDisconnected
	}
	return keys
}

// AgentPane is an agent pane as herdrkeys cares about it.
type AgentPane struct {
	PaneID  string
	Agent   string
	State   AgentState
	Focused bool
	// Where the pane is, so the git key can ask git about it. Empty when
	// Herdr has not reported one, which is a normal answer, not an error.
	Cwd string
	// The project colour herdrcolor reported for this pane, `#rrggbb`. Empty
	// when that plugin is not installed, or has not synced since this pane
	// appeared -- both normal, and both mean the key falls back to its state
	// colour. See ADR 0010.
	Colour string
}

// Frame is what all 16 keys should show. Absolute, never a delta.
//
// Keys carries meaning and Colours carries data. The device decides what
// `blocked` looks like; it cannot decide what colour a project is, because
// that is not a meaning -- it is a fact about the world that only the host can
// know. So identity crosses the wire as hex and states do not (ADR 0010).
//
// Both live on one value so a frame stays absolute: there is no way for a
// key's colour and its meaning to arrive out of step.
//
// Forge is data for the git key in the same way: which service its page is
// on, when that is known (ADR 0018). Only a board that can draw a logo reads
// it; the Keybow is never sent it.
//
// A Frame is comparable with ==, which is how the daemon decides whether
// anything changed.
type Frame struct {
	Keys    [SlotCount]byte
	Colours [SlotCount]string // "" is no colour
	Forge   string            // "" is no forge
}

// KeyString is the frame's keys as the 16-character string the wire carries.
func (f Frame) KeyString() string { return string(f.Keys[:]) }

// Blank is a frame with no agents on it.
func Blank(connected, gitPage bool) Frame {
	return Frame{Keys: FeatureKeys(Features{Connected: connected, GitPage: gitPage})}
}
