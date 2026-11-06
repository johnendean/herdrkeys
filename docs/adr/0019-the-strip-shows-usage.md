# 19. The strip shows usage

Date: 2026-11-05

## Status

Accepted

Takes up the **strip** that ADR 0014 left dark. Adds a second outside source
beside Herdr. The frame, the Keybow and the TUI are unchanged.

## Context

The CN001 has three small displays to the right of its keys. They report
nothing when pressed, so they hold no slot and have stayed dark. They take the
same 96-pixel JPEG as a key and turn it the same quarter clockwise, cropping a
few pixels at the edges (measured on the board).

The thing most worth glancing at while agents work, that the keys do not
already say, is **usage**: how much of the Claude plan's 5-hour session and
weekly limits has gone, and when they reset. Small ESP32 desk displays exist for
exactly this.

Usage does not come from Herdr. Until now the daemon talked to Herdr all the
time and to anything else only on a press (ADR 0013, the git key).

## Decision

**While a CN001 is attached, the daemon polls Anthropic for usage once a
minute, off the loop's thread.** It asks the endpoint Claude Code's own
`/usage` uses (`api.anthropic.com/api/oauth/usage`), with `urllib`, so no new
dependencies come in (ADR 0003). No Keybow-only setup ever makes the request,
because nothing there would show it.

**The credential is Claude Code's, and it is read and never written.** The OAuth
access token comes out of the `Claude Code-credentials` Keychain item through
`security`. herdrkeys never refreshes it. Claude Code refreshes it while it
runs, and refreshing it here would mean writing another program's credential,
racing its own refresh. An expired token is answered as "usage unknown" until
Claude Code next refreshes it.

**macOS's Keychain prompt is the consent.** The strip is on by default (`usage
= false` turns it off). Unlike provisioning (ADR 0004) nothing is written. A
refused or missing credential stops the asking for the rest of the run, so that
a dialog never returns once a minute, and `doctor` names the reason.

**The endpoint is undocumented, and treated that way.** Any response that does
not parse is "usage unknown", logged once per distinct reason. It never counts
as a daemon failure. A 429 backs the polling off.

**What the strip shows**, top to bottom:

| display | shows                                                       |
| ------- | ----------------------------------------------------------- |
| top     | the 5-hour session: a gauge filling upwards, and its percent |
| middle  | the weekly limit, the same way                              |
| bottom  | how long until you can work again                           |

The bottom display counts down to the session's reset. Once a limit is spent it
counts down to *that* limit's reset, since that is when work can start again.
With no session in progress the top gauge is empty and the countdown is a dash.
A reset that has already passed is shown as reset, without waiting for the next
poll.

Colour follows the board's rule that a saturated colour means *what*. A gauge
is a neutral light grey, amber from 80%, and red only once the limit is spent.
The countdown turns red with it. Nothing on the strip ever moves (ADR 0012).

**Unknown and stale are told apart.** The strip stays dark until usage has been
known once. After that it always shows the last reading, drawn in dim grey once
that reading is more than five minutes old: dark means never known, grey means
"this is what it was".

Digits and letters are a 5x7 bitmap font, scaled and drawn in pure Python like
the marks (ADR 0016). That keeps it testable anywhere, with no CoreText.

## Consequences

The daemon now holds a Claude credential in memory, and makes a network request
every minute while a CN001 is plugged in. Both are bounded: the request is made
only while there is somewhere to show the answer, and the token is read fresh
each time and kept no longer than that.

When the endpoint changes shape, the strip goes grey and the log says why, once.
Fixing it is one parser.

A crash leaves the strip showing its last reading, as it leaves the keys (ADR
0014). A clean exit blanks it with the rest of the panel.
