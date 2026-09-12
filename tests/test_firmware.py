"""The firmware, exercised on the host.

`device/code.py` cannot be imported here: it opens USB devices at module scope
and ends in an infinite loop. So the module body *up to* that loop is executed
against stand-in libraries, which leaves every decision it makes -- when to hold
the dictation key, when to let go, what colour to show -- testable with no
CircuitPython and no board.

A stuck modifier is the worst thing this file can do to a machine, so the ways it
lets go are tested one by one.
"""

import sys
import time
import types
from pathlib import Path

import pytest

from herdrkeys.model import MIC, REPO_NO_PAGE, REPO_PAGE, REPO_SLOT

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

    def press(self, code):
        self.held.append(code)

    def release(self, code):
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
            Keycode=types.SimpleNamespace(RIGHT_ALT="RIGHT_ALT")
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
    fw["frame"] = "-" * 12 + MIC + "--f"
    fw["frame_at"] = 0.0
    return fw


def held(fw):
    return fw["keyboard"].held


# -- the frame character both sides have to agree on ----------------------


def test_the_firmware_answers_to_the_character_the_host_sends(firmware):
    # Two constants, two files, no import between them. If they drift, the key
    # silently goes dark and stops typing.
    assert firmware["MIC_CODE"] == MIC


def test_it_types_the_hotkey_wispr_flow_listens_for(firmware):
    # Wispr Flow's push-to-talk here is Right Option held: its config records
    # "61": "ptt", and 61 is the macOS keycode for Right Option.
    assert firmware["DICTATION_KEY"] == "RIGHT_ALT"


# -- holding --------------------------------------------------------------


def test_holding_the_key_holds_the_hotkey_down(firmware):
    firmware["mic_pressed"](12, 0.0)
    assert held(firmware) == ["RIGHT_ALT"]

    firmware["mic_released"](2.0)
    assert held(firmware) == [], "a hold ends when your finger does"
    assert firmware["mic_slot"] is None


# -- latching -------------------------------------------------------------


def test_a_tap_latches_the_microphone_open(firmware):
    firmware["mic_pressed"](12, 0.0)
    firmware["mic_released"](0.1)  # inside LATCH_TAP_SECONDS
    assert held(firmware) == ["RIGHT_ALT"], "still talking, with no finger on the key"
    assert firmware["mic_latched"]


def test_tapping_again_ends_it(firmware):
    firmware["mic_pressed"](12, 0.0)
    firmware["mic_released"](0.1)
    firmware["mic_pressed"](12, 5.0)
    assert held(firmware) == []
    assert not firmware["mic_latched"]

    firmware["mic_released"](5.05)
    assert held(firmware) == [], "the release that ends the second tap must not re-latch"


def test_a_hold_never_latches(firmware):
    firmware["mic_pressed"](12, 0.0)
    firmware["mic_released"](firmware["LATCH_TAP_SECONDS"] + 0.01)
    assert held(firmware) == [] and not firmware["mic_latched"]


# -- the ways it lets go --------------------------------------------------


def test_a_latch_does_not_outlive_its_timeout(firmware):
    firmware["mic_pressed"](12, 0.0)
    firmware["mic_released"](0.1)
    timeout = firmware["LATCH_TIMEOUT"]

    firmware["frame_at"] = timeout / 2  # the host is alive and still says "mic"
    firmware["paint"](timeout / 2)
    assert held(firmware) == ["RIGHT_ALT"], "not yet"

    firmware["frame_at"] = timeout + 1.0
    firmware["paint"](timeout + 1.0)
    assert held(firmware) == [], "nothing on the board can see whether Flow is still listening"


def test_the_host_going_quiet_lets_go(firmware):
    firmware["mic_pressed"](12, 0.0)
    firmware["mic_released"](0.1)
    firmware["paint"](firmware["HOST_TIMEOUT"] + 1.0)
    assert held(firmware) == [], "a dead daemon must not leave a modifier down"


def test_a_frame_that_stops_naming_the_key_lets_go(firmware):
    firmware["mic_pressed"](12, 0.0)
    firmware["mic_released"](0.1)
    firmware["frame"] = "-" * 15 + "f"
    firmware["frame_at"] = 1.0
    firmware["paint"](1.1)
    assert held(firmware) == []


# -- what it looks like ---------------------------------------------------


def test_the_key_shows_idle_open_and_latched_apart(firmware):
    idle = firmware["colour_for"](MIC, 0.0, 12)
    assert idle == firmware["MIC_IDLE"]

    firmware["mic_pressed"](12, 0.0)
    assert firmware["colour_for"](MIC, 0.0, 12) == firmware["MIC_OPEN"], "held is steady"

    firmware["mic_released"](0.1)
    pulse = firmware["MIC_PULSE_SECONDS"]
    over_a_cycle = {firmware["colour_for"](MIC, pulse * f, 12) for f in (0.0, 0.25, 0.5, 0.75)}
    assert len(over_a_cycle) > 1, "latched breathes, so it cannot be forgotten"
    assert firmware["MIC_OPEN"] in over_a_cycle
    assert all(rgb[0] == 0 and rgb[2] >= rgb[1] for rgb in over_a_cycle), "still cyan throughout"


def test_only_the_key_holding_it_lights_up(firmware):
    firmware["mic_pressed"](12, 0.0)
    assert firmware["colour_for"](MIC, 0.0, 13) == firmware["MIC_IDLE"]


# -- a board without the library ------------------------------------------


def test_a_board_with_no_hid_library_still_lights_up(monkeypatch):
    # Nothing here installs lib/: provisioning writes boot.py and code.py and
    # nothing else. Dictation is what such a board loses, not the grid.
    fw = load(monkeypatch, hid=False)
    assert fw["keyboard"] is None

    fw["frame"] = "-" * 12 + MIC + "--f"
    fw["frame_at"] = 0.0
    fw["mic_pressed"](12, 0.0)
    fw["mic_released"](0.1)
    fw["paint"](0.2)
    assert fw["mic_slot"] is None, "nothing was ever held, so nothing is stuck"


# -- the repository key ---------------------------------------------------


def test_the_repo_key_answers_to_the_characters_the_host_sends(firmware):
    # Same drift risk as the microphone key: two constants, two files -- and
    # now two of them, either of which could go dark on its own.
    assert firmware["REPO_CODE"] == REPO_PAGE
    assert firmware["REPO_NONE_CODE"] == REPO_NO_PAGE


def test_the_repo_key_is_steady_and_unlike_every_other_feature_key(firmware):
    now = time.monotonic()
    repo = firmware["colour_for"](REPO_PAGE, now, REPO_SLOT)

    assert repo == firmware["REPO_IDLE"]
    # Steady is the promise: it says the daemon is listening, not that there is
    # a page. Motion here would compete with `blocked` and with a latched mic.
    over_a_cycle = {firmware["colour_for"](REPO_PAGE, now + t, REPO_SLOT) for t in (0.0, 0.3, 0.7, 1.1, 2.3)}
    assert over_a_cycle == {repo}, "the repo key does not animate"
    assert repo not in (firmware["MIC_IDLE"], firmware["FN_IDLE"], firmware["NO_HOST"])


def test_a_flash_naming_a_key_flashes_only_that_key(firmware):
    firmware["handle"]({"t": "flash", "k": REPO_SLOT})
    now = time.monotonic()

    assert firmware["colour_for"](REPO_PAGE, now, REPO_SLOT) == firmware["FN_FLASH"]
    assert firmware["colour_for"]("f", now, 15) == firmware["FN_IDLE"], (
        "the function key must not answer for the repo key"
    )


def test_a_flash_naming_nothing_still_flashes_the_function_key(firmware):
    # What every host before this change sends, and what the function key's own
    # "nothing wants you" still sends.
    firmware["handle"]({"t": "flash"})
    now = time.monotonic()

    assert firmware["colour_for"]("f", now, 15) == firmware["FN_FLASH"]
    assert firmware["colour_for"](REPO_PAGE, now, REPO_SLOT) == firmware["REPO_IDLE"]


def test_a_nonsense_flash_target_falls_back_to_the_function_key(firmware):
    for wanted in (99, -1, "thirteen", None):
        firmware["handle"]({"t": "flash", "k": wanted})
        assert firmware["flash_slot"] is None


def test_the_two_repo_states_are_told_apart_without_motion(firmware):
    now = time.monotonic()
    page = firmware["colour_for"](REPO_PAGE, now, REPO_SLOT)
    none = firmware["colour_for"](REPO_NO_PAGE, now, REPO_SLOT)

    assert page == firmware["REPO_IDLE"] and none == firmware["REPO_NONE"]
    assert page != none
    # Brighter, not a different hue: the difference is one of degree, and a
    # second hue here would start competing with the agent states.
    assert sum(page) > sum(none)
    # Neither animates. Motion stays reserved for `blocked` and the mic latch.
    for code in (REPO_PAGE, REPO_NO_PAGE):
        over_a_cycle = {firmware["colour_for"](code, now + t, REPO_SLOT) for t in (0.0, 0.4, 0.9, 1.6)}
        assert len(over_a_cycle) == 1


def test_neither_repo_state_is_dark(firmware):
    # Dark would read as the spare key beside it, or as no daemon at all.
    for code in (REPO_PAGE, REPO_NO_PAGE):
        assert firmware["colour_for"](code, time.monotonic(), REPO_SLOT) != firmware["OFF"]


def test_a_repo_key_with_no_page_still_flashes_when_pressed(firmware):
    # Pressing it can still succeed -- the colour reads only the config file,
    # git knows more -- so the failure signal has to work in this state too.
    firmware["handle"]({"t": "flash", "k": REPO_SLOT})
    assert firmware["colour_for"](REPO_NO_PAGE, time.monotonic(), REPO_SLOT) == firmware["FN_FLASH"]
