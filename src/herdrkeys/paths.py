"""Where config and state live, resolved the same way however herdrkeys started.

herdrkeys can be launched three ways -- by Herdr as a plugin action, by Herdr's
startup hook, or by hand from a checkout -- and only the first two get
`HERDR_PLUGIN_CONFIG_DIR` in the environment. Keying off that variable alone
meant the same command read a different config file depending on how it was
invoked, which is the sort of thing that costs an hour to notice.

So the plugin's directory is resolved whether or not Herdr set the variable, and
one order of precedence applies everywhere.

State is deliberately *not* treated this way: it always lives under XDG, so the
slot map and the log stay in one place no matter who started the daemon.
"""

from __future__ import annotations

import os
import shutil
import subprocess
from dataclasses import dataclass, field
from pathlib import Path

PLUGIN_ID = "herdrkeys"
CONFIG_FILE = "config.toml"


def _xdg(var: str, default: str) -> Path:
    return Path(os.environ.get(var) or Path.home() / default)


def xdg_config_dir() -> Path:
    return _xdg("XDG_CONFIG_HOME", ".config") / "herdrkeys"


def state_dir() -> Path:
    """Always XDG, never the plugin's state dir: one slot map, one log."""
    return _xdg("XDG_STATE_HOME", ".local/state") / "herdrkeys"


def plugin_config_dir() -> Path | None:
    """The plugin's config directory, as Herdr itself computes it.

    Asks the binary rather than rebuilding the path here, because named sessions
    move Herdr's config root and duplicating that logic would drift.
    """
    explicit = os.environ.get("HERDR_PLUGIN_CONFIG_DIR")
    if explicit:
        return Path(explicit)
    binary = os.environ.get("HERDR_BIN_PATH") or shutil.which("herdr")
    if not binary:
        return None
    try:
        result = subprocess.run(
            [binary, "plugin", "config-dir", PLUGIN_ID],
            capture_output=True, text=True, timeout=5,
        )
    except (OSError, subprocess.SubprocessError):
        return None
    path = result.stdout.strip()
    return Path(path) if result.returncode == 0 and path else None


@dataclass
class ConfigLocation:
    path: Path                      # the file in effect, whether or not it exists
    exists: bool
    shadowed: list[Path] = field(default_factory=list)  # real files being ignored


def resolve_config() -> ConfigLocation:
    """Pick one config file, preferring the plugin's when Herdr knows about it.

    A directory Herdr has created counts as the plugin being present, since
    `plugin config-dir` answers for uninstalled plugins too and so cannot be
    used to tell them apart.
    """
    candidates: list[Path] = []
    plugin_dir = plugin_config_dir()
    if plugin_dir is not None:
        candidates.append(plugin_dir)
    candidates.append(xdg_config_dir())

    files = [directory / CONFIG_FILE for directory in candidates]
    present = [f for f in files if f.is_file()]
    if present:
        return ConfigLocation(path=present[0], exists=True, shadowed=present[1:])

    for directory, file in zip(candidates, files):
        if directory.is_dir():
            return ConfigLocation(path=file, exists=False)
    return ConfigLocation(path=files[-1], exists=False)
