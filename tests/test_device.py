import json
import re
from pathlib import Path

from herdrkeys.device import FakeDevice, decode_presses, encode_flash, encode_frame, encode_hello
from herdrkeys.model import MIC, MIC_SLOT, PROTOCOL_VERSION, Frame


def test_frames_are_absolute_so_a_dropped_byte_self_heals():
    frame = Frame("iwB------------f")
    payload = json.loads(encode_frame(frame))
    assert payload == {"v": PROTOCOL_VERSION, "t": "frame", "k": "iwB------------f"}
    assert encode_frame(frame).endswith(b"\n"), "newline framing, like Herdr's own"


def test_a_frame_must_describe_every_key():
    try:
        Frame("too short")
    except ValueError as exc:
        assert "16" in str(exc)
    else:
        raise AssertionError("a partial frame must not be constructible")


def test_control_messages():
    assert json.loads(encode_flash())["t"] == "flash"
    assert json.loads(encode_hello())["t"] == "hello"


def test_only_well_formed_key_messages_count_as_presses():
    assert decode_presses(
        [
            {"t": "key", "k": 3},
            {"t": "key"},
            {"t": "key", "k": "3"},
            {"t": "hello", "proto": 1},
            {"t": "key", "k": 15},
        ]
    ) == [3, 15]


def test_fake_device_records_what_the_daemon_would_have_shown():
    device = FakeDevice()
    device.send_frame(Frame.blank(connected=True))
    device.flash()
    device.queued_presses = [4]
    assert device.last_frame.keys.endswith("f")
    assert device.flashes == 1
    assert device.read_presses() == [4]
    assert device.read_presses() == [], "presses are consumed once"


# -- the firmware, which cannot be imported here --------------------------


def firmware():
    return (Path(__file__).parent.parent / "device" / "code.py").read_text()


def test_the_firmware_answers_to_the_frame_character_the_host_sends():
    # Two constants, two files, no import between them: the host says which key
    # is the microphone, the device decides what that means. If they drift, the
    # key silently goes dark and stops typing.
    match = re.search(r'^MIC_CODE = "(.)"', firmware(), re.M)
    assert match, "device/code.py must declare MIC_CODE"
    assert match.group(1) == MIC


def test_the_firmware_types_the_hotkey_wispr_flow_listens_for():
    # Wispr Flow's push-to-talk here is Right Option held: its config records
    # "61": "ptt", and 61 is the macOS keycode for Right Option.
    source = firmware()
    assert "Keycode.RIGHT_ALT" in source
    assert "keyboard.press(DICTATION_KEY)" in source
    assert "keyboard.release(DICTATION_KEY)" in source


def test_the_firmware_lets_go_when_the_host_goes_quiet():
    # A modifier still held by a board whose daemon has died would turn every
    # later keystroke into an Option chord.
    source = firmware()
    paint = source[source.index("def paint(now):"):]
    assert "release_mic()" in paint[: paint.index("def ", 10)]


def test_a_blank_frame_still_carries_the_feature_row():
    frame = Frame.blank(connected=True)
    assert frame.keys[MIC_SLOT] == MIC
    assert frame.keys.endswith("f")
