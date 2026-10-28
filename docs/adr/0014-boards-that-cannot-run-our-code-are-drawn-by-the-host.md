# 14. Boards that cannot run our code are drawn by the host

Date: 2026-10-27

## Status

Accepted

Narrows ADR 0010's "the device decides what every meaning looks like" to
boards that run code of ours.

## Context

The Soomfon CN001 is a second board: fifteen keys behind one LCD panel, laid
out three rows by five, driven over vendor HID by sending each key a JPEG. It
runs the manufacturer's firmware and nothing else. There is no `code.py` to put
a palette, a blink or a saturation curve in.

Everything that made the Keybow design work still holds -- one frame, absolute,
carrying meaning; the daemon the only thing that decides -- except the claim
that the *device* turns meaning into light. On the CN001 something on the host
has to.

The CN001 is also not an LED. ADR 0010 saturates herdrcolor's pastels because
at idle duty on the Keybow green and teal differ by one count. An LCD shows
the sidebar's hex as it is, which is the whole reason for wanting this board:
its keys can be the same colour as the screen beside it.

## Decision

A board that can run our code decides what each meaning looks like on the
board. A board that cannot is given a renderer on the host, which owns that
board's palette, brightness and animation and nothing else. Both receive the
same frame; neither renderer knows about agents.

The CN001's renderer keeps the board's visual rules exactly: motion means this
agent wants you (ADR 0012), hue carries identity on `idle` and `done` and state
on `working` and `blocked`, brightness carries focus. What changes is the
values:

- a project colour is shown as herdrcolor's hex, unsaturated -- near full on
  `done`, darkened for `idle`, and brighter on focus;
- `working`, `blocked` and `unknown` are strong, saturated amber, red and blue:
  colours herdrcolor never produces.

So on this board a pastel always means *whose* and a saturated colour always
means *what*. The state colours were first going to be Catppuccin Mocha's
peach, red and blue, to match the screen -- but herdrcolor's palette *is* those
colours, so a red project's `done` would have been pixel-identical to
`blocked`, both blinking. The Keybow never had this collision only because it
saturates every project colour on the way to its LEDs.

Saturation of project colours stays a Keybow concern.

Dictation stays on the Keybow. The Keybow types Right Option itself (ADR 0006);
the CN001 cannot, and making the host type instead would reopen the
Accessibility permission ADR 0006 avoided. Its microphone and return keys are
dark until that is decided on its own.

## Consequences

There are now two places that say what `blocked` looks like, and they can drift.
They are kept honest by sharing the rules above, not the values.

A crashed daemon cannot blank the CN001. The Keybow drops frames older than six
seconds and shows "no host"; the CN001 keeps whatever it was last sent, and was
watched doing so for several minutes with no heartbeat -- it never gives up on
its own. A clean exit clears the panel; a crash leaves stale states lit, and
the README says so.

The strip of three displays to the right of the keys reports nothing when
pressed, so it holds no slot and stays dark.

The CN001 shows the first ten slots of the grid in its top two rows and the
feature row along its bottom. The eleventh and twelfth agents are on the Keybow
only, but the function key pressed on either board still reaches them: there is
one frame and one attention order.
