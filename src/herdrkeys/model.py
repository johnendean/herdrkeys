"""Core domain types and the frame encoding.

Pure: no I/O, no clock, no serial, no sockets.
"""

from __future__ import annotations

from dataclasses import dataclass
from enum import Enum

SLOT_COUNT = 16

# One row of four holds no agents. Twelve keys is more grid than the agents
# have ever asked for, so that row is free for keys that do something else.
# Which row it is physically depends on ROTATION, which only the device knows.
FN_SLOT = 15
MIC_SLOT = 12
REPO_SLOT = 13
FEATURE_SLOTS = (12, 13, 14, 15)
AGENT_SLOTS = tuple(range(12))  # 0..11, and contiguous: slot n is the nth agent

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
MIC = "m"  # the device decides what a microphone key emits, and what it looks like
# The repo key, in its two states: the focused agent's directory has a page, or
# it does not. Both are lit -- dark would be indistinguishable from the spare
# key beside it -- and the device decides how far apart they look.
REPO_PAGE = "r"
REPO_NO_PAGE = "n"

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


def feature_keys(*, connected: bool, repo_page: bool = False) -> list[str]:
    """An empty frame with the feature row already filled in.

    One place decides what the non-agent keys show, so a frame and a blank frame
    can never disagree about them.
    """
    keys = [EMPTY] * SLOT_COUNT
    keys[MIC_SLOT] = MIC
    keys[REPO_SLOT] = REPO_PAGE if repo_page else REPO_NO_PAGE
    keys[FN_SLOT] = FN_CONNECTED if connected else FN_DISCONNECTED
    return keys


@dataclass(frozen=True)
class AgentPane:
    """An agent pane as herdrkeys cares about it."""

    pane_id: str
    agent: str
    state: AgentState
    focused: bool
    # Where the pane is, so the repo key can ask git about it. Absent when
    # Herdr has not reported one, which is a normal answer, not an error.
    cwd: str | None = None
    # The project colour herdrcolor reported for this pane, `#rrggbb`. Absent
    # when that plugin is not installed, or has not synced since this pane
    # appeared -- both normal, and both mean the key falls back to its state
    # colour. See ADR 0010.
    colour: str | None = None


@dataclass(frozen=True)
class Frame:
    """What all 16 keys should show. Absolute, never a delta.

    `keys` carries meaning and `colours` carries data. The device decides what
    `blocked` looks like; it cannot decide what colour a project is, because
    that is not a meaning -- it is a fact about the world that only the host can
    know. So identity crosses the wire as hex and states do not (ADR 0010).

    Both live on one message so a frame stays absolute: there is no way for a
    key's colour and its meaning to arrive out of step.
    """

    keys: str
    colours: tuple[str | None, ...] = (None,) * SLOT_COUNT

    def __post_init__(self) -> None:
        if len(self.keys) != SLOT_COUNT:
            raise ValueError(f"frame must be {SLOT_COUNT} chars, got {len(self.keys)!r}")
        if len(self.colours) != SLOT_COUNT:
            raise ValueError(
                f"frame must carry {SLOT_COUNT} colours, got {len(self.colours)!r}"
            )

    @classmethod
    def blank(cls, *, connected: bool, repo_page: bool = False) -> Frame:
        return cls("".join(feature_keys(connected=connected, repo_page=repo_page)))
