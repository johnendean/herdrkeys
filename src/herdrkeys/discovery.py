"""Finding the Keybow's data serial port on macOS.

Enabling `usb_cdc.data` gives the board two `/dev/cu.usbmodem*` ports, console
and data, and macOS names them by interface index -- which shifts with hub
topology, so the path cannot be hardcoded.

Ports are located by USB VID/PID through the IORegistry, and the data port is
the higher CDC interface (CircuitPython puts the console on interface 0). The
choice is then confirmed with a handshake, because writing frames into the
*console* port would feed JSON to the REPL rather than failing cleanly.
"""

from __future__ import annotations

import plistlib
import subprocess
from dataclasses import dataclass

KEYBOW_VID = 0x16D0  # 5840, Pimoroni
KEYBOW_PID = 0x08C6  # 2246, Keybow 2040


@dataclass(frozen=True)
class SerialPort:
    device: str
    interface: int


def _walk_interfaces(node: dict, vid: int | None, pid: int | None, iface: int | None, found: list[SerialPort]) -> None:
    if "idVendor" in node:
        vid, pid = node.get("idVendor"), node.get("idProduct")
    if "bInterfaceNumber" in node:
        iface = node.get("bInterfaceNumber")
    callout = node.get("IOCalloutDevice")
    if callout and vid == KEYBOW_VID and pid == KEYBOW_PID:
        if iface is None:
            suffix = str(node.get("IOTTYSuffix") or "")
            iface = int(suffix[-1]) if suffix[-1:].isdigit() else 0
        found.append(SerialPort(device=callout, interface=int(iface)))
    for child in node.get("IORegistryEntryChildren") or []:
        _walk_interfaces(child, vid, pid, iface, found)


def find_ports() -> list[SerialPort]:
    """Every serial port belonging to a Keybow 2040, lowest interface first."""
    try:
        raw = subprocess.run(
            ["ioreg", "-a", "-r", "-c", "IOUSBHostDevice", "-l"],
            capture_output=True,
            timeout=10,
        ).stdout
    except (OSError, subprocess.SubprocessError):
        return []
    if not raw:
        return []
    try:
        tree = plistlib.loads(raw)
    except Exception:
        return []
    found: list[SerialPort] = []
    for node in tree:
        _walk_interfaces(node, None, None, None, found)
    unique = {port.device: port for port in found}
    return sorted(unique.values(), key=lambda p: p.interface)


def find_data_port() -> str | None:
    """The port frames should go to, or None if the board isn't there.

    With console and data both enabled the data channel is the higher CDC
    interface. With only one port present the firmware has not had `boot.py`
    deployed yet, so there is no data channel to talk to.
    """
    ports = find_ports()
    if len(ports) < 2:
        return None
    return ports[-1].device
