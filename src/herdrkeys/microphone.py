"""The microphone: one, however many boards are attached (ADR 0017).

Pure: no I/O and no clock of its own. What it decides reaches the machine
through a **typist** -- the board itself where it can type, the daemon where it
cannot -- and whichever board opened the microphone is the one that holds it.
"""

from __future__ import annotations

from typing import Protocol

# A press shorter than this latches the microphone open instead of closing it
# with your finger; anything longer is an ordinary hold. Long enough not to fire
# on a deliberate short phrase, short enough that a tap never feels like a wait.
LATCH_TAP_SECONDS = 0.4

# ...and a latch does not last forever. Nothing here can tell whether Wispr
# Flow is still listening, and a modifier held down for an afternoon turns every
# keystroke into an Option chord.
LATCH_TIMEOUT = 300.0


class Typist(Protocol):
    """Whatever presses keys for one board."""

    def hold(self, on: bool) -> None: ...
    def type_return(self) -> None: ...


class Microphone:
    def __init__(self) -> None:
        self._holder: Typist | None = None
        self._pressed_at = 0.0
        self._latched = False
        self._latched_at = 0.0

    @property
    def state(self) -> str:
        if self._holder is None:
            return "closed"
        return "latched" if self._latched else "open"

    def down(self, typist: Typist, now: float) -> None:
        """The microphone key went down: open it, or end a latch."""
        if self._latched:
            self._close()
            return
        if self._holder is not None:
            return  # held by another board's finger; a second holder is a second thing to forget
        self._holder = typist
        self._pressed_at = now
        typist.hold(True)

    def up(self, typist: Typist, now: float) -> None:
        """...and came up: a tap latches what a hold would have ended."""
        if self._holder is not typist or self._latched:
            return
        if now - self._pressed_at < LATCH_TAP_SECONDS:
            self._latched = True
            self._latched_at = now
            return
        self._close()

    def press_return(self, typist: Typist) -> None:
        """Close an open microphone, or type Return. Never both.

        While the microphone is open Flow has not yet inserted what it heard,
        so a Return now would send the prompt without it -- and with Right
        Option still down it would not even be a Return. Closing it is the
        useful half; the next press, once the text is there, sends it.
        """
        if self._holder is not None:
            self._close()
            return
        typist.type_return()

    def lost(self, typist: Typist) -> None:
        """A board went away. If it was holding the microphone, it closes."""
        if self._holder is typist:
            self._close()

    def tick(self, now: float) -> None:
        if self._latched and now - self._latched_at > LATCH_TIMEOUT:
            self._close()

    def next_change(self, now: float) -> float | None:
        """Seconds until a latch times out, so the loop wakes for it."""
        if not self._latched:
            return None
        return max(0.0, self._latched_at + LATCH_TIMEOUT - now)

    def close(self) -> None:
        """Let go, whoever is holding it. Safe to call when nothing is."""
        self._close()

    def _close(self) -> None:
        if self._holder is None:
            return
        holder, self._holder = self._holder, None
        self._latched = False
        holder.hold(False)
