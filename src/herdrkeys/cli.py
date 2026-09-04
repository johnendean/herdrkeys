"""Command line entry points."""

from __future__ import annotations

import argparse
import logging
import sys

import os
import subprocess

from . import activate, discovery, herdr, provision
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
    print(f"slot map         {config.state_path}{'' if config.state_path.exists() else '  (absent, will be created)'}")

    print(f"herdr socket     {socket_path}", end="  ")
    try:
        snapshot = herdr.snapshot(socket_path)
        agents = [p for p in (snapshot.get("panes") or []) if p.get("agent")]
        print(f"ok  ({len(snapshot.get('panes') or [])} panes, {len(agents)} agent panes)")
    except (OSError, herdr.HerdrError) as exc:
        ok = False
        print(f"UNREACHABLE  ({exc})")

    ports = discovery.find_ports()
    if not ports:
        ok = False
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
    parser = argparse.ArgumentParser(prog="herdrkeys", description="Bind a Keybow 2040 to Herdr.")
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

    try:
        Daemon(config).run_forever()
    except KeyboardInterrupt:
        return 0
    return 0


if __name__ == "__main__":
    sys.exit(main())
