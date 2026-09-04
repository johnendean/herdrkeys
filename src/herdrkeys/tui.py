"""A keypad made of text.

Same `Device` interface as the real board, so the whole daemon can be driven
with no hardware attached: frames are drawn as a 4x4 grid and typing a hex digit
(0-9, a-f) counts as a key press.

The grid mirrors the physical board: PMK numbers keys row-major (x = n % 4,
y = n // 4) with key 0 bottom-left, so slot 15 is top-right.
"""

from __future__ import annotations

import sys
import termios
import tty

from .model import EMPTY, FN_DISCONNECTED, SLOT_COUNT, Frame

RESET = "\x1b[0m"

# Terminal stand-ins for the palette the firmware owns.
SWATCH = {
    "i": ("\x1b[38;5;22m", "idle"),
    "I": ("\x1b[38;5;46m", "idle*"),
    "w": ("\x1b[38;5;130m", "working"),
    "W": ("\x1b[38;5;214m", "working*"),
    "b": ("\x1b[38;5;88m", "blocked"),
    "B": ("\x1b[38;5;196m", "blocked*"),
    "d": ("\x1b[38;5;28m", "done"),
    "D": ("\x1b[38;5;82m", "done*"),
    "u": ("\x1b[38;5;17m", "unknown"),
    "U": ("\x1b[38;5;33m", "unknown*"),
    EMPTY: ("\x1b[38;5;236m", ""),
    "f": ("\x1b[38;5;245m", "fn"),
    FN_DISCONNECTED: ("\x1b[38;5;124m", "offline"),
}


def draw(frame: Frame) -> str:
    """The frame as a 4x4 grid, key 0 bottom-left, numbering along each row."""
    lines = []
    for row_from_top in range(4):
        row = 3 - row_from_top
        cells = []
        for column in range(4):
            slot = row * 4 + column
            code = frame.keys[slot]
            colour, label = SWATCH.get(code, ("", code))
            cells.append(f"{colour}[{slot:>2} {code} {label:<8}]{RESET}")
        lines.append(" ".join(cells))
    return "\n".join(lines)


class TuiDevice:
    """Draws frames to the terminal; hex digits typed on stdin are key presses."""

    def __init__(self, stream=None) -> None:
        self._out = stream or sys.stdout
        self._in = sys.stdin
        self.firmware = "tui"
        self._saved: list | None = None
        if self._in.isatty():
            self._saved = termios.tcgetattr(self._in)
            tty.setcbreak(self._in.fileno())

    def fileno(self) -> int:
        return self._in.fileno()

    def send_frame(self, frame: Frame) -> None:
        self._out.write("\x1b[H\x1b[2J")
        self._out.write("herdrkeys - press 0-9 a-f to simulate a key, ctrl-c to quit\n\n")
        self._out.write(draw(frame) + "\n\n")
        self._out.write(f"frame: {frame.keys}\n")
        self._out.flush()

    def flash(self) -> None:
        self._out.write("\a[flash] nothing wants your attention\n")
        self._out.flush()

    def read_presses(self) -> list[int]:
        data = self._in.read(1)
        if not data:
            return []
        try:
            slot = int(data, 16)
        except ValueError:
            return []
        return [slot] if 0 <= slot < SLOT_COUNT else []

    def close(self) -> None:
        if self._saved is not None:
            termios.tcsetattr(self._in, termios.TCSADRAIN, self._saved)
            self._saved = None
