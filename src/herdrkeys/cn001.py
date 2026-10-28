"""The Soomfon CN001, a board that cannot run our code.

Fifteen keys behind one LCD panel, three rows by five, plus a narrow strip of
three more displays on the right. It speaks Mirabox's "v3" vendor HID protocol:
every output report is 1024 bytes beginning `CRT\\0\\0` and a command; every
input report is 512 bytes beginning `ACK\\0\\0OK\\0\\0`, then the key and whether
it went down or up.

There is no firmware of ours on it, so the host decides what each meaning looks
like -- this module is to the CN001 what `device/code.py` is to the Keybow.
See ADR 0014.

Commands, report parsing, where each slot sits and what each meaning looks
like are pure; `CN001Device` at the bottom is the only part that touches the
board.
"""

from __future__ import annotations

import time

from .device import DeviceError
from .macos import HidDevice, encode_jpeg, find_hid, release
from .model import FN_SLOT, GIT_SLOT, Frame

VENDOR_ID = 0x1500
PRODUCT_ID = 0x3003
USAGE_PAGE = 0xFFA0  # the vendor interface; its boot keyboard is never opened

REPORT_SIZE = 1024
INPUT_SIZE = 512
KEY_PIXELS = 96

# The hardware numbers its keys 1-15 column by column, starting at the top of
# the rightmost column; the strip is 16-18. The same number addresses a key's
# image and names it in a press.
#
#     13 10  7  4  1 | 16
#     14 11  8  5  2 | 17
#     15 12  9  6  3 | 18
ROWS = 3
COLUMNS = 5


def hardware_key(row: int, column: int) -> int:
    """Row 0 is the top, column 0 the left."""
    return (COLUMNS - 1 - column) * ROWS + row + 1


# Agents read like the agents pane beside it: the first ten, left to right
# across the top two rows. The feature row is the bottom row, with the git and
# function keys at its right-hand end as on the Keybow; the three to their left
# are kept for dictation, which stays on the Keybow (ADR 0014).
SLOT_TO_KEY: dict[int, int] = {
    **{slot: hardware_key(slot // COLUMNS, slot % COLUMNS) for slot in range(10)},
    GIT_SLOT: hardware_key(2, 3),
    FN_SLOT: hardware_key(2, 4),
}
KEY_TO_SLOT = {key: slot for slot, key in SLOT_TO_KEY.items()}
ALL_KEYS = tuple(range(1, ROWS * COLUMNS + 1))


def _command(name: bytes, *args: int) -> bytes:
    return (b"CRT\0\0" + name + bytes(args)).ljust(REPORT_SIZE, b"\0")


def wake() -> bytes:
    return _command(b"DIS")


def brightness_report(percent: int) -> bytes:
    return _command(b"LIG", 0, 0, max(0, min(100, percent)))


def clear(key: int = 0xFF) -> bytes:
    """Blank one key, or every key and the strip."""
    return _command(b"CLE", 0, 0, 0, key)


def commit() -> bytes:
    """Nothing drawn or cleared appears until this is sent."""
    return _command(b"STP")


def heartbeat() -> bytes:
    return _command(b"CONNECT")


def image(key: int, jpeg: bytes) -> list[bytes]:
    """The reports that put `jpeg` on `key`: a header, then the bytes in chunks."""
    size = len(jpeg)
    reports = [_command(b"BAT", (size >> 24) & 0xFF, (size >> 16) & 0xFF, (size >> 8) & 0xFF, size & 0xFF, key)]
    for start in range(0, size, REPORT_SIZE):
        reports.append(jpeg[start : start + REPORT_SIZE].ljust(REPORT_SIZE, b"\0"))
    return reports


def parse_input(report: bytes) -> tuple[int, bool] | None:
    """(hardware key, went down) for a key report; None for anything else."""
    if not report.startswith(b"ACK\0\0OK\0\0") or len(report) < 11:
        return None
    key, state = report[9], report[10]
    if key == 0:
        return None
    return key, state == 1


# -- what each meaning looks like -------------------------------------------
#
# The same rules as device/code.py, for a different surface (ADR 0014). Motion
# means this agent wants you: `done` and `blocked` blink to black, at the same
# rate, and nothing else on the grid moves. Hue carries identity on `idle` and
# `done` and state on `working` and `blocked`. Brightness carries focus.
#
# What differs is the values. This is an LCD, not an LED, so a project colour
# is shown as the sidebar's hex rather than saturated to survive low duty. And
# the state colours are strong and saturated, because herdrcolor's palette *is*
# Catppuccin's pastel red, peach and blue: a pastel always means whose, a
# saturated colour always means what, and a red project's `done` never matches
# `blocked`.

RGB = tuple[int, int, int]

OFF: RGB = (0, 0, 0)
STATE_COLOUR: dict[str, RGB] = {
    "i": (0, 200, 0),      # idle, when its agent has no project colour
    "w": (240, 140, 0),    # working       amber
    "b": (224, 0, 0),      # blocked       red, blinks
    "d": (0, 200, 0),      # done, when its agent has no project colour
    "u": (32, 58, 128),    # unknown       blue
}
# How bright each state sits when unfocused, as a share of its colour. Focus
# multiplies it, so a focused key is the brightest version of what it is. An
# idle key is dark and a done key bright, which -- with only one of them moving
# -- is what keeps the two apart when they wear the same project colour.
STATE_LEVEL = {"i": 0.3, "w": 0.5, "b": 0.85, "d": 0.85, "u": 0.4}
FOCUS_GAIN = 2.5

FN_IDLE: RGB = (90, 104, 90)        # function key, daemon connected
FN_OFFLINE: RGB = (200, 0, 0)       # daemon cannot see Herdr; pulses
FLASH: RGB = (230, 230, 230)        # "heard you"
GIT_PAGE: RGB = (108, 0, 168)       # the focused agent's directory has a page
GIT_NO_PAGE: RGB = (20, 0, 32)      # ...and this one does not
GIT_LOOKING: RGB = (170, 0, 255)    # a press is finding out where to go

BLINK_HZ = 1.4
PULSE_SECONDS = 2.0
PULSE_STEP_SECONDS = 0.125  # the panel is redrawn, not dimmed, so a pulse is stepped
FLASH_SECONDS = 0.35


def parse_hex(colour: str | None) -> RGB | None:
    if not colour or len(colour) != 7 or colour[0] != "#":
        return None
    try:
        return int(colour[1:3], 16), int(colour[3:5], 16), int(colour[5:7], 16)
    except ValueError:
        return None


def _scaled(rgb: RGB, gain: float) -> RGB:
    return tuple(max(0, min(255, round(channel * gain))) for channel in rgb)  # type: ignore[return-value]


def _blink_off(now: float) -> bool:
    return int(now * BLINK_HZ * 2) % 2 == 0


def _pulse(now: float) -> float:
    stepped = now - now % PULSE_STEP_SECONDS
    phase = (stepped % PULSE_SECONDS) / PULSE_SECONDS
    return 0.25 + 0.75 * (1.0 - abs(phase * 2.0 - 1.0))


def colour_for(code: str, colour: str | None, now: float, *, flashing: bool) -> RGB:
    """The colour a key should be showing at `now`."""
    if code == "-":
        return OFF
    if code == "f":
        return FLASH if flashing else FN_IDLE
    if code == "x":
        return _scaled(FN_OFFLINE, _pulse(now))
    if code == "g":
        return GIT_LOOKING
    if code in ("r", "n"):
        if flashing:
            return FLASH
        return GIT_PAGE if code == "r" else GIT_NO_PAGE
    state = code.lower()
    base = STATE_COLOUR.get(state)
    if base is None:
        return OFF  # a meaning this board does not show, such as the microphone
    if state in ("i", "d"):
        base = parse_hex(colour) or base
    if state in ("b", "d") and _blink_off(now):
        return OFF
    level = STATE_LEVEL[state] * (FOCUS_GAIN if code.isupper() else 1.0)
    return _scaled(base, min(1.0, level))


def next_change(frame_keys: str, now: float, flash_until: float) -> float | None:
    """Seconds until some key on this board would look different, if ever."""
    shown = [frame_keys[slot] for slot in SLOT_TO_KEY]
    waits = []
    if any(code.lower() in ("b", "d") for code in shown):
        period = 1.0 / (BLINK_HZ * 2)
        waits.append(period - (now % period))
    if "x" in shown:
        waits.append(PULSE_STEP_SECONDS - (now % PULSE_STEP_SECONDS))
    if flash_until > now:
        waits.append(flash_until - now)
    return min(waits) if waits else None


def solid(rgb: RGB) -> bytes:
    """A key filled with one colour, as the encoder wants its pixels.

    The panel turns every image 90 degrees clockwise -- measured on the board,
    a corner drawn top-left appears top-right -- so anything that is not a flat
    fill has to be turned anticlockwise first. A flat fill does not care.
    """
    return bytes((*rgb, 0)) * (KEY_PIXELS * KEY_PIXELS)


# -- the board itself --------------------------------------------------------


class CN001Device:
    """A CN001 as the daemon sees it: a `Device`, like the Keybow.

    It differs in one way the daemon has to know about. The Keybow animates
    itself; this board has to be redrawn for anything on it to move, so the
    daemon calls `animate` every pass and asks `next_change` how soon it must.

    Only keys whose colour changed are redrawn, and each colour is encoded once,
    so a blink costs a couple of reports per key rather than a panel's worth.
    """

    def __init__(self, hid, *, brightness: int = 80, clock=time.monotonic) -> None:
        self._hid = hid
        self._clock = clock
        self._frame: Frame | None = None
        self._flash_until = 0.0
        self._flash_slot: int | None = None
        self._drawn: dict[int, RGB] = {}
        self._jpegs: dict[RGB, bytes] = {}
        self.firmware = "Soomfon CN001"
        for report in (heartbeat(), wake(), brightness_report(brightness), clear(), commit()):
            self._write(report)

    def _write(self, report: bytes) -> None:
        try:
            self._hid.write(report)
        except OSError as exc:  # HidError, and the unplugged cases with it
            raise DeviceError(f"cn001: {exc}") from exc

    def _jpeg(self, rgb: RGB) -> bytes:
        jpeg = self._jpegs.get(rgb)
        if jpeg is None:
            jpeg = encode_jpeg(KEY_PIXELS, KEY_PIXELS, solid(rgb))
            self._jpegs[rgb] = jpeg
        return jpeg

    def _wanted(self, now: float) -> dict[int, RGB]:
        wanted = {key: OFF for key in ALL_KEYS}
        if self._frame is None:
            return wanted
        # A flash names its key; one that names none is the function key's.
        flashing_slot = FN_SLOT if self._flash_slot is None else self._flash_slot
        for slot, key in SLOT_TO_KEY.items():
            flashing = now < self._flash_until and slot == flashing_slot
            wanted[key] = colour_for(
                self._frame.keys[slot], self._frame.colours[slot], now, flashing=flashing
            )
        return wanted

    def _redraw(self, now: float) -> bool:
        """Draw what changed. False if nothing had."""
        changed = {key: rgb for key, rgb in self._wanted(now).items() if self._drawn.get(key) != rgb}
        if not changed:
            return False
        for key, rgb in changed.items():
            if rgb == OFF:
                self._write(clear(key))
            else:
                for report in image(key, self._jpeg(rgb)):
                    self._write(report)
            self._drawn[key] = rgb
        self._write(commit())
        return True

    def fileno(self) -> int:
        return self._hid.fileno()

    def send_frame(self, frame: Frame) -> None:
        """Draw `frame`. Sent again unchanged, it proves the board is still there.

        The daemon resends its frame every few seconds whether or not anything
        changed. Here that is a heartbeat rather than a redraw: it is what
        notices an unplugged board, since nothing else is written while the
        board is quiet.
        """
        self._frame = frame
        if not self._redraw(self._clock()):
            self._write(heartbeat())

    def flash(self, slot: int | None = None) -> None:
        self._flash_until = self._clock() + FLASH_SECONDS
        self._flash_slot = slot
        self._redraw(self._clock())

    def animate(self, now: float) -> None:
        self._redraw(now)

    def next_change(self, now: float) -> float | None:
        if self._frame is None:
            return None
        return next_change(self._frame.keys, now, self._flash_until)

    def read_presses(self) -> list[int]:
        presses = []
        for report in self._hid.read():
            parsed = parse_input(report)
            if parsed is None:
                continue
            key, down = parsed
            # Presses fire on the way down, as on the Keybow. A key that holds
            # no slot -- the three kept for dictation -- does nothing.
            if down and key in KEY_TO_SLOT:
                presses.append(KEY_TO_SLOT[key])
        return presses

    def close(self) -> None:
        """Blank the panel. A crash cannot, and the panel never gives up by itself."""
        try:
            self._write(clear())
            self._write(commit())
        except DeviceError:
            pass  # already gone; there is nothing left to blank
        finally:
            self._hid.close()


def open_cn001(*, brightness: int) -> CN001Device | None:
    """The attached CN001, opened and blank; None when there is not one."""
    found = find_hid(VENDOR_ID, PRODUCT_ID, USAGE_PAGE)
    if not found:
        return None
    (_info, ref), *others = found
    for _other, extra in others:
        release(extra)
    try:
        hid = HidDevice(ref, input_size=INPUT_SIZE)
    except OSError as exc:
        raise DeviceError(f"cn001: {exc}") from exc
    try:
        return CN001Device(hid, brightness=brightness)
    except DeviceError:
        hid.close()
        raise
