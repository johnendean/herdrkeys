# 21. Speak Herdr's socket, not its CLI

Date: 2026-11-06

## Status

Accepted

Keeps ADR 0001's transport through the port in ADR 0020. Deliberately departs
from herdrcolor, which talks to Herdr only through the `herdr` CLI.

## Context

herdrcolor uses the CLI so that it never has to know Herdr's transport, which
is a Unix socket on macOS and a named pipe on Windows. That costs herdrcolor
nothing, because it runs once and exits. herdrkeys is a long-lived daemon. It
polls `agent.list` twice a second (ADR 0005), keeps an `events.subscribe`
stream open, and focuses a pane on every press. A CLI call takes about 55 ms on
Windows (measured with Herdr 0.9.5), against 0.22 ms for a socket round trip,
and the CLI has no event stream at all.

## Decision

herdrkeys speaks the JSON-lines API directly. On macOS that is the Unix socket
at the socket path. On Windows it is the named pipe `\\.\pipe\<socket path>`,
for example `\\.\pipe\C:\Users\<user>\AppData\Roaming\herdr\herdr.sock`. The
file at the socket path is only a marker holding `pid:start-time`. This was
checked against Herdr 0.9.5 with a live `agent.list` over the pipe.

## Consequences

The pipe naming is not documented, so a Herdr release could change it. `doctor`
names the pipe it tried, so a change shows up as a clear failure rather than a
dark board.
