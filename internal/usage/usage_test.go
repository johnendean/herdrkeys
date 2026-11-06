package usage

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 5, 17, 0, 0, 0, time.UTC)

// Trimmed from a real response: the endpoint returns many more fields, and
// every one of them but these two is ignored.
const response = `{
	"five_hour": {"utilization": 7.0, "resets_at": "2026-11-05T21:50:00.409228+00:00"},
	"seven_day": {"utilization": 9.0, "resets_at": "2026-11-06T13:00:00.409249+00:00"},
	"seven_day_opus": null,
	"extra_usage": {"is_enabled": false}}`

func withLimit(name, limit string) string {
	other := map[string]string{"five_hour": "seven_day", "seven_day": "five_hour"}[name]
	return `{"` + name + `": ` + limit + `, "` + other + `": {"utilization": 9.0, "resets_at": null}}`
}

func TestAResponseGivesTheSessionAndWeeklyLimits(t *testing.T) {
	u, err := Parse([]byte(response))
	if err != nil {
		t.Fatal(err)
	}
	if u.Session.Percent != 7 || u.Weekly.Percent != 9 {
		t.Fatalf("%+v", u)
	}
	if want := time.Date(2026, 11, 5, 21, 50, 0, 409228000, time.UTC); !u.Session.ResetsAt.Equal(want) {
		t.Fatalf("resets at %v", u.Session.ResetsAt)
	}
}

func TestNoSessionInProgressIsARealAnswerNotUnknown(t *testing.T) {
	u, err := Parse([]byte(withLimit("five_hour", "null")))
	if err != nil || u.Session != nil {
		t.Fatalf("%+v %v", u, err)
	}
	u, err = Parse([]byte(withLimit("five_hour", `{"utilization": 0, "resets_at": null}`)))
	if err != nil || u.Session == nil || u.Session.Percent != 0 || !u.Session.ResetsAt.IsZero() {
		t.Fatalf("%+v %v", u, err)
	}
}

func TestAnyOtherShapeIsUsageUnknown(t *testing.T) {
	for _, payload := range []string{
		"null", "[]", "{}", `{"five_hour": null}`, "not json",
		withLimit("five_hour", `"lots"`),
		withLimit("five_hour", `{"utilization": "7"}`),
		withLimit("five_hour", `{"utilization": true}`),
		withLimit("seven_day", `{"utilization": 9, "resets_at": "soon"}`),
		withLimit("seven_day", `{"utilization": 9, "resets_at": "2026-11-06T13:00:00"}`),
		// Not a finite number: drawing one would crash the strip (#27).
		withLimit("five_hour", `{"utilization": 1e400}`),
	} {
		var unknown *Error
		if _, err := Parse([]byte(payload)); !errors.As(err, &unknown) {
			t.Errorf("%s: %v", payload, err)
		}
	}
}

func limitAt(percent float64, resets time.Duration) *Limit {
	l := &Limit{Percent: percent}
	if resets != 0 {
		l.ResetsAt = now.Add(resets)
	}
	return l
}

func TestAWindowThatHasEndedHasResetWithoutWaitingToBeTold(t *testing.T) {
	u := Usage{Session: limitAt(100, time.Hour), Weekly: limitAt(50, 48*time.Hour)}
	later := u.At(now.Add(2 * time.Hour))
	if later.Session.Percent != 0 || !later.Session.ResetsAt.IsZero() || later.Weekly.Percent != 50 {
		t.Fatalf("%+v", later)
	}
	if u.Limited(now.Add(2 * time.Hour)) {
		t.Fatal("no longer limited")
	}
}

func TestTheCountdown(t *testing.T) {
	session := Usage{Session: limitAt(40, time.Hour), Weekly: limitAt(50, 48*time.Hour)}
	if !session.FreeAt(now).Equal(now.Add(time.Hour)) {
		t.Fatal("to the session reset while nothing is spent")
	}
	weekly := Usage{Session: limitAt(40, time.Hour), Weekly: limitAt(100, 48*time.Hour)}
	if !weekly.FreeAt(now).Equal(now.Add(48*time.Hour)) || !weekly.Limited(now) {
		t.Fatal("a spent weekly limit counts down to the weekly reset")
	}
	if !(Usage{Weekly: limitAt(10, 48*time.Hour)}).FreeAt(now).IsZero() {
		t.Fatal("with no session and nothing spent, nothing counts down")
	}
}

func TestTheTokenIsReadFromClaudeCodesCredential(t *testing.T) {
	token, err := TokenFrom([]byte(`{"claudeAiOauth": {"accessToken": "sk-ant-oat", "expiresAt": 1893456000000}}`), now, "it")
	if err != nil || token != "sk-ant-oat" {
		t.Fatalf("%q %v", token, err)
	}
	for _, bad := range []string{"", "{}", `{"claudeAiOauth": {}}`, `{"claudeAiOauth": {"accessToken": ""}}`} {
		var refused *CredentialRefused
		if _, err := TokenFrom([]byte(bad), now, "it"); !errors.As(err, &refused) {
			t.Errorf("%q: %v: not asked for again", bad, err)
		}
	}
	// Expired: unknown for now, not refused -- Claude Code refreshes it.
	var unknown *Error
	if _, err := TokenFrom([]byte(`{"claudeAiOauth": {"accessToken": "x", "expiresAt": 1}}`), now, "it"); !errors.As(err, &unknown) {
		t.Fatalf("%v", err)
	}
}

func TestTheRequestCarriesTheTokenAndFailuresAreKnownApart(t *testing.T) {
	status := http.StatusOK
	var seen *http.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r
		w.WriteHeader(status)
		w.Write([]byte(response))
	}))
	defer server.Close()
	saved := Client
	Client = &http.Client{Transport: rewrite{server.URL}}
	defer func() { Client = saved }()

	body, err := Fetch("tok")
	if err != nil {
		t.Fatal(err)
	}
	if seen.Header.Get("Authorization") != "Bearer tok" || seen.Header.Get("anthropic-beta") != "oauth-2025-04-20" {
		t.Fatalf("headers %v", seen.Header)
	}
	if _, err := Parse(body); err != nil {
		t.Fatal(err)
	}
	status = 429
	if _, err := Fetch("tok"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("429 = %v", err)
	}
	for _, code := range []int{401, 500} {
		status = code
		var unknown *Error
		if _, err := Fetch("tok"); !errors.As(err, &unknown) || errors.Is(err, ErrRateLimited) {
			t.Fatalf("%d = %v", code, err)
		}
	}
}

type rewrite struct{ base string }

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	if !strings.HasPrefix(req.URL.String(), URL) {
		panic("asked somewhere other than the usage endpoint")
	}
	out := req.Clone(req.Context())
	out.URL, _ = out.URL.Parse(r.base)
	return http.DefaultTransport.RoundTrip(out)
}

// -- the poller --------------------------------------------------------------

var reading = Usage{Session: &Limit{Percent: 7}, Weekly: &Limit{Percent: 9}}

func poller(answers ...error) (*Poller, *int) {
	asked := 0
	p := &Poller{interval: PollSeconds}
	p.Ask = func() (Usage, error) {
		asked++
		if len(answers) > 0 {
			err := answers[0]
			answers = answers[1:]
			if err != nil {
				return Usage{}, err
			}
		}
		return reading, nil
	}
	return p, &asked
}

func TestNothingIsAskedWhileNothingWantsIt(t *testing.T) {
	p, asked := poller()
	p.Tick(0, false)
	if *asked != 0 {
		t.Fatal("asked")
	}
}

func TestItAsksAtMostOnceAMinuteAndCollectsOnALaterTick(t *testing.T) {
	p, asked := poller()
	p.Tick(0, true)
	p.Wait(0)
	if *asked != 1 || p.Reading == nil {
		t.Fatalf("asked %d, reading %v", *asked, p.Reading)
	}
	p.Tick(30, true)
	if *asked != 1 {
		t.Fatal("not yet a minute")
	}
	p.Tick(60, true)
	p.Wait(60)
	if *asked != 2 {
		t.Fatal("a minute on")
	}
}

func TestAFailureKeepsTheLastReadingAndItGoesStale(t *testing.T) {
	p, _ := poller(nil, &Error{"offline"})
	p.Tick(0, true)
	p.Wait(0)
	p.Tick(60, true)
	p.Wait(60)
	if p.Reading == nil || p.LastError != "offline" {
		t.Fatalf("%v %q", p.Reading, p.LastError)
	}
	if p.Stale(StaleSeconds-1) || !p.Stale(StaleSeconds+1) {
		t.Fatal("five minutes without a reading is stale")
	}
}

func TestARefusedCredentialStopsTheAsking(t *testing.T) {
	p, asked := poller(&CredentialRefused{"no credential"})
	p.Tick(0, true)
	p.Wait(0)
	p.Tick(1000, true)
	if *asked != 1 || p.Refused == "" {
		t.Fatalf("asked %d, refused %q", *asked, p.Refused)
	}
}

func TestA429BacksOffAndRecoveryPollsAMinuteAfterTheRequestStarted(t *testing.T) {
	p, asked := poller(ErrRateLimited, ErrRateLimited)
	p.Tick(0, true)
	p.Wait(0) // 429: the next ask is two minutes on
	p.Tick(60, true)
	if *asked != 1 {
		t.Fatal("backed off")
	}
	p.Tick(120, true)
	p.Wait(120) // 429 again: four minutes on
	p.Tick(480, true)
	p.Wait(490) // a good answer, landing ten seconds after it was asked
	if p.Reading == nil {
		t.Fatal("known again")
	}
	p.Tick(539, true)
	if *asked != 3 {
		t.Fatal("too soon")
	}
	p.Tick(540, true)
	p.Wait(540)
	if *asked != 4 {
		t.Fatalf("asked %d: a minute after the last request started (#28, #31)", *asked)
	}
}
