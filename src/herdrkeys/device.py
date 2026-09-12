"""The keypad, and a stand-in for it.

Wire protocol, newline-delimited JSON in both directions -- the same framing
Herdr itself uses, so `screen <data-port> 115200` is a live debugger.

    host -> device  {"v":1,"t":"hello"}
                    {"v":1,"t":"frame","k":"iwB------------f"}
                    {"v":1,"t":"flash"}          flashes the function key
                    {"v":1,"t":"flash","k":13}   flashes that key instead
    device -> host  {"v":1,"t":"hello","proto":1,"fw":"..."}
                    {"v":1,"t":"key","k":3}

Frames are absolute, never deltas: a dropped byte or a board reset self-heals on
the next frame instead of leaving a stale LED lit forever. Key presses fire on
press, not release.
"""

from __future__ import annotations

import json
import time
from typing import Any, Protocol

from .model import PROTOCOL_VERSION, Frame
from .serial_port import SerialPort


class DeviceError(RuntimeError):
    pass


class Device(Protocol):
    """What the daemon needs from a keypad. Implemented by real and fake alike."""

    def fileno(self) -> int: ...
    def send_frame(self, frame: Frame) -> None: ...
    def flash(self, slot: int | None = None) -> None: ...
    def read_presses(self) -> list[int]: ...
    def close(self) -> None: ...


def encode_frame(frame: Frame) -> bytes:
    return json.dumps({"v": PROTOCOL_VERSION, "t": "frame", "k": frame.keys}).encode() + b"\n"


def encode_flash(slot: int | None = None) -> bytes:
    """Ask the board to flash a key. Omit the slot for the function key.

    The slot is optional so that older firmware, which knows nothing about it,
    still flashes something rather than nothing.
    """
    message: dict[str, Any] = {"v": PROTOCOL_VERSION, "t": "flash"}
    if slot is not None:
        message["k"] = slot
    return json.dumps(message).encode() + b"\n"


def encode_hello() -> bytes:
    return json.dumps({"v": PROTOCOL_VERSION, "t": "hello"}).encode() + b"\n"


def decode_presses(messages: list[dict[str, Any]]) -> list[int]:
    presses = []
    for message in messages:
        if message.get("t") == "key" and isinstance(message.get("k"), int):
            presses.append(message["k"])
    return presses


class SerialDevice:
    """A Keybow on the other end of a CDC data port."""

    def __init__(self, port: str, *, baudrate: int = 115200, handshake_timeout: float = 3.0) -> None:
        self._serial = SerialPort(port, baudrate)
        self.port = port
        self._buffer = b""
        self.firmware = self._handshake(handshake_timeout)

    def _handshake(self, timeout: float) -> str:
        """Confirm a herdrkeys firmware is listening, and that its protocol matches."""
        self._serial.reset_input()
        self._serial.write(encode_hello())
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            for message in self._read_messages():
                if message.get("t") != "hello":
                    continue
                if message.get("proto") != PROTOCOL_VERSION:
                    raise DeviceError(
                        f"{self.port}: firmware speaks protocol {message.get('proto')}, "
                        f"herdrkeys speaks {PROTOCOL_VERSION} -- redeploy device/code.py"
                    )
                return str(message.get("fw") or "unknown")
            time.sleep(0.05)
        raise DeviceError(f"{self.port}: no hello from the keypad -- is device/code.py deployed?")

    def _read_messages(self) -> list[dict[str, Any]]:
        try:
            chunk = self._serial.read()
        except OSError as exc:  # the port goes away when the board is unplugged
            raise DeviceError(f"{self.port}: {exc}") from exc
        if chunk:
            self._buffer += chunk
        messages = []
        while b"\n" in self._buffer:
            line, self._buffer = self._buffer.split(b"\n", 1)
            line = line.strip()
            if not line:
                continue
            try:
                messages.append(json.loads(line))
            except ValueError:
                continue  # device debug prints are not our problem
        return messages

    def fileno(self) -> int:
        return self._serial.fileno()

    def _write(self, payload: bytes) -> None:
        try:
            self._serial.write(payload)
        except OSError as exc:
            raise DeviceError(f"{self.port}: {exc}") from exc

    def send_frame(self, frame: Frame) -> None:
        self._write(encode_frame(frame))

    def flash(self, slot: int | None = None) -> None:
        self._write(encode_flash(slot))

    def read_presses(self) -> list[int]:
        return decode_presses(self._read_messages())

    def close(self) -> None:
        self._serial.close()


class FakeDevice:
    """Records frames instead of lighting LEDs. Lets tests run with no hardware."""

    def __init__(self) -> None:
        self.frames: list[Frame] = []
        self.flashes = 0
        self.flashed_slots: list[int | None] = []
        self.queued_presses: list[int] = []
        self.closed = False
        self.firmware = "fake"

    @property
    def last_frame(self) -> Frame | None:
        return self.frames[-1] if self.frames else None

    def fileno(self) -> int:
        raise NotImplementedError("FakeDevice is driven directly, not polled")

    def send_frame(self, frame: Frame) -> None:
        self.frames.append(frame)

    def flash(self, slot: int | None = None) -> None:
        self.flashes += 1
        self.flashed_slots.append(slot)

    def read_presses(self) -> list[int]:
        presses, self.queued_presses = self.queued_presses, []
        return presses

    def close(self) -> None:
        self.closed = True
