"""`doctor` is the troubleshooting tool, so it must not report false failures."""

import herdrkeys.cli as cli
from herdrkeys.config import Config


class Ports:
    def __init__(self, *devices):
        self.devices = devices

    def __call__(self):
        return [type("P", (), {"device": d, "interface": i})() for i, d in enumerate(self.devices)]


def run_doctor(monkeypatch, capsys, *, daemon_pid, probe_raises=False):
    monkeypatch.setattr(cli, "running_daemon_pid", lambda: daemon_pid)
    monkeypatch.setattr(cli.discovery, "find_ports", Ports("/dev/console", "/dev/data"))
    monkeypatch.setattr(cli.herdr, "snapshot", lambda path: {"panes": []})
    monkeypatch.setattr(cli.activate, "detect_host_app", lambda: "/Applications/Test.app")

    def probe(port):
        raise AssertionError("doctor must not open a port the daemon holds")

    monkeypatch.setattr(cli, "SerialDevice", probe if daemon_pid else _ok_device)
    code = cli.doctor(Config())
    return code, capsys.readouterr().out


class _ok_device:
    def __init__(self, port):
        self.firmware = "herdrkeys-device/0.1.0"

    def close(self):
        pass


def test_a_port_held_by_the_daemon_is_not_a_failure(monkeypatch, capsys):
    # Probing it would either take the port from under the running daemon or get
    # no reply because the daemon read it. Reported as a firmware fault before
    # this, while the keypad was demonstrably working.
    code, out = run_doctor(monkeypatch, capsys, daemon_pid=4321)
    assert code == 0
    assert "in use by the running daemon (pid 4321)" in out
    assert "NO HANDSHAKE" not in out
    assert "running, pid 4321" in out


def test_with_no_daemon_the_keypad_is_probed_directly(monkeypatch, capsys):
    code, out = run_doctor(monkeypatch, capsys, daemon_pid=None)
    assert code == 0
    assert "herdrkeys-device/0.1.0" in out
    assert "not running" in out


def test_the_daemon_does_not_find_itself(monkeypatch):
    import os

    monkeypatch.setattr(
        cli.subprocess,
        "run",
        lambda *a, **k: type("R", (), {"stdout": f"{os.getpid()} python -m herdrkeys run\n"})(),
    )
    assert cli.running_daemon_pid() is None
