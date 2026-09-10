"""The debug grid must match the physical board, or it teaches the wrong map."""

import re

from herdrkeys.model import EMPTY, FN_CONNECTED, FN_DISCONNECTED, MIC, AgentState, Frame, state_code
from herdrkeys.tui import SWATCH, draw

ANSI = re.compile(r"\x1b\[[0-9;]*m")
CELL = re.compile(r"\[\s*(\d+) ")


def number_to_xy(number):
    """PMK's own mapping, copied from lib/pmk/__init__.py on the device."""
    return number % 4, number // 4


def grid():
    """draw() parsed back into slot numbers, row 0 at the top."""
    return [
        [int(n) for n in CELL.findall(ANSI.sub("", line))]
        for line in draw(Frame("i" + "-" * 14 + "f")).splitlines()
    ]


def test_the_grid_is_four_by_four():
    rows = grid()
    assert len(rows) == 4 and all(len(row) == 4 for row in rows)


def test_the_corners_are_where_the_hardware_puts_them():
    rows = grid()
    assert rows[3][0] == 0, "slot 0 is bottom-left"
    assert rows[3][3] == 3, "slot 3 is bottom-right"
    assert rows[0][0] == 12, "slot 12 is top-left"
    assert rows[0][3] == 15, "slot 15, the function key, is top-right"


def test_every_slot_matches_pmks_numbering():
    # Row-major with key 0 bottom-left. Transposing this drew a plausible but
    # wrong grid for every slot off the diagonal.
    rows = grid()
    for slot in range(16):
        x, y = number_to_xy(slot)
        assert rows[3 - y][x] == slot, f"slot {slot} belongs at column {x}, row {y}"


# -- board orientation ---------------------------------------------------


def key_order(degrees):
    """The device's own rotation, re-implemented so a typo in it gets caught."""
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


def test_every_rotation_is_a_permutation():
    # A rotation that repeats or drops a key would leave LEDs stuck lit and key
    # presses reporting the wrong slot.
    for degrees in (0, 90, 180, 270):
        assert sorted(key_order(degrees)) == list(range(16)), f"{degrees} is not a bijection"


def test_half_a_turn_reverses_the_board():
    assert key_order(180) == list(reversed(range(16)))
    assert key_order(0) == list(range(16))


def test_rotating_twice_by_a_quarter_is_a_half_turn():
    quarter = key_order(90)
    assert [quarter[quarter[slot]] for slot in range(16)] == key_order(180)


def test_the_device_declares_a_rotation_the_math_supports():
    import re
    from pathlib import Path

    source = (Path(__file__).parent.parent / "device" / "code.py").read_text()
    match = re.search(r"^ROTATION = (\d+)", source, re.M)
    assert match, "device/code.py must declare ROTATION"
    assert int(match.group(1)) in (0, 90, 180, 270)


def test_every_character_a_frame_can_carry_has_a_swatch():
    # The TUI is the only place a frame is read by a human, so a character it
    # cannot colour is a character nobody can see is wrong.
    codes = {EMPTY, FN_CONNECTED, FN_DISCONNECTED, MIC}
    for state in AgentState:
        codes.add(state_code(state, focused=False))
        codes.add(state_code(state, focused=True))
    assert codes <= set(SWATCH), f"no swatch for {sorted(codes - set(SWATCH))}"
