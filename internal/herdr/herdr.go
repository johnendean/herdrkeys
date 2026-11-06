// Package herdr talks to Herdr over its socket API (ADR 0021).
//
// Newline-delimited JSON, over a Unix socket on macOS and a named pipe on
// Windows. Two connection styles, deliberately:
//
//   - a long-lived connection that stays open after `events.subscribe` and
//     receives pushed events, and
//   - a fresh short-lived connection per request.
//
// Requests are rare (one per key press, two polls a second) and mixing them
// into the subscription connection would mean demultiplexing responses out of
// the event stream for no benefit.
package herdr

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/johnendean/herdrkeys/internal/state"
)

// DefaultTimeout bounds every request.
const DefaultTimeout = 5 * time.Second

// Subscriptions are the global events herdrkeys asks for.
// `pane.agent_status_changed` is deliberately absent: it requires a specific
// pane_id, so it cannot be subscribed to globally.
//
// None of these is trusted for agent status. The stream replays a backlog and
// then goes quiet (ADR 0005), so status is polled with `agent.list` and these
// events only shorten the wait when they do arrive.
var Subscriptions = []map[string]string{
	{"type": "pane.created"},
	{"type": "pane.updated"},
	{"type": "pane.closed"},
	{"type": "pane.exited"},
	{"type": "pane.focused"},
	{"type": "pane.moved"},
	{"type": "pane.agent_detected"},
}

// Error is an error reply from Herdr, or Herdr going away mid-request.
type Error struct {
	Method  string
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("%s: %s", e.Method, e.Message)
	}
	return fmt.Sprintf("%s: %s: %s", e.Method, e.Code, e.Message)
}

// conn is what a dialled socket or pipe gives back. A net.Conn is one, and so
// is an *os.File opened on an overlapped pipe handle.
type conn interface {
	io.ReadWriteCloser
	SetDeadline(time.Time) error
}

// dial connects to the socket at path, however this platform spells it. A
// variable so tests can swap in an in-memory pair.
var dial = dialPlatform

// DefaultSocketPath is the default session's socket, or the one the
// environment names.
func DefaultSocketPath() string {
	if explicit := os.Getenv("HERDR_SOCKET_PATH"); explicit != "" {
		return explicit
	}
	base := configDir()
	if session := os.Getenv("HERDR_SESSION"); session != "" {
		return filepath.Join(base, "sessions", session, "herdr.sock")
	}
	return filepath.Join(base, "herdr.sock")
}

// configDir is where Herdr keeps its config and socket: `%APPDATA%\herdr` on
// Windows, `~/.config/herdr` elsewhere.
func configDir() string {
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "herdr")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "herdr")
}

type reply struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func send(c conn, method string, params any) error {
	if params == nil {
		params = map[string]any{}
	}
	line, err := json.Marshal(map[string]any{
		"id": "herdrkeys:" + method, "method": method, "params": params,
	})
	if err != nil {
		return err
	}
	_, err = c.Write(append(line, '\n'))
	return err
}

func decodeReply(method string, line []byte) (json.RawMessage, error) {
	var r reply
	if err := json.Unmarshal(line, &r); err != nil {
		return nil, fmt.Errorf("%s: unreadable reply: %w", method, err)
	}
	if r.Error != nil {
		return nil, &Error{Method: method, Code: r.Error.Code, Message: r.Error.Message}
	}
	return r.Result, nil
}

// Request makes one request on its own connection and returns its result.
func Request(path, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	c, err := dial(path, timeout)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if err := c.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}
	if err := send(c, method, params); err != nil {
		return nil, err
	}
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, &Error{Method: method, Message: "connection closed before a reply"}
		}
		return nil, err
	}
	return decodeReply(method, line)
}

func requestInto(path, method string, params any, timeout time.Duration, into any) error {
	result, err := Request(path, method, params, timeout)
	if err != nil {
		return err
	}
	if len(result) == 0 || string(result) == "null" {
		return nil
	}
	if err := json.Unmarshal(result, into); err != nil {
		return fmt.Errorf("%s: unreadable result: %w", method, err)
	}
	return nil
}

// GetSnapshot asks for the whole session.
func GetSnapshot(path string, timeout time.Duration) (state.Snapshot, error) {
	var out struct {
		Snapshot state.Snapshot `json:"snapshot"`
	}
	err := requestInto(path, "session.snapshot", nil, timeout, &out)
	return out.Snapshot, err
}

// Agents is every agent pane and the state it is in, right now.
//
// The status source. `agent.list` is cheap enough to poll -- a round trip
// measured at 0.22ms against a live session -- and unlike the event stream it
// cannot fall behind: it is a question, not a feed. See ADR 0005.
func Agents(path string, timeout time.Duration) ([]state.Pane, error) {
	var out struct {
		Agents []state.Pane `json:"agents"`
	}
	err := requestInto(path, "agent.list", nil, timeout, &out)
	return out.Agents, err
}

// FocusAgent focuses the agent in a pane, by focusing the pane.
//
// `pane.focus` is what moves the viewport, and it is the only part of this
// that must succeed.
//
// Not `agent.focus` alone, which ADR 0001 chose and which stopped working in
// Herdr 0.9.0: it still answers `ok` and never reaches a surface-active
// client, so nothing on screen moves (herdr#3760, ADR 0008).
//
// `agent.focus` is then sent anyway, and its failure ignored. Marking an agent
// seen -- what turns `done` back into `idle` -- is the reason ADR 0001 went
// through the agent surface. It is sent second because it is the expendable
// one: if the agent has left the pane since the frame was drawn it raises
// `agent_not_found`, which must not cost the navigation already done.
func FocusAgent(path, paneID string, timeout time.Duration) error {
	if _, err := Request(path, "pane.focus", map[string]string{"pane_id": paneID}, timeout); err != nil {
		return err
	}
	_, err := Request(path, "agent.focus", map[string]string{"target": paneID}, timeout)
	var herdrErr *Error
	if errors.As(err, &herdrErr) {
		return nil // an error reply: the agent has gone, the pane is focused
	}
	return err
}

// EventStream is a subscribed connection.
//
// Herdr replays a backlog of past events on subscribe, so the first events off
// this stream are history, not news. The state fold discards stale updates by
// per-pane revision, and the daemon reconciles against `session.snapshot`
// periodically, so neither a truncated nor a replayed backlog can strand the
// view.
type EventStream struct {
	c      conn
	reader *bufio.Reader
}

// Subscribe opens an event stream and waits for Herdr to acknowledge it.
func Subscribe(path string, timeout time.Duration) (*EventStream, error) {
	c, err := dial(path, timeout)
	if err != nil {
		return nil, err
	}
	s := &EventStream{c: c, reader: bufio.NewReader(c)}
	if err := s.subscribe(timeout); err != nil {
		c.Close()
		return nil, err
	}
	return s, nil
}

func (s *EventStream) subscribe(timeout time.Duration) error {
	if err := s.c.SetDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	params := map[string]any{"subscriptions": Subscriptions}
	if err := send(s.c, "events.subscribe", params); err != nil {
		return err
	}
	line, err := s.reader.ReadBytes('\n')
	if err != nil {
		return &Error{Method: "subscribe", Message: "connection closed before acknowledgement"}
	}
	if _, err := decodeReply("subscribe", line); err != nil {
		return err
	}
	return s.c.SetDeadline(time.Time{})
}

// Next blocks for the next event. Lines that are not events are skipped. An
// error means Herdr went away, or Close was called.
func (s *EventStream) Next() (state.Event, error) {
	for {
		line, err := s.reader.ReadBytes('\n')
		if err != nil {
			return state.Event{}, &Error{Method: "events", Message: "event stream closed"}
		}
		var event state.Event
		if json.Unmarshal(line, &event) == nil {
			return event, nil
		}
	}
}

// Close ends the stream, and unblocks a pending Next.
func (s *EventStream) Close() error { return s.c.Close() }
