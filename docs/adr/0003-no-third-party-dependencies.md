# 3. No third-party dependencies

Date: 2026-09-04

## Status

Accepted

Supersedes the venv-and-pyserial arrangement introduced in ADR 0001's wake.

## Context

The daemon originally used pyserial, installed into a virtualenv, with the
launchd plist pointing at that venv's interpreter by absolute path so a Homebrew
Python upgrade could not break it.

That was tolerable while herdrkeys was one person's setup. It stops being
tolerable once the goal is `herdr plugin install <repo>` with nothing else to do,
because every dependency becomes a way for a stranger's install to fail: pip
must work, the network must be up, the build step must succeed, and the venv's
interpreter must keep existing.

pyserial was doing very little for us. Opening a port, setting it raw at a fixed
baud, non-blocking reads and writes -- roughly sixty lines of `termios` and `os`.
That code had already been written and debugged three times over during this
project's development, once while probing the board before any of this existed.

## Decision

herdrkeys depends on nothing outside the standard library. `serial_port.py`
provides the small part of pyserial that is needed.

This makes the plugin's `[[build]]` step unnecessary for its usual purpose:
there is nothing to compile and nothing to install, so build is used only to
start the daemon once, since startup hooks do not run until Herdr next starts.

## Consequences

Installation is `herdr plugin install`, and the plugin runs against the system
`python3` with `PYTHONPATH` pointed at the checkout. No venv, no pip, no network
at install time, and nothing for a Homebrew upgrade to break -- which also
removes the reason the launchd plist pinned an absolute interpreter path.

We own the serial code. It is small and covered by tests that run against a pty,
so it needs no hardware, but a platform quirk pyserial would have absorbed is now
ours to fix.

The venv survives for development only, because pytest is still a dependency of
running the tests. Nothing in the shipped path uses it.
