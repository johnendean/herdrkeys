# 20. A Go binary, fetched at install, replaces bare Python

Date: 2026-11-06

## Status

Accepted

Supersedes ADR 0003 (no third-party dependencies) and ADR 0015 (the CN001
needs no third-party dependencies). Follows herdrcolor's ADR 0001.

## Context

herdrkeys should install with `herdr plugin install johnendean/herdrkeys` and
nothing else, on Windows as well as macOS. Bare Python cannot do that. A fresh
Windows machine has no Python 3.11, and `python3` there is often the Microsoft
Store stub. CPython on Windows has no `AF_UNIX`, and Herdr's Windows transport
is not a Unix socket anyway. The CN001 draws its keys as JPEGs on the host, and
Python's standard library has no JPEG encoder. On macOS that gap was filled by
ImageIO through `ctypes`. On Windows the equivalent is WIC through COM, which
would be a second hand-rolled binding to a platform framework.

## Decision

herdrkeys becomes a Go binary. `[[build]]` runs a committed fetch script
(`bin/fetch.sh`, `bin/fetch.ps1`). The script downloads the release asset
tagged with the manifest's `version` and refuses it unless its SHA-256 matches
`checksums.txt` in the cloned commit, as herdrcolor does. The manifest claims
`macos` and `windows`, with both boards supported on both.

The CircuitPython firmware in `device/` is unaffected. It runs on the Keybow,
not the host.

## Considered Options

- **Keep Python and require 3.11 on PATH.** It fails the goal on a fresh
  Windows machine, and it needs WIC via COM to draw the CN001.
- **Python through `uv`.** This only swaps "install Python" for "install uv".
- **Commit prebuilt binaries.** Every release would add to git history
  permanently.
- **Linux as well.** The core would port cheaply, but the boards need udev
  rules before a normal user can open them, so "the whole install" would not
  hold there. Left for a later decision.

## Consequences

- Changes on `main` reach users only when a release is tagged.
- A linked checkout prefers a locally built binary, so development needs Go.
- The macOS build talks to IOKit, so it needs cgo or purego, and it is built on
  a macOS runner.
- Behaviour must survive the port. The 99-event recording in `tests/fixtures/`
  is the parity check for ordering, settling and attention.
- **Herdr's startup hook is the only supervisor on every platform.** The binary
  detaches itself and enforces a single instance on its own. That replaces
  `bin/herdrkeys-start`, its `pgrep` and `nohup`, and the launchd plist, which
  is deleted. ADR 0017's restart-in-place after an Accessibility grant must
  still work without launchd.
- **A different build takes over.** Every `herdr plugin update` installs into a
  fresh directory, so the instance lock records the running daemon's version
  and path. A start from a different build stops it cleanly, blanking the
  boards and releasing the dictation chord, and then runs itself. A start from
  the same build leaves it alone.
- **Uninstall is two steps**, because Herdr has no uninstall hook: the `stop`
  action, then `herdr plugin uninstall`.
- **Dependencies are allowed again, a few.** ADR 0003 refused them because
  each was a way for an install to fail. A binary built in CI removes that
  risk. Allowed: `golang.org/x/sys`, `go-toml/v2` and `purego`, so the macOS
  IOKit binding needs no cgo and every target cross-compiles from one runner.
  Serial and HID stay hand-written.
- **Unsigned, ad-hoc on macOS.** macOS ties the Accessibility grant to the
  binary's path and hash, so CN001 dictation must be re-granted after every
  update. ADR 0017's grey keys and log line show when it is needed.
- The release plumbing is copied from herdrcolor and adapted, not shared.
  `min_herdr_version` is 0.9.5, the version ADR 0021's pipe naming was checked
  against.
