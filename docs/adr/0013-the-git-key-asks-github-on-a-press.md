# 13. The git key asks GitHub on a press, off the loop

Date: 2026-10-03

## Status

Accepted

## Context

The repo key -- renamed the **git key** as part of this change -- opened the
focused agent's repository page. Most of the time that is one click short of
where you wanted to be: the agent has pushed a branch and opened a pull request,
and the pull request is what you want to look at.

So the key now opens the most specific place there is: the agent's own pull
request, else the repository's open pull requests, else the page (see
CONTEXT.md). Knowing which means asking GitHub, and that raises three questions
the page never did. Everything about the page could be worked out from
`.git/config` in a twentieth of a millisecond. A pull request cannot be worked
out locally at all.

Measured on this machine, `gh pr list` takes about 0.5s.

## Decision

**`gh` is asked, and only on a press.** Not GitHub's API from Python: private
repositories need a token, and the obvious place to get one is `gh` anyway,
along with GitHub Enterprise hosts and the login flow. `gh` is a command the
user may or may not have, not a package herdrkeys installs, so ADR 0003 stands.
The repository is named outright (`--repo host/owner/repo`, from the page)
rather than left for `gh` to infer, so the pull requests are always those of the
repository the page is, and `gh` never stops to ask which remote is meant.

`gh` is asked about every host, not only `github.com`. It sends no credentials
to a host it is not logged into, and a forge that is not GitHub fails within a
second. Checking first would have cost a round trip of its own: `gh auth status`
was measured at 0.3s.

**The colour does not show pull requests.** It still says only whether there is
a page. Showing pull requests would mean polling GitHub in the background for
whichever agent is focused, for a fact that matters only at the moment of a
press -- network traffic, rate limits and a laptop that never sleeps quietly, to
save one glance.

**The lookup runs on a thread.** Everything else the daemon does runs on its one
loop, which also animates every key. Half a second of `gh` on that loop would
freeze the board on every press, and a hung network would freeze it for as long
as the timeout. So a press hands the whole journey -- git, `gh`, the browser --
to a worker thread, and the key shows a brighter steady violet (`g` in the
frame) until it lands. Steady, not moving: motion means an agent wants you (ADR
0012), and this is the board acknowledging you.

The worker never writes to the keypad. When a press finds nowhere to go, the
answer to that is a flash, and the loop collects the result and sends the flash
from its own thread, so the serial port only ever has one writer. A second
press while one is looking is ignored rather than queued.

**Every failure is the page.** No `gh`, not logged in, not GitHub, offline, or
more than three seconds of waiting: all of them open the page, exactly as the
key did before this change. It can be slower than it was. It cannot be worse.

## Consequences

The key is no longer fully host-agnostic. Its page still is, but only GitHub's
pull requests are known about; on GitLab the key opens the page, as before.
Adding merge requests would mean asking `glab` the same way.

A press now costs up to a second before the browser moves, where it used to be
instant. The brighter key covers the gap.

There is now a thread in a daemon that had none. It is confined to this one
key, holds no shared state but the result it hands back, and is a daemon thread,
so it can never keep the process alive on its own. A lookup that raises is
logged and counted as finding nothing, so the key cannot be left showing that
it is looking.

Opening the pull request list rather than the single pull request when it
belongs to another branch is deliberate, even when it is the only one open. A
pull request for some other branch is not the focused agent's work, and opening
it would look like the key had found something it had not.
The same goes for a pull request from a fork whose branch merely shares the
name: forks are full of `main`s, so only pull requests from the repository
itself can be the agent's own.
