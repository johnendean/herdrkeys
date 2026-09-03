"""herdrkeys firmware for the Pimoroni Keybow 2040.

The board is deliberately dumb: it renders frames and reports key presses. It
holds no idea of what an agent is, no slot map, and no policy. All of that lives
in the host daemon.

It does own the palette and the animation, because a frame carries meaning
(`blocked`) rather than colour -- so what red looks like, and what blinks, is
decided here and nowhere else.

Wire protocol, newline-delimited JSON on the usb_cdc data channel:

    in   {"v":1,"t":"hello"}
         {"v":1,"t":"frame","k":"iwB------------f"}
         {"v":1,"t":"flash"}
    out  {"v":1,"t":"hello","proto":1,"fw":"herdrkeys-device/0.1.0"}
         {"v":1,"t":"key","k":3}
"""

import json
import time

import usb_cdc
from pmk import PMK
from pmk.platform.keybow2040 import Keybow2040 as Hardware

PROTOCOL = 1
FIRMWARE = "herdrkeys-device/0.1.0"

# --- palette ---------------------------------------------------------------
# One hue per meaning. Focus is shown by brightness, never by hue, so hue only
# ever means state. Motion is reserved for `blocked`: it is the one state with a
# human waiting on it, and if anything else moved, movement would stop meaning
# "look at me".

BASE = {
    "i": (0, 25, 0),      # idle          dim green
    "w": (120, 45, 0),    # working       amber
    "b": (150, 0, 0),     # blocked       red, blinks
    "d": (0, 170, 0),     # done          bright green
    "u": (0, 0, 35),      # unknown       dim blue
}
OFF = (0, 0, 0)
FN_IDLE = (16, 16, 16)      # function key, daemon connected
FN_OFFLINE = (70, 0, 0)     # daemon cannot see Herdr; pulses
FN_FLASH = (140, 140, 140)  # "heard you, nothing wants your attention"
NO_HOST = (25, 0, 25)       # no daemon has ever said hello

FOCUS_GAIN = 2.5
BLINK_HZ = 1.4
PULSE_SECONDS = 2.0
FLASH_SECONDS = 0.35

# If the host stops sending, the last frame is a lie: it would keep showing agent
# states with no daemon running. Longer than the daemon's HEARTBEAT_SECONDS so a
# quiet session is never mistaken for a dead one.
HOST_TIMEOUT = 6.0

# Physical key numbering: key 0 is bottom-left and numbering runs up each
# column. To turn the board, replace this with the permutation you want; the
# host is unaware of orientation and always talks in slot numbers.
KEY_ORDER = list(range(16))

keybow = PMK(Hardware())
keys = keybow.keys
keybow.led_sleep_enabled = False  # a status display must never time out

data = usb_cdc.data

frame = None            # last frame received, or None before the first one
frame_at = 0.0          # when it arrived, so a stale one can be discarded
last_rgb = [None] * 16
pressed_before = [False] * 16
flash_until = 0.0
buffer = ""


def clamp(value):
    return 0 if value < 0 else (255 if value > 255 else int(value))


def scaled(rgb, gain):
    return (clamp(rgb[0] * gain), clamp(rgb[1] * gain), clamp(rgb[2] * gain))


def send(payload):
    if data is None:
        return
    try:
        data.write((json.dumps(payload) + "\n").encode())
    except Exception:
        pass  # host went away mid-write; the next frame will resync us


def colour_for(code, now):
    """The colour a key should be showing right now."""
    if code == "-":
        return OFF
    if code == "f":
        if now < flash_until:
            return FN_FLASH
        return FN_IDLE
    if code == "x":
        # Slow pulse: distinguishes "not connected" from "no agents", which
        # would otherwise both be sixteen dark keys.
        phase = (now % PULSE_SECONDS) / PULSE_SECONDS
        ramp = 1.0 - abs(phase * 2.0 - 1.0)
        return scaled(FN_OFFLINE, 0.25 + 0.75 * ramp)

    focused = code.isupper()
    base = BASE.get(code.lower())
    if base is None:
        return OFF
    if code.lower() == "b" and int(now * BLINK_HZ * 2) % 2 == 0:
        return OFF
    return scaled(base, FOCUS_GAIN if focused else 1.0)


def paint(now):
    stale = frame is None or (now - frame_at) > HOST_TIMEOUT
    for slot in range(16):
        if stale:
            rgb = NO_HOST if slot == 15 else OFF
        else:
            rgb = colour_for(frame[slot], now)
        key = KEY_ORDER[slot]
        if last_rgb[key] != rgb:
            keys[key].set_led(*rgb)
            last_rgb[key] = rgb


def handle(message):
    global frame, frame_at, flash_until
    if not isinstance(message, dict):
        return
    kind = message.get("t")
    if kind == "hello":
        send({"v": PROTOCOL, "t": "hello", "proto": PROTOCOL, "fw": FIRMWARE})
    elif kind == "frame":
        keys_string = message.get("k")
        if isinstance(keys_string, str) and len(keys_string) == 16:
            frame = keys_string
            frame_at = time.monotonic()
    elif kind == "flash":
        flash_until = time.monotonic() + FLASH_SECONDS


def read_host():
    global buffer
    if data is None or not data.in_waiting:
        return
    try:
        chunk = data.read(data.in_waiting)
    except Exception:
        return
    buffer += chunk.decode("utf-8", "ignore") if hasattr(chunk, "decode") else str(chunk)
    while "\n" in buffer:
        line, buffer = buffer.split("\n", 1)
        line = line.strip()
        if not line:
            continue
        try:
            handle(json.loads(line))
        except Exception:
            pass  # a malformed line is never worth crashing the keypad over


while True:
    keybow.update()
    now = time.monotonic()

    read_host()

    for slot in range(16):
        key = KEY_ORDER[slot]
        is_pressed = keys[key].pressed
        if is_pressed and not pressed_before[key]:
            send({"v": PROTOCOL, "t": "key", "k": slot})
        pressed_before[key] = is_pressed

    paint(now)
