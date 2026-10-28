import pytest

from herdrkeys import cn001
from herdrkeys.device import DeviceError
from herdrkeys.model import FN_SLOT, GIT_SLOT, MIC_SLOT, RETURN_SLOT, SLOT_COUNT, Frame


class FakeHid:
    """Records reports instead of sending them; queued input is read once."""

    def __init__(self) -> None:
        self.reports: list[bytes] = []
        self.queued: list[bytes] = []
        self.closed = False
        self.unplugged = False

    def write(self, report: bytes) -> None:
        if self.unplugged:
            raise OSError("IOHIDDeviceSetReport failed: kIOReturnNotAttached")
        assert len(report) == cn001.REPORT_SIZE
        self.reports.append(report)

    def read(self) -> list[bytes]:
        queued, self.queued = self.queued, []
        return queued

    def fileno(self) -> int:
        return 99

    def close(self) -> None:
        self.closed = True

    def commands(self) -> list[bytes]:
        return [r[5:8] for r in self.reports if r.startswith(b"CRT\0\0")]


class Clock:
    def __init__(self, now: float = 0.0) -> None:
        self.now = now

    def __call__(self) -> float:
        return self.now


@pytest.fixture(autouse=True)
def no_jpeg_encoder(monkeypatch):
    # A stand-in that keeps the colour readable, so tests run anywhere.
    monkeypatch.setattr(cn001, "encode_jpeg", lambda w, h, px: b"JPEG" + px[:3])


def key_report(key: int, down: bool) -> bytes:
    return (b"ACK\0\0OK\0\0" + bytes([key, 1 if down else 0])).ljust(cn001.INPUT_SIZE, b"\0")


def frame(keys: dict[int, str], colours: dict[int, str] | None = None) -> Frame:
    chars = ["-"] * SLOT_COUNT
    chars[GIT_SLOT], chars[FN_SLOT] = "r", "f"
    for slot, code in keys.items():
        chars[slot] = code
    tones = [None] * SLOT_COUNT
    for slot, colour in (colours or {}).items():
        tones[slot] = colour
    return Frame("".join(chars), tuple(tones))


def drawn(hid: FakeHid) -> dict[int, bytes]:
    """The last colour each key was given, by hardware key, or b"" when cleared."""
    shown: dict[int, bytes] = {}
    reports = iter(hid.reports)
    for report in reports:
        if report.startswith(b"CRT\0\0BAT"):
            shown[report[12]] = next(reports)[4:7]
        elif report.startswith(b"CRT\0\0CLE") and report[11] != 0xFF:
            shown[report[11]] = b""
    return shown


# -- where each slot sits ---------------------------------------------------


def test_the_hardware_numbers_keys_down_each_column_from_the_right():
    # Measured on the board: top-left is 13, top-right 1, bottom-right 3.
    assert cn001.hardware_key(0, 0) == 13
    assert cn001.hardware_key(0, 4) == 1
    assert cn001.hardware_key(2, 4) == 3
    assert cn001.hardware_key(1, 2) == 8


def test_agents_read_left_to_right_across_the_top_two_rows():
    top = [cn001.SLOT_TO_KEY[slot] for slot in range(5)]
    middle = [cn001.SLOT_TO_KEY[slot] for slot in range(5, 10)]
    assert top == [13, 10, 7, 4, 1]
    assert middle == [14, 11, 8, 5, 2]


def test_the_feature_row_is_the_bottom_row_with_git_and_function_at_its_right():
    assert cn001.SLOT_TO_KEY[GIT_SLOT] == cn001.hardware_key(2, 3)
    assert cn001.SLOT_TO_KEY[FN_SLOT] == cn001.hardware_key(2, 4)


def test_the_eleventh_and_twelfth_agents_and_dictation_are_not_on_this_board():
    for slot in (10, 11, MIC_SLOT, RETURN_SLOT):
        assert slot not in cn001.SLOT_TO_KEY


# -- commands ---------------------------------------------------------------


def test_every_command_is_one_full_report():
    for report in (cn001.wake(), cn001.brightness_report(50), cn001.clear(), cn001.commit(), cn001.heartbeat()):
        assert len(report) == cn001.REPORT_SIZE
        assert report.startswith(b"CRT\0\0")


def test_brightness_is_clamped_and_sits_at_offset_ten():
    assert cn001.brightness_report(55)[10] == 55
    assert cn001.brightness_report(500)[10] == 100
    assert cn001.brightness_report(-1)[10] == 0


def test_an_image_is_a_header_naming_its_size_and_key_then_its_bytes_in_chunks():
    jpeg = bytes(range(256)) * 9  # 2304 bytes: three chunks
    reports = cn001.image(7, jpeg)
    header = reports[0]
    assert header.startswith(b"CRT\0\0BAT")
    assert int.from_bytes(header[8:12], "big") == len(jpeg)
    assert header[12] == 7
    assert len(reports) == 4
    assert b"".join(reports[1:])[: len(jpeg)] == jpeg


def test_only_key_reports_are_presses():
    assert cn001.parse_input(key_report(13, True)) == (13, True)
    assert cn001.parse_input(key_report(13, False)) == (13, False)
    assert cn001.parse_input(key_report(0, False)) is None, "key 0 is a reset, not a key"
    assert cn001.parse_input(b"\0" * 512) is None


# -- what each meaning looks like -------------------------------------------


def test_a_project_colour_is_the_sidebars_hex_unsaturated():
    # Focused done at full: exactly what herdrcolor published.
    assert cn001.colour_for("D", "#a6e3a1", 0.5, flashing=False) == (0xA6, 0xE3, 0xA1)


def test_idle_is_the_same_hue_as_done_but_dark_and_still():
    idle = cn001.colour_for("i", "#89b4fa", 0.5, flashing=False)
    done = cn001.colour_for("d", "#89b4fa", 0.5, flashing=False)
    assert sum(idle) * 2 < sum(done)
    assert idle == cn001.colour_for("i", "#89b4fa", 0.0, flashing=False), "idle never moves"


def test_done_and_blocked_blink_to_black_together_and_nothing_else_does():
    off, on = 0.0, 0.5
    for code in ("b", "d"):
        assert cn001.colour_for(code, None, off, flashing=False) == cn001.OFF
        assert cn001.colour_for(code, None, on, flashing=False) != cn001.OFF
    for code in ("i", "w", "u"):
        assert cn001.colour_for(code, None, off, flashing=False) == cn001.colour_for(code, None, on, flashing=False)


def test_a_red_projects_done_never_looks_like_blocked():
    # herdrcolor's red is Catppuccin's pastel; blocked is a saturated red. On
    # an LCD shown as-is, the two would otherwise be pixel-identical.
    red_done = cn001.colour_for("d", "#f38ba8", 0.5, flashing=False)
    blocked = cn001.colour_for("b", None, 0.5, flashing=False)
    assert red_done != blocked
    assert min(red_done) > 100 and min(blocked) == 0


def test_state_colours_are_never_herdrcolors_pastels():
    pastels = {"#f38ba8", "#fab387", "#a6e3a1", "#89b4fa", "#cba6f7", "#94e2d5"}
    for state, rgb in cn001.STATE_COLOUR.items():
        assert "#%02x%02x%02x" % rgb not in pastels, state
        assert min(rgb) < 60, f"{state} must be saturated, not a pastel"


def test_focus_is_brightness_not_hue():
    plain = cn001.colour_for("w", None, 0.5, flashing=False)
    focused = cn001.colour_for("W", None, 0.5, flashing=False)
    assert sum(focused) > sum(plain)
    assert plain.index(max(plain)) == focused.index(max(focused))


def test_a_key_without_a_project_colour_falls_back_to_its_state_colour():
    assert cn001.colour_for("I", None, 0.5, flashing=False) == cn001.colour_for("I", "nonsense", 0.5, flashing=False)


def test_the_board_asks_for_a_pass_only_while_something_moves():
    still = frame({0: "i", 1: "w"})
    assert cn001.next_change(still.keys, 0.1, 0.0) is None
    blinking = frame({0: "b"})
    wait = cn001.next_change(blinking.keys, 0.1, 0.0)
    assert wait is not None and 0 < wait <= 1 / (cn001.BLINK_HZ * 2)


# -- the board --------------------------------------------------------------


def test_opening_wakes_the_panel_and_blanks_it():
    hid = FakeHid()
    cn001.CN001Device(hid, brightness=40, clock=Clock())
    assert hid.commands() == [b"CON", b"DIS", b"LIG", b"CLE", b"STP"]
    assert hid.reports[2][10] == 40


def test_a_frame_draws_each_slot_on_its_key():
    hid = FakeHid()
    board = cn001.CN001Device(hid, clock=Clock(0.5))
    board.send_frame(frame({0: "D"}, {0: "#a6e3a1"}))
    shown = drawn(hid)
    assert shown[13] == bytes((0xA6, 0xE3, 0xA1)), "slot 0 is the top-left key"
    assert hid.commands()[-1] == b"STP", "nothing appears until committed"


def test_an_unchanged_frame_is_a_heartbeat_not_a_redraw():
    hid = FakeHid()
    board = cn001.CN001Device(hid, clock=Clock(0.5))
    board.send_frame(frame({0: "i"}))
    sent = len(hid.reports)
    board.send_frame(frame({0: "i"}))
    assert hid.reports[sent:] == [cn001.heartbeat()]


def test_a_blink_redraws_only_the_blinking_key():
    hid = FakeHid()
    clock = Clock(0.5)
    board = cn001.CN001Device(hid, clock=clock)
    board.send_frame(frame({0: "b", 1: "w"}))
    sent = len(hid.reports)
    board.animate(0.0)  # the blink's off phase
    redrawn = hid.reports[sent:]
    assert [r[:8] for r in redrawn] == [b"CRT\0\0CLE", b"CRT\0\0STP"]
    assert redrawn[0][11] == cn001.SLOT_TO_KEY[0]


def test_a_bare_flash_is_the_function_keys_and_a_named_one_is_that_keys():
    hid = FakeHid()
    clock = Clock(10.0)
    board = cn001.CN001Device(hid, clock=clock)
    board.send_frame(frame({}))
    board.flash()
    assert drawn(hid)[cn001.SLOT_TO_KEY[FN_SLOT]] == bytes(cn001.FLASH)
    clock.now += cn001.FLASH_SECONDS
    board.animate(clock.now)
    board.flash(GIT_SLOT)
    shown = drawn(hid)
    assert shown[cn001.SLOT_TO_KEY[GIT_SLOT]] == bytes(cn001.FLASH)
    assert shown[cn001.SLOT_TO_KEY[FN_SLOT]] == bytes(cn001.FN_IDLE)


def test_presses_fire_on_the_way_down_and_name_slots():
    hid = FakeHid()
    board = cn001.CN001Device(hid, clock=Clock())
    hid.queued = [key_report(13, True), key_report(13, False), key_report(3, True)]
    assert board.read_presses() == [0, FN_SLOT]


def test_the_keys_kept_for_dictation_do_nothing():
    hid = FakeHid()
    board = cn001.CN001Device(hid, clock=Clock())
    hid.queued = [key_report(cn001.hardware_key(2, 0), True)]
    assert board.read_presses() == []


def test_an_unplugged_board_is_a_device_error():
    hid = FakeHid()
    board = cn001.CN001Device(hid, clock=Clock())
    hid.unplugged = True
    with pytest.raises(DeviceError):
        board.send_frame(frame({0: "i"}))


def test_closing_blanks_the_panel_because_nothing_else_will():
    hid = FakeHid()
    board = cn001.CN001Device(hid, clock=Clock(0.5))
    board.send_frame(frame({0: "i"}))
    board.close()
    assert hid.commands()[-2:] == [b"CLE", b"STP"]
    assert hid.reports[-2][11] == 0xFF
    assert hid.closed


def test_closing_a_board_already_gone_still_lets_it_go():
    hid = FakeHid()
    board = cn001.CN001Device(hid, clock=Clock())
    hid.unplugged = True
    board.close()
    assert hid.closed
