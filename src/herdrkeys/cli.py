"""Command line entry points."""

from __future__ import annotations

import argparse
import logging
import sys

from . import activate, discovery, herdr
from .config import CONFIG_PATH, Config
from .daemon import Daemon
from .device import SerialDevice
from .model import PROTOCOL_VERSION


def _configure_logging(verbose: bool) -> None:
    logging.basicConfig(
        level=logging.DEBUG if verbose else logging.INFO,
        format="%(asctime)s %(levelname)s %(message)s",
    )


def doctor(config: Config) -> int:
    """Report on everything the daemon depends on, without changing anything."""
    ok = True
    socket_path = config.socket_path or herdr.default_socket_path()

    print(f"config file      {CONFIG_PATH}{'' if CONFIG_PATH.exists() else '  (absent, using defaults)'}")
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
        print(f"keybow           {ports[0].device}  console only")
        print("                 no data channel -- deploy device/boot.py and replug (make deploy)")
    else:
        port = config.serial_port or ports[-1].device
        print(f"keybow           {port}  (console {ports[0].device})", end="  ")
        try:
            device = SerialDevice(port)
            print(f"ok  (firmware {device.firmware}, protocol {PROTOCOL_VERSION})")
            device.close()
        except Exception as exc:
            ok = False
            print(f"NO HANDSHAKE  ({exc})")

    app = config.terminal_app or activate.detect_host_app()
    if config.activate_terminal:
        print(f"terminal app     {app or 'UNDETECTED  (set terminal_app in config.toml)'}")
    else:
        print("terminal app     disabled in config")

    return 0 if ok else 1


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="herdrkeys", description="Bind a Keybow 2040 to Herdr.")
    parser.add_argument("-v", "--verbose", action="store_true")
    sub = parser.add_subparsers(dest="command")
    sub.add_parser("run", help="run the daemon against the real keypad (default)")
    sub.add_parser("tui", help="run the daemon against a keypad drawn in the terminal")
    sub.add_parser("doctor", help="check everything the daemon depends on")
    args = parser.parse_args(argv)

    _configure_logging(args.verbose)
    config = Config.load()

    if args.command == "doctor":
        return doctor(config)

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
