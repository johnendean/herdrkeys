"""Configuration and where things live on disk."""

from __future__ import annotations

import os
import tomllib
from dataclasses import dataclass
from pathlib import Path


def _xdg(var: str, default: str) -> Path:
    return Path(os.environ.get(var) or Path.home() / default)


CONFIG_DIR = Path(os.environ.get("HERDR_PLUGIN_CONFIG_DIR") or _xdg("XDG_CONFIG_HOME", ".config") / "herdrkeys")
CONFIG_PATH = CONFIG_DIR / "config.toml"
STATE_PATH = _xdg("XDG_STATE_HOME", ".local/state") / "herdrkeys" / "slots.json"


@dataclass
class Config:
    settle_seconds: float = 0.3
    reconcile_seconds: float = 30.0
    reconnect_min_seconds: float = 0.5
    reconnect_max_seconds: float = 15.0
    activate_terminal: bool = True
    terminal_app: str | None = None
    socket_path: Path | None = None
    serial_port: str | None = None
    state_path: Path = STATE_PATH
    salvage_dir: Path = CONFIG_DIR / "salvage"
    provision: bool = True

    @classmethod
    def load(cls, path: Path = CONFIG_PATH) -> Config:
        try:
            raw = tomllib.loads(path.read_text())
        except (OSError, tomllib.TOMLDecodeError):
            return cls()
        config = cls()
        if "settle_ms" in raw:
            config.settle_seconds = float(raw["settle_ms"]) / 1000.0
        if "reconcile_seconds" in raw:
            config.reconcile_seconds = float(raw["reconcile_seconds"])
        if "provision" in raw:
            config.provision = bool(raw["provision"])
        if raw.get("salvage_dir"):
            config.salvage_dir = Path(str(raw["salvage_dir"])).expanduser()
        if "activate_terminal" in raw:
            config.activate_terminal = bool(raw["activate_terminal"])
        if raw.get("terminal_app"):
            config.terminal_app = str(raw["terminal_app"])
        if raw.get("socket_path"):
            config.socket_path = Path(str(raw["socket_path"])).expanduser()
        if raw.get("serial_port"):
            config.serial_port = str(raw["serial_port"])
        if raw.get("state_path"):
            config.state_path = Path(str(raw["state_path"])).expanduser()
        return config
