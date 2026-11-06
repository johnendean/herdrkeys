"""The firmware, exercised on the host.

`device/code.py` cannot be imported here: it opens USB devices at module scope
and ends in an infinite loop. So the module body *up to* that loop is executed
against stand-in libraries, which leaves every decision it makes -- when to hold
the dictation key, when to let go, what colour to show -- testable with no
CircuitPython and no board.

A stuck modifier is the worst thing this file can do to a machine, so the ways it
lets go are tested one by one.
"""

import json
import sys
import time
import types
from pathlib import Path

import pytest

from herdrkeys.model import (
    GIT_LOOKING,
    GIT_NO_PAGE,
    GIT_PAGE,
    GIT_SLOT,
    MIC,
    MIC_LATCHED,
    MIC_OPEN,
    MIC_SLOT,
    PROTOCOL_VERSION,
    RETURN,
    RETURN_SLOT,
)

SOURCE = (Path(__file__).parent.parent / "device" / "code.py").read_text()


class FakeKey:
    def __init__(self):
        self.pressed = False
        self.rgb = None

    def set_led(self, r, g, b):
        self.rgb = (r, g, b)


class FakeKeyboard:
    """Records what is being held down, which is the whole question."""

    def __init__(self, devices):
        self.held = []
        self.pressed = []  # every key that ever went down, in order

    def press(self, *codes):
        for code in codes:
            self.held.append(code)
            self.pressed.append(code)

    def release(self, *codes):
        for code in codes:
            if code in self.held:
                self.held.remove(code)


def _stub_modules(*, hid: bool):
    keybow = types.SimpleNamespace(keys=[FakeKey() for _ in range(16)], led_sleep_enabled=True, update=lambda: None)
    modules = {
        "usb_cdc": types.SimpleNamespace(data=None, console=None),
        "pmk": types.SimpleNamespace(PMK=lambda hardware: keybow),
        "pmk.platform": types.SimpleNamespace(),
        "pmk.platform.keybow2040": types.SimpleNamespace(Keybow2040=object),
    }
    if hid:
        modules["usb_hid"] = types.SimpleNamespace(devices=[])
        modules["adafruit_hid"] = types.SimpleNamespace()
        modules["adafruit_hid.keyboard"] = types.SimpleNamespace(Keyboard=FakeKeyboard)
        modules["adafruit_hid.keycode"] = types.SimpleNamespace(
            Keycode=types.SimpleNamespace(
                ENTER="ENTER", **{name: name for name in ("LEFT_CONTROL", "RIGHT_CONTROL", "LEFT_SHIFT",
                    "RIGHT_SHIFT", "LEFT_ALT", "RIGHT_ALT", "LEFT_GUI", "RIGHT_GUI", "F13", "F14", "F15", "A")}
            )
        )
    return modules


def load(monkeypatch, *, hid=True):
    """Run the firmware's module body and hand back its namespace."""
    body = SOURCE.split("\nwhile True:")[0]
    assert body != SOURCE, "device/code.py must still end in its main loop"
    for name, module in _stub_modules(hid=hid).items():
        monkeypatch.setitem(sys.modules, name, module)
    if not hid:
        for name in ("usb_hid", "adafruit_hid", "adafruit_hid.keyboard", "adafruit_hid.keycode"):
            monkeypatch.setitem(sys.modules, name, None)  # import raises, as on a board without the lib
    namespace = {"__name__": "code"}
    exec(compile(body, "device/code.py", "exec"), namespace)
    return namespace


@pytest.fixture
def firmware(monkeypatch):
    fw = load(monkeypatch)
    fw["frame"] = "-" * 12 + MIC + RETURN + "nf"
    fw["frame_at"] = 0.0
    return fw


def held(fw):
    return fw["keyboard"].held


# -- the frame character both sides have to agree on ----------------------


def test_it_types_the_hotkey_wispr_flow_listens_for(firmware):
    # Wispr Flow's push-to-talk here is Right Option held: its config records
    # "61": "ptt", and 61 is the macOS keycode for Right Option.
    assert firmware["DICTATION_KEY"] == "RIGHT_ALT"


class FakeData:
    """The CDC data channel: what the board says to the host."""

    def __init__(self):
        self.lines = []

    def write(self, payload):
        self.lines.append(json.loads(payload))


def told(fw):
    return [line for line in fw["data"].lines if line.get("t") in ("key", "up")]


@pytest.fixture
def talking(firmware):
    firmware["data"] = FakeData()
    return firmware


def test_the_firmware_and_host_speak_the_same_protocol(firmware):
    assert firmware["PROTOCOL"] == PROTOCOL_VERSION


def test_the_firmware_answers_to_every_microphone_character_the_host_sends(firmware):
    assert set(firmware["MIC_CODES"]) == {MIC, MIC_OPEN, MIC_LATCHED}


# -- the host decides, the board types (ADR 0017) --------------------------


def test_a_key_reports_both_edges(talking):
    talking["key_went_down"](3, 0.0)
    talking["key_came_up"](3, 0.5)
    assert told(talking) == [{"v": PROTOCOL_VERSION, "t": "key", "k": 3}, {"v": PROTOCOL_VERSION, "t": "up", "k": 3}]


def test_pressing_the_microphone_key_types_nothing_by_itself(talking):
    # Whether this is a hold or a tap, and whether the microphone is already
    # open on another board, is the host's to decide.
    talking["key_went_down"](MIC_SLOT, 0.0)
    talking["key_came_up"](MIC_SLOT, 0.1)
    assert talking["keyboard"].pressed == []


def test_pressing_the_return_key_types_nothing_by_itself(talking):
    talking["key_went_down"](RETURN_SLOT, 0.0)
    assert talking["keyboard"].pressed == []


def test_it_holds_the_hotkey_while_the_host_says_to(firmware):
    firmware["handle"]({"v": 2, "t": "hold", "on": True})
    assert held(firmware) == ["RIGHT_ALT"]

    firmware["handle"]({"v": 2, "t": "hold", "on": True})
    assert held(firmware) == ["RIGHT_ALT"], "told twice is still held once"

    firmware["handle"]({"v": 2, "t": "hold", "on": False})
    assert held(firmware) == []


def test_it_holds_the_chord_the_host_names(firmware):
    # Protocol 3: the host chooses the chord and names it on every hold, so
    # the board keeps no setting of its own (ADR 0017, as amended).
    firmware["handle"]({"v": 3, "t": "hold", "on": True, "keys": ["LEFT_CONTROL", "LEFT_GUI"]})
    assert held(firmware) == ["LEFT_CONTROL", "LEFT_GUI"]

    firmware["handle"]({"v": 3, "t": "hold", "on": False})
    assert held(firmware) == [], "it lets go of exactly what it held"


def test_a_chord_of_ordinary_keys_is_not_held(firmware):
    # It would type into whatever has focus for as long as dictation runs.
    firmware["handle"]({"v": 3, "t": "hold", "on": True, "keys": ["A", "LEFT_GUI", "NOT_A_KEY", 5]})
    assert held(firmware) == ["LEFT_GUI"]


def test_a_hold_that_names_nothing_it_knows_holds_right_option(firmware):
    # What every host before protocol 3 meant.
    firmware["handle"]({"v": 3, "t": "hold", "on": True, "keys": ["A"]})
    assert held(firmware) == ["RIGHT_ALT"]


def test_the_chord_is_let_go_when_the_host_goes_quiet(firmware):
    firmware["handle"]({"v": 3, "t": "hold", "on": True, "keys": ["LEFT_CONTROL", "LEFT_GUI"]})
    firmware["paint"](firmware["HOST_TIMEOUT"] + 1.0)
    assert held(firmware) == [], "a dead daemon must not leave a chord down"


def test_it_types_return_when_the_host_says_to(firmware):
    firmware["handle"]({"v": 2, "t": "return"})
    assert firmware["keyboard"].pressed == ["ENTER"]
    assert held(firmware) == [], "a tap, never a held key that would repeat"


# -- the ways it lets go by itself -----------------------------------------
#
# The host decides when the microphone closes, but a host can die. These are
# the board's own safety net under it, and each is a test.


def test_the_host_going_quiet_lets_go(firmware):
    firmware["handle"]({"v": 2, "t": "hold", "on": True})
    firmware["paint"](firmware["HOST_TIMEOUT"] + 1.0)
    assert held(firmware) == [], "a dead daemon must not leave a modifier down"


def test_a_frame_that_stops_naming_a_microphone_lets_go(firmware):
    firmware["handle"]({"v": 2, "t": "hold", "on": True})
    firmware["frame"] = "-" * 15 + "f"
    firmware["frame_at"] = 1.0
    firmware["paint"](1.1)
    assert held(firmware) == []


def test_a_hold_with_no_live_frame_is_refused(firmware):
    firmware["frame"] = None
    firmware["handle"]({"v": 2, "t": "hold", "on": True})
    assert held(firmware) == [], "nothing would be watching to let it go"


# -- what it looks like ---------------------------------------------------


def test_the_key_shows_closed_open_and_latched_apart(firmware):
    assert firmware["colour_for"](MIC, 0.0, MIC_SLOT) == firmware["MIC_IDLE"]
    assert firmware["colour_for"](MIC_OPEN, 0.0, MIC_SLOT) == firmware["MIC_OPEN"], "open is steady"

    pulse = firmware["MIC_PULSE_SECONDS"]
    over_a_cycle = {firmware["colour_for"](MIC_LATCHED, pulse * f, MIC_SLOT) for f in (0.0, 0.25, 0.5, 0.75)}
    assert len(over_a_cycle) > 1, "latched breathes, so it cannot be forgotten"
    assert firmware["MIC_OPEN"] in over_a_cycle
    assert all(rgb[0] == 0 and rgb[2] >= rgb[1] for rgb in over_a_cycle), "still cyan throughout"


def test_the_key_shows_a_microphone_another_board_opened(firmware):
    # There is one microphone: this board is not holding it, and still shows it.
    assert held(firmware) == []
    assert firmware["colour_for"](MIC_OPEN, 0.0, MIC_SLOT) == firmware["MIC_OPEN"]


# -- a board without the library ------------------------------------------


def test_a_board_with_no_hid_library_still_lights_up(monkeypatch):
    # Nothing here installs lib/: provisioning writes boot.py and code.py and
    # nothing else. Dictation is what such a board loses, not the grid.
    fw = load(monkeypatch, hid=False)
    assert fw["keyboard"] is None

    fw["frame"] = "-" * 12 + MIC + RETURN + "nf"
    fw["frame_at"] = 0.0
    fw["handle"]({"v": 2, "t": "hold", "on": True})
    fw["handle"]({"v": 2, "t": "return"})
    fw["paint"](0.2)
    assert not fw["held"], "nothing was ever held, so nothing is stuck"


# -- the return key -------------------------------------------------------


def test_the_firmware_answers_to_the_return_character_the_host_sends(firmware):
    assert firmware["RETURN_CODE"] == RETURN


def test_the_return_key_is_a_dimmer_steady_cyan_that_brightens_while_held(talking):
    firmware = talking
    now = time.monotonic()
    idle = firmware["colour_for"](RETURN, now, RETURN_SLOT)
    assert idle == firmware["RETURN_IDLE"]
    assert idle[0] == 0 and idle[1] == idle[2], "the microphone's hue"
    assert sum(idle) < sum(firmware["MIC_IDLE"]), "the microphone stays the brighter of the two"
    over_a_cycle = {firmware["colour_for"](RETURN, now + t, RETURN_SLOT) for t in (0.0, 0.4, 0.9, 1.6)}
    assert over_a_cycle == {idle}, "the return key does not animate"

    firmware["key_went_down"](RETURN_SLOT, now)
    assert firmware["colour_for"](RETURN, now, RETURN_SLOT) == firmware["RETURN_HELD"]
    firmware["key_came_up"](RETURN_SLOT, now)
    assert firmware["colour_for"](RETURN, now, RETURN_SLOT) == idle


# -- the git key ---------------------------------------------------


def test_the_git_key_answers_to_the_characters_the_host_sends(firmware):
    # Same drift risk as the microphone key: two constants, two files -- and
    # now two of them, either of which could go dark on its own.
    assert firmware["GIT_CODE"] == GIT_PAGE
    assert firmware["GIT_NONE_CODE"] == GIT_NO_PAGE


def test_the_git_key_is_steady_and_unlike_every_other_feature_key(firmware):
    now = time.monotonic()
    repo = firmware["colour_for"](GIT_PAGE, now, GIT_SLOT)

    assert repo == firmware["GIT_IDLE"]
    # Steady is the promise: it says the daemon is listening, not that there is
    # a page. Motion here would compete with `blocked` and with a latched mic.
    over_a_cycle = {firmware["colour_for"](GIT_PAGE, now + t, GIT_SLOT) for t in (0.0, 0.3, 0.7, 1.1, 2.3)}
    assert over_a_cycle == {repo}, "the git key does not animate"
    assert repo not in (firmware["MIC_IDLE"], firmware["FN_IDLE"], firmware["NO_HOST"])


def test_a_flash_naming_a_key_flashes_only_that_key(firmware):
    firmware["handle"]({"t": "flash", "k": GIT_SLOT})
    now = time.monotonic()

    assert firmware["colour_for"](GIT_PAGE, now, GIT_SLOT) == firmware["FN_FLASH"]
    assert firmware["colour_for"]("f", now, 15) == firmware["FN_IDLE"], (
        "the function key must not answer for the git key"
    )


def test_a_flash_naming_nothing_still_flashes_the_function_key(firmware):
    # What every host before this change sends, and what the function key's own
    # "nothing wants you" still sends.
    firmware["handle"]({"t": "flash"})
    now = time.monotonic()

    assert firmware["colour_for"]("f", now, 15) == firmware["FN_FLASH"]
    assert firmware["colour_for"](GIT_PAGE, now, GIT_SLOT) == firmware["GIT_IDLE"]


def test_a_nonsense_flash_target_falls_back_to_the_function_key(firmware):
    for wanted in (99, -1, "thirteen", None):
        firmware["handle"]({"t": "flash", "k": wanted})
        assert firmware["flash_slot"] is None


def test_the_two_git_key_states_are_told_apart_without_motion(firmware):
    now = time.monotonic()
    page = firmware["colour_for"](GIT_PAGE, now, GIT_SLOT)
    none = firmware["colour_for"](GIT_NO_PAGE, now, GIT_SLOT)

    assert page == firmware["GIT_IDLE"] and none == firmware["GIT_NONE"]
    assert page != none
    # Brighter, not a different hue: the difference is one of degree, and a
    # second hue here would start competing with the agent states.
    assert sum(page) > sum(none)
    # Neither animates: on the grid, movement means an agent wants you, and
    # this key has nothing to want. ADR 0012.
    for code in (GIT_PAGE, GIT_NO_PAGE):
        over_a_cycle = {firmware["colour_for"](code, now + t, GIT_SLOT) for t in (0.0, 0.4, 0.9, 1.6)}
        assert len(over_a_cycle) == 1


def test_neither_git_key_state_is_dark(firmware):
    # Dark would read as no daemon at all.
    for code in (GIT_PAGE, GIT_NO_PAGE):
        assert firmware["colour_for"](code, time.monotonic(), GIT_SLOT) != firmware["OFF"]


def test_a_git_key_with_no_page_still_flashes_when_pressed(firmware):
    # Pressing it can still succeed -- the colour reads only the config file,
    # git knows more -- so the failure signal has to work in this state too.
    firmware["handle"]({"t": "flash", "k": GIT_SLOT})
    assert firmware["colour_for"](GIT_NO_PAGE, time.monotonic(), GIT_SLOT) == firmware["FN_FLASH"]


# -- project colours on the LEDs ------------------------------------------


def test_an_idle_key_wears_its_project_colour(firmware):
    firmware["frame"] = "i" + "-" * 11 + MIC + RETURN + "nf"
    firmware["frame_colours"] = ["#a6e3a1"] + [None] * 15
    idle_green = firmware["BASE"]["i"]
    assert firmware["colour_for"]("i", 0.0, 0) != idle_green
    assert firmware["colour_for"]("i", 0.0, 1) == idle_green, "no colour, no change"


def test_what_is_happening_owns_the_hue_on_working_and_blocked(firmware):
    # `working` and `blocked` say what is going on, not whose it is. `unknown`
    # is a fault report, not an agent at work. ADR 0012.
    firmware["frame_colours"] = ["#a6e3a1"] * 16
    # `blocked` blinks, so it is asked at a moment it is lit rather than at
    # t=0, which lands in its dark half.
    for code, when in (("w", 0.0), ("b", 0.5), ("u", 0.0)):
        assert firmware["colour_for"](code, when, 0) == firmware["BASE"][code], (
            f"{code} keeps its state colour"
        )


def test_working_is_steady_amber(firmware):
    # It breathed its project colour for exactly one day; see ADR 0012.
    firmware["frame_colours"] = ["#a6e3a1"] * 16
    samples = {firmware["colour_for"]("w", t / 40, 0) for t in range(80)}
    assert samples == {firmware["BASE"]["w"]}, "one colour, and it does not move"


def test_done_wears_the_project_colour_and_blinks(firmware):
    firmware["frame_colours"] = ["#a6e3a1"] * 16
    samples = [firmware["colour_for"]("d", t / 40, 0) for t in range(80)]
    lit = [s for s in samples if s != (0, 0, 0)]
    assert (0, 0, 0) in samples, "it blinks"
    assert lit and firmware["BASE"]["d"] not in lit, "and it is not state green any more"

    firmware["frame_colours"] = None
    plain = [firmware["colour_for"]("d", t / 40, 0) for t in range(80)]
    assert firmware["BASE"]["d"] in plain, "without a colour it is bright green, as it was"
    assert (0, 0, 0) in plain, "and it blinks either way: the blink is the state"


def test_only_the_states_that_want_you_move(firmware):
    # The whole scheme in one assertion: motion means come here. If a third
    # state ever starts moving, it stops meaning that.
    firmware["frame_colours"] = ["#a6e3a1"] * 16
    window = [t / 40 for t in range(80)]
    moving = {
        code
        for code in ("i", "w", "b", "d", "u")
        if len({firmware["colour_for"](code, t, 0) for t in window}) > 1
    }
    assert moving == {"b", "d"}, "exactly the two the function key jumps to"


def test_idle_and_done_share_a_hue_but_never_read_alike(firmware):
    # Both wear the project colour, so brightness and motion carry the rest.
    firmware["frame_colours"] = ["#a6e3a1"] * 16
    idle = firmware["colour_for"]("i", 0.0, 0)
    done_lit = max(
        (firmware["colour_for"]("d", t / 40, 0) for t in range(80)), key=max
    )
    assert max(done_lit) > max(idle) * 4, "done is far brighter"
    assert (0, 0, 0) not in {firmware["colour_for"]("i", t / 40, 0) for t in range(80)}


def test_an_identity_colour_is_no_brighter_than_the_idle_it_replaces(firmware):
    # Otherwise a coloured idle key and a plain one would read as two states.
    firmware["frame_colours"] = ["#a6e3a1"] * 16
    level = firmware["BASE"]["i"][1]
    for hexcolour in ("#f38ba8", "#fab387", "#a6e3a1", "#89b4fa", "#cba6f7", "#94e2d5"):
        assert max(firmware["identity_rgb"](hexcolour)) == level


def test_the_palette_stays_apart_once_saturated(firmware):
    # Raw, these pastels land within a few counts of neutral at idle duty --
    # green against teal differs by one. Saturation is what keeps them apart,
    # so assert the separation rather than the constant that produces it.
    palette = ("#f38ba8", "#fab387", "#a6e3a1", "#89b4fa", "#cba6f7", "#94e2d5")
    rendered = [firmware["identity_rgb"](c) for c in palette]
    for first in range(len(rendered)):
        for second in range(first + 1, len(rendered)):
            apart = sum(abs(a - b) for a, b in zip(rendered[first], rendered[second]))
            assert apart >= 6, f"{palette[first]} and {palette[second]} are indistinguishable"


def test_focus_still_brightens_a_coloured_key(firmware):
    firmware["frame_colours"] = ["#89b4fa"] * 16
    unfocused = firmware["colour_for"]("i", 0.0, 0)
    focused = firmware["colour_for"]("I", 0.0, 0)
    assert max(focused) > max(unfocused), "focus is brightness, whatever the hue"


def test_nonsense_falls_back_to_the_state_colour(firmware):
    for bad in (None, "", "green", "#12345", "#gggggg", "#000000"):
        firmware["frame_colours"] = [bad] * 16
        assert firmware["colour_for"]("i", 0.0, 0) == firmware["BASE"]["i"], (
            f"{bad!r} must not blank the key"
        )


def test_a_frame_without_colours_clears_the_last_ones(firmware):
    # A host that has stopped reporting colours, or one older than herdrcolor,
    # must not leave the previous frame's colours painted on the board.
    firmware["handle"]({"t": "frame", "k": "i" * 16, "c": ["#a6e3a1"] * 16})
    assert firmware["frame_colours"] == ["#a6e3a1"] * 16
    firmware["handle"]({"t": "frame", "k": "i" * 16})
    assert firmware["frame_colours"] is None


def test_a_malformed_colour_list_is_ignored_whole(firmware):
    firmware["handle"]({"t": "frame", "k": "i" * 16, "c": ["#a6e3a1"]})
    assert firmware["frame_colours"] is None, "sixteen or nothing, like the frame itself"


# -- the git key, looking ---------------------------------------------------


def test_the_firmware_answers_to_the_looking_character_the_host_sends(firmware):
    assert firmware["GIT_LOOKING_CODE"] == GIT_LOOKING


def test_looking_is_a_brighter_steady_violet(firmware):
    now = time.monotonic()
    looking = firmware["colour_for"](GIT_LOOKING, now, GIT_SLOT)
    assert looking == firmware["GIT_LOOKING"]
    assert sum(looking) > sum(firmware["GIT_IDLE"]), "brighter than a key at rest"
    assert looking[1] == 0 and looking[2] > looking[0], "still the git key's violet"
    over_a_cycle = {firmware["colour_for"](GIT_LOOKING, now + t, GIT_SLOT) for t in (0.0, 0.4, 0.9, 1.6)}
    assert over_a_cycle == {looking}, "acknowledging you is not asking for you: no motion"
