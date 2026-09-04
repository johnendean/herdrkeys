"""The debug grid must match the physical board, or it teaches the wrong map."""

import re

from herdrkeys.model import Frame
from herdrkeys.tui import draw

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
