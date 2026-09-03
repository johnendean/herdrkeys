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

**Agent state** — Herdr's lifecycle classification of an agent. A closed set:
`idle`, `working`, `blocked`, `done`, `unknown`. `done` is the same underlying
idle state as `idle`, distinguished only by the work having finished unseen;
focusing the agent marks it seen and it becomes `idle`. `blocked` means Herdr
recognised an approval or question prompt. `unknown` means an agent is present
but unclassifiable — it does not mean the agent finished.

### herdrkeys terms

**Agent pane** — A pane that currently has an agent. Only agent panes are
eligible for a slot; plain shell panes are not represented on the keypad.

**Slot** — One of the keypad's 16 physical keys, together with the pane bound to
it. Slots 0–14 hold agent panes. Slot 15 is the **function key** and never holds
one.

**Slot map** — The binding from slot to pane. A pane claims the lowest free slot
when it first becomes an agent pane and holds that slot until the pane closes —
not until the agent exits. Restarting an agent inside a pane therefore keeps its
slot. The slot map outlives the daemon.

**Frame** — One complete description of what all 16 keys should show: a
16-character string, one character per key. A frame is absolute, never a delta,
so any frame fully determines the keypad's appearance.

**Settling** — Holding a state change briefly before it reaches a frame, so that
the flapping Herdr emits while an agent starts does not reach the LEDs. A
transition into `blocked` is never settled.

**Function key** — Slot 15. Focuses the next agent needing attention, and is the
only key that shows whether the daemon can see Herdr at all.

**Daemon** — The host process. It is the only component that talks to Herdr, and
the only component that decides anything; the keypad renders frames and reports
key presses.
