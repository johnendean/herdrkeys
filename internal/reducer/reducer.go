// Package reducer turns Herdr's view of the world into a frame, and resolves
// the function key.
//
// Pure. This is where every interesting decision lives, which is why it takes
// plain data and returns plain data -- no hardware and no running Herdr needed
// to test any of it.
//
// Keys are positions in Herdr's agent list, not bindings an agent holds: the
// first agent Herdr lists is key 0, the second key 1, and so on (ADR 0009).
// Nothing here remembers anything -- the grid is an argument, so the whole
// board is a function of what Herdr says right now.
//
// A grid entry is a pane and the project colour it wears when idle. The colour
// is passed through rather than interpreted: which states wear it is the
// device's decision, because that is a question about what a state looks like
// (ADR 0010).
package reducer

import (
	"github.com/johnendean/herdrkeys/internal/model"
	"github.com/johnendean/herdrkeys/internal/settling"
)

// attentionOrder is the function key's priority: `blocked` has a human
// waiting on it, `done` finished unseen, everything else does not want you.
var attentionOrder = [...]model.AgentState{model.Blocked, model.Done}

// Panes are the agent panes Herdr knows of, by pane ID.
type Panes map[string]model.AgentPane

// OnTheGrid is the prefix of the agent list the grid has room for.
//
// Past twelve agents the rest are simply not on the board, and not reachable
// by the function key either. That is what the old scheme did too -- the
// thirteenth agent to appear got no key and could not be jumped to -- so
// nothing regresses here; it is just now the last in Herdr's order that misses
// out rather than the last to arrive.
func OnTheGrid(grid settling.Grid) settling.Grid {
	if len(grid) > model.GridSize {
		return grid[:model.GridSize]
	}
	return grid
}

// Input is everything a frame is drawn from besides the agents.
//
// GitPage and Forge are passed in rather than worked out here. Deciding them
// means touching the filesystem, and this package stays pure: it is the one
// place every interesting decision can be tested without a running Herdr, a
// keypad, or a checkout on disk.
type Input struct {
	model.Features
	Forge string
}

// Render draws the frame.
func Render(panes Panes, grid settling.Grid, settler *settling.Settler, in Input) model.Frame {
	frame := model.Frame{Keys: model.FeatureKeys(in.Features)}
	for slot, entry := range OnTheGrid(grid) {
		pane, ok := panes[entry.PaneID]
		if !ok {
			// In the settled grid but no longer an agent: the grid has not
			// caught up yet. Leave a hole for the moment rather than
			// shifting every key below it twice.
			continue
		}
		state, ok := settler.Settled(entry.PaneID)
		if !ok {
			continue // not settled yet: stay dark rather than strobe
		}
		frame.Keys[slot] = model.StateCode(state, pane.Focused)
		// Sent whenever the agent has one, for every state. Which states
		// actually wear it is the device's call: "what does idle look like"
		// is exactly the question the device is there to answer, and
		// answering it here would put the look of a key in two places (ADR
		// 0010).
		frame.Colours[slot] = entry.Colour
	}
	// A forge belongs to a page, so a key with no page carries none, whatever
	// it was handed.
	if in.GitPage {
		frame.Forge = in.Forge
	}
	return frame
}

// NextAttentionTarget is the pane the function key should focus, or "" if
// nothing wants you.
//
// Blocked agents first, then done, each in key order wrapping from whichever
// key is focused now, so repeated presses walk the queue instead of sticking.
//
// The wrap is over the agents that are actually on the grid, not over the
// twelve slots: with no holes left to skip, the two are the same walk, and
// counting agents means the queue is right however few there are.
func NextAttentionTarget(panes Panes, grid settling.Grid, settler *settling.Settler) string {
	onGrid := OnTheGrid(grid)
	if len(onGrid) == 0 {
		return ""
	}
	start := 0
	for slot, entry := range onGrid {
		if pane, ok := panes[entry.PaneID]; ok && pane.Focused {
			start = slot + 1
			break
		}
	}
	for _, wanted := range attentionOrder {
		for offset := range onGrid {
			paneID := onGrid[(start+offset)%len(onGrid)].PaneID
			pane, ok := panes[paneID]
			if !ok || pane.Focused {
				continue
			}
			if state, ok := settler.Settled(paneID); ok && state == wanted {
				return paneID
			}
		}
	}
	return ""
}

// PaneForSlot is the pane a key press should focus, or "" if that key holds
// nothing.
//
// A feature-row slot falls out of this naturally: the grid is at most twelve
// long, so 12 to 15 are always past its end.
func PaneForSlot(slot int, panes Panes, grid settling.Grid) string {
	onGrid := OnTheGrid(grid)
	if slot < 0 || slot >= len(onGrid) {
		return ""
	}
	paneID := onGrid[slot].PaneID
	if _, ok := panes[paneID]; !ok {
		return ""
	}
	return paneID
}
