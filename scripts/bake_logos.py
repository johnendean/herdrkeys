"""Bake the forges' SVG logos into coverage masks for the CN001 (ADR 0018).

    python3 scripts/bake_logos.py

Reads `assets/logos/<forge>.svg` and writes `src/herdrkeys/logos.py`. Run at
development time only: nothing here ships, and nothing at run time parses SVG.

The rasteriser understands exactly what Simple Icons' files use -- one `<path>`
per file in a square viewBox, filled nonzero -- and every path command, arcs
included. It is pure Python for the same reason the marks are (ADR 0016): it
is testable anywhere and needs nothing installed.
"""

from __future__ import annotations

import base64
import math
import re
import sys
import zlib
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
SOURCES = REPO / "assets" / "logos"
OUTPUT = REPO / "src" / "herdrkeys" / "logos.py"

LOGO_PIXELS = 56  # inside the key's 96, clear of the focus border
SUPERSAMPLE = 4
CURVE_STEPS = 24

Point = tuple[float, float]


# -- reading a path ------------------------------------------------------------

_NUMBER = re.compile(r"[-+]?(?:\d+\.?\d*|\.\d+)(?:[eE][-+]?\d+)?")
_SEPARATORS = " \t\r\n,"


class _Reader:
    """A path's data, read one command letter, number or arc flag at a time.

    Hand-rolled rather than tokenised up front because an arc's flags are
    single digits that may be packed together and against the next number:
    `a.768.768 0 00-.768.892` is two flags, `0` and `0`, then `-.768`.
    """

    def __init__(self, data: str) -> None:
        self.data = data
        self.at = 0

    def _skip(self) -> None:
        while self.at < len(self.data) and self.data[self.at] in _SEPARATORS:
            self.at += 1

    def command(self) -> str | None:
        self._skip()
        if self.at < len(self.data) and self.data[self.at].isalpha():
            self.at += 1
            return self.data[self.at - 1]
        return None

    def has_number(self) -> bool:
        self._skip()
        return bool(_NUMBER.match(self.data, self.at))

    def number(self) -> float:
        self._skip()
        match = _NUMBER.match(self.data, self.at)
        if not match:
            raise ValueError(f"expected a number at {self.at}: {self.data[self.at:self.at + 12]!r}")
        self.at = match.end()
        return float(match.group())

    def flag(self) -> bool:
        self._skip()
        if self.at >= len(self.data) or self.data[self.at] not in "01":
            raise ValueError(f"expected an arc flag at {self.at}")
        self.at += 1
        return self.data[self.at - 1] == "1"


def _cubic(p0: Point, p1: Point, p2: Point, p3: Point) -> list[Point]:
    out = []
    for step in range(1, CURVE_STEPS + 1):
        t = step / CURVE_STEPS
        u = 1 - t
        out.append((
            u**3 * p0[0] + 3 * u * u * t * p1[0] + 3 * u * t * t * p2[0] + t**3 * p3[0],
            u**3 * p0[1] + 3 * u * u * t * p1[1] + 3 * u * t * t * p2[1] + t**3 * p3[1],
        ))
    return out


def _quadratic(p0: Point, p1: Point, p2: Point) -> list[Point]:
    out = []
    for step in range(1, CURVE_STEPS + 1):
        t = step / CURVE_STEPS
        u = 1 - t
        out.append((
            u * u * p0[0] + 2 * u * t * p1[0] + t * t * p2[0],
            u * u * p0[1] + 2 * u * t * p1[1] + t * t * p2[1],
        ))
    return out


def _arc(p0: Point, rx: float, ry: float, rotation: float, large: bool, sweep: bool, p1: Point) -> list[Point]:
    """An SVG elliptical arc as points, by the endpoint-to-centre conversion in
    the SVG specification's implementation notes (F.6.5)."""
    if p0 == p1:
        return []
    rx, ry = abs(rx), abs(ry)
    if rx == 0 or ry == 0:
        return [p1]
    phi = math.radians(rotation)
    cos_phi, sin_phi = math.cos(phi), math.sin(phi)
    dx, dy = (p0[0] - p1[0]) / 2, (p0[1] - p1[1]) / 2
    x1 = cos_phi * dx + sin_phi * dy
    y1 = -sin_phi * dx + cos_phi * dy
    # Radii too small to reach are scaled up until they just do.
    scale = (x1 * x1) / (rx * rx) + (y1 * y1) / (ry * ry)
    if scale > 1:
        rx, ry = rx * math.sqrt(scale), ry * math.sqrt(scale)
    numerator = rx * rx * ry * ry - rx * rx * y1 * y1 - ry * ry * x1 * x1
    denominator = rx * rx * y1 * y1 + ry * ry * x1 * x1
    factor = math.sqrt(max(0.0, numerator / denominator))
    if large == sweep:
        factor = -factor
    cx1, cy1 = factor * rx * y1 / ry, -factor * ry * x1 / rx
    cx = cos_phi * cx1 - sin_phi * cy1 + (p0[0] + p1[0]) / 2
    cy = sin_phi * cx1 + cos_phi * cy1 + (p0[1] + p1[1]) / 2

    def angle(ux: float, uy: float, vx: float, vy: float) -> float:
        return math.atan2(ux * vy - uy * vx, ux * vx + uy * vy)

    start = angle(1, 0, (x1 - cx1) / rx, (y1 - cy1) / ry)
    extent = angle((x1 - cx1) / rx, (y1 - cy1) / ry, (-x1 - cx1) / rx, (-y1 - cy1) / ry)
    if not sweep and extent > 0:
        extent -= 2 * math.pi
    elif sweep and extent < 0:
        extent += 2 * math.pi
    steps = max(4, math.ceil(abs(extent) / (2 * math.pi) * CURVE_STEPS * 4))
    out = []
    for step in range(1, steps + 1):
        theta = start + extent * step / steps
        x, y = rx * math.cos(theta), ry * math.sin(theta)
        out.append((cos_phi * x - sin_phi * y + cx, sin_phi * x + cos_phi * y + cy))
    out[-1] = p1  # exactly, so the outline closes where the path says
    return out


def outlines(data: str) -> list[list[Point]]:
    """A path's data as closed polygons, one per subpath."""
    reader = _Reader(data)
    shapes: list[list[Point]] = []
    current: list[Point] = []
    here: Point = (0.0, 0.0)
    start: Point = (0.0, 0.0)
    last_control: Point | None = None  # for S and T
    last_kind = ""
    command = reader.command()
    if command is None:
        return []
    while command is not None:
        relative = command.islower()
        kind = command.upper()

        def point() -> Point:
            x, y = reader.number(), reader.number()
            return (here[0] + x, here[1] + y) if relative else (x, y)

        if kind == "Z":
            if current:
                shapes.append(current)
            current = []
            here = start
            last_control, last_kind = None, "Z"
            command = reader.command()
            continue

        first = True
        while first or reader.has_number():
            if kind == "M":
                if current:
                    shapes.append(current)
                here = point()
                start = here
                current = [here]
                # Further pairs after a moveto are linetos (SVG 9.3.3).
                kind = "L"
                last_control = None
            elif kind == "L":
                here = point()
                current.append(here)
                last_control = None
            elif kind == "H":
                x = reader.number()
                here = (here[0] + x if relative else x, here[1])
                current.append(here)
                last_control = None
            elif kind == "V":
                y = reader.number()
                here = (here[0], here[1] + y if relative else y)
                current.append(here)
                last_control = None
            elif kind in ("C", "S"):
                if kind == "C":
                    c1 = point()
                else:
                    c1 = (
                        (2 * here[0] - last_control[0], 2 * here[1] - last_control[1])
                        if last_control and last_kind in ("C", "S")
                        else here
                    )
                c2, end = point(), point()
                current.extend(_cubic(here, c1, c2, end))
                here, last_control = end, c2
            elif kind in ("Q", "T"):
                if kind == "Q":
                    c = point()
                else:
                    c = (
                        (2 * here[0] - last_control[0], 2 * here[1] - last_control[1])
                        if last_control and last_kind in ("Q", "T")
                        else here
                    )
                end = point()
                current.extend(_quadratic(here, c, end))
                here, last_control = end, c
            elif kind == "A":
                rx, ry, rotation = reader.number(), reader.number(), reader.number()
                large, sweep = reader.flag(), reader.flag()
                end = point()
                current.extend(_arc(here, rx, ry, rotation, large, sweep, end))
                here, last_control = end, None
            else:
                raise ValueError(f"unknown path command {command!r}")
            last_kind = kind
            first = False
        command = reader.command()
    if current:
        shapes.append(current)
    return shapes


# -- filling it ----------------------------------------------------------------


def rasterise(shapes: list[list[Point]], view: float, pixels: int) -> bytes:
    """Coverage of each pixel, 0-255, row by row, filled by the nonzero rule.

    `view` is the side of the square viewBox the shapes are drawn in; the
    image is that square scaled to `pixels` on a side.
    """
    grid = pixels * SUPERSAMPLE
    scale = grid / view
    edges = []
    for shape in shapes:
        for i, a in enumerate(shape):
            b = shape[(i + 1) % len(shape)]
            if a[1] != b[1]:
                edges.append((a[0] * scale, a[1] * scale, b[0] * scale, b[1] * scale))
    counts = [0] * (pixels * pixels)
    for row in range(grid):
        y = row + 0.5
        crossings = []
        for ax, ay, bx, by in edges:
            if (ay <= y < by) or (by <= y < ay):
                x = ax + (y - ay) * (bx - ax) / (by - ay)
                crossings.append((x, 1 if by > ay else -1))
        crossings.sort()
        winding = 0
        for (x, direction), following in zip(crossings, crossings[1:] + [(None, 0)]):
            winding += direction
            if winding == 0 or following[0] is None:
                continue
            first = max(0, math.ceil(x - 0.5))
            last = min(grid - 1, math.floor(following[0] - 0.5))
            out_row = (row // SUPERSAMPLE) * pixels
            for column in range(first, last + 1):
                counts[out_row + column // SUPERSAMPLE] += 1
    full = SUPERSAMPLE * SUPERSAMPLE
    return bytes(round(255 * count / full) for count in counts)


_VIEWBOX = re.compile(r'viewBox="\s*0[\s,]+0[\s,]+([\d.]+)[\s,]+([\d.]+)\s*"')
_PATH = re.compile(r'<path[^>]*\sd="([^"]+)"')


def mask_from_svg(svg: str, pixels: int = LOGO_PIXELS) -> bytes:
    view = _VIEWBOX.search(svg)
    if not view or float(view.group(1)) != float(view.group(2)):
        raise ValueError("expected a square viewBox at the origin")
    paths = _PATH.findall(svg)
    if len(paths) != 1:
        raise ValueError(f"expected exactly one <path>, found {len(paths)}")
    return rasterise(outlines(paths[0]), float(view.group(1)), pixels)


# -- writing the module ----------------------------------------------------------


_HEADER = '''"""The forges' logos, as coverage masks for the CN001's git key (ADR 0018).

Generated by `scripts/bake_logos.py` from `assets/logos/*.svg`. Do not edit;
rerun the script instead.

Each mask is LOGO_PIXELS square, one byte per pixel, row by row: how much of
that pixel is logo, from 0 (none) to 255 (all of it).
"""

from __future__ import annotations

import base64
import zlib
from functools import cache

LOGO_PIXELS = {pixels}

_MASKS = {{
{entries}}}


def names() -> frozenset[str]:
    return frozenset(_MASKS)


@cache
def mask(name: str) -> bytes:
    return zlib.decompress(base64.b64decode(_MASKS[name]))
'''


def module_source(sources: Path = SOURCES, pixels: int = LOGO_PIXELS) -> str:
    entries = []
    for svg in sorted(sources.glob("*.svg")):
        encoded = base64.b64encode(zlib.compress(mask_from_svg(svg.read_text(), pixels), 9)).decode()
        lines = [encoded[i : i + 72] for i in range(0, len(encoded), 72)]
        body = "\n".join(f'        "{line}"' for line in lines)
        entries.append(f'    "{svg.stem}": (\n{body}\n    ),\n')
    return _HEADER.format(pixels=pixels, entries="".join(entries))


def main() -> int:
    OUTPUT.write_text(module_source())
    print(f"wrote {OUTPUT.relative_to(REPO)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
