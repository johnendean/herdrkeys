"""A serial port, using only the standard library.

pyserial would do this, but a third-party dependency is the difference between
a plugin that installs with no build step and one that needs a working pip and
a network at install time. This is the whole of what herdrkeys needs from it.
"""

from __future__ import annotations

import os
import termios

BAUD_RATES = {
    9600: termios.B9600,
    19200: termios.B19200,
    38400: termios.B38400,
    57600: termios.B57600,
    115200: termios.B115200,
}


class SerialPort:
    """A raw, non-blocking serial port.

    Reads never block and return b"" when nothing is waiting. Every failure is
    an OSError, including the ENXIO/EIO the kernel raises once a USB device is
    unplugged mid-read.
    """

    def __init__(self, path: str, baudrate: int = 115200) -> None:
        self.path = path
        self._fd = os.open(path, os.O_RDWR | os.O_NOCTTY | os.O_NONBLOCK)
        try:
            self._configure(baudrate)
        except Exception:
            self.close()
            raise

    def _configure(self, baudrate: int) -> None:
        speed = BAUD_RATES.get(baudrate)
        if speed is None:
            raise ValueError(f"unsupported baud rate {baudrate}")
        attrs = termios.tcgetattr(self._fd)
        attrs[0] = 0  # iflag: no translation, no flow control
        attrs[1] = 0  # oflag: no post-processing
        attrs[2] = termios.CS8 | termios.CREAD | termios.CLOCAL
        attrs[3] = 0  # lflag: no canonical mode, no echo, no signals
        attrs[4] = speed
        attrs[5] = speed
        attrs[6] = list(attrs[6])
        attrs[6][termios.VMIN] = 0
        attrs[6][termios.VTIME] = 0
        termios.tcsetattr(self._fd, termios.TCSANOW, attrs)

    def fileno(self) -> int:
        return self._fd

    def read(self, size: int = 4096) -> bytes:
        try:
            return os.read(self._fd, size)
        except BlockingIOError:
            return b""

    def write(self, data: bytes) -> None:
        written = 0
        while written < len(data):
            try:
                written += os.write(self._fd, data[written:])
            except BlockingIOError:
                continue

    def reset_input(self) -> None:
        try:
            termios.tcflush(self._fd, termios.TCIFLUSH)
        except OSError:
            pass

    def close(self) -> None:
        fd, self._fd = getattr(self, "_fd", -1), -1
        if fd >= 0:
            try:
                os.close(fd)
            except OSError:
                pass

    def __enter__(self) -> SerialPort:
        return self

    def __exit__(self, *_exc) -> None:
        self.close()
