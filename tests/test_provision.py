"""Whether we may write to a board is a consent question, not a detection one."""

from pathlib import Path

import pytest

from herdrkeys import provision
from herdrkeys.provision import DriveState

BOOT_OUT = "Adafruit CircuitPython 10.3.0 on 2026-08-31; Pimoroni Keybow 2040 with rp2040\nBoard ID:pimoroni_keybow2040\n"


@pytest.fixture
def volumes(tmp_path):
    root = tmp_path / "Volumes"
    root.mkdir()
    return root


def make_drive(volumes, name="CIRCUITPY", *, board_id=True, code=None):
    drive = volumes / name
    drive.mkdir()
    if board_id:
        (drive / "boot_out.txt").write_text(BOOT_OUT)
    if code is not None:
        (drive / "code.py").write_text(code)
    return drive


def test_a_keybow_is_found_by_board_id_not_volume_name(volumes):
    # Owners rename volumes, and the name says nothing about what board it is.
    make_drive(volumes, "MY KEYPAD")
    assert provision.find_drive(volumes) is not None


def test_other_drives_are_ignored(volumes):
    other = volumes / "Backup Disk"
    other.mkdir()
    (other / "boot_out.txt").write_text("CircuitPython on some other board\nBoard ID:raspberry_pi_pico\n")
    assert provision.find_drive(volumes) is None


def test_no_volumes_at_all_is_not_an_error(tmp_path):
    assert provision.find_drive(tmp_path / "nope") is None


def test_our_own_firmware_may_be_overwritten(volumes):
    make_drive(volumes, code='FIRMWARE = "herdrkeys-device/0.1.0"\n')
    board = provision.inspect(volumes)
    assert board.state is DriveState.OURS and board.may_write


def test_an_empty_board_may_be_written_to(volumes):
    make_drive(volumes)
    board = provision.inspect(volumes)
    assert board.state is DriveState.BLANK and board.may_write

    make_drive(volumes, "BLANK2", code="   \n")
    assert provision.classify(volumes / "BLANK2") is DriveState.BLANK


def test_somebody_elses_firmware_is_left_alone(volumes):
    # The board this project was built on arrived carrying a four-layer HID
    # keyboard its owner had no other copy of.
    make_drive(volumes, code="from pmk import PMK  # someone's macro pad\n")
    board = provision.inspect(volumes)
    assert board.state is DriveState.FOREIGN
    assert not board.may_write, "installing herdrkeys is not consent to destroy this"


def test_salvage_keeps_every_copy(volumes, tmp_path):
    drive = make_drive(volumes, code="original\n")
    (drive / "boot.py").write_text("import usb_hid\n")
    destination = tmp_path / "salvage"

    first = provision.salvage(drive, destination)
    assert {p.name.split("-", 2)[-1] for p in first} == {"code.py", "boot.py", "boot_out.txt"}
    assert (destination / first[0].name).read_text() == "original\n"

    # A second salvage must not clobber the first: it may be the only copy.
    (drive / "code.py").write_text("changed\n")
    second = provision.salvage(drive, destination)
    assert all(p.exists() for p in first + second)


def test_salvaging_an_empty_board_saves_nothing(volumes, tmp_path):
    drive = make_drive(volumes, board_id=False)
    assert provision.salvage(drive, tmp_path / "salvage") == []


def test_the_firmware_that_ships_is_the_firmware_we_check_for():
    # classify() looks for FIRMWARE_MARKER; if device/code.py stopped declaring
    # it, every board would look foreign and nothing would ever provision.
    code = (provision.firmware_dir() / "code.py").read_text()
    assert provision.FIRMWARE_MARKER in code
    for name in provision.FIRMWARE_FILES:
        assert (provision.firmware_dir() / name).is_file()


def test_copy_firmware_writes_both_files(volumes):
    drive = make_drive(volumes)
    assert provision.copy_firmware(drive) == ["boot.py", "code.py"]
    assert provision.classify(drive) is DriveState.OURS, "and the result is recognised as ours"
