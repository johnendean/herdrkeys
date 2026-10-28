"""`doctor` is the troubleshooting tool, so it must not report false failures."""

import herdrkeys.cli as cli
from herdrkeys.config import Config


class Ports:
    def __init__(self, *devices):
        self.devices = devices

    def __call__(self):
        return [type("P", (), {"device": d, "interface": i})() for i, d in enumerate(self.devices)]


def run_doctor(monkeypatch, capsys, *, daemon_pid, probe_raises=False, keybow=True, cn001=None):
    monkeypatch.setattr(cli, "running_daemon_pid", lambda: daemon_pid)
    monkeypatch.setattr(cli.discovery, "find_ports", Ports("/dev/console", "/dev/data") if keybow else Ports())
    # The real probe would find whatever is on this machine's USB bus.
    monkeypatch.setattr(cli, "_cn001_report", lambda pid: cn001 or (False, True, "not attached"))
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


def test_a_cn001_alone_is_enough(monkeypatch, capsys):
    code, out = run_doctor(monkeypatch, capsys, daemon_pid=None, keybow=False,
                           cn001=(True, True, "serial X  ok"))
    assert code == 0
    assert "keybow           NOT FOUND" in out


def test_no_board_at_all_is_a_failure(monkeypatch, capsys):
    code, _out = run_doctor(monkeypatch, capsys, daemon_pid=None, keybow=False)
    assert code == 1


def test_a_cn001_that_will_not_open_fails_even_with_a_daemon_running(monkeypatch, capsys):
    # A running daemon only retries a board another app holds; it does not
    # prove it has one.
    code, out = run_doctor(monkeypatch, capsys, daemon_pid=4321, keybow=False,
                           cn001=(True, False, "serial X  WILL NOT OPEN"))
    assert code == 1
    assert "WILL NOT OPEN" in out


def test_the_cn001_is_probed_even_while_the_daemon_runs(monkeypatch, capsys):
    opened = []

    class Hid:
        def __init__(self, ref, *, input_size):
            opened.append(ref)

        def close(self):
            pass

    info = cli.macos.HidInfo(0x1500, 0x3003, 0xFFA0, 1, "X", "CN001")
    monkeypatch.setattr(cli.macos, "find_hid", lambda *a: [(info, 7)])
    monkeypatch.setattr(cli.macos, "HidDevice", Hid)
    present, healthy, line = cli._cn001_report(4321)
    assert opened == [7]
    assert present and healthy
    assert "pid 4321" in line
