# 11. Working breathes its project colour

Date: 2026-09-19

## Status

Superseded by ADR 0012, after testing on the board.

The retreat this ADR predicted -- "if it proves wrong on a busy board, the
cheapest retreat is not to restore amber but to make `blocked` louder" -- was
itself wrong. Restoring amber is exactly what was needed, because the problem
was never contrast between two moving things; it was that motion had stopped
meaning anything in particular.

## Context

ADR 0010 put the project colour on `idle` and argued that `idle` was the one
state that could afford to give up its hue. In use that turns out to be half
the answer. An idle agent is one you are not waiting on; the board spends much
of its time showing agents that *are* working, and those were all amber —
identical to each other, so the colour told you nothing exactly when several
things were running at once and knowing which was which was worth most.

`working` has the same property that made `idle` safe: it is not asking you for
anything. Nothing is lost by spending its hue on identity, provided "working"
still reads as a state.

The difficulty is that there is nothing left to say it with. Hue is identity on
these two states now. Brightness is focus. That leaves motion, and motion was
reserved — `device/code.py` has carried the rule since the first version:

> Motion is reserved for `blocked`: it is the one state with a human waiting on
> it, and if anything else moved, movement would stop meaning "look at me".

## Decision

A `working` key breathes its project colour. Without one it breathes amber: the
motion is the state, so `working` reads the same way whether or not herdrcolor
is installed. Motion that meant "has a colour" would be motion that means
nothing.

The reserved-motion rule is therefore broken deliberately, and replaced with a
narrower one: **something that blinks to black is asking for you; something
that breathes is alive.** The two waveforms are kept as unalike as the hardware
allows:

| state     | waveform                        | reads as |
| --------- | ------------------------------- | -------- |
| `blocked` | hard on/off to black, 1.4Hz     | an alarm |
| `working` | smooth breath, 1.8s, never below 45% | a pulse |

`working` is also slower than the microphone latch (0.9s, down to 30%), which
was already a second moving thing on this board and is the precedent that the
rule was never quite absolute.

Brightness ordering is untouched, because each identity colour is scaled to the
state colour it replaces: idle 25, working 120, done 170. So when every key on
the board happens to be the same hue, brightness still says which state each
one is in.

## Consequences

The thing this costs is named plainly: with six agents working, six keys are
moving at once, and a blocked key now has to *win* against movement rather than
own it. That is a real regression against the original design, accepted because
identity on the busy states is worth more in practice than an exclusive channel
was in principle. The mitigations are the waveform gap above and the fact that
`blocked` is the only thing that reaches black, which is a much louder signal
than rate alone.

If it proves wrong on a busy board, the cheapest retreat is not to restore
amber but to make `blocked` louder — a faster blink, or a brief full-brightness
flash on entry — because the problem would be contrast, not colour.

`done` and `unknown` still do not take part, for the reason ADR 0010 gave:
`done` is the other state the function key jumps to, and `unknown` is a fault
report rather than an agent at work.

The TUI can show the colour but not the breath, since it redraws per frame
rather than animating. So `idle` and `working` are distinguishable there only
by the state letter, which is a gap in the preview rather than in the board.

Firmware `0.5.0`. The protocol does not move: nothing new crosses the wire, and
a host that speaks `0.4.0`'s frames is speaking this one's too. An un-redeployed
board simply keeps showing amber.
