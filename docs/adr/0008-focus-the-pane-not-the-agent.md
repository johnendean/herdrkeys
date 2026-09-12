# 8. Focus the pane, not the agent

Date: 2026-09-12

## Status

Accepted

Amends ADR 0001, which chose `agent.focus`.

## Context

The keys lit correctly and pressing one did nothing visible. The terminal's
title bar changed to the name of the workspace the key pointed at, and the
screen stayed where it was.

That title bar is the whole diagnosis in miniature: the request arrived, Herdr
acted on it, and the part a human actually looks at did not move.

What the evidence showed:

* Herdr's server log recorded every press: `method="agent.focus"`,
  `changes_ui=true`, then `workspace focused outcome="ok"` and `tab focused
  outcome="ok"` for the right target, then `outcome="ok"`. Nothing failed, and
  nothing was slow.
* So the input path was healthy. This is the case ADR 0007 said to watch for --
  a keypad that lights up correctly and ignores presses on a daemon carrying
  that change -- but the conclusion it predicted was wrong. The selector was
  exonerated and the fault was not lower down at the descriptor or the port. It
  was downstream of the daemon entirely: the presses were read, sent, and
  answered, and the API call itself no longer did what it used to.
* Driving `agent.focus` by hand and polling `session.snapshot` for six seconds:
  focus moved to the target and **stayed** there. So the server and the screen
  simply disagreed, persistently, about what was focused.
* The client log dated the change exactly. Up to 2026-09-06 every attach logged
  `handshake succeeded version=20 encoding=SemanticFrame`. From 2026-09-11
  16:33, `endpoint handshake succeeded generation=1 server_version=0.9.0`.

Herdr 0.9.0 moved the terminal UI into each client so that clients can view
workspaces independently. `agent.focus` did not come with it. Upstream is
herdr#3760: it is fixed on master and, as of 0.9.0, unreleased -- so it cannot
be waited out. The issue's comments establish the part that matters here, which
is that the regression is specific to that one method:

| call | server state | viewport |
|---|---|---|
| `agent.focus` | updated | **does not move** |
| `workspace.focus` | updated | moves |
| `tab.focus` | updated | moves, including across workspaces |
| `pane.focus` | updated | moves, landing on the exact pane |

There is no client identity anywhere in the 0.9.0 focus API, so there is no
supported way to aim a focus call at one client; the calls that do propagate
move every attached client. That is not a problem we have -- there is one
keypad and one person looking at one screen -- but it is why the fix is a
different method and not a new parameter.

## Decision

Send `pane.focus` with the agent's pane ID. It is the only call that must
succeed, and it goes first.

Then send `agent.focus` and ignore its failure. ADR 0001 went through the agent
surface because that is what marks an agent seen, which is what turns `done`
back into `idle`, and the keys are drawn from the server's seen state as
`agent.list` reports it. It still does that server-side, and it costs a
fraction of a millisecond. It is second because it is the expendable one: a
pane outlives the agents that occupy it, so a frame can name a pane whose agent
has since gone, and the `agent_not_found` that follows must not cost us the
navigation already done.

Note that `agent.focus` had in practice stopped marking anything seen the
moment the viewport stopped following it -- a completion is seen when a client
displays it. Restoring the viewport hop is what restores `done` collapsing to
`idle`, not the second call.

## Consequences

Pressing a key moves the screen again.

ADR 0001 stands: navigation is still absolute, still over the socket, and the
keypad still types nothing. Only the method changed.

When herdr#3760 ships, nothing here needs undoing -- `pane.focus` is not a
workaround for a broken call, it is the documented way to focus a pane by ID,
and it will keep landing on the exact pane in a split tab where `agent.focus`
lands on the tab's focused one.

The wire-level detail is now pinned by tests (`tests/test_focus.py`) that drive
a real unix socket and assert on the method names. Everything above that level
fakes `focus_agent` whole and would pass just as happily against the broken
call, which is exactly how this got out: no test here ever failed.

**The general lesson is that `outcome="ok"` from Herdr means the request was
accepted, not that anything happened.** Both this and ADR 0007 were a day of
looking at healthy green signals. When the complaint is "nothing moves", stop
reading logs early and find the one observable the user is actually describing
-- here, that server focus and screen focus could be read separately and
compared, which settled it in one command.
