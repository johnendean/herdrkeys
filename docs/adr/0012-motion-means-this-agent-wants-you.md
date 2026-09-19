# 12. Motion means this agent wants you

Date: 2026-09-19

## Status

Accepted

Supersedes ADR 0011, which gave `working` the project colour and a breath.
Amends ADR 0010, whose central claim -- that `done` must keep a hue of its own
-- is reversed here.

## Context

ADR 0011 was tried on the board and did not survive contact. It put the project
colour on `working` and gave that state a slow breath to keep it apart from
`idle`. Tested deliberately -- five keys breathing, then eleven, with one
`blocked` among them -- the answer was that it did not help. Motion spread
across the busy states does not make a board more informative; it makes it
harder to find the one key that matters.

The mistake is visible in hindsight in ADR 0011's own reasoning, which argued
that `working` "has the same property that made `idle` safe: it is not asking
you for anything". True, and irrelevant: the question is not which states can
*afford* to give up their hue, but what the board is for. A key that is working
does not need to say whose it is, because you are not going to press it. A key
that has *finished* does.

## Decision

Motion means one thing: this agent wants you.

| | steady — needs nothing | blinking — wants you |
| --- | --- | --- |
| **whose** | `idle` — project colour | `done` — project colour |
| **what** | `working` — amber | `blocked` — red |

The two blinking states are exactly `ATTENTION_ORDER`, the pair the function key
walks. So the board is read in two passes: anything moving needs you, and its
hue says which kind of needing -- your project's colour for work finished and
unseen, red for an agent stopped and waiting. Anything steady needs nothing,
and its hue says whether it is yours and idle, or merely busy.

Hue therefore carries identity on `idle` and `done`, the two states where
knowing *which* agent is the useful thing, and state on `working` and
`blocked`, where what is happening matters more than to whom.

`done` and `blocked` blink at the same rate and both to black. They are one
signal -- come here -- and the hue distinguishes them. Two rates would make the
board something to decode rather than notice, which is the error ADR 0011 made
in a different form.

`working` returns to steady amber exactly as it was before ADR 0011.
`unknown` keeps its dim blue: a fault report, not an agent at work.

## Consequences

`idle` and `done` now share a hue, which ADR 0010 explicitly refused. What
separates them is brightness and motion together, not either alone: done is
nearly seven times brighter (170 against 25) *and* it is the only one moving.
That is a wider gap than the one ADR 0010 was protecting against, where idle
and done would have been separated by brightness while focus was also using it.

Focus still multiplies brightness by 2.5, so a focused idle key reaches 62 --
still far below done's 170, and still steady.

A project whose colour is red is the closest this gets to ambiguity: its `done`
renders `(170, 46, 80)` against blocked's `(150, 0, 0)`, both blinking. They are
146 counts apart, brighter and markedly pinker, which is a far wider margin than
the 12 counts that separate the closest pair of idle colours -- but it is the
pair to watch if herdrcolor's palette ever changes.

The board now moves less than at any point since ADR 0010: only agents that
want you. That is the property ADR 0011 spent and this one buys back, at the
price of `working` telling you nothing about whose work it is.

Firmware `0.6.0`. The protocol does not move; nothing new crosses the wire.
