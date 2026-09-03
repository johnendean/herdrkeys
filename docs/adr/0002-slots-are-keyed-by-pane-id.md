# 2. Slots are keyed by pane ID, and persisted

Date: 2026-09-03

## Status

Accepted

## Context

A physical keypad is worth having because of muscle memory. That makes the
binding from key to agent the central design question: if the thing under your
finger changes, the keypad is worse than the keyboard it replaced.

Candidates:

1. **Flat live list, row-major.** Agents ordered by position, keys renumbered as
   agents come and go. Rejected: starting an agent in workspace 1 shifts every
   key below it, so the key you learned now focuses something else.
2. **Herdr topology.** Row per workspace, column per tab. Rejected: it makes the
   grid a function of a layout that changes for reasons unrelated to the keypad.
3. **A human-durable identity**, such as the pane's `cwd` or workspace label --
   "sevenbyseven is always key 3, forever". Attractive, but it collapses when
   two agents run in one repository, which is exactly what git worktrees are
   for, and Herdr supports worktrees as a first-class feature.
4. **Pane ID.**

Herdr's pane IDs are stable and never reused. We confirmed from a live server
log and from a 99-event recording that a pane keeps its ID while agents come and
go inside it: `w1:p1` cycled `agent=None -> claude -> None -> claude` four times
in one capture, always as `w1:p1`. So the common case -- quitting an agent and
starting another in the same pane -- does not cost the slot.

## Decision

A pane claims the lowest free slot (0-14) the first time it becomes an agent
pane, and holds it until the **pane** closes -- not until its agent exits. The
map is persisted to `~/.local/state/herdrkeys/slots.json` and garbage-collected
against `session.snapshot` at startup.

## Consequences

Restarting an agent in place keeps its key. Unplugging the keypad, or restarting
the daemon, keeps every key.

Bindings do not survive closing a pane or restarting the Herdr session, since
pane IDs are scoped to it. Option 3 would have survived both; we traded that for
correctness under worktrees.

A slot whose agent has exited stays reserved and renders dark. This is
deliberate: it is the mechanism that makes restart-in-place free, and it means a
dark key does not always mean a free key.

Herdr's event backlog is bounded -- our recording contains no `pane_closed` or
`pane_exited` events at all despite a pane having been closed during it -- so
folding events alone would leave phantom panes holding slots forever. Periodic
reconciliation against `session.snapshot` is therefore load-bearing, not a
belt-and-braces nicety.

Two further properties of that replay, both found by running against a live
session rather than by reading the docs:

* A replayed event can carry the *same* per-pane `revision` as a snapshot taken
  alongside it, because Herdr does not bump `revision` for every status change.
  So the revision guard cannot drop it, and stale history wins over truth.
* The replay is rate-limited -- 94 backlog events took about four seconds to
  arrive -- and its length grows with session age.

Together those mean the corrective reconcile after connecting must wait for the
event stream to fall quiet, not for a fixed delay. A fixed delay fires mid-replay
on any session older than the delay, and the grid then shows states the agents
were in minutes ago until the next periodic reconcile.
