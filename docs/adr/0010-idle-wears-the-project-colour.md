# 10. Idle wears the project colour

Date: 2026-09-18

## Status

Accepted, and amended by ADR 0012. Its reasoning about the wire format, the
saturation of the palette and the fallback still holds. Its decision that
`done` must keep a hue of its own does not: ADR 0012 gives `done` the project
colour too, and separates it from `idle` by blink and brightness together.

## Context

[herdrcolor](https://github.com/johnendean/herdrcolor) gives every project a
colour in Herdr's sidebar, and publishes the assigned hex as `tokens.color` on
each pane. Its palette module says why that field exists: six hues, "about as
many as stay distinguishable at a glance in a sidebar -- and, later, on RGB
LEDs". This board was the reason.

The difficulty is that the board has no spare channel. `device/code.py` has
carried the rule since the first version: "One hue per meaning. Focus is shown
by brightness, never by hue, so hue only ever means state. Motion is reserved
for `blocked`." Hue means state, brightness means focus, motion means blocked.
Identity has nowhere to go that is not already carrying meaning.

Three ways out were considered:

1. **Identity always; state moves to motion.** Every key its project's colour,
   `working` pulsing, `blocked` blinking faster. Rejected: it pays for identity
   with the two states you most need to read from across a room, and it ends
   "motion means look at me".
2. **Tint** -- state hue with the project colour blended in. Rejected on the
   firmware's own evidence: it already documents `(16,16,16)` reading "strongly
   magenta" because the green die is least efficient, so a blend at these duty
   levels is not a colour anyone can name.
3. **Identity on the quiet states only.**

## Decision

An `idle` key wears its project's colour instead of dim green. Every other
state keeps the hue it had.

`idle` is the one state that can afford it: it wants nothing from you, so
nothing is lost by spending its hue on identity. `done` deliberately does not
take part, though it is equally quiet -- it is one of the two states the
function key jumps to (`ATTENTION_ORDER`), so it has to read as a state. That
also keeps brightness from carrying three meanings at once: idle and done stay
separable by hue, and focus keeps brightness to itself.

The colour crosses the wire as hex, on the frame message, one entry per key:

    {"v":1,"t":"frame","k":"iWiii-------mr-f","c":["#89b4fa","#a6e3a1",...]}

`keys` carries meaning and `colours` carries data. The device still decides
what every *meaning* looks like, including which states wear a project colour
at all; what it cannot decide is what colour a project is, because that is not
a meaning -- it is a fact only the host can know.

Both travel on one message so a frame stays absolute: there is no way for a
key's colour to arrive out of step with the meaning it belongs to.

The protocol version does **not** move. The field is additive in both
directions: old firmware ignores it, new firmware reads its absence as "no
colours", and `encode_frame` omits it entirely when no key has one, so a board
sees byte-for-byte what it saw before herdrcolor existed. Bumping would have
been worse than useless here -- a board with old firmware still presents a data
port, so `_maybe_provision` (which only fires when no port is found) never
runs, and a version mismatch would leave the board dark indefinitely behind a
single log line.

The pastels are saturated on the device before being scaled to idle
brightness. Measured across herdrcolor's six at idle duty, the raw channel
spread is 6-12 counts out of 25 and green against teal differs by **one**: six
dim whites. Pushed 1.7x towards their dominant channel the closest pair is 12
counts apart. Doing it on the device is the same rule as the rest of that file:
what a colour must become to survive these particular LEDs is the device's
problem, and only the device knows it.

## Consequences

A quiet board now says *which* agents are waiting rather than only that they
are -- which matters more since ADR 0009, because the keys renumber as agents
come and go, so "which key is this?" is a question asked more often. It is
asked when the board is quiet, which is exactly when the colour is showing.

The colour is a **project** colour, not an agent colour: herdrcolor keys it on
the basename of the pane's directory. Two agents in one repository therefore
wear the same colour and two keys look alike. This is not worked around. The
sidebar shares the colour too, so nothing the board says is untrue, and the
alternative -- herdrkeys assigning its own per-agent colours -- would disagree
with the screen it exists to agree with.

Colour settles with the order rather than separately. herdrcolor recolours a
project when a *colliding* project appears or goes away, so a key can change
colour because of an agent somewhere else entirely: the same shape of event as
a reorder, and it must not flicker for the same reason. `OrderSettler` now
holds `(pane_id, colour)` pairs.

Absence of a colour is read as silence, never as a retraction. Herdr omits
`tokens` from some pane payloads exactly as it omits `cwd`, and treating that
as "no colour" would drop a key back to green until the next payload that
happened to carry one. A pane's colour goes away when the pane does.

A key falling back to its state colour and a key whose agent simply has no
colour are indistinguishable on the board, by design. `doctor` reports the
difference, because nothing else can.

`code.py` no longer keeps the rule it opens with, and says so: hue means state,
with `idle` named as the exception.
