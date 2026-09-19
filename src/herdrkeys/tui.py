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

from .model import EMPTY, FN_DISCONNECTED, MIC, SLOT_COUNT, Frame

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
    MIC: ("\x1b[38;5;44m", "mic"),
}


def identity_ansi(colour: str | None) -> str | None:
    """A `#rrggbb` project colour as 24-bit ANSI, or None if it is not one.

    Shown raw, without the saturation the firmware applies: a terminal is not
    an LED, and pretending otherwise would make this a worse preview, not a
    better one. What the TUI can show is *which* key wears *which* colour; only
    the board can settle whether two of them are far enough apart.
    """
    if not colour or len(colour) != 7 or not colour.startswith("#"):
        return None
    try:
        int(colour[1:], 16)
    except ValueError:
        return None
    r, g, b = (int(colour[i : i + 2], 16) for i in (1, 3, 5))
    return f"\x1b[38;2;{r};{g};{b}m"


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
            # `idle` and `done` wear a project colour, exactly as on the
            # board: the TUI is a preview of the keypad, so it must not show a
            # key a colour the keypad would not. What it cannot show is the
            # blink that tells those two apart -- the grid is redrawn per
            # frame, not animated -- so the state letter is doing that work
            # here in a way it does not have to on the board.
            if code.lower() in ("i", "d"):
                identity = identity_ansi(frame.colours[slot])
                if identity is not None:
                    colour = identity
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
