# 1. Navigate over Herdr's socket API, not as a USB keyboard

Date: 2026-09-03

## Status

Accepted. Amended by ADR 0008, which replaces `agent.focus` with `pane.focus`
after Herdr 0.9.0 stopped propagating the former to a viewing client.

## Context

The Keybow 2040 is a USB HID keyboard. The obvious design is to keep it one:
press a key, it types a chord, Herdr's keybindings interpret the chord. Nothing
new has to exist on the host.

Three things argue against it.

Herdr has no binding for "focus agent by index". Its documented bindings are
directional and relative -- `prefix+h/j/k/l` between panes, `prefix+w` between
workspaces. Jumping to the fourth agent would mean synthesising a sequence of
relative moves and hoping the layout is what we assumed.

A keyboard types into whatever the OS has focused. Press a key while your
browser is frontmost and the chord goes to the browser.

The LEDs need data flowing *to* the device, which HID keyboard descriptors do
not provide. A host process is therefore required no matter what, which removes
the "nothing new has to exist" advantage entirely.

We verified that `agent.focus` accepts a pane ID and works when called from a
process outside any Herdr pane, over `~/.config/herdr/herdr.sock`.

## Decision

Key presses travel to the host daemon over the `usb_cdc` data channel. The
daemon calls `agent.focus` on Herdr's socket. The keypad declares no HID
keyboard usage of its own and never types anything.

`agent.focus` rather than `pane.focus`: focusing through the agent surface marks
the agent seen, which is what collapses `done` back to `idle`. (Superseded by
ADR 0008: `agent.focus` alone no longer moves the viewport on Herdr 0.9.0.)

## Consequences

Navigation is absolute and deterministic -- slot 4 focuses the agent in slot 4
regardless of layout, and works while another application has OS focus.

Because it works while another application has focus, focusing an agent you
cannot see becomes possible, so a key press also raises the host terminal app
(found by walking up from a Herdr client process to its `.app` ancestor).

The keypad is useless without the daemon running. This is the accepted price:
the previous firmware's numpad and media layers stop working. HID is left
*enabled* in `boot.py` so restoring them later needs no `boot.py` change and no
power cycle, but our `code.py` sends no keystrokes.
