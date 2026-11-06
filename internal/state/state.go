// Package state folds Herdr's event stream into a view of the panes.
//
// Pure. Feed it snapshots and events; ask it for agent panes.
//
// Two things about Herdr's stream shape drive this design:
//
//   - `events.subscribe` replays a backlog on connect, so events can arrive
//     that are older than a snapshot taken afterwards. Every `pane_updated`
//     carries a monotonic per-pane `revision`, so stale updates are discarded
//     by revision.
//   - The stream cannot be trusted to keep arriving, so agent status is polled
//     with `agent.list` and folded by ApplyAgents. `pane_updated` carries the
//     whole pane object, status included, so events still count -- they just
//     are not the thing being waited on. See ADR 0005.
//
// `agent.list` also fixes the order the agents are in, and that order is what
// the keys mirror (ADR 0009), so it is recorded rather than inferred. Map
// order will not do: panes are built from a snapshot and then mutated by
// events, and a `pane_moved` re-key or a pane learned from an event lands
// wherever it happens to, which is not where Herdr shows it.
package state

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/johnendean/herdrkeys/internal/model"
)

// Pane is a pane as Herdr sends it, in `agent.list` rows, snapshots and
// events alike. Only the fields herdrkeys reads are declared.
type Pane struct {
	PaneID        string          `json:"pane_id"`
	Agent         *string         `json:"agent"`
	AgentStatus   string          `json:"agent_status"`
	Revision      float64         `json:"revision"`
	Cwd           string          `json:"cwd"`
	ForegroundCwd string          `json:"foreground_cwd"`
	Focused       bool            `json:"focused"`
	Tokens        json.RawMessage `json:"tokens"`
}

// Snapshot is the part of `session.snapshot` herdrkeys reads.
type Snapshot struct {
	Panes         []Pane  `json:"panes"`
	FocusedPaneID *string `json:"focused_pane_id"`
	// The same list `agent.list` answers with, in the same order. Nil when
	// the snapshot has none, which is not the same as an empty list.
	Agents *[]Pane `json:"agents"`
}

// Event is one line off `events.subscribe`.
type Event struct {
	Event string    `json:"event"`
	Data  EventData `json:"data"`
}

// EventData is the union of the event payloads herdrkeys reads.
type EventData struct {
	Type           string  `json:"type"`
	Pane           *Pane   `json:"pane"`
	PaneID         string  `json:"pane_id"`
	PreviousPaneID string  `json:"previous_pane_id"`
	Agent          *string `json:"agent"`
	Released       bool    `json:"released"`
}

// Record is what is known about one pane.
type Record struct {
	PaneID   string
	Agent    *string // nil when the pane has no agent
	State    model.AgentState
	Revision int
	Cwd      string
	Colour   string
}

// IsAgentPane reports whether the pane has an agent in it now.
func (r Record) IsAgentPane() bool { return r.Agent != nil }

// colourOf is the project colour herdrcolor reported for this pane, if any.
//
// It rides along on the pane payloads herdrkeys already fetches, as
// `tokens.color`, so reading it costs nothing and needs no agreement with that
// plugin beyond the name of the field. Anything that is not a `#rrggbb` string
// is treated as absent: a key falling back to its state colour is the designed
// answer to "no colour here", so a malformed one need not be an error (ADR
// 0010).
func colourOf(tokens json.RawMessage) string {
	var fields map[string]json.RawMessage
	if len(tokens) == 0 || json.Unmarshal(tokens, &fields) != nil {
		return ""
	}
	var colour string
	if raw, ok := fields["color"]; !ok || json.Unmarshal(raw, &colour) != nil {
		return ""
	}
	colour = strings.TrimSpace(colour)
	// Digits checked one by one: a numeric parse would also take a sign, a 0x
	// or an underscore, and "#-1ffff" is not a colour.
	if len(colour) != 7 || colour[0] != '#' {
		return ""
	}
	for _, c := range colour[1:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return ""
		}
	}
	return strings.ToLower(colour)
}

func recordFromPane(pane Pane) Record {
	// `foreground_cwd` is where the running program is; `cwd` is the pane's
	// own. They agree for an agent pane, and `cwd` survives the agent
	// exiting, so it is the one asked first.
	cwd := pane.Cwd
	if cwd == "" {
		cwd = pane.ForegroundCwd
	}
	return Record{
		PaneID:   pane.PaneID,
		Agent:    pane.Agent,
		State:    model.ParseState(pane.AgentStatus),
		Revision: int(pane.Revision),
		Cwd:      cwd,
		Colour:   colourOf(pane.Tokens),
	}
}

// keepingKnown carries what is already known across an update that does not
// mention it.
//
// Herdr omits `cwd` from some pane payloads, and `tokens` from others. Taking
// the new record whole would blank them, and the git key would do nothing --
// or a key would drop back to its state colour -- until the next payload that
// happened to include one.
//
// Absence is therefore read as silence, never as a retraction. Neither field
// is ever cleared here; a pane's colour goes away when the pane does.
func keepingKnown(record Record, existing *Record) Record {
	if existing == nil {
		return record
	}
	if record.Cwd == "" {
		record.Cwd = existing.Cwd
	}
	if record.Colour == "" {
		record.Colour = existing.Colour
	}
	return record
}

// State is herdrkeys' view of Herdr's panes.
type State struct {
	Panes         map[string]Record
	FocusedPaneID string // "" when no pane is focused, or none is known
	// The order Herdr lists agents in, which is the order the keys take.
	AgentOrder []string
}

// New is an empty view.
func New() *State { return &State{Panes: map[string]Record{}} }

func (s *State) existing(paneID string) *Record {
	if r, ok := s.Panes[paneID]; ok {
		return &r
	}
	return nil
}

// ApplySnapshot replaces everything. The snapshot is authoritative.
func (s *State) ApplySnapshot(snapshot Snapshot) {
	s.Panes = map[string]Record{}
	for _, pane := range snapshot.Panes {
		record := recordFromPane(pane)
		s.Panes[record.PaneID] = record
	}
	s.FocusedPaneID = ""
	if snapshot.FocusedPaneID != nil {
		s.FocusedPaneID = *snapshot.FocusedPaneID
	}
	// A snapshot without an agent list leaves the order alone rather than
	// clearing it: the next status poll is 500ms away at most.
	if snapshot.Agents != nil {
		s.setOrder(*snapshot.Agents)
	}
}

// ApplyAgents folds a status poll: authoritative about state, silent about
// lifetime.
//
// `agent.list` answers "which panes have an agent, and what is it doing", so
// a pane it does not mention either has no agent or is not a pane at all.
// Panes are therefore updated and released here, never forgotten --
// forgetting stays with the snapshot and `pane_closed`. The pane survives
// losing its agent; its key does not, because the keys mirror this list and
// this list no longer contains it (ADR 0009).
func (s *State) ApplyAgents(rows []Pane) {
	s.setOrder(rows)
	seen := map[string]bool{}
	focused := ""
	unfocused := map[string]bool{}
	for _, row := range rows {
		if row.PaneID == "" {
			continue
		}
		record := recordFromPane(row)
		seen[record.PaneID] = true
		existing := s.existing(record.PaneID)
		if existing != nil {
			// Hold the high-water revision, so a `pane_updated` replayed
			// after this poll cannot overwrite it through the guard in
			// upsert.
			record.Revision = max(record.Revision, existing.Revision)
		}
		s.Panes[record.PaneID] = keepingKnown(record, existing)
		if row.Focused {
			focused = record.PaneID
		} else {
			unfocused[record.PaneID] = true
		}
	}
	if focused != "" {
		s.FocusedPaneID = focused
	} else if unfocused[s.FocusedPaneID] {
		// Focus has moved to a pane with no agent. `agent.list` cannot say
		// which one, but it can say it is no longer this one, and a key left
		// bright is a key claiming focus it does not have.
		s.FocusedPaneID = ""
	}
	for paneID, record := range s.Panes {
		if record.IsAgentPane() && !seen[paneID] {
			// The agent left the pane; the pane itself survives.
			record.Agent, record.State = nil, model.Unknown
			s.Panes[paneID] = record
		}
	}
}

// ApplyEvent folds one event. Unknown or stale events are ignored.
func (s *State) ApplyEvent(event Event) {
	data := event.Data
	kind := data.Type
	if kind == "" {
		kind = event.Event
	}
	switch kind {
	case "pane_updated", "pane_created":
		if data.Pane != nil {
			s.upsert(*data.Pane, false)
		}
	case "pane_moved":
		// The pane keeps its identity but gets a new workspace-qualified ID.
		if previous := data.PreviousPaneID; previous != "" {
			delete(s.Panes, previous)
			if s.FocusedPaneID == previous {
				s.FocusedPaneID = ""
			}
		}
		if data.Pane != nil {
			s.upsert(*data.Pane, true)
		}
	case "pane_closed", "pane_exited":
		if paneID := data.PaneID; paneID != "" {
			delete(s.Panes, paneID)
			if s.FocusedPaneID == paneID {
				s.FocusedPaneID = ""
			}
		}
	case "pane_focused":
		s.FocusedPaneID = data.PaneID
	case "pane_agent_detected":
		s.agentDetected(data)
	}
}

func (s *State) upsert(pane Pane, force bool) {
	if pane.PaneID == "" {
		return
	}
	record := recordFromPane(pane)
	existing := s.existing(record.PaneID)
	if existing != nil && !force && record.Revision < existing.Revision {
		return // backlog replay: an update older than what is already held
	}
	s.Panes[record.PaneID] = keepingKnown(record, existing)
	if pane.Focused {
		s.FocusedPaneID = record.PaneID
	}
}

func (s *State) agentDetected(data EventData) {
	paneID := data.PaneID
	if paneID == "" {
		return
	}
	existing := s.existing(paneID)
	if data.Released {
		// The agent left the pane; the pane itself survives.
		if existing != nil {
			existing.Agent, existing.State = nil, model.Unknown
			s.Panes[paneID] = *existing
		}
		return
	}
	if existing == nil {
		// No cwd: the event does not carry one; a snapshot or poll will.
		s.Panes[paneID] = Record{PaneID: paneID, Agent: data.Agent, State: model.Unknown}
		return
	}
	existing.Agent = data.Agent
	s.Panes[paneID] = *existing
}

// AgentPanes is every agent pane, in the order Herdr lists them.
//
// The order is load-bearing: it is what the keys are numbered by. An agent
// Herdr has not listed yet -- learned from `pane_agent_detected` between two
// polls -- goes on the end in pane ID order, so it has a key within the poll
// interval rather than none, and a deterministic one.
func (s *State) AgentPanes() []model.AgentPane {
	listed := map[string]bool{}
	var order []string
	for _, paneID := range s.AgentOrder {
		if r, ok := s.Panes[paneID]; ok && r.IsAgentPane() && !listed[paneID] {
			listed[paneID] = true
			order = append(order, paneID)
		}
	}
	var unlisted []string
	for paneID, r := range s.Panes {
		if r.IsAgentPane() && !listed[paneID] {
			unlisted = append(unlisted, paneID)
		}
	}
	sort.Strings(unlisted)
	order = append(order, unlisted...)

	panes := make([]model.AgentPane, len(order))
	for i, paneID := range order {
		r := s.Panes[paneID]
		panes[i] = model.AgentPane{
			PaneID:  paneID,
			Agent:   *r.Agent,
			State:   r.State,
			Focused: paneID == s.FocusedPaneID,
			Cwd:     r.Cwd,
			Colour:  r.Colour,
		}
	}
	return panes
}

// LivePaneIDs is every pane known, agent or not.
func (s *State) LivePaneIDs() map[string]bool {
	ids := make(map[string]bool, len(s.Panes))
	for paneID := range s.Panes {
		ids[paneID] = true
	}
	return ids
}

func (s *State) setOrder(rows []Pane) {
	s.AgentOrder = s.AgentOrder[:0:0]
	for _, row := range rows {
		if row.PaneID != "" {
			s.AgentOrder = append(s.AgentOrder, row.PaneID)
		}
	}
}
