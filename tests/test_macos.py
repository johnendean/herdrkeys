import os
import threading

from herdrkeys import macos


class Recorder:
    """Stands in for a framework: records every call, returns nothing."""

    def __init__(self) -> None:
        self.calls: list[str] = []

    def __getattr__(self, name):
        def call(*args):
            self.calls.append(name)
            return 0

        return call


def device_with_reader(reader: threading.Thread) -> macos.HidDevice:
    """A HidDevice wired by hand, so no framework or hardware is needed."""
    device = macos.HidDevice.__new__(macos.HidDevice)
    device._cf, device._iokit = Recorder(), Recorder()
    device._ref = 1
    device._open = True
    device._loop = 2
    device._wake_r, device._wake_w = os.pipe()
    device._reader = reader
    return device


def test_closing_stops_the_reader_then_releases_what_it_held():
    stopped = threading.Event()
    reader = threading.Thread(target=stopped.wait, daemon=True)
    reader.start()
    device = device_with_reader(reader)
    device._cf.CFRunLoopStop = lambda loop: stopped.set()
    device.close()
    assert not reader.is_alive()
    assert "CFRelease" in device._cf.calls
    assert "IOHIDDeviceClose" in device._iokit.calls


def test_a_reader_that_will_not_stop_keeps_its_resources(monkeypatch):
    # Releasing the device ref or the pipe under a live reader is a crash, or
    # a write into whatever file reuses the descriptor.
    monkeypatch.setattr(macos.HidDevice, "_POLL_SECONDS", 0.01)
    never = threading.Event()
    reader = threading.Thread(target=never.wait, daemon=True)
    reader.start()
    device = device_with_reader(reader)
    try:
        device.close()
        assert "CFRelease" not in device._cf.calls
        assert "IOHIDDeviceClose" not in device._iokit.calls
        os.fstat(device._wake_w)  # still open: raises if it was closed
    finally:
        never.set()
        reader.join()
        os.close(device._wake_r)
        os.close(device._wake_w)
