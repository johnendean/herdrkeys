# 16. The CN001 draws a mark for state

Date: 2026-11-03

## Status

Accepted

Amends ADR 0014's CN001 renderer. The Keybow, the frame and the protocol are
unchanged.

## Context

ADR 0014 gave the CN001 the Keybow's visual rules with new values: hue carries
identity on `idle` and `done` and state on `working` and `blocked`, brightness
carries focus, and motion means this agent wants you (ADR 0012). Those rules
were shaped by an LED, which has one colour and a brightness and nothing else.
Hue had to do two jobs because there was no other channel.

The CN001 is an LCD and draws a JPEG per key. ADR 0015 kept a real image
encoder for exactly this reason: its keys can show more than a flat colour. So
on this board *what* an agent is doing can be drawn instead of coloured, and
colour is free to say *whose* in every state -- which is what ADR 0011 wanted
for `working` and could not have on an LED.

## Decision

On the CN001's grid, a key's background is its project colour in every state,
at herdrcolor's hex exactly as the sidebar shows it, and its **mark** says what
the agent is doing:

| state     | mark                      | badge colour |
| --------- | ------------------------- | ------------ |
| `idle`    | none                      | --           |
| `working` | frozen eight-spoke spinner | amber        |
| `blocked` | `!`                       | red          |
| `done`    | tick                      | green        |
| `unknown` | `?`                       | blue         |

Each mark is drawn in its state's saturated colour on a dark disc, so it reads
on a light or dark project colour alike, and ADR 0014's rule survives in a new
place: a pastel always means whose, a saturated colour always means what.
`blocked` is still red on both boards.

A key whose agent has no project colour is neutral grey behind its mark.

Motion is unchanged. `done` and `blocked` blink the whole key to black, at the
same rate, and nothing else moves. The spinner is frozen: a turning one would
be motion that does not mean "wants you", which is the mistake ADR 0011 made.

Brightness stops carrying anything per key. `idle` and `done` no longer need it
to stay apart -- one has a tick and blinks -- so every background is the true
hex, and focus is shown by a light border round the key instead.

Marks are simple geometric shapes rasterised in pure Python into the key's
pixels before encoding. SF Symbols or text through CoreText and CoreGraphics
were considered: smoother, but a much larger `ctypes` surface where a wrong
signature is a crash (ADR 0015), bought for anti-aliasing that a few bold
shapes at 96 pixels do not need. Pure Python marks are testable anywhere, like
the rest of the renderer.

Only slots 0-9 change. The git and function keys keep their flat colours until
the feature row is considered on its own.

## Consequences

`working` keys now say whose work it is without moving, so a busy board is
more informative and no noisier.

`blocked` and `done` both blink and both wear the project colour; what tells
them apart is the mark's shape and the badge's hue. That is a smaller area of
red than a whole key, and is the pair to watch on the real panel.

The spinner is the most detailed mark. If it does not read on the panel, the
fallback is an open arc, and changing it is one drawing function.

Nothing crosses the wire that did not before: the state codes already say
everything a mark needs. The Keybow and the TUI are unaffected by construction,
not by care.

The panel turns every image 90 degrees clockwise, so anything that is not a
flat fill must be turned anticlockwise before it is encoded.
