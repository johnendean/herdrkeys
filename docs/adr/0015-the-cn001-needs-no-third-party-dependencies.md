# 15. The CN001 needs no third-party dependencies

Date: 2026-10-27

## Status

Accepted

Extends ADR 0003 to a board it did not anticipate.

## Context

ADR 0003 holds herdrkeys to the standard library so that `herdr plugin install`
is the whole install. The Keybow made that cheap: it is a serial port, and
`termios` is sixty lines.

The CN001 is not. Every existing driver for it -- Bitfocus Companion, OpenDeck's
mirajazz, the Python `Soomfon_15` -- talks to it through hidapi and draws its
keys with an image library: in Python, `hidapi` and Pillow. Both are compiled
dependencies, exactly the class of thing ADR 0003 exists to keep out.

Two pieces of the standard library are missing: a HID transport, and a JPEG
encoder. The plugin is already macOS-only (`platforms = ["macos"]`), and macOS
ships both as system frameworks.

## Decision

The CN001 is driven through IOKit's `IOHIDManager` and its keys are encoded
through ImageIO and CoreGraphics, both via `ctypes`. Nothing is installed.

Only the vendor interface (usage page `0xFFA0`) is opened, and it is opened
without seizing it. The device's boot-keyboard interface is never matched, so
the daemon is not a keyboard reader and should not need Input Monitoring.

A hand-written encoder for flat-colour JPEGs was considered: pure Python,
testable anywhere, and enough for the first version. It was rejected because
flat colour is all it could ever draw, and the reason to want an LCD board at
all is that its keys can eventually show more than that.

## Consequences

We own a `ctypes` binding to two Apple frameworks. It is more code than the
serial port and less forgiving: a wrong signature is a crash, not an exception.
It is kept behind the board's renderer and transport so the rest of the daemon
never sees it, and the tests drive a fake transport, so they still need no
hardware and run anywhere.

hidapi on macOS opens devices exclusively by default; the native route does
not, but the official Soomfon app does hold the device, and `doctor` reports
that rather than leaving the board dark without a reason.
