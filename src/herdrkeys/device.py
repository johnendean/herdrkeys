"""The keypad, and a stand-in for it.

Wire protocol, newline-delimited JSON in both directions -- the same framing
Herdr itself uses, so `screen <data-port> 115200` is a live debugger.

    host -> device  {"v":2,"t":"hello"}
                    {"v":2,"t":"frame","k":"iwB------------f"}
                    {"v":2,"t":"flash"}          flashes the function key
                    {"v":2,"t":"flash","k":13}   flashes that key instead
                    {"v":2,"t":"hold","on":true} holds the dictation hotkey down
                    {"v":2,"t":"return"}         types Return
    device -> host  {"v":2,"t":"hello","proto":2,"fw":"..."}
                    {"v":2,"t":"key","k":3}      key 3 went down
                    {"v":2,"t":"up","k":3}       ...and came up

Frames are absolute, never deltas: a dropped byte or a board reset self-heals on
the next frame instead of leaving a stale LED lit forever. Key presses fire on
press, not release; releases are reported too, because the microphone is decided
here and a hold is told from a tap by when the key comes up (ADR 0017).
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

    # Whether the board presses keys itself. One that cannot has the daemon
    # type for it (ADR 0017).
    can_type: bool

    def fileno(self) -> int: ...
    def send_frame(self, frame: Frame) -> None: ...
    def flash(self, slot: int | None = None) -> None: ...
    def read_keys(self) -> list[tuple[int, bool]]: ...
    def close(self) -> None: ...
    # A board that animates itself does nothing here and never needs a pass;
    # one that the host draws (ADR 0014) is redrawn by `animate`, and says by
    # `next_change` how soon it next has to be.
    def animate(self, now: float) -> None: ...
    def next_change(self, now: float) -> float | None: ...


def encode_frame(frame: Frame) -> bytes:
    """`c` is omitted entirely when no key has a colour.

    So a board sees exactly the bytes it saw before herdrcolor existed whenever
    that plugin is absent, and the protocol version does not move: the field is
    additive in both directions, old firmware ignoring it and new firmware
    reading its absence as "no colours" (ADR 0010).
    """
    message: dict[str, Any] = {"v": PROTOCOL_VERSION, "t": "frame", "k": frame.keys}
    if any(colour is not None for colour in frame.colours):
        message["c"] = list(frame.colours)
    return json.dumps(message).encode() + b"\n"


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


def encode_hold(on: bool) -> bytes:
    return json.dumps({"v": PROTOCOL_VERSION, "t": "hold", "on": on}).encode() + b"\n"


def encode_return() -> bytes:
    return json.dumps({"v": PROTOCOL_VERSION, "t": "return"}).encode() + b"\n"


_KEY_EDGES = {"key": True, "up": False}


def decode_keys(messages: list[dict[str, Any]]) -> list[tuple[int, bool]]:
    """(slot, went down) for every well-formed key message, in order."""
    keys = []
    for message in messages:
        down = _KEY_EDGES.get(message.get("t"))
        if down is not None and isinstance(message.get("k"), int):
            keys.append((message["k"], down))
    return keys


class SerialDevice:
    """A Keybow on the other end of a CDC data port."""

    can_type = True

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

    def read_keys(self) -> list[tuple[int, bool]]:
        return decode_keys(self._read_messages())

    # The Keybow types for itself (ADR 0006), so it is its own typist; it types
    # only what it is told to, because the microphone is decided here.
    def hold(self, on: bool) -> None:
        self._write(encode_hold(on))

    def type_return(self) -> None:
        self._write(encode_return())

    def close(self) -> None:
        self._serial.close()

    def animate(self, now: float) -> None:
        pass  # the Keybow animates itself

    def next_change(self, now: float) -> float | None:
        return None


class FakeDevice:
    """Records frames instead of lighting LEDs. Lets tests run with no hardware."""

    def __init__(self) -> None:
        self.frames: list[Frame] = []
        self.flashes = 0
        self.flashed_slots: list[int | None] = []
        self.queued_keys: list[tuple[int, bool]] = []
        self.closed = False
        self.firmware = "fake"
        # A fake Keybow types for itself; by default it is a board that cannot.
        self.can_type = False
        self.holding = False
        self.returns = 0

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

    def read_keys(self) -> list[tuple[int, bool]]:
        keys, self.queued_keys = self.queued_keys, []
        return keys

    def close(self) -> None:
        self.closed = True

    def hold(self, on: bool) -> None:
        self.holding = on

    def type_return(self) -> None:
        self.returns += 1

    def animate(self, now: float) -> None:
        pass

    def next_change(self, now: float) -> float | None:
        return None
