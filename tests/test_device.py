import json

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


def test_a_blank_frame_still_carries_the_feature_row():
    frame = Frame.blank(connected=True)
    assert frame.keys[MIC_SLOT] == MIC
    assert frame.keys.endswith("f")


def test_a_frame_with_no_colours_is_byte_for_byte_what_it_always_was():
    # The protocol version does not move for this, so a board running older
    # firmware has to keep working. It does, because without herdrcolor there
    # is nothing to send: the field is absent, not null.
    frame = Frame("i" + "-" * 11 + "mn-f")
    assert b'"c"' not in encode_frame(frame)


def test_colours_ride_on_the_frame_itself():
    frame = Frame("i" + "-" * 11 + "mn-f", ("#a6e3a1",) + (None,) * 15)
    payload = json.loads(encode_frame(frame))
    assert payload["k"] == frame.keys
    assert payload["c"][0] == "#a6e3a1"
    assert payload["c"][1] is None
    assert len(payload["c"]) == 16, "one entry per key, so a frame stays absolute"
