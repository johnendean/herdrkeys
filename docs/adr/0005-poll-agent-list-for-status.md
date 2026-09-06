# 5. Agent status is polled, not subscribed

Date: 2026-09-06

## Status

Accepted

## Context

The keys showed the right colours but changed them five to ten seconds after the
agent did, in every state -- including `blocked`, which is exempt from settling
precisely so a human waiting on an approval is not kept waiting by us. That
exemption is what pinned the fault: everything downstream of `HerdrState` was
already fast, and the delay was in learning the status at all.

Measured against a live session (herdr 0.8.2), subscribing exactly as the daemon
does and polling `session.snapshot` alongside it:

* `events.subscribe` replays its backlog at almost exactly **ten events per
  second** -- 82 events, 0.10s apart, ending on each pane's current revision
  after 8.4 seconds. ADR 0002 saw the same pacing at 94 events in four seconds.
* **After the replay the stream went silent.** In the following 140 seconds it
  delivered nothing at all, while polling saw five real status changes:
  `w2:p1 -> blocked`, then `w1:p1` walking `working -> idle -> working ->
  blocked`. A per-pane `pane.agent_status_changed` subscription, opened at the
  same time, delivered nothing either.

So the LEDs were never being driven by the event stream. They were being driven
by the 30-second `session.snapshot` reconcile, which is exactly a nought-to-
thirty-second lag, and exactly what was being seen.

`agent.list` returns `pane_id`, `agent`, `agent_status` and `focused` for every
agent pane, and costs **0.22ms per call** measured over twenty calls, each on a
fresh connection. Polling it twice a second is roughly a tenth of a percent of a
core.

## Decision

Agent status comes from polling `agent.list` every `status_poll_ms` (default
500ms). The event stream is kept, unchanged, as an accelerator: when an event
does arrive it is folded immediately, and it still carries pane lifetime and
focus. `session.snapshot` remains the periodic authority for which panes exist.

The poll is authoritative about *state* and silent about *lifetime*. A pane
absent from `agent.list` has no agent, so it goes dark -- but it is not
forgotten, and it keeps its slot until the pane itself closes (ADR 0002).

## Consequences

A key now follows its agent within about the poll interval plus the settle
window -- under a second, against five to ten before -- and that no longer
depends on the event stream behaving. `blocked` gets its promised fast path back.

The daemon now wakes twice a second while connected rather than once, and makes
two socket round trips a second it did not make before. At 0.22ms each that is
not a cost worth optimising, but it is a cost: `status_poll_ms` exists for
anyone who disagrees.

It also fixes a smaller staleness window nobody had noticed. For up to ten
seconds after connecting, the daemon deliberately discards every event as
replayed history (ADR 0002); the poll carries truth through that gap.

Polling contradicts nothing in ADR 0002 -- it is the same conclusion, followed
further. That ADR already treats the stream as untrustworthy enough to need
reconciling; this one stops treating it as a feed at all.
