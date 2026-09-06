# 6. The keypad may type, but only one keystroke

Date: 2026-09-06

## Status

Accepted

## Context

Fifteen keys for agents was always more than the grid was asked for. Twelve
leaves a row of four for keys that do something other than focus an agent, and
the first of those is dictation: hold a key, speak, let go.

Wispr Flow listens for a hotkey. On this machine its config records
`"61": "ptt"` -- keycode 61 is Right Option, held. That is an ordinary HID
modifier, which is the whole reason this is a small change. Flow's shipped
default is the `fn`/Globe key, and `fn` is **not** a keycode a USB keyboard can
send: it is handled inside Apple's own keyboard controller. Had the trigger still
been `fn`, no external keypad could have produced it at all.

Three ways to press a key the machine will believe:

1. **The daemon synthesises it** with a `CGEvent`, or `osascript` telling System
   Events to press it. Needs Accessibility permission -- a system dialog, granted
   to the terminal that happens to have launched the daemon, and silently lost
   when that changes. It also dies with the daemon, and the point of a hardware
   key is that it is hardware.
2. **The board types it**, as its previous firmware did: `salvage/code.py` is a
   four-layer HID keyboard, and `device/boot.py` already leaves USB HID enabled
   with a comment explaining that it is left on deliberately. No permission, no
   dialog, nothing to lose.
3. Something in between -- a helper, a hotkey daemon. More parts than the job.

## Decision

The board types it. The host says **which** key is the microphone key, by putting
`m` in that slot of the frame; the firmware decides **what** that means, which is
holding `Keycode.RIGHT_ALT` for as long as the key is held. That split is the one
already in use: a frame carries meaning, never colour, and the device decides
that `blocked` is red and blinks.

Slot 12 is the microphone key, 15 stays the function key, 13 and 14 are spare.
`AGENT_SLOTS` is `range(12)` and stays **contiguous**, because
`reducer.next_attention_target` rotates the attention queue with modular
arithmetic over `len(AGENT_SLOTS)` and would order the queue wrongly if the agent
slots had a hole in them.

Hold to talk, not tap to toggle. A toggle means the board holds a modifier down
between two presses, so every other keystroke in between becomes an Option chord,
and a board reset mid-dictation leaves it held with nobody to release it.

## Consequences

Dictation costs no permissions and no host code: the keystroke never crosses the
wire, and the daemon is not in the path between the finger and the microphone.

The modifier is released defensively, in three places: when the key comes up,
when a frame stops naming that key the microphone, and when the host goes quiet
past `HOST_TIMEOUT`. A modifier still held by a board whose daemon has died would
turn every later keystroke into an Option chord, which is a far worse failure
than a missed dictation.

Dictation stops when the daemon does, because the frame is what marks the key.
That was a deliberate choice over a device-side constant: it keeps every "which
key does what" decision on the host, as CONTEXT.md says, at the cost of a key
that goes inert when the keypad shows "no host". The board could be taught the
slot number instead, and then dictation would survive a dead daemon -- if that
ever matters more than the tidiness, it is a small change.

`adafruit_hid` is imported inside a `try`. Nothing in this repo installs `lib/`
-- provisioning writes `boot.py` and `code.py` and nothing else -- so a board
that arrives without the library must still light up. It loses dictation, not the
grid. Vendoring the library was the alternative; ADR 0003 argues against carrying
dependencies we can do without, and a keypad that lights up is worth more than
one that dictates.

Changing the hotkey means editing `device/code.py` and `make deploy`, like
`ROTATION`. That is the right cost for something that changes about as often.

The persisted slot map needs no migration: `SlotMap.load` already drops bindings
to slots outside `AGENT_SLOTS`, so a pane parked on 12-14 quietly takes a new key
on the next start. `STATE_VERSION` is deliberately **not** bumped, because that
would discard every binding rather than the three that became invalid.
