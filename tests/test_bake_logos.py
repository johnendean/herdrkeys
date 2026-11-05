import importlib.util
from pathlib import Path

import pytest

from herdrkeys import logos

SCRIPT = Path(__file__).resolve().parent.parent / "scripts" / "bake_logos.py"
_spec = importlib.util.spec_from_file_location("bake_logos", SCRIPT)
bake = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(bake)


def svg(path: str, view: int = 24) -> str:
    return f'<svg viewBox="0 0 {view} {view}" xmlns="http://www.w3.org/2000/svg"><path d="{path}"/></svg>'


def covered(mask: bytes, pixels: int, x: int, y: int) -> int:
    return mask[y * pixels + x]


def test_a_square_fills_exactly_where_it_is():
    mask = bake.mask_from_svg(svg("M2 2H6V6H2z", view=8), pixels=8)
    for y in range(8):
        for x in range(8):
            inside = 2 <= x < 6 and 2 <= y < 6
            assert covered(mask, 8, x, y) == (255 if inside else 0), (x, y)


def test_relative_commands_and_implicit_linetos_are_the_same_square():
    absolute = bake.mask_from_svg(svg("M2 2H6V6H2z", view=8), pixels=8)
    assert bake.mask_from_svg(svg("m2 2 4 0 0 4-4 0z", view=8), pixels=8) == absolute
    assert bake.mask_from_svg(svg("M2 2h4v4h-4Z", view=8), pixels=8) == absolute


def test_an_edge_through_a_pixel_covers_part_of_it():
    mask = bake.mask_from_svg(svg("M0 0H1.5V2H0z", view=2), pixels=2)
    assert covered(mask, 2, 0, 0) == 255
    assert 0 < covered(mask, 2, 1, 0) < 255


def test_a_subpath_wound_the_other_way_is_a_hole():
    # Bitbucket's bucket is drawn this way: an outline, and its slot cut out.
    mask = bake.mask_from_svg(svg("M0 0H8V8H0zM2 2V6H6V2z", view=8), pixels=8)
    assert covered(mask, 8, 0, 0) == 255
    assert covered(mask, 8, 4, 4) == 0


def test_packed_arc_flags_are_read_one_digit_at_a_time():
    # `a1 1 0 00-1 1`: flags 0 and 0, then -1. Read as a number, `00` would
    # swallow both flags and the arc would end in the wrong place.
    shapes = bake.outlines("M2 0a1 1 0 00-1 1L2 1z")
    end_of_arc = shapes[0][-2]
    assert end_of_arc == pytest.approx((1.0, 1.0))


def test_a_circle_of_arcs_is_round():
    mask = bake.mask_from_svg(svg("M12 2a10 10 0 1 0 0 20a10 10 0 1 0 0-20z"), pixels=24)
    assert covered(mask, 24, 12, 12) == 255
    assert covered(mask, 24, 0, 0) == 0
    assert covered(mask, 24, 2, 2) == 0, "a corner is outside the circle"
    filled = sum(mask) / 255
    assert filled == pytest.approx(3.14159 * 100, rel=0.03)


def test_a_cubic_bulges_toward_its_controls():
    flat = bake.mask_from_svg(svg("M0 4H8V8H0z", view=8), pixels=8)
    bulged = bake.mask_from_svg(svg("M0 4C2 0 6 0 8 4V8H0z", view=8), pixels=8)
    assert sum(bulged) > sum(flat)
    assert covered(bulged, 8, 4, 2) > 0


def test_the_checked_in_logos_are_what_the_script_makes_of_the_svgs():
    # The module is generated, and must never drift from its sources.
    assert Path(logos.__file__).read_text() == bake.module_source()


def test_every_logo_is_a_square_of_coverage_with_something_in_it():
    for name in logos.names():
        mask = logos.mask(name)
        assert len(mask) == logos.LOGO_PIXELS**2, name
        assert 255 in mask and 0 in mask, name


def test_a_sample_on_an_edge_shared_by_two_filled_spans_is_counted_once():
    # A square inside another, wound the same way, so both spans either side
    # of the inner edge are filled. That edge falls exactly on a sample point
    # (2.125 * SUPERSAMPLE = 8.5), and counting it twice would push a pixel
    # past 255.
    mask = bake.mask_from_svg(svg("M0 0H8V8H0zM2.125 2H6V6H2.125z", view=8), pixels=8)
    assert mask == bytes([255]) * 64
