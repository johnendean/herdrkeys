// Package usage is how much of the Claude plan's rate limits has gone, for
// the CN001's strip.
//
// Asked of Anthropic, not Herdr: the same endpoint Claude Code's own `/usage`
// calls, with Claude Code's own OAuth token, read and never written back (ADR
// 0019). The endpoint is undocumented, so anything it says that does not parse
// is "usage unknown", never a failure of the daemon.
//
// Parsing is pure; ReadToken and Fetch are the only parts that reach out, and
// Poller is what the daemon holds: it asks off the loop, at most once a
// minute, and only while it is told something wants the answer.
package usage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"time"
)

const (
	URL            = "https://api.anthropic.com/api/oauth/usage"
	requestTimeout = 10 * time.Second

	PollSeconds    = 60.0
	MaxPollSeconds = 15 * 60.0 // how far a 429 backs the polling off
	StaleSeconds   = 5 * 60.0
)

// Limit is one rate limit: the share of its window spent, and when the window
// ends -- zero with no window running.
type Limit struct {
	Percent  float64
	ResetsAt time.Time
}

// Spent reports whether the limit has been reached.
func (l Limit) Spent() bool { return l.Percent >= 100 }

// At is this limit as it stands at wall: a window that has ended has reset.
func (l Limit) At(wall time.Time) Limit {
	if !l.ResetsAt.IsZero() && !wall.Before(l.ResetsAt) {
		return Limit{}
	}
	return l
}

// Usage is the two limits the strip shows. Either may be absent.
type Usage struct {
	Session *Limit // the 5-hour limit
	Weekly  *Limit
}

// At is the usage as it stands at wall.
func (u Usage) At(wall time.Time) Usage {
	var out Usage
	if u.Session != nil {
		s := u.Session.At(wall)
		out.Session = &s
	}
	if u.Weekly != nil {
		w := u.Weekly.At(wall)
		out.Weekly = &w
	}
	return out
}

// FreeAt is when work can next start, if a limit is spent; else when the
// session resets. Zero when nothing is counting down: no session in progress
// and nothing spent.
func (u Usage) FreeAt(wall time.Time) time.Time {
	now := u.At(wall)
	var latest time.Time
	for _, l := range []*Limit{now.Session, now.Weekly} {
		if l != nil && l.Spent() && !l.ResetsAt.IsZero() && l.ResetsAt.After(latest) {
			latest = l.ResetsAt
		}
	}
	if !latest.IsZero() {
		return latest
	}
	if now.Session != nil {
		return now.Session.ResetsAt
	}
	return time.Time{}
}

// Limited reports whether any limit is spent at wall.
func (u Usage) Limited(wall time.Time) bool {
	now := u.At(wall)
	return (now.Session != nil && now.Session.Spent()) || (now.Weekly != nil && now.Weekly.Spent())
}

// Error is usage being unknown this time. Asking again later may work.
type Error struct{ Reason string }

func (e *Error) Error() string { return e.Reason }

// CredentialRefused is no credential to be had. Asking again would only ask
// again for nothing -- on macOS, raise the Keychain dialog again.
type CredentialRefused struct{ Reason string }

func (e *CredentialRefused) Error() string { return e.Reason }

// ErrRateLimited is a 429.
var ErrRateLimited = &Error{"asked too often (HTTP 429)"}

func unknown(format string, args ...any) error { return &Error{fmt.Sprintf(format, args...)} }

func limit(raw json.RawMessage, name string) (*Limit, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, unknown("unexpected %s: not an object", name)
	}
	// A number, and only a number: true is not a percentage.
	var percent float64
	if p, ok := fields["utilization"]; !ok || json.Unmarshal(p, &percent) != nil {
		return nil, unknown("unexpected %s utilization: %s", name, fields["utilization"])
	}
	// Numbers too big for a float parse as an error above, or as infinity;
	// none is a percentage, and drawing one would crash the strip (#27).
	if math.IsInf(percent, 0) || math.IsNaN(percent) {
		return nil, unknown("unexpected %s utilization: not a finite number", name)
	}
	out := &Limit{Percent: percent}
	resets, ok := fields["resets_at"]
	if !ok || string(resets) == "null" {
		return out, nil
	}
	var text string
	if json.Unmarshal(resets, &text) != nil {
		return nil, unknown("unexpected %s resets_at: %s", name, resets)
	}
	moment, err := parseMoment(text)
	if err != nil {
		return nil, unknown("unexpected %s resets_at: %q", name, text)
	}
	out.ResetsAt = moment
	return out, nil
}

// parseMoment reads an ISO 8601 time that names its timezone, with or
// without fractional seconds.
func parseMoment(text string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999Z07:00"} {
		if t, err := time.Parse(layout, text); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("not a time with a timezone")
}

// Parse is the usage a response describes, or an Error for any other shape.
func Parse(payload []byte) (Usage, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(payload, &fields) != nil {
		return Usage{}, unknown("response was not JSON")
	}
	fiveHour, okFive := fields["five_hour"]
	sevenDay, okSeven := fields["seven_day"]
	if !okFive || !okSeven {
		return Usage{}, unknown("unexpected response: no five_hour or seven_day")
	}
	session, err := limit(fiveHour, "five_hour")
	if err != nil {
		return Usage{}, err
	}
	weekly, err := limit(sevenDay, "seven_day")
	if err != nil {
		return Usage{}, err
	}
	return Usage{Session: session, Weekly: weekly}, nil
}

// TokenFrom is the access token in Claude Code's stored credential, if it is
// still good.
func TokenFrom(credential []byte, wall time.Time, where string) (string, error) {
	var stored struct {
		OAuth *struct {
			AccessToken any `json:"accessToken"`
			ExpiresAt   any `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if json.Unmarshal(credential, &stored) != nil || stored.OAuth == nil {
		return "", &CredentialRefused{where + " has no OAuth token"}
	}
	token, ok := stored.OAuth.AccessToken.(string)
	if !ok || token == "" {
		return "", &CredentialRefused{where + " has no OAuth token"}
	}
	if expires, ok := stored.OAuth.ExpiresAt.(float64); ok && expires/1000 <= float64(wall.Unix()) {
		return "", unknown("Claude Code's token has expired; it refreshes it when it next runs")
	}
	return token, nil
}

// Client is the HTTP client usage is fetched with. A variable so tests can
// stand in for Anthropic.
var Client = &http.Client{Timeout: requestTimeout}

// Fetch asks Anthropic, and returns the response body.
func Fetch(token string) ([]byte, error) {
	request, err := http.NewRequest(http.MethodGet, URL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("anthropic-beta", "oauth-2025-04-20")
	request.Header.Set("User-Agent", "herdrkeys")
	response, err := Client.Do(request)
	if err != nil {
		return nil, unknown("cannot reach Anthropic: %v", err)
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusTooManyRequests:
		return nil, ErrRateLimited
	case response.StatusCode == 401 || response.StatusCode == 403:
		return nil, unknown("token not accepted (HTTP %d); Claude Code refreshes it when it next runs", response.StatusCode)
	case response.StatusCode != http.StatusOK:
		return nil, unknown("HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, unknown("cannot reach Anthropic: %v", err)
	}
	return bytes.TrimSpace(body), nil
}

// Current is usage as Anthropic has it now. Blocks.
func Current() (Usage, error) {
	token, err := ReadToken(time.Now())
	if err != nil {
		return Usage{}, err
	}
	body, err := Fetch(token)
	if err != nil {
		return Usage{}, err
	}
	return Parse(body)
}

type answer struct {
	usage Usage
	err   error
}

// Poller is the last usage known, kept fresh while something wants it.
//
// Asks at most once every PollSeconds, one request at a time, off the
// caller's goroutine; Tick collects the answer. Nothing is asked while
// nothing wants it, and nothing at all once the credential has been refused.
type Poller struct {
	Ask func() (Usage, error)
	Log *slog.Logger

	Reading   *Usage
	ReadAt    float64
	Refused   string // why, once the credential is out of reach
	LastError string

	interval float64
	nextAsk  float64
	asking   chan answer
	askedAt  float64 // when the request out now, or the last one, started
}

// NewPoller asks Anthropic with Claude Code's credential.
func NewPoller(log *slog.Logger) *Poller {
	return &Poller{Ask: Current, Log: log, interval: PollSeconds}
}

// Stale reports whether the reading is too old to trust.
func (p *Poller) Stale(now float64) bool {
	return p.Reading != nil && now-p.ReadAt > StaleSeconds
}

// Tick collects an answer that has landed, and asks again when it is due and
// wanted.
func (p *Poller) Tick(now float64, wanted bool) {
	p.collect(now, false)
	if !wanted || p.Refused != "" || p.asking != nil || now < p.nextAsk {
		return
	}
	if p.interval == 0 {
		p.interval = PollSeconds
	}
	asking := make(chan answer, 1)
	p.asking, p.askedAt, p.nextAsk = asking, now, now+p.interval
	ask := p.Ask
	go func() {
		u, err := ask()
		asking <- answer{u, err}
	}()
	p.collect(now, false)
}

// Wait blocks until the request out now lands, and collects it. For tests,
// and for doctor.
func (p *Poller) Wait(now float64) { p.collect(now, true) }

func (p *Poller) collect(now float64, block bool) {
	if p.asking == nil {
		return
	}
	var got answer
	if block {
		got = <-p.asking
	} else {
		select {
		case got = <-p.asking:
		default:
			return
		}
	}
	p.asking = nil
	if got.err == nil {
		u := got.usage
		p.Reading, p.ReadAt = &u, now
		// Back to once a minute from when this request started -- not at
		// the backed-off deadline set when it did, which after a run of 429s
		// left a good reading to go stale before the next (#28).
		p.interval = PollSeconds
		p.nextAsk = p.askedAt + PollSeconds
		if p.LastError != "" {
			p.log().Info("usage: known again")
		}
		p.LastError = ""
		return
	}
	reason := got.err.Error()
	var refused *CredentialRefused
	if errors.As(got.err, &refused) {
		p.Refused = reason
		p.log().Warn("usage: " + reason + " -- the strip stays as it is until the daemon restarts")
		return
	}
	if errors.Is(got.err, ErrRateLimited) {
		p.interval = min(MaxPollSeconds, p.interval*2)
		p.nextAsk = now + p.interval
	}
	if reason != p.LastError {
		p.LastError = reason
		p.log().Warn("usage unknown: " + reason)
	}
}

func (p *Poller) log() *slog.Logger {
	if p.Log == nil {
		return slog.Default()
	}
	return p.Log
}
