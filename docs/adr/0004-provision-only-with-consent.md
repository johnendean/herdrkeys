# 4. Provision a board only with consent

Date: 2026-09-04

## Status

Accepted

## Context

For the plugin to need no follow-up steps, the daemon has to be able to get the
firmware onto a board by itself: copy `boot.py` and `code.py` to the CIRCUITPY
drive, then make `boot.py` take effect.

That second half looked impossible -- `boot.py` is only read at power-on, and no
software can replug a USB cable. It turns out it can be done: interrupting the
running `code.py` over the console serial to reach the REPL and calling
`microcontroller.reset()` performs a genuine hard reset. Measured on the board:
the USB nodes disappear entirely and are back roughly two seconds later with
`boot.py` applied, and the daemon reconnects about two seconds after that.

Interrupting must wait for the `>>> ` prompt rather than sleeping a fixed
interval. A one-second wait was too short in practice and left the board sitting
at a dead REPL with its LEDs off and `code.py` stopped.

So full automation is possible. The question becomes whether it is right.

A Keybow 2040 that appears on this machine is not necessarily a blank one. The
board this project was built on arrived carrying a four-layer HID keyboard --
numpad, media keys, mixxx controls -- that its owner had no other copy of.
Detecting an unprovisioned board and silently overwriting it would have
destroyed that.

## Decision

The daemon provisions a board only when the board is already running herdrkeys,
or is carrying nothing at all. Anything else is left untouched, with one log line
explaining how to take it over deliberately.

`herdrkeys adopt` is that deliberate path. It copies whatever is on the board
into the plugin's config directory, timestamped so repeated runs never overwrite
an earlier rescue, and only then writes the firmware and resets.

Boards are identified by the board ID in `boot_out.txt`, not by volume name,
which the owner may have changed and which proves nothing.

## Consequences

Installing the plugin with a herdrkeys board attached, or a blank one, needs no
further steps: the daemon writes the firmware, resets the board, and connects.

Someone whose board carries their own firmware gets a keypad that stays dark
until they run one command. That is the intended outcome. Installing herdrkeys
is consent to run herdrkeys; it is not consent to destroy something else days
later, when a different board happens to be plugged in.

The refusal is logged once per board rather than on every reconnect attempt, so
a keypad that will never be adopted does not turn the log into a flood.

Provisioning can be disabled outright with `provision = false` in config.
