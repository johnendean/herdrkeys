"""Bringing the terminal that hosts Herdr to the front.

Focusing an agent you cannot see is not focusing it: press a key from Slack and
Herdr's focus moves somewhere off-screen. So a key press also activates the
host terminal app.

The app is found by walking up from a Herdr *client* process to the first
`/Applications/*.app` ancestor -- the server is detached with ppid 1 and has no
terminal above it, so it is useless for this. Verified on this machine as:

    herdr -> zsh -> login -> iTermServer-3.6.11 -> /Applications/iTerm.app

The walk can legitimately fail (a detached session, `--remote`, or no client
attached at all), which is why config can name the app outright.
"""

from __future__ import annotations

import re
import subprocess

_APP_PATH = re.compile(r"^(/Applications/.+?\.app)/")


def _processes() -> list[tuple[int, int, str]]:
    try:
        raw = subprocess.run(
            ["ps", "-Ao", "pid=,ppid=,command="], capture_output=True, text=True, timeout=5
        ).stdout
    except (OSError, subprocess.SubprocessError):
        return []
    rows = []
    for line in raw.splitlines():
        parts = line.strip().split(None, 2)
        if len(parts) < 3:
            continue
        try:
            rows.append((int(parts[0]), int(parts[1]), parts[2]))
        except ValueError:
            continue
    return rows


def detect_host_app() -> str | None:
    """The .app bundle hosting a Herdr client, or None if it can't be determined."""
    rows = _processes()
    by_pid = {pid: (ppid, command) for pid, ppid, command in rows}

    clients = [
        pid
        for pid, _ppid, command in rows
        if command.rstrip().endswith("herdr") or "/herdr" in command.split()[0]
        if " server" not in command
    ]
    for pid in clients:
        seen = set()
        cursor = pid
        for _ in range(12):
            if cursor in seen or cursor <= 1:
                break
            seen.add(cursor)
            entry = by_pid.get(cursor)
            if entry is None:
                break
            ppid, command = entry
            match = _APP_PATH.match(command)
            if match:
                return match.group(1)
            cursor = ppid
    return None


def activate(app_path: str) -> None:
    """Raise an already-running app. Best effort: never worth failing a key press."""
    try:
        subprocess.run(["open", "-a", app_path], capture_output=True, timeout=5)
    except (OSError, subprocess.SubprocessError):
        pass
