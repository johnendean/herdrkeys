"""herdrkeys firmware for the Pimoroni Keybow 2040.

The board is deliberately dumb: it renders frames and reports key presses. It
holds no idea of what an agent is, no slot map, and no policy. All of that lives
in the host daemon.

It does own the palette and the animation, because a frame carries meaning
(`blocked`) rather than colour -- so what red looks like, and what blinks, is
decided here and nowhere else. The same goes for the microphone key: the host
says which key it is, this file says that holding it means holding Right Option,
which is what Wispr Flow listens for, and that a tap latches it open until the
next tap.

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

# Optional, and deliberately so. Nothing in herdrkeys installs lib/ -- the
# daemon writes boot.py and code.py and nothing else -- so a board that arrives
# without adafruit_hid must still light up. It loses dictation, not the grid.
try:
    import usb_hid
    from adafruit_hid.keyboard import Keyboard
    from adafruit_hid.keycode import Keycode

    keyboard = Keyboard(usb_hid.devices)
    DICTATION_KEY = Keycode.RIGHT_ALT
except Exception:
    keyboard = None
    DICTATION_KEY = None

PROTOCOL = 1
FIRMWARE = "herdrkeys-device/0.2.0"

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
MIC_CODE = "m"
# Neutral white, green-biased and kept off the floor of the PWM range: at very
# low duty these LEDs read strongly magenta, because the green die is the least
# efficient of the three. (16, 16, 16) looked purple on the bench -- close enough
# to NO_HOST to make the two indistinguishable, which defeats the whole point of
# this key.
FN_IDLE = (34, 44, 34)      # function key, daemon connected
# The microphone key. Cyan is the one hue no agent state uses, and open is told
# from idle by brightness rather than motion, which stays reserved for `blocked`.
# Green and blue are kept equal: at low duty an unbalanced pair reads as a tint.
MIC_IDLE = (0, 26, 26)
MIC_OPEN = (0, 160, 200)

# A press shorter than this latches the microphone open instead of closing it
# with your finger; anything longer is an ordinary hold. Long enough not to fire
# on a deliberate short phrase, short enough that a tap never feels like a wait.
LATCH_TAP_SECONDS = 0.4

# A latched microphone breathes rather than sitting still, because the whole
# risk of latching is forgetting it is on. Faster and smoother than the blink
# `blocked` uses, so the two never read as the same signal.
MIC_PULSE_SECONDS = 0.9

# ...and it does not stay latched forever. Nothing on the board can tell whether
# Wispr Flow is still listening, and a modifier held down for an afternoon turns
# every keystroke into an Option chord.
LATCH_TIMEOUT = 300.0
FN_OFFLINE = (70, 0, 0)     # daemon cannot see Herdr; pulses
FN_FLASH = (140, 140, 140)  # "heard you, nothing wants your attention"
NO_HOST = (60, 0, 60)       # no daemon; blinks, so hue alone need not carry it

FOCUS_GAIN = 2.5
BLINK_HZ = 1.4
PULSE_SECONDS = 2.0
NO_HOST_BLINK_HZ = 0.4
FLASH_SECONDS = 0.35

# If the host stops sending, the last frame is a lie: it would keep showing agent
# states with no daemon running. Longer than the daemon's HEARTBEAT_SECONDS so a
# quiet session is never mistaken for a dead one.
HOST_TIMEOUT = 6.0

# How the board is sitting on the desk, in degrees anticlockwise from the
# orientation Pimoroni ships it in: 0, 90, 180 or 270. Orientation lives here
# and only here -- the host is unaware of it and always talks in slot numbers.
#
# PMK numbers the physical keys with number_to_xy (x = n % 4, y = n // 4), key 0
# bottom-left, so at ROTATION = 0 slot 15 is the top-right key.
ROTATION = 270


def _key_order(degrees):
    """Map each logical slot to the physical key that should render it."""
    order = []
    for slot in range(16):
        x, y = slot % 4, slot // 4
        if degrees == 90:
            x, y = y, 3 - x
        elif degrees == 180:
            x, y = 3 - x, 3 - y
        elif degrees == 270:
            x, y = 3 - y, x
        order.append(y * 4 + x)
    return order


KEY_ORDER = _key_order(ROTATION)

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
mic_slot = None         # the slot holding the dictation key down, if any
mic_pressed_at = 0.0    # when it went down, to tell a tap from a hold
mic_latched = False     # held with no finger on it, until tapped again
mic_latched_at = 0.0    # when the latch started, so it cannot run forever


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


def hold_mic(slot):
    """Hold the dictation hotkey down while the key is held down."""
    global mic_slot
    if keyboard is None:
        return
    try:
        keyboard.press(DICTATION_KEY)
    except Exception:
        return  # a keystroke that will not go is not worth crashing the keypad
    mic_slot = slot


def release_mic():
    """Let go, and be willing to be called when nothing is held.

    Called on release, when a frame stops calling that key the microphone, and
    when the host goes quiet: a modifier held down by a board whose daemon has
    died would turn every later keystroke into an Option chord.
    """
    global mic_slot, mic_latched
    if mic_slot is None:
        return
    mic_slot = None
    mic_latched = False
    if keyboard is None:
        return
    try:
        keyboard.release(DICTATION_KEY)
    except Exception:
        pass


def mic_pressed(slot, now):
    """A press on the microphone key: start talking, or end a latched session."""
    global mic_pressed_at
    if mic_latched and mic_slot == slot:
        release_mic()
        return
    hold_mic(slot)
    mic_pressed_at = now


def mic_released(now):
    """A release: a tap latches the microphone open, a hold ends there."""
    global mic_latched, mic_latched_at
    if now - mic_pressed_at < LATCH_TAP_SECONDS:
        mic_latched = True
        mic_latched_at = now
        return
    release_mic()


def colour_for(code, now, slot):
    """The colour a key should be showing right now."""
    if code == "-":
        return OFF
    if code == "f":
        if now < flash_until:
            return FN_FLASH
        return FN_IDLE
    if code == MIC_CODE:
        if slot != mic_slot:
            return MIC_IDLE
        if not mic_latched:
            return MIC_OPEN
        phase = (now % MIC_PULSE_SECONDS) / MIC_PULSE_SECONDS
        ramp = 1.0 - abs(phase * 2.0 - 1.0)
        return scaled(MIC_OPEN, 0.3 + 0.7 * ramp)
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
    if mic_slot is not None and (stale or frame[mic_slot] != MIC_CODE):
        release_mic()
    elif mic_latched and now - mic_latched_at > LATCH_TIMEOUT:
        release_mic()
    # A slow blink rather than a steady colour: whether a hue reads as intended
    # depends on the LED, but motion does not, and this must never be mistaken
    # for the steady white that means all is well.
    no_host = NO_HOST if int(now * NO_HOST_BLINK_HZ * 2) % 2 == 0 else OFF
    for slot in range(16):
        if stale:
            rgb = no_host if slot == 15 else OFF
        else:
            rgb = colour_for(frame[slot], now, slot)
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
            # Every key reports its press; only this one also types. The host
            # ignores presses on keys it holds no agent for.
            if frame is not None and (now - frame_at) <= HOST_TIMEOUT and frame[slot] == MIC_CODE:
                mic_pressed(slot, now)
        elif pressed_before[key] and not is_pressed and mic_slot == slot:
            mic_released(now)
        pressed_before[key] = is_pressed

    paint(now)
