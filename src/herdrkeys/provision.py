"""Getting the firmware onto a board, and deciding whether we may.

Copying two files is the easy part. The hard parts are that `boot.py` only takes
effect on a USB re-enumeration, and that the board we found might not be ours to
overwrite.

The re-enumeration turns out to be automatable: breaking into the CircuitPython
REPL over the console serial and calling `microcontroller.reset()` performs a
genuine hard reset. Measured on a Keybow 2040: the USB nodes disappear entirely
and are back about two seconds later, with `boot.py` applied.

The consent question is the more important one. A Keybow that shows up on this
machine may be carrying firmware its owner cares about and has no copy of, so
herdrkeys provisions only a board that is already running herdrkeys or is
carrying nothing at all. Anything else is left alone until someone asks for it
by running `herdrkeys adopt`, which salvages what is there first.
"""

from __future__ import annotations

import shutil
import subprocess
import time
from dataclasses import dataclass
from enum import Enum
from pathlib import Path

from .serial_port import SerialPort

BOARD_ID = "pimoroni_keybow2040"
FIRMWARE_MARKER = "herdrkeys-device/"
FIRMWARE_FILES = ("boot.py", "code.py")

REPL_PROMPT = b">>> "
REPL_TIMEOUT = 5.0
RESET_SETTLE = 0.5


class DriveState(Enum):
    """What is on the board, and therefore whether we may write to it."""

    OURS = "ours"          # already running herdrkeys; safe to update
    BLANK = "blank"        # no code.py; nothing to lose
    FOREIGN = "foreign"    # somebody else's firmware; needs consent


@dataclass(frozen=True)
class Board:
    drive: Path
    state: DriveState

    @property
    def may_write(self) -> bool:
        return self.state in (DriveState.OURS, DriveState.BLANK)


def find_drive(volumes: Path = Path("/Volumes")) -> Path | None:
    """The mounted CIRCUITPY drive of a Keybow 2040, identified by boot_out.txt.

    Matched on board ID rather than volume name, which the owner may have
    renamed and which says nothing about what board it is.
    """
    try:
        candidates = sorted(volumes.iterdir())
    except OSError:
        return None
    for candidate in candidates:
        marker = candidate / "boot_out.txt"
        try:
            if BOARD_ID in marker.read_text(errors="replace"):
                return candidate
        except (OSError, UnicodeError):
            continue
    return None


def classify(drive: Path) -> DriveState:
    code = drive / "code.py"
    try:
        contents = code.read_text(errors="replace")
    except FileNotFoundError:
        return DriveState.BLANK
    except OSError:
        return DriveState.FOREIGN  # unreadable: assume it matters
    if FIRMWARE_MARKER in contents:
        return DriveState.OURS
    return DriveState.BLANK if not contents.strip() else DriveState.FOREIGN


def inspect(volumes: Path = Path("/Volumes")) -> Board | None:
    drive = find_drive(volumes)
    return None if drive is None else Board(drive=drive, state=classify(drive))


def firmware_dir() -> Path:
    """Where boot.py and code.py live, in a checkout or an installed plugin."""
    package = Path(__file__).resolve().parent
    for candidate in (package / "device", package.parent.parent / "device"):
        if (candidate / "code.py").is_file():
            return candidate
    raise FileNotFoundError("cannot locate the device/ firmware next to the package")


def salvage(drive: Path, destination: Path) -> list[Path]:
    """Copy whatever firmware is on the board somewhere safe. Never overwrites."""
    destination.mkdir(parents=True, exist_ok=True)
    saved = []
    stamp = time.strftime("%Y%m%d-%H%M%S")
    for name in ("code.py", "boot.py", "boot_out.txt"):
        source = drive / name
        if not source.is_file():
            continue
        target = destination / f"{stamp}-{name}"
        shutil.copy2(source, target)
        saved.append(target)
    return saved


def copy_firmware(drive: Path, source: Path | None = None) -> list[str]:
    source = source or firmware_dir()
    written = []
    for name in FIRMWARE_FILES:
        shutil.copy(source / name, drive / name)
        written.append(name)
    subprocess.run(["sync"], capture_output=True, timeout=30)
    return written


def hard_reset(console_port: str, *, timeout: float = REPL_TIMEOUT) -> bool:
    """Re-enumerate the board so a new boot.py takes effect.

    Interrupts the running code.py to reach the REPL, then resets. Waits for the
    prompt rather than guessing a delay -- a fixed one-second wait was too short
    on a real board, which left it sitting at a dead REPL with the LEDs off.
    """
    try:
        port = SerialPort(console_port)
    except OSError:
        return False
    try:
        port.reset_input()
        port.write(b"\x03")  # ctrl-c: interrupt code.py
        buffer = b""
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            buffer += port.read()
            if REPL_PROMPT in buffer:
                break
            time.sleep(0.05)
        else:
            return False
        port.write(b"import microcontroller; microcontroller.reset()\r\n")
        time.sleep(RESET_SETTLE)
        return True
    except OSError:
        return False
    finally:
        port.close()
