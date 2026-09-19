# Context

herdrkeys binds a Pimoroni Keybow 2040 macro keypad to a running Herdr session:
its LEDs show agent lifecycle state, and its keys focus agents.

## Glossary

### Herdr terms

These are Herdr's own terms and are used here exactly as Herdr defines them.

**Workspace** — Herdr's outermost container. Contains tabs. Public ID `w1`.

**Tab** — Contains panes. Public ID `w1:t1`.

**Pane** — One terminal. Public ID `w1:p1`. Pane IDs are stable and never reused.
A pane keeps its ID when the program inside it exits and a new one starts, so a
pane outlives the agents that occupy it.

**Agent** — A coding agent Herdr has recognised inside a pane. A pane has an
agent only while one is detected; the pane's `agent` field is absent otherwise.

**Agent list** — Every agent Herdr currently recognises, in the order Herdr
presents them. Its agents pane renders this list; `agent.list` answers with it.
Spoken of as "the agents pane", but **pane** already means one terminal here, so
the list is the list.

**Agent state** — Herdr's lifecycle classification of an agent. A closed set:
`idle`, `working`, `blocked`, `done`, `unknown`. `done` is the same underlying
idle state as `idle`, distinguished only by the work having finished unseen;
focusing the agent marks it seen and it becomes `idle`. `blocked` means Herdr
recognised an approval or question prompt. `unknown` means an agent is present
but unclassifiable — it does not mean the agent finished.

### herdrkeys terms

**Agent pane** — A pane that currently has an agent. Only agent panes appear in
the agent list, so only they reach the keypad; plain shell panes do not. A pane
survives its agent leaving; its key does not.

**Slot** — One of the keypad's 16 physical keys, together with whatever it shows.
Slots 0–11 are the **grid** and hold agents; slots 12–15 are the **feature row**
and never do.

**Grid** — Slots 0–11: the keys that hold agents. Holds twelve, which is as many
as fit; an agent past the twelfth in the agent list is not on the board and the
function key cannot reach it.

**Feature row** — Slots 12–15, which carry no agents: 12 is the **microphone
key**, 13 the **repo key**, 15 the **function key**, and 14 is spare. Which row
this is physically depends on how the board is turned, which only the device
knows. There is no "function row": the row is the feature row, and the function
key is one key in it.

**Key order** — Which agent is on which key: position in the **agent list**, and
nothing else. The first agent Herdr lists is slot 0, the second slot 1. An agent
leaving the list frees its key and the agents after it move up; one appearing
above another moves it down. No agent owns a key, nothing is remembered between
runs, and a dark key in the grid is a free key.

**Project colour** — The colour herdrcolor gives a project, worn by a key whose
agent is `idle` or `done` — the two states where which agent it is matters more
than what it is doing. A
**project** there is a directory's name, so two agents in one repository share a
colour and a worktree is its own project. An agent has one only while that
plugin is installed and has reported since the pane appeared; a key with none
shows its agent state instead, which is what every key did before the plugin
existed. It is not an *agent* colour: nothing colours agents.

**Blinking** — What a key does when its agent wants you: `done` and `blocked`,
which are exactly the states the function key walks. Nothing else on the grid
moves, so movement can be read before colour is.

**Frame** — One complete description of what all 16 keys should show: a
16-character string, one character per key, and a **project colour** per key
where there is one. A frame is absolute, never a delta, so any frame fully
determines the keypad's appearance. The characters carry meaning and the device
decides what each one looks like; the colours are data, because what colour a
project is cannot be worked out on the board.

**Settling** — Holding a change briefly before it reaches a frame, so that the
flapping Herdr emits while an agent starts does not reach the LEDs. Two things
settle: what a key shows, and the **key order** — which carries each key's
**project colour**, since a recolour is something an unrelated agent did, and
flickers for the same reasons a reorder would. A transition into `blocked` is
never settled.

**Function key** — Slot 15. Focuses the next agent needing attention, and is the
only key that shows whether the daemon can see Herdr at all.

**Repo key** — Slot 13. Opens the repository of whichever agent is focused,
in a browser. It follows focus rather than holding a repository of its own, so
what it opens changes as you move around, and it shows whether the agent it is
following has a **page** before you press it. Never dark: "no page here" is a
state it reports, not an absence.

**Page** — The web address a repository's remote names. A remote that names no
host — a local clone, a path — has no page, and neither does a directory
outside a repository. A press that finds none is answered with a flash rather
than treated as a failure.

**Microphone key** — Slot 12. Held, it makes the keypad hold down the hotkey
Wispr Flow dictates on; tapped, it **latches** that hotkey down until the next
tap. The frame says which key it is; the firmware decides which keystroke that
means, exactly as it decides what `blocked` looks like.

**Latch** — The microphone held open with no finger on the key. It pulses while
latched, and ends on a second tap, on a five-minute timeout, or on anything that
would otherwise release the key.

**Daemon** — The host process. It is the only component that talks to Herdr, and
the only component that decides anything; the keypad renders frames, reports key
presses, and types the one keystroke the microphone key stands for.
