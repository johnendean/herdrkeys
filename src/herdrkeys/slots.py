"""The slot map: which pane owns which key.

A pane claims the lowest free slot when it first becomes an agent pane and holds
it until the pane closes -- not until its agent exits. Pane IDs are stable and
never reused by Herdr, so an agent restarted inside a pane keeps its key.

Pure apart from load/save.
"""

from __future__ import annotations

import json
from collections.abc import Iterable
from pathlib import Path

from .model import AGENT_SLOTS

STATE_VERSION = 1


class SlotMap:
    def __init__(self, slots: dict[int, str] | None = None) -> None:
        self._by_slot: dict[int, str] = dict(slots or {})
        self._by_pane: dict[str, int] = {p: s for s, p in self._by_slot.items()}

    # -- queries ---------------------------------------------------------

    def slot_of(self, pane_id: str) -> int | None:
        return self._by_pane.get(pane_id)

    def pane_at(self, slot: int) -> str | None:
        return self._by_slot.get(slot)

    def items(self) -> Iterable[tuple[int, str]]:
        return sorted(self._by_slot.items())

    def as_dict(self) -> dict[int, str]:
        return dict(self._by_slot)

    # -- mutation --------------------------------------------------------

    def assign(self, pane_id: str) -> int | None:
        """Bind a pane to the lowest free slot. Returns None if the grid is full."""
        existing = self._by_pane.get(pane_id)
        if existing is not None:
            return existing
        for slot in AGENT_SLOTS:
            if slot not in self._by_slot:
                self._by_slot[slot] = pane_id
                self._by_pane[pane_id] = slot
                return slot
        return None

    def release(self, pane_id: str) -> None:
        slot = self._by_pane.pop(pane_id, None)
        if slot is not None:
            del self._by_slot[slot]

    def gc(self, live_pane_ids: Iterable[str]) -> None:
        """Drop bindings for panes Herdr no longer knows about."""
        live = set(live_pane_ids)
        for pane_id in [p for p in self._by_pane if p not in live]:
            self.release(pane_id)

    # -- persistence -----------------------------------------------------

    @classmethod
    def load(cls, path: Path) -> SlotMap:
        try:
            raw = json.loads(path.read_text())
        except (OSError, ValueError):
            return cls()
        if raw.get("version") != STATE_VERSION:
            return cls()
        slots = {}
        for key, pane_id in (raw.get("slots") or {}).items():
            try:
                slot = int(key)
            except ValueError:
                continue
            if slot in AGENT_SLOTS and isinstance(pane_id, str):
                slots[slot] = pane_id
        return cls(slots)

    def save(self, path: Path) -> None:
        path.parent.mkdir(parents=True, exist_ok=True)
        payload = {
            "version": STATE_VERSION,
            "slots": {str(s): p for s, p in sorted(self._by_slot.items())},
        }
        tmp = path.with_suffix(".tmp")
        tmp.write_text(json.dumps(payload, indent=2) + "\n")
        tmp.replace(path)
