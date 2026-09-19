"""herdrkeys firmware for the Pimoroni Keybow 2040.

The board is deliberately dumb: it renders frames and reports key presses. It
holds no idea of what an agent is, no slot map, and no policy. All of that lives
in the host daemon.

It owns the palette and the animation, because a frame carries meaning
(`blocked`) rather than colour -- so what red looks like, and what blinks, is
decided here and nowhere else. The same goes for the microphone key: the host
says which key it is, this file says that holding it means holding Right Option,
which is what Wispr Flow listens for, and that a tap latches it open until the
next tap.

Wire protocol, newline-delimited JSON on the usb_cdc data channel:

    in   {"v":1,"t":"hello"}
         {"v":1,"t":"frame","k":"iwB------------f"}
         {"v":1,"t":"frame","k":"iwB------------f","c":["#a6e3a1",null,...]}
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
FIRMWARE = "herdrkeys-device/0.6.0"

# --- palette ---------------------------------------------------------------
# Focus is shown by brightness, never by hue.
#
# Motion means one thing: this agent wants you. The two states that blink are
# exactly the two the function key jumps to.
#
#   steady                          blinking
#     idle      project colour        done      project colour
#     working   amber                 blocked   red
#
# So the board is read in two passes. Anything moving needs you, and its hue
# says which kind of needing: your own project's colour means work finished
# that you have not seen, red means an agent stopped and is waiting. Anything
# steady needs nothing, and its hue says whether it is yours and idle or simply
# busy.
#
# Hue therefore carries identity on `idle` and `done` -- the two states where
# knowing *which* agent is the useful thing -- and state on `working` and
# `blocked`, where what is happening matters more than to whom. `unknown` keeps
# its blue: it is a fault report, not an agent at work. See ADR 0012.

BASE = {
    "i": (0, 25, 0),      # idle          dim green
    "w": (120, 45, 0),    # working       amber
    "b": (150, 0, 0),     # blocked       red, blinks
    "d": (0, 170, 0),     # done          bright green
    "u": (0, 0, 35),      # unknown       dim blue
}
OFF = (0, 0, 0)
MIC_CODE = "m"

# How hard a project colour is pushed towards its dominant channel before it is
# scaled down to idle brightness. The colours arrive as sidebar pastels, and a
# pastel at 10% duty is just a dim white: measured across herdrcolor's six,
# the raw channel spread at idle level is 6-12 counts out of 25, and green
# against teal is a single count. At 1.7 the spread roughly doubles and the six
# stay apart. Saturating here rather than at the host is the same rule as the
# rest of this file -- what a colour has to become to survive these LEDs is the
# device's problem, and only the device knows it.
IDENTITY_SATURATION = 1.7

# What each identity-coloured state is scaled to, taken from the state colour it
# replaces rather than repeated, so a coloured key is exactly as bright as the
# fallback key beside it and the two read as one state. A `done` key is far
# brighter than an idle one in the same colour, which is what keeps the two
# apart when a project is on both -- that, and only one of them moving.
IDENTITY_LEVEL = BASE["i"][1]
DONE_LEVEL = max(BASE["d"])
# Neutral white, green-biased and kept off the floor of the PWM range: at very
# low duty these LEDs read strongly magenta, because the green die is the least
# efficient of the three. (16, 16, 16) looked purple on the bench -- close enough
# to NO_HOST to make the two indistinguishable, which defeats the whole point of
# this key.
FN_IDLE = (34, 44, 34)      # function key, daemon connected
# The microphone key. Cyan is the one hue no agent state uses, and open is told
# from idle by brightness rather than by starting to move -- on the grid,
# movement means an agent wants you, and a feature key must not borrow that.
# Green and blue are kept equal: at low duty an unbalanced pair reads as a tint.
MIC_IDLE = (0, 26, 26)
MIC_OPEN = (0, 160, 200)

# The repository key, in its two states. Violet is unused by every agent state
# and by the microphone. Both states are lit: dark would be indistinguishable
# from the spare key beside it, and from a board with no daemon behind it.
#
# Brightness rather than hue separates them, because the difference is one of
# degree -- there is a page, or there is not -- and because a second hue here
# would start competing with the agent states for meaning. Steady in both: this
# key never animates, because nothing here ever wants you.
REPO_CODE = "r"          # the focused agent's directory has a page
REPO_NONE_CODE = "n"     # ...and this one does not
REPO_IDLE = (45, 0, 70)
REPO_NONE = (7, 0, 11)

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

flash_slot = None       # which key is flashing, or None for the function key
frame = None            # last frame received, or None before the first one
frame_at = 0.0          # when it arrived, so a stale one can be discarded
frame_colours = None    # per-key project colours, or None when the host sent none
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


def identity_rgb(colour, level=None):
    """A `#rrggbb` project colour as this board should show it at `level`.

    Saturated, then scaled so its brightest channel sits at `level`, which is
    the brightness of whatever state colour it is standing in for. A
    colour that cannot be parsed returns None, and the caller falls back to the
    state colour -- the same answer as no colour at all, which is a state the
    board is designed to be in rather than an error.
    """
    if not colour or len(colour) != 7 or colour[0] != "#":
        return None
    try:
        r = int(colour[1:3], 16)
        g = int(colour[3:5], 16)
        b = int(colour[5:7], 16)
    except ValueError:
        return None
    top = max(r, g, b)
    if top == 0:
        return None
    if level is None:
        level = IDENTITY_LEVEL
    out = []
    for channel in (r, g, b):
        pulled = top - (top - channel) * IDENTITY_SATURATION
        if pulled < 0:
            pulled = 0
        out.append(clamp(pulled * level / top))
    return (out[0], out[1], out[2])


def breath(now, seconds, floor):
    """A smooth triangle between `floor` and 1.0, for a key that is alive."""
    phase = (now % seconds) / seconds
    ramp = 1.0 - abs(phase * 2.0 - 1.0)
    return floor + (1.0 - floor) * ramp


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
        if now < flash_until and flash_slot in (None, slot):
            return FN_FLASH
        return FN_IDLE
    if code == REPO_CODE or code == REPO_NONE_CODE:
        if now < flash_until and flash_slot == slot:
            return FN_FLASH
        return REPO_IDLE if code == REPO_CODE else REPO_NONE
    if code == MIC_CODE:
        if slot != mic_slot:
            return MIC_IDLE
        if not mic_latched:
            return MIC_OPEN
        return scaled(MIC_OPEN, breath(now, MIC_PULSE_SECONDS, 0.3))
    if code == "x":
        # Slow pulse: distinguishes "not connected" from "no agents", which
        # would otherwise both be sixteen dark keys.
        phase = (now % PULSE_SECONDS) / PULSE_SECONDS
        ramp = 1.0 - abs(phase * 2.0 - 1.0)
        return scaled(FN_OFFLINE, 0.25 + 0.75 * ramp)

    focused = code.isupper()
    state = code.lower()
    base = BASE.get(state)
    if base is None:
        return OFF
    if state in ("i", "d"):
        level = IDENTITY_LEVEL if state == "i" else DONE_LEVEL
        identity = identity_rgb(frame_colours[slot] if frame_colours else None, level)
        if identity is not None:
            base = identity
    if state in ("b", "d") and int(now * BLINK_HZ * 2) % 2 == 0:
        # Both states that want you blink, and at the same rate: they are one
        # signal -- "come here" -- and the hue says which kind. A second rate
        # would make the board ask to be decoded rather than noticed.
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
    global frame, frame_at, frame_colours, flash_until, flash_slot
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
            # Set together with the frame, never separately: a colour that
            # could arrive out of step with the meaning it belongs to would
            # paint one agent's key in another agent's colour. A host that
            # sends no `c` at all -- one older than herdrcolor, or one whose
            # agents have no colours -- clears them rather than leaving the
            # last frame's behind.
            colours = message.get("c")
            if isinstance(colours, list) and len(colours) == 16:
                frame_colours = colours
            else:
                frame_colours = None
    elif kind == "flash":
        flash_until = time.monotonic() + FLASH_SECONDS
        # No slot means the function key, which is what every older host sends.
        wanted = message.get("k")
        flash_slot = wanted if isinstance(wanted, int) and 0 <= wanted < 16 else None


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
