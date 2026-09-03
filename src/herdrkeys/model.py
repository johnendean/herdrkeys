"""Core domain types and the frame encoding.

Pure: no I/O, no clock, no serial, no sockets.
"""

from __future__ import annotations

from dataclasses import dataclass
from enum import Enum

SLOT_COUNT = 16
FN_SLOT = 15
AGENT_SLOTS = tuple(range(FN_SLOT))  # 0..14

PROTOCOL_VERSION = 1


class AgentState(str, Enum):
    """Herdr's agent lifecycle states. Closed set: see CONTEXT.md."""

    IDLE = "idle"
    WORKING = "working"
    BLOCKED = "blocked"
    DONE = "done"
    UNKNOWN = "unknown"


# A frame is one character per key. Lowercase is unfocused, uppercase focused.
# The device owns the palette; these codes carry meaning, never colour.
EMPTY = "-"
FN_CONNECTED = "f"
FN_DISCONNECTED = "x"

_STATE_CODE = {
    AgentState.IDLE: "i",
    AgentState.WORKING: "w",
    AgentState.BLOCKED: "b",
    AgentState.DONE: "d",
    AgentState.UNKNOWN: "u",
}


def state_code(state: AgentState, *, focused: bool) -> str:
    code = _STATE_CODE[state]
    return code.upper() if focused else code


@dataclass(frozen=True)
class AgentPane:
    """An agent pane as herdrkeys cares about it."""

    pane_id: str
    agent: str
    state: AgentState
    focused: bool


@dataclass(frozen=True)
class Frame:
    """What all 16 keys should show. Absolute, never a delta."""

    keys: str

    def __post_init__(self) -> None:
        if len(self.keys) != SLOT_COUNT:
            raise ValueError(f"frame must be {SLOT_COUNT} chars, got {len(self.keys)!r}")

    @classmethod
    def blank(cls, *, connected: bool) -> Frame:
        fn = FN_CONNECTED if connected else FN_DISCONNECTED
        return cls(EMPTY * FN_SLOT + fn)
