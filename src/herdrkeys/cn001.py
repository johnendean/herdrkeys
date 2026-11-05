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

import math
import time
from string import hexdigits
from typing import NamedTuple

from .device import DeviceError
from .macos import HidDevice, encode_jpeg, find_hid, release
from . import logos
from .model import FN_SLOT, GIT_SLOT, MIC_SLOT, RETURN_SLOT, Frame

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
# across the top two rows. The feature row is the bottom row in the Keybow's
# order: the microphone and return keys at its left, the git and function keys
# at its right, and the one between them holds nothing (ADR 0017).
SLOT_TO_KEY: dict[int, int] = {
    **{slot: hardware_key(slot // COLUMNS, slot % COLUMNS) for slot in range(10)},
    MIC_SLOT: hardware_key(2, 0),
    RETURN_SLOT: hardware_key(2, 1),
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
# The same rules as device/code.py where an LED and an LCD can share them, and
# not where they cannot (ADRs 0014 and 0016). Motion means this agent wants
# you: `done` and `blocked` blink to black, at the same rate, and nothing else
# on the grid moves.
#
# What differs is that this board can draw. On the grid, colour only ever says
# whose: every key is its project's colour, as the sidebar shows it. What the
# agent is doing is its mark, drawn on a dark badge in a strong colour of its
# own -- so a pastel always means whose, a saturated colour always means what,
# and a red project's `done` never matches `blocked`. Focus is a border, which
# leaves brightness meaning nothing at all.

RGB = tuple[int, int, int]


class Look(NamedTuple):
    """Everything that decides a key's pixels, and so what its image is cached by."""

    background: RGB
    mark: str | None = None  # the state code whose mark it wears
    focused: bool = False
    muted: bool = False  # its mark greyed: a key this board cannot act on
    logo: str | None = None  # the forge whose logo it wears: the git key only


OFF: RGB = (0, 0, 0)
DARK = Look(OFF)  # a key with nothing on it is cleared, not drawn

NEUTRAL: RGB = (108, 108, 108)  # behind a mark whose agent has no project colour
BADGE: RGB = (22, 22, 22)
BORDER: RGB = (240, 240, 240)  # focus

MARK_COLOUR: dict[str, RGB] = {
    "w": (240, 140, 0),    # working       amber
    "b": (224, 0, 0),      # blocked       red
    "d": (0, 200, 0),      # done          green
    "u": (40, 100, 255),   # unknown       blue
}

FN_IDLE: RGB = (90, 104, 90)        # function key, daemon connected
FN_OFFLINE: RGB = (200, 0, 0)       # daemon cannot see Herdr; pulses
FLASH: RGB = (230, 230, 230)        # "heard you"
GIT_PAGE: RGB = (108, 0, 168)       # the focused agent's directory has a page
GIT_NO_PAGE: RGB = (20, 0, 32)      # ...and this one does not
GIT_LOOKING: RGB = (170, 0, 255)    # a press is finding out where to go

# A page on a known forge wears that forge's logo in white on its brand colour
# instead of the violet (ADR 0018). The feature row was never bound by "a
# saturated colour means what": GitLab's orange is not `working` here.
FORGE_COLOUR: dict[str, RGB] = {
    "github": (0x18, 0x17, 0x17),
    "gitlab": (0xFC, 0x6D, 0x26),
    "bitbucket": (0x00, 0x52, 0xCC),
    "codeberg": (0x21, 0x85, 0xD0),
}
LOGO_COLOUR: RGB = (255, 255, 255)

# The microphone and return keys (ADR 0017), in the Keybow's cyan: the one hue
# no agent state uses. Each draws its symbol, and the key behind it says how the
# one microphone is -- dim closed, bright open, breathing latched. Return is
# the dimmer of the two, so the microphone stays the key the eye finds first.
MIC_IDLE: RGB = (0, 52, 62)
MIC_OPEN: RGB = (0, 150, 180)
RETURN_IDLE: RGB = (0, 30, 36)
SYMBOL: RGB = (0, 210, 240)          # the symbols themselves
MUTED: RGB = (96, 96, 96)            # ...greyed, when this board cannot type
MIC_PULSE_SECONDS = 0.9

BLINK_HZ = 1.4
PULSE_SECONDS = 2.0
PULSE_STEP_SECONDS = 0.125  # the panel is redrawn, not dimmed, so a pulse is stepped
FLASH_SECONDS = 0.35


def parse_hex(colour: str | None) -> RGB | None:
    # Digits checked one by one, because int(..., 16) also takes a sign: a
    # channel of -1 would fail the encoder rather than fall back to NEUTRAL.
    if not colour or len(colour) != 7 or colour[0] != "#" or not all(c in hexdigits for c in colour[1:]):
        return None
    return int(colour[1:3], 16), int(colour[3:5], 16), int(colour[5:7], 16)


def _scaled(rgb: RGB, gain: float) -> RGB:
    return tuple(max(0, min(255, round(channel * gain))) for channel in rgb)  # type: ignore[return-value]


def _blink_off(now: float) -> bool:
    return int(now * BLINK_HZ * 2) % 2 == 0


def _pulse(now: float) -> float:
    stepped = now - now % PULSE_STEP_SECONDS
    phase = (stepped % PULSE_SECONDS) / PULSE_SECONDS
    return 0.25 + 0.75 * (1.0 - abs(phase * 2.0 - 1.0))


def look_for(
    code: str,
    colour: str | None,
    now: float,
    *,
    flashing: bool,
    can_type: bool = True,
    forge: str | None = None,
) -> Look:
    """What a key should be showing at `now`.

    `can_type` is whether a press on this board can type. Without it the
    microphone and return keys grey their symbols -- but still show the one
    microphone, which another board may have opened.

    `forge` is the git key's, and only matters there. One this board has no
    logo for is no forge at all.
    """
    if code == "-":
        return DARK
    if code in ("m", "o", "l", "e"):
        if flashing:
            return Look(FLASH)
        mark = "e" if code == "e" else "m"
        return Look(_dictation_background(code, now), mark, muted=not can_type)
    if code == "f":
        return Look(FLASH if flashing else FN_IDLE)
    if code == "x":
        return Look(_scaled(FN_OFFLINE, _pulse(now)))
    known = forge if forge in FORGE_COLOUR and forge in logos.names() else None
    if code == "g":
        # Looking is the focus border, held steady: motion means an agent
        # wants you (ADR 0012), and this is the board acknowledging you.
        return Look(FORGE_COLOUR[known], logo=known, focused=True) if known else Look(GIT_LOOKING)
    if code in ("r", "n"):
        if flashing:
            return Look(FLASH)
        if code == "n":
            return Look(GIT_NO_PAGE)
        return Look(FORGE_COLOUR[known], logo=known) if known else Look(GIT_PAGE)
    state = code.lower()
    if state not in ("i", *MARKS):
        return DARK  # a meaning this board does not show
    if state in ("b", "d") and _blink_off(now):
        return DARK
    return Look(parse_hex(colour) or NEUTRAL, state if state in MARKS else None, code.isupper())


def _dictation_background(code: str, now: float) -> RGB:
    if code == "e":
        return RETURN_IDLE
    if code == "o":
        return MIC_OPEN
    if code == "l":
        # Stepped like every pulse here: the panel is redrawn, not dimmed.
        stepped = now - now % PULSE_STEP_SECONDS
        phase = (stepped % MIC_PULSE_SECONDS) / MIC_PULSE_SECONDS
        return _scaled(MIC_OPEN, 0.3 + 0.7 * (1.0 - abs(phase * 2.0 - 1.0)))
    return MIC_IDLE


def next_change(frame_keys: str, now: float, flash_until: float) -> float | None:
    """Seconds until some key on this board would look different, if ever."""
    shown = [frame_keys[slot] for slot in SLOT_TO_KEY]
    waits = []
    if any(code.lower() in ("b", "d") for code in shown):
        period = 1.0 / (BLINK_HZ * 2)
        waits.append(period - (now % period))
    if "x" in shown or "l" in shown:
        waits.append(PULSE_STEP_SECONDS - (now % PULSE_STEP_SECONDS))
    if flash_until > now:
        waits.append(flash_until - now)
    return min(waits) if waits else None


# -- drawing a key ------------------------------------------------------------
#
# Marks are a few bold strokes and dots, measured in pixels from the key's
# centre with y pointing down, and rasterised here rather than through
# CoreGraphics: a few shapes at 96 pixels do not need a font, and this way the
# drawing is testable anywhere (ADR 0016). Only the badge is anti-aliased; the
# border and background are square and need nothing.

BADGE_RADIUS = 27.0
BORDER_WIDTH = 5
KEYLINE_WIDTH = 2  # dark, inside the border, so it shows on a pale project too
SUPERSAMPLE = 3

# Each part is ("stroke", ax, ay, bx, by, half-width, strength),
# ("dot", x, y, radius, strength) or ("arc", x, y, radius, half-width,
# from-degrees, to-degrees, strength); strength is how much of the mark's
# colour it has, over the badge.
_SPOKES = 8
MARKS: dict[str, tuple[tuple, ...]] = {
    # working: a spinner, frozen -- its spokes fade round from the top, and it
    # never turns.
    "w": tuple(
        (
            "stroke",
            9 * math.cos(angle), 9 * math.sin(angle),
            19 * math.cos(angle), 19 * math.sin(angle),
            3.0, 1.0 - 0.8 * spoke / (_SPOKES - 1),
        )
        for spoke in range(_SPOKES)
        for angle in [math.radians(-90 - 360 * spoke / _SPOKES)]
    ),
    # blocked: an exclamation mark
    "b": (("stroke", 0, -15, 0, 3, 4.0, 1.0), ("dot", 0, 13, 4.5, 1.0)),
    # done: a tick
    "d": (("stroke", -13, 0, -4, 9, 4.0, 1.0), ("stroke", -4, 9, 13, -10, 4.0, 1.0)),
    # unknown: a question mark
    "u": (
        ("arc", 0, -7, 8, 3.5, -180, 90, 1.0),
        ("stroke", 0, 1, 0, 5, 3.5, 1.0),
        ("dot", 0, 13, 4.0, 1.0),
    ),
    # the microphone key: a microphone -- capsule, cradle, stand
    "m": (
        ("stroke", 0, -13, 0, -2, 6.0, 1.0),
        ("arc", 0, -4, 11, 2.0, 0, 180, 1.0),
        ("stroke", 0, 7, 0, 13, 2.0, 1.0),
        ("stroke", -7, 14, 7, 14, 2.0, 1.0),
    ),
    # the return key: a return arrow
    "e": (
        ("stroke", 12, -12, 12, 4, 3.0, 1.0),
        ("stroke", 12, 4, -10, 4, 3.0, 1.0),
        ("stroke", -10, 4, -3, -3, 3.0, 1.0),
        ("stroke", -10, 4, -3, 11, 3.0, 1.0),
    ),
}


def _to_segment(x: float, y: float, ax: float, ay: float, bx: float, by: float) -> float:
    dx, dy = bx - ax, by - ay
    t = max(0.0, min(1.0, ((x - ax) * dx + (y - ay) * dy) / (dx * dx + dy * dy)))
    return math.hypot(x - ax - t * dx, y - ay - t * dy)


def _strength(parts: tuple[tuple, ...], x: float, y: float) -> float:
    """How much of the mark's colour shows at (x, y): 0 for none of it."""
    best = 0.0
    for part in parts:
        kind = part[0]
        if kind == "stroke":
            _, ax, ay, bx, by, half, strength = part
            inside = _to_segment(x, y, ax, ay, bx, by) <= half
        elif kind == "dot":
            _, cx, cy, radius, strength = part
            inside = math.hypot(x - cx, y - cy) <= radius
        else:
            _, cx, cy, radius, half, start, end, strength = part
            angle = math.degrees(math.atan2(y - cy, x - cx))
            inside = abs(math.hypot(x - cx, y - cy) - radius) <= half and start <= angle <= end
        if inside:
            best = max(best, strength)
    return best


def _mix(under: RGB, over: RGB, amount: float) -> RGB:
    return tuple(round(u + (o - u) * amount) for u, o in zip(under, over))  # type: ignore[return-value]


def _badge_pixel(look: Look, colour: RGB, x: int, y: int) -> RGB:
    """One pixel near the badge, averaged over a few samples so its edges are smooth."""
    parts = MARKS[look.mark] if look.mark else ()
    total = [0, 0, 0]
    for sy in range(SUPERSAMPLE):
        for sx in range(SUPERSAMPLE):
            px = x + (sx + 0.5) / SUPERSAMPLE - KEY_PIXELS / 2
            py = y + (sy + 0.5) / SUPERSAMPLE - KEY_PIXELS / 2
            if math.hypot(px, py) > BADGE_RADIUS:
                sample = look.background
            else:
                sample = _mix(BADGE, colour, _strength(parts, px, py))
            for channel in range(3):
                total[channel] += sample[channel]
    samples = SUPERSAMPLE * SUPERSAMPLE
    return tuple(round(channel / samples) for channel in total)  # type: ignore[return-value]


def _shown(look: Look) -> list[list[RGB]]:
    """The key as it should appear, row by row."""
    n = KEY_PIXELS
    rows = [[look.background] * n for _ in range(n)]
    if look.focused:
        for y in range(n):
            for x in range(n):
                edge = min(x, y, n - 1 - x, n - 1 - y)
                if edge < BORDER_WIDTH:
                    rows[y][x] = BORDER
                elif edge < BORDER_WIDTH + KEYLINE_WIDTH:
                    rows[y][x] = BADGE
    if look.mark:
        colour = MUTED if look.muted else MARK_COLOUR.get(look.mark, SYMBOL)
        reach = math.ceil(BADGE_RADIUS) + 1
        for y in range(n // 2 - reach, n // 2 + reach):
            for x in range(n // 2 - reach, n // 2 + reach):
                rows[y][x] = _badge_pixel(look, colour, x, y)
    if look.logo:
        size = logos.LOGO_PIXELS
        offset = (n - size) // 2
        mask = logos.mask(look.logo)
        for y in range(size):
            for x in range(size):
                coverage = mask[y * size + x]
                if coverage:
                    under = rows[offset + y][offset + x]
                    rows[offset + y][offset + x] = _mix(under, LOGO_COLOUR, coverage / 255)
    return rows


def render(look: Look) -> bytes:
    """A key's pixels, as the encoder wants them.

    The panel turns every image 90 degrees clockwise -- measured on the board,
    a corner drawn top-left appears top-right -- so the key is drawn turned a
    quarter anticlockwise: what should show at (x, y) is drawn at (y, 95 - x).
    """
    n = KEY_PIXELS
    if not look.focused and not look.mark and not look.logo:
        return bytes((*look.background, 0)) * (n * n)
    shown = _shown(look)
    out = bytearray(n * n * 4)
    for x in range(n):
        for y in range(n):
            i = ((n - 1 - x) * n + y) * 4
            out[i : i + 3] = bytes(shown[y][x])
    return bytes(out)


# -- the board itself --------------------------------------------------------


class CN001Device:
    """A CN001 as the daemon sees it: a `Device`, like the Keybow.

    It differs in one way the daemon has to know about. The Keybow animates
    itself; this board has to be redrawn for anything on it to move, so the
    daemon calls `animate` every pass and asks `next_change` how soon it must.

    Only keys whose look changed are redrawn, and each look is encoded once,
    so a blink costs a couple of reports per key rather than a panel's worth.
    """

    can_type = False  # the manufacturer's firmware types nothing we ask it to

    def __init__(self, hid, *, brightness: int = 80, clock=time.monotonic, can_type=lambda: True) -> None:
        self._hid = hid
        self._clock = clock
        # Asked on every draw rather than once, so the permission switched off
        # or back on in System Settings shows on the keys straight away. (A
        # first grant needs a restart, which the daemon does itself.)
        self._can_type = can_type
        self._frame: Frame | None = None
        self._flash_until = 0.0
        self._flash_slot: int | None = None
        self._drawn: dict[int, Look] = {}
        self._jpegs: dict[Look, bytes] = {}
        self.firmware = "Soomfon CN001"
        for report in (heartbeat(), wake(), brightness_report(brightness), clear(), commit()):
            self._write(report)

    def _write(self, report: bytes) -> None:
        try:
            self._hid.write(report)
        except OSError as exc:  # HidError, and the unplugged cases with it
            raise DeviceError(f"cn001: {exc}") from exc

    def _jpeg(self, look: Look) -> bytes:
        jpeg = self._jpegs.get(look)
        if jpeg is None:
            try:
                jpeg = encode_jpeg(KEY_PIXELS, KEY_PIXELS, render(look))
            except OSError as exc:
                # This board's failure, not the daemon's: as a DeviceError it
                # drops the CN001 and leaves the Keybow running.
                raise DeviceError(f"cn001: {exc}") from exc
            self._jpegs[look] = jpeg
        return jpeg

    def _wanted(self, now: float) -> dict[int, Look]:
        wanted = {key: DARK for key in ALL_KEYS}
        if self._frame is None:
            return wanted
        # A flash names its key; one that names none is the function key's.
        flashing_slot = FN_SLOT if self._flash_slot is None else self._flash_slot
        can_type = self._can_type()
        for slot, key in SLOT_TO_KEY.items():
            flashing = now < self._flash_until and slot == flashing_slot
            wanted[key] = look_for(
                self._frame.keys[slot],
                self._frame.colours[slot],
                now,
                flashing=flashing,
                can_type=can_type,
                forge=self._frame.forge if slot == GIT_SLOT else None,
            )
        return wanted

    def _redraw(self, now: float) -> bool:
        """Draw what changed. False if nothing had."""
        changed = {key: look for key, look in self._wanted(now).items() if self._drawn.get(key) != look}
        if not changed:
            return False
        for key, look in changed.items():
            if look == DARK:
                self._write(clear(key))
            else:
                for report in image(key, self._jpeg(look)):
                    self._write(report)
            self._drawn[key] = look
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

    def read_keys(self) -> list[tuple[int, bool]]:
        keys = []
        for report in self._hid.read():
            parsed = parse_input(report)
            if parsed is None:
                continue
            key, down = parsed
            # Both edges, as on the Keybow. A key that holds no slot -- the
            # one between return and git -- does nothing.
            if key in KEY_TO_SLOT:
                keys.append((KEY_TO_SLOT[key], down))
        return keys

    def close(self) -> None:
        """Blank the panel. A crash cannot, and the panel never gives up by itself."""
        try:
            self._write(clear())
            self._write(commit())
        except DeviceError:
            pass  # already gone; there is nothing left to blank
        finally:
            self._hid.close()


def open_cn001(*, brightness: int, can_type=lambda: True) -> CN001Device | None:
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
        return CN001Device(hid, brightness=brightness, can_type=can_type)
    except DeviceError:
        hid.close()
        raise
