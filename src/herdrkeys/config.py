"""Configuration and where things live on disk."""

from __future__ import annotations

import tomllib
from dataclasses import dataclass
from pathlib import Path


from .paths import resolve_config, state_dir, xdg_config_dir

STATE_PATH = state_dir() / "slots.json"


@dataclass
class Config:
    settle_seconds: float = 0.3
    status_poll_seconds: float = 0.5
    reconcile_seconds: float = 30.0
    reconnect_min_seconds: float = 0.5
    reconnect_max_seconds: float = 15.0
    activate_terminal: bool = True
    terminal_app: str | None = None
    socket_path: Path | None = None
    serial_port: str | None = None
    state_path: Path = STATE_PATH
    salvage_dir: Path | None = None  # defaults beside the config file in effect
    provision: bool = True

    def __post_init__(self) -> None:
        if self.salvage_dir is None:
            self.salvage_dir = xdg_config_dir() / "salvage"

    @classmethod
    def load(cls, path: Path | None = None) -> Config:
        location = resolve_config()
        path = path or location.path
        config = cls()
        config.salvage_dir = path.parent / "salvage"
        try:
            raw = tomllib.loads(path.read_text())
        except (OSError, tomllib.TOMLDecodeError):
            return config
        if "settle_ms" in raw:
            config.settle_seconds = float(raw["settle_ms"]) / 1000.0
        if "status_poll_ms" in raw:
            config.status_poll_seconds = float(raw["status_poll_ms"]) / 1000.0
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
