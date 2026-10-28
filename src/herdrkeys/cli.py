"""Command line entry points."""

from __future__ import annotations

import argparse
import logging
import signal
import sys

import os
import subprocess

from . import activate, cn001, discovery, herdr, herdr_state, macos, provision
from .config import Config
from .paths import resolve_config
from .daemon import PROCESS_MARKER, Daemon
from .device import SerialDevice
from .model import PROTOCOL_VERSION


def _configure_logging(verbose: bool) -> None:
    logging.basicConfig(
        level=logging.DEBUG if verbose else logging.INFO,
        format="%(asctime)s %(levelname)s %(message)s",
    )


def running_daemon_pid() -> int | None:
    """The PID of an already-running daemon, if there is one.

    Only one process can usefully hold the keypad's serial port, so knowing this
    is what stops `doctor` reporting a healthy setup as a firmware failure.
    """
    try:
        raw = subprocess.run(["ps", "-Ao", "pid=,command="], capture_output=True,
                             text=True, timeout=5).stdout
    except (OSError, subprocess.SubprocessError):
        return None
    ours = os.getpid()
    for line in raw.splitlines():
        parts = line.strip().split(None, 1)
        if len(parts) != 2 or not parts[1].endswith(PROCESS_MARKER):
            continue
        try:
            pid = int(parts[0])
        except ValueError:
            continue
        if pid != ours:
            return pid
    return None


def _colour_report(agent_panes: list[dict]) -> str:
    """How many agents carry a project colour, and what that means for the keys."""
    if not agent_panes:
        return "no agents to colour"
    coloured = [p for p in agent_panes if herdr_state._colour_of(p) is not None]
    if not coloured:
        return (
            f"none of {len(agent_panes)} agents; idle keys use agent state "
            "(install herdrcolor, or run its sync)"
        )
    if len(coloured) == len(agent_panes):
        return f"all {len(agent_panes)} agents; idle keys wear their project colour"
    return (
        f"{len(coloured)} of {len(agent_panes)} agents; the rest fall back to "
        "agent state (herdrcolor may not have synced since they appeared)"
    )


def _cn001_report(daemon_pid: int | None) -> tuple[bool, bool, str]:
    """(attached, healthy, what to print) for the Soomfon CN001.

    Absent is not a failure on its own; `doctor` decides that once it knows
    whether there is a Keybow instead.
    """
    try:
        found = macos.find_hid(cn001.VENDOR_ID, cn001.PRODUCT_ID, cn001.USAGE_PAGE)
    except OSError as exc:
        return False, False, f"UNAVAILABLE  ({exc})"
    if not found:
        return False, True, "not attached  (1500:3003)"
    (info, ref), *others = found
    for _other, extra in others:
        macos.release(extra)
    where = f"serial {info.serial}" if info.serial else "attached"
    # Probed even while the daemon runs: a running daemon does not mean it got
    # the board -- another app may hold it, and the daemon only retries. Our
    # open never seizes the device, so it succeeds alongside the daemon's.
    try:
        macos.HidDevice(ref, input_size=cn001.INPUT_SIZE).close()
    except OSError as exc:
        hint = "  -- quit the Soomfon Stream Controller app" if "Exclusive" in str(exc) else ""
        return True, False, f"{where}  WILL NOT OPEN  ({exc}){hint}"
    if daemon_pid is not None:
        return True, True, f"{where}  ok, opens alongside the running daemon (pid {daemon_pid})"
    return True, True, f"{where}  ok"


def doctor(config: Config) -> int:
    """Report on everything the daemon depends on, without changing anything."""
    ok = True
    socket_path = config.socket_path or herdr.default_socket_path()
    daemon_pid = running_daemon_pid()

    location = resolve_config()
    print(f"config file      {location.path}{'' if location.exists else '  (absent, using defaults)'}")
    for ignored in location.shadowed:
        ok = False
        print(f"                 IGNORING {ignored} -- two config files, only the first is read")
    print(f"herdr socket     {socket_path}", end="  ")
    agents: list[dict] = []
    reachable = True
    try:
        snapshot = herdr.snapshot(socket_path)
        agents = [p for p in (snapshot.get("panes") or []) if p.get("agent")]
        print(f"ok  ({len(snapshot.get('panes') or [])} panes, {len(agents)} agent panes)")
    except (OSError, herdr.HerdrError) as exc:
        ok = False
        reachable = False
        print(f"UNREACHABLE  ({exc})")

    if reachable:
        # A key showing its state colour and a key whose agent has no project
        # colour look identical on the board, by design -- so this is the only
        # place the difference can be seen. Not a failure either way: no
        # colours is what herdrkeys did before herdrcolor existed.
        print(f"colours          {_colour_report(agents)}")

    ports = discovery.find_ports()
    cn001_present, cn001_ok, cn001_line = _cn001_report(daemon_pid)
    if not ports:
        # One board is enough: a missing Keybow is a failure only when there
        # is no other board for the daemon to show anything on -- decided
        # below, once the CN001 has been reported.
        print("keybow           NOT FOUND  (no 16d0:08c6 serial ports; is it plugged in?)")
    elif len(ports) == 1:
        ok = False
        print(f"keybow           {ports[0].device}  console only, no data channel")
        board = provision.inspect()
        if board is None:
            print("                 CIRCUITPY not mounted; cannot provision it")
        elif board.may_write:
            print(f"                 {board.drive} is {board.state.value}; the daemon will provision it")
        else:
            print(f"                 {board.drive} carries other firmware -- run 'herdrkeys adopt' to save it and take over")
    else:
        port = config.serial_port or ports[-1].device
        print(f"keybow           {port}  (console {ports[0].device})", end="  ")
        if daemon_pid is not None:
            # Probing now would take the port from under the daemon, or get no
            # reply because the daemon read it. Neither proves anything.
            print(f"in use by the running daemon (pid {daemon_pid})")
        else:
            try:
                device = SerialDevice(port)
                print(f"ok  (firmware {device.firmware}, protocol {PROTOCOL_VERSION})")
                device.close()
            except Exception as exc:
                ok = False
                print(f"NO HANDSHAKE  ({exc})")

    print(f"cn001            {cn001_line}")
    if cn001_present:
        ok = ok and cn001_ok
    elif not ports:
        ok = False

    print(f"daemon           {'running, pid ' + str(daemon_pid) if daemon_pid else 'not running  (make install-agent, or make run)'}")

    app = config.terminal_app or activate.detect_host_app()
    if config.activate_terminal:
        print(f"terminal app     {app or 'UNDETECTED  (set terminal_app in config.toml)'}")
    else:
        print("terminal app     disabled in config")

    return 0 if ok else 1


def adopt(config: Config) -> int:
    """Take over a board carrying somebody else's firmware, saving it first."""
    board = provision.inspect()
    if board is None:
        print("no Keybow 2040 found -- plug it in and check its CIRCUITPY drive is mounted")
        return 1

    print(f"board            {board.drive}  ({board.state.value})")
    if board.state is provision.DriveState.OURS:
        print("already running herdrkeys; nothing to salvage")
    else:
        saved = provision.salvage(board.drive, config.salvage_dir)
        if saved:
            print(f"saved            {len(saved)} file(s) to {config.salvage_dir}")
            for path in saved:
                print(f"                 {path.name}")
        else:
            print("nothing to salvage; the board is empty")

    try:
        provision.copy_firmware(board.drive)
    except (OSError, FileNotFoundError) as exc:
        print(f"could not write the firmware: {exc}")
        return 1
    print("wrote            boot.py, code.py")

    ports = discovery.find_ports()
    if ports and provision.hard_reset(ports[0].device):
        print("reset            board re-enumerating; it will reconnect in a few seconds")
    else:
        print("reset            FAILED -- unplug and replug the board to finish")
        return 1
    return 0


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="herdrkeys", description="Bind a Keybow 2040 or a Soomfon CN001 to Herdr.")
    parser.add_argument("-v", "--verbose", action="store_true")
    sub = parser.add_subparsers(dest="command")
    sub.add_parser("run", help="run the daemon against the real keypad (default)")
    sub.add_parser("tui", help="run the daemon against a keypad drawn in the terminal")
    sub.add_parser("doctor", help="check everything the daemon depends on")
    sub.add_parser("adopt", help="take over a board, saving any firmware already on it")
    args = parser.parse_args(argv)

    _configure_logging(args.verbose)
    config = Config.load()

    if args.command == "doctor":
        return doctor(config)

    if args.command == "adopt":
        return adopt(config)

    if args.command == "tui":
        from .tui import TuiDevice

        device = TuiDevice()
        daemon = Daemon(config, device_factory=lambda: device)
        try:
            daemon.run_forever()
        except KeyboardInterrupt:
            return 0
        finally:
            device.close()
        return 0

    # launchd and `kill` stop the daemon with SIGTERM. Raised as an exception it
    # unwinds through `close`, which is the only chance there is to blank the
    # CN001: that panel keeps whatever it was last sent, indefinitely.
    signal.signal(signal.SIGTERM, lambda _signum, _frame: sys.exit(0))
    daemon = Daemon(config)
    try:
        daemon.run_forever()
    except KeyboardInterrupt:
        return 0
    finally:
        daemon.close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
