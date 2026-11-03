# 17. The daemon types for a board that cannot

Date: 2026-11-03

## Status

Accepted

Replaces ADR 0014's "Dictation stays on the Keybow". Amends ADR 0006: the
microphone's state moves to the host, though the Keybow still types for itself.

## Context

ADR 0014 left the CN001's microphone and return keys dark, because the CN001
cannot type and the host typing would reopen the Accessibility permission ADR
0006 avoided. That was right while the Keybow was always beside it. It is not:
the CN001 is used on its own, and dictation is half of what the feature row is
for.

Three ways to type for it were weighed:

1. **Relay through the Keybow.** The daemon tells the Keybow to press the key.
   No permission -- but no dictation on a CN001 used alone, which is the case
   that prompted this.
2. **The CN001's own firmware.** It does present a boot keyboard (usage page 1,
   usage 6, product string `HOTSPOTEKUSB HID DEMO`), but no open driver --
   mirajazz, Deckplate, Companion's -- knows a command that stores a keystroke
   on the board, and it types nothing on a press today. Even if one exists, the
   stock firmware cannot latch, time out, or make Return close the microphone;
   that logic would be on the host regardless.
3. **The daemon posts the keystroke**, with `CGEventPost`.

A spike settled whether 3 works at all. A synthetic Right Option (a
`flagsChanged` for keycode 61) opened a Flow dictation in 0.21s, every time,
and its release closed it. Killed while holding, the process left no Option
modifier stuck -- the next keystrokes typed plainly -- but left Flow listening
until something sent a release. A lone release with nothing held closed it at
once.

## Decision

The daemon types for a board that cannot, by posting events. The Keybow keeps
typing for itself.

There is one **microphone**, and the daemon owns it. Each board reports its
microphone and return keys going down and up; the daemon decides hold from tap,
the latch and its timeout, and that Return while the microphone is open only
closes it -- logic that moves off the Keybow rather than being written twice.
The frame says whether the microphone is open or latched, so every board's
microphone key shows it, and the Keybow holds Right Option only while the frame
says to.

Whichever board opened the microphone holds the key. A dictation started on the
Keybow costs no permission, and a Return on the CN001 closes it by sending the
Keybow a frame, not by posting anything.

The daemon sends one release of Right Option every time it starts. A crash
leaves Flow listening, launchd restarts the daemon, and the release ends the
orphaned dictation. It also releases on every exit it controls: a clean exit, a
signal, and the CN001 being unplugged.

On the CN001, the microphone and return keys are the two leftmost of its bottom
row; the third stays dark. Each draws its symbol in the microphone's hue, and
the microphone key pulses while latched. Without the permission their symbols
are greyed -- the key behind still shows the one microphone -- and a press
flashes.

The daemon asks for the permission itself, once a run, on the first press that
needs it, and logs which binary to allow. macOS grants it to whichever process
asks, so asking from `doctor` or a command in a terminal would grant the
terminal and leave the daemon without it. A press is the consent ADR 0004 asks
for: nothing is requested until someone has tried to dictate on the CN001.

## Consequences

The permission ADR 0006 avoided is back, for the CN001 only. Under launchd it
belongs to the Python binary, so an upgrade can lose it silently; the greyed
keys and the log line are how that is noticed.

The Keybow's firmware loses its latch and timeout, and the wire protocol gains
key releases, `hold` and `return` messages, and a microphone state in the
frame. That is protocol 2: a Keybow on older firmware is refused at the
handshake until `make deploy`. Two of its own releases -- the frame no longer
naming a microphone, the host going quiet -- stay, as a safety net under a host
that now decides.

A crash still costs a dictation that runs on until launchd restarts the daemon.
Flow's own maximum dictation length bounds it beyond that.

The gap ADR 0006 noted remains: if Flow ends a dictation by itself, the
microphone stays open in the frame until the next tap or the timeout.
