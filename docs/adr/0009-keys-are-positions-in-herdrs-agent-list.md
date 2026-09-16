# 9. Keys are positions in Herdr's agent list

Date: 2026-09-16

## Status

Accepted

Supersedes ADR 0002, which bound keys to pane IDs and persisted them.

## Context

ADR 0002 made the binding from key to agent the central design question, and
answered it with muscle memory: a pane claims the lowest free slot the first
time it holds an agent and keeps that slot until the pane closes. It listed
four candidates and rejected the first of them outright:

> **Flat live list, row-major.** Agents ordered by position, keys renumbered as
> agents come and go. Rejected: starting an agent in workspace 1 shifts every
> key below it, so the key you learned now focuses something else.

That reasoning is sound and the prediction was wrong. What it missed is *which*
memory is doing the work. In use, the keypad is not consulted from memory at
all: it is consulted next to a screen showing Herdr's agents pane, and what a
hand reaches for is the position it can see there. A key an agent claimed on
Tuesday is not a memory anybody has.

Meanwhile the cost of holding slots accumulates where it cannot be seen. A slot
is reserved until its *pane* closes, so a workspace whose agent has quit keeps
its key, dark, indefinitely; agents that arrive later fill in around it. After a
few days the live session looked like this:

```
agent.list (what the agents pane renders)   slots.json (what the keys did)
  [0] w6:p1  sevenbyseven                     key 0 -> w6:p1   sevenbyseven
  [1] w8:p1  herdrkeys                        key 1 -> w9:p1   vlans  (no agent)
                                              key 2 -> w8:p1   herdrkeys
```

Two agents, and the second one is on key 2 because a third workspace is holding
key 1 for an agent that left. Nothing on the board says why. Reading the agents
pane and then pressing the key beside it is exactly the translation the keypad
was meant to remove.

## Decision

The keys mirror Herdr's agent list. The first agent `agent.list` returns is key
0, the second key 1, and so on to key 11; there is no binding, nothing to
assign, and nothing persisted.

The order is taken from Herdr verbatim rather than sorted here, so there is only
ever one answer to "why is this agent on this key": because that is where Herdr
shows it. `session.snapshot` carries the same list in the same order, so a
reconnect does not renumber the board while it waits for the first poll.

`slots.json` is deleted on startup by the version that first ships this.

## Consequences

A departure is now visible on the board: the agent below moves up into the key
that was freed, instead of the grid quietly acquiring another dark key that is
not free. A dark key means a free key again, which ADR 0002 had to give up.

An agent appearing above another moves it down. This is the cost ADR 0002
refused, accepted knowingly, and `test_an_agent_appearing_above_pushes_the_
others_down` states it as a test so it is not later mistaken for a bug.

Restarting an agent in place mostly still keeps its key, but for a different
reason and with a weaker guarantee. A pane's *position* in the list does not
change because the agent inside it came and went, so it returns to the key it
had -- unless some other agent appeared or vanished in the meantime, in which
case it does not. ADR 0002 guaranteed this; we now merely expect it. The
recording that established pane-ID stability is kept, and `test_replay` still
asserts it: it is the evidence any future attempt to pin keys to panes would
have to start from.

The order settles like everything else, on the same clock. `agent.list` is
polled twice a second and `apply_agents` releases an agent the moment one poll
omits it, so without settling a single blip would renumber every key below it
and renumber them back half a second later. Settling does *not* protect a real
restart in place -- quitting an agent and starting another takes seconds -- and
`OrderSettler` says so, because a reader who assumed otherwise would file the
resulting movement as a bug.

There remains a race that nothing here removes: the order can change in the
window between a frame being drawn and a finger landing, so a press can focus
the agent that just moved into that key. Presses resolve against the *settled*
order, which is the one the board was drawn from, so the window is one poll
rather than unbounded. No lockout is applied after a reorder; a keypad that
ignores presses for a moment after something moved would feel broken in exactly
the situation where you are most likely to be pressing it.

Past twelve agents the rest are not on the board and the function key cannot
reach them. That was already true -- the thirteenth agent to appear got no slot
-- so nothing regresses; it is now the last in Herdr's order that misses out
rather than the last to arrive.

Nothing is persisted, so there is nothing to reconcile against and nothing to
garbage-collect. `SlotMap`, `sync_slots`, the `state_path` config key and the
periodic `slots.gc` all go. The snapshot reconcile stays: ADR 0002 established
that Herdr's backlog is bounded and folding events alone strands phantom panes,
and that is about `HerdrState`, not about slots.
