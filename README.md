# herdrkeys

[![CI](https://github.com/johnendean/herdrkeys/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/johnendean/herdrkeys/actions/workflows/ci.yml?query=branch%3Amain)

Bind a [Pimoroni Keybow 2040](https://shop.pimoroni.com/products/keybow-2040),
a Soomfon CN001, or both, to a running [Herdr](https://herdr.dev) session. The
keys show what every agent is doing; pressing one jumps to it. On macOS and
Windows.

```
[12 m mic    ] [13 e return ] [14 r git    ] [15 f fn      ]   <- the feature row
[ 8 -        ] [ 9 -        ] [10 -        ] [11 -         ]
[ 4 -        ] [ 5 -        ] [ 6 -        ] [ 7 -         ]
[ 0 w working] [ 1 B blocked] [ 2 i idle   ] [ 3 -         ]
```

## What it does

The keys mirror Herdr's agents pane: the first agent it lists is key 0, the
second key 1, and so on along the bottom three rows. Close one and the agents
below move up, exactly as they do on screen, so the key to press is the one in
the position you can already see. The key's colour is that agent's lifecycle
state:

| State     | Colour                      | Meaning |
| --------- | --------------------------- | ------- |
| `idle`    | its project's colour, dim   | ready for input |
| `working` | amber                       | busy |
| `blocked` | red, **blinking**           | waiting on you: an approval or a question |
| `done`    | its project's colour, bright, **blinking** | finished work you have not looked at yet |
| `unknown` | dim blue                    | an agent is there but Herdr cannot classify it |

Focus is shown by **brightness**, never by hue. Motion means one thing:

> If a key is **moving**, that agent wants you.

|            | steady — needs nothing | blinking — wants you |
| ---------- | ---------------------- | -------------------- |
| **whose**  | `idle` — project colour | `done` — project colour |
| **what**   | `working` — amber       | `blocked` — red |

The two that blink are exactly the two the function key jumps to, so the board
reads in two passes. Anything moving needs you, and its hue says which kind:
your project's colour for work finished and unseen, red for an agent stopped
and waiting. Anything steady needs nothing, and its hue says whether it is
yours and idle, or merely busy.

So hue carries identity where knowing *which* agent is the useful thing, and
state where what is happening matters more than whose it is. That matters more
than it sounds, because the keys renumber as agents come and go -- the colour is
what tells you which key is which. `unknown` keeps its dim blue: a fault report,
not an agent at work.

`idle` and `done` share a hue, and are never mistakable: done is seven times
brighter and it is the only one of the two that moves.

The colours come from
[herdrcolor](https://github.com/johnendean/herdrcolor), which colours projects
in Herdr's sidebar and publishes the hex for tools like this one. Without that
plugin every idle key is dim green, exactly as before -- and `doctor` will say
so, since the board cannot.

Press a key to focus that agent -- and to raise the terminal, because focusing
an agent you cannot see is not focusing it.

One row of four holds no agents. Twelve keys is more grid than the agents have
ever asked for, and the fourth row is worth more as keys that do something else.

The **function key** (slot 15) is one of them. Press it to jump to the next agent
that wants you: `blocked` first, then `done`, wrapping from wherever you are. If
nothing wants you it flashes rather than doing nothing, so you know it heard you.
It is also the only key that shows whether the daemon can see Herdr at all --
dark keys otherwise mean "no agents", which is not the same thing.

The **microphone key** (slot 12) is another. Hold it to dictate with Wispr
Flow: the keypad holds down the **dictation chord** for as long as your finger
is down, which is what Flow's push-to-talk listens for, and lets go when you
do. **Tap it instead and the microphone latches open** until you tap again --
for anything longer than a sentence, or when you would rather not hold a key
down.

The chord is Right Option on macOS and Ctrl+Win on Windows (where Right Alt is
AltGr on many layouts), and `dictation_chord` in `config.toml` changes it --
`"ctrl+win"`, `"right_alt"`, `"f13"`. It has to match the binding Flow listens
for, so `doctor` reads Flow's own settings and says when the two disagree.

Steady bright cyan means held; a **pulsing** cyan means latched, because the only
real risk of latching is forgetting it is on. A latch also gives up on its own
after five minutes: nothing on the board can tell whether Flow is still
listening, and a chord held down all afternoon would turn every keystroke into
a shortcut.

There is one microphone, however many boards are attached: open it on either
board and both show it, and either board's keys close it. Whether a press is a
hold or a tap is decided by the daemon, so a keypad showing "no host" will not
dictate. The Keybow types the chord itself, when the daemon tells it to, so it
needs no permission; the CN001 cannot type, so the daemon types for it -- see
below.

The **return key** (slot 13) sits beside it and types Return, so sending what
you just dictated is the key next to the one you dictated with. It is a dimmer
cyan than the microphone -- the two are one tool -- and brightens while pressed.
**Pressed while the microphone is open, it closes the microphone and types
nothing**: Flow has not inserted the text yet, and a Return then would send the
prompt without it. Press again once the text is there.

The **git key** (slot 14) opens the most specific place the focused agent's
repository has, in a browser:

1. **the agent's pull request** -- an open one, draft or not, from the branch its
   directory has checked out;
2. otherwise **the repository's open pull requests**, if there are any -- never
   one of them on its own, because a pull request for another branch is not
   this agent's work;
3. otherwise **the repository's page**, as before.

It follows focus rather than remembering a repository of its own, so what it
opens is whatever you are looking at. Any remote that names a host has a page --
GitHub, GitLab, Bitbucket, a forge of your own -- because the rewrite from
`git@host:o/r` to `https://host/o/r` is the same everywhere; there is no list of
supported hosts to be missing from.

Pull requests are GitHub's, found by asking [`gh`](https://cli.github.com/) when
you press, which takes about half a second. The key goes brighter while it asks,
and the rest of the board carries on. Without `gh`, logged out, offline, on
another host, or after three seconds of waiting, the key opens the page exactly
as it always did -- `gh` is optional, never a dependency. See ADR 0013.

**Bright violet means there is a page; dim violet means there is not** -- so you
know before you press -- though not whether there is a pull request, because
finding that out means asking GitHub, which is done on a press and never in the
background. The key is never dark, which would read as no daemon at all. It
changes as you move between agents.

On the CN001, a page on **github.com, gitlab.com, bitbucket.org or
codeberg.org** wears that service's logo, in white on its brand colour, instead
of the violet, so you also know where a press will take you. While a press is
looking, the logo gets a light border. Any other host -- a self-hosted GitLab,
GitHub Enterprise -- keeps the violet: the host name cannot prove which service
it is, and a wrong logo is worse than none. The Keybow is unchanged. See ADR 0018.

That colour is read straight out of `.git/config` rather than by running git:
the frame is rebuilt on every pass of the loop, at least twice a second, and
`git remote get-url` costs 14.2ms against 0.055ms for the file. A subprocess
there would sit between every status poll and the LEDs, and a git that hung
would freeze all sixteen keys rather than just this one.

Reading the file understands plain remotes and not git's `include.path` or
`insteadOf` rewrites, so a press falls through to git itself when the file says
nothing. The key therefore under-promises rather than over-promises: it can be
dim somewhere that still opens. A press that genuinely finds nothing flashes
*that* key, the way the function key flashes when nothing wants you.

## The Soomfon CN001

The CN001 is a second board: fifteen keys behind one LCD panel. Plug it in and
the daemon finds it; with both boards attached, both show the same agents and a
press on either means the same thing.

```
[ 0      ] [ 1      ] [ 2      ] [ 3      ] [ 4      ] | 5h session
[ 5      ] [ 6      ] [ 7      ] [ 8      ] [ 9      ] | week
[12 mic  ] [13 ret  ] [ -      ] [14 git  ] [15 fn   ] | reset in
```

Agents read left to right across the top two rows, like the agents pane
beside it, so it shows the first ten; the eleventh and twelfth are on the
Keybow only, though the function key pressed on either board still reaches
them. The feature row is the bottom row in the Keybow's order: the microphone and
return keys at its left, each drawing its symbol in the microphone's cyan, and
the git and function keys at its right; the key between them is dark. The strip
to the right reports nothing when pressed; it shows your Claude plan's usage.

**The strip** shows how much of your Claude plan's rate limits you have used:
the 5-hour session at the top and the weekly limit in the middle, each as a
gauge with its percentage, and at the bottom how long until you can work again.
A gauge turns amber at 80% and red once the limit is spent, and the countdown
turns red with it. The daemon asks Anthropic once a minute while a CN001 is
attached, using Claude Code's own sign-in, which it reads and never changes: on
macOS from the Keychain -- the first time, macOS may ask whether `security` may
read `Claude Code-credentials`; allow it -- and on Windows from
`%USERPROFILE%\.claude\.credentials.json`, which `doctor` names. The strip stays
dark until usage is first known, and goes grey when its reading is more than
five minutes old. `doctor` says why, and `usage = false` in `config.toml` turns
it off. See ADR 0019.

The CN001 cannot type, so the daemon types for it. **On Windows that needs
nothing**, though Windows will not let it type into a window running as
administrator. **On macOS it needs Accessibility permission**. The first time
you press its microphone or return key without it, the daemon asks macOS, the
key flashes, and the log names the binary to allow in System Settings > Privacy
& Security > Accessibility. Until then those two keys draw their symbols in
grey. Once you have allowed it, press either key again: macOS only lets a newly
started process use the grant, so that press restarts the daemon in place, and
the one after it types. The binary is unsigned, so macOS ties the grant to the
exact build, and **every update needs it granting again** -- the grey keys say
so. Dictating on the Keybow never needs it. If the daemon crashes
mid-dictation, Flow keeps listening until the daemon next starts -- the first
thing it does is let go -- or until you tap the chord's keys yourself. See ADR
0017.

It says exactly what the Keybow says -- moving means this agent wants you --
but where the Keybow can only colour a key, the CN001 can draw on it. So every
agent key is its project's colour, as in the sidebar, and a **mark** on a dark
badge says what the agent is doing:

| State     | CN001 |
| --------- | ----- |
| `idle`    | its project's colour, no mark |
| `working` | a spinner, amber, frozen |
| `blocked` | `!`, red, **blinking** |
| `done`    | a tick, green, **blinking** |
| `unknown` | `?`, blue |

A key whose agent has no project colour is grey behind its mark; a focused key
has a light border. Project colours are herdrcolor's hex exactly; the Keybow
has to saturate them to tell them apart on its LEDs, and the LCD does not. Mark
colours are deliberately strong and *not* Catppuccin, which is herdrcolor's
palette: a pastel means *whose*; a saturated colour means *what*. The spinner
does not turn, because on both boards only an agent that wants you moves. See
[ADR 0014](docs/adr/0014-boards-that-cannot-run-our-code-are-drawn-by-the-host.md)
and [ADR 0016](docs/adr/0016-the-cn001-draws-a-mark-for-state.md).

**Quit Soomfon's own Stream Controller app first**: it holds the device, and
`doctor` will say so. Nothing is installed for the CN001 either -- it is
driven through IOKit on macOS and `hid.dll` on Windows, and its keys are drawn
and encoded as JPEGs by herdrkeys itself (ADR 0020).

**A crashed daemon leaves the panel lit.** The Keybow notices six seconds of
silence and shows "no host"; the CN001 keeps whatever it was last sent,
indefinitely. A clean stop -- `herdrkeys stop`, `kill`, ctrl-c -- blanks it.

## Install

```bash
herdr plugin install johnendean/herdrkeys
```

That is the whole install, on macOS and Windows, x86_64 or arm64. Nothing else
needs to be installed first: herdrkeys is a single binary. Herdr clones the
plugin and runs `bin/fetch.sh` (or `bin/fetch.ps1` on Windows), which downloads
the release binary for your platform, refuses it unless its SHA-256 matches
`checksums.txt` in the commit Herdr cloned, and starts the daemon. With a
Keybow plugged in, the daemon writes the firmware, resets the board so
`boot.py` takes effect, and connects -- no replugging. A Keybow already
running older herdrkeys firmware is brought up to date the same way. Windows is
supported, but Herdr itself still calls plugins on Windows a preview.

There is one daemon, and Herdr's startup hook starts it each time Herdr starts.
`herdr plugin update` installs the new build into a fresh directory, and the
new build's start asks the old daemon to stop and takes over from it. See
[ADR 0020](docs/adr/0020-a-go-binary-fetched-at-install.md).

### Uninstall

Herdr has no uninstall hook, so it takes two steps:

```bash
herdr plugin action invoke herdrkeys.stop   # stop the daemon, blanking the boards
herdr plugin uninstall herdrkeys
```

Skip the first and the daemon runs on, from a directory that is about to go.

**If your Keybow already carries firmware of your own**, herdrkeys will not
touch it. The keypad stays dark and the log tells you to run:

```bash
herdr plugin action invoke herdrkeys.adopt
```

which copies what is on the board into the plugin's config directory --
timestamped, so it never overwrites an earlier rescue -- and only then takes the
board over. See [ADR 0004](docs/adr/0004-provision-only-with-consent.md).

From a checkout instead, with Go installed -- a checkout that has a binary of
its own skips the download:

```bash
go build -o bin/herdrkeys ./cmd/herdrkeys   # bin/herdrkeys.exe on Windows
herdr plugin link .                         # and Herdr runs it as the plugin
bin/herdrkeys run                           # or run the daemon in the foreground
bin/herdrkeys deploy                        # write device/ to the Keybow
```

`herdr plugin action invoke herdrkeys.doctor` (or `bin/herdrkeys doctor`) tells
you which half is unhappy:

```
config file      C:\Users\you\AppData\Roaming\herdr\plugins\config\herdrkeys\config.toml  (absent, using defaults)
herdr            \\.\pipe\C:\Users\you\AppData\Roaming\herdr\herdr.sock  ok  (3 panes, 2 agent panes)
colours          all 2 agents; their keys wear their project colour
keybow           COM4  (console COM3)  in use by the running daemon (pid 24584)
cn001            serial 470B03784011  ok, opens alongside the running daemon (pid 24584)
usage            ok  (session 20%, weekly 11%; credential: C:\Users\you\.claude\.credentials.json)
dictation chord  left_control+left_gui  (matches Wispr Flow's push-to-talk)
daemon           running, pid 24584  (0.2.0, C:\...\herdrkeys\bin\herdrkeys.exe)
log              C:\Users\you\AppData\Local\herdrkeys\herdrkeys.log
terminal         C:\Program Files\WindowsApps\...\WindowsTerminal.exe (window 0x803e0)
```

Only one process can hold the keypad's serial port, so when the daemon is
already running `doctor` reports the port as in use rather than probing it. A
board that is not attached is fine as long as the other one is.

## Developing without hardware

```bash
go test ./...          # no keypad and no running Herdr required
bin/herdrkeys tui      # the whole daemon against a keypad drawn in the terminal;
                       # type 0-9 a-f to simulate a key press
pip install pytest && python -m pytest tests   # the CircuitPython firmware
```

The interesting logic -- key ordering, settling, attention ordering -- is a
pure function of plain data, so it is tested against a real 99-event recording
of a live Herdr session in `internal/state/testdata/`. The transport is tested
against a real Unix socket on macOS and a real named pipe on Windows. Tests
that need the hardware are opt-in: `HERDRKEYS_LIVE=1` talks to the running
Herdr, `HERDRKEYS_LIVE_KEYBOW=1` and `HERDRKEYS_KEYBOW_PORT=COM4` to a Keybow,
and `HERDRKEYS_LIVE_CN001=1` draws on a CN001.

The firmware stays CircuitPython, so `tests/` still runs `device/code.py` under
CPython against stand-in libraries. `scripts/bake_logos.py` turns
`assets/logos/*.svg` into the masks the CN001's drawing embeds.

Releases are cut by setting `version` in `herdr-plugin.toml` and `Version` in
`cmd/herdrkeys/main.go`, running `go run ./tools/release` to rewrite
`checksums.txt`, and pushing a `v` tag once that commit is on `main`. CI
rebuilds from the tag and refuses to publish if any binary differs from the
sums.

## Configuration

Everything has a working default, and the config file is optional.

herdrkeys can be started three ways -- by a Herdr plugin action, by Herdr's
startup hook, or by hand from a checkout -- and only the first two get
`HERDR_PLUGIN_CONFIG_DIR` in the environment. Rather than let that decide, the
plugin's directory is resolved either way and one precedence applies:

| On disk | In effect |
| --- | --- |
| both files | the plugin's, and `doctor` names the shadowed one |
| only your own | that one; your settings are never silently dropped |
| only the plugin's | the plugin's |
| neither | the plugin's directory if Herdr made one, else your own |

Your own is `~/.config/herdrkeys/config.toml` on macOS and
`%APPDATA%\herdrkeys\config.toml` on Windows. `doctor` always prints the file
actually in effect. The log is deliberately not resolved this way: it always
lives in `~/.local/state/herdrkeys/` (`%LOCALAPPDATA%\herdrkeys\` on Windows), so
it is in one place whoever started the daemon. Beside it is the lock that keeps
herdrkeys to one daemon. Nothing else is kept between runs -- the keys are
wherever Herdr's list puts them, so there is nothing to remember.

### Named sessions

There is one keypad, so there is one daemon, and it targets Herdr's **default**
session. The startup hook deliberately does not inherit the socket of whichever
server invoked it -- otherwise a named or throwaway session could capture the
keypad just by starting first. To follow a named session instead, set its socket
explicitly:

```toml
socket_path = "~/.config/herdr/sessions/<name>/herdr.sock"   # macOS
socket_path = 'C:\Users\you\AppData\Roaming\herdr\sessions\<name>\herdr.sock'   # Windows
```

```toml
settle_ms         = 300     # how long a state must hold before it lights up
status_poll_ms    = 500     # how often to ask Herdr what every agent is doing
provision         = true    # write firmware to an unprovisioned board
reconcile_seconds = 30      # how often to re-sync against session.snapshot
activate_terminal = true    # raise the terminal when focusing an agent
terminal_app      = ""      # macOS: the .app to raise, if it cannot be found
serial_port       = ""      # override the auto-discovered data port
socket_path       = ""      # override the Herdr socket
cn001_brightness  = 80      # the CN001's panel, 0-100
usage             = true    # the CN001 strip shows Claude plan usage (ADR 0019)
dictation_chord   = ""      # the keys Flow listens for; "" is this platform's default
```

On Windows the terminal is found through the Herdr client's console, which
names the Windows Terminal or console window it is drawn in, so there is
nothing to set.

Keys are numbered row-major with key 0 bottom-left, following PMK's own
`number_to_xy`. To turn the board, set `ROTATION` in `device/code.py` to 0, 90,
180 or 270 degrees anticlockwise from the orientation Pimoroni ships it in,
rebuild, then `bin/herdrkeys deploy` -- the firmware travels inside the binary.
Orientation lives on the device and only there; the host knows nothing about it
and only ever talks in slot numbers.

## How it fits together

```
Keybow 2040 ──usb_cdc data, JSON lines──┐
   code.py: palette + animation only    │
                                        │
Soomfon CN001 ──vendor HID, JPEGs───────┤
   internal/cn001: marks + animation,   │
   on the host                          ▼
                                   herdrkeys daemon
                                   ordering, settling, attention
                                        │
                                        ▼
            Herdr's socket, JSON lines (ADR 0021): a Unix socket on
            macOS, the named pipe \\.\pipe\<socket path> on Windows
                  agent.list (polled, the status source)
                  events.subscribe (pushed, a bonus)
                  pane.focus on a press (+ agent.focus, ADR 0008)
```

The board is deliberately dumb: it renders frames and reports presses. It knows
nothing about agents and holds no policy. A frame is a 16-character string, one character per
key, carrying *meaning* rather than colour -- so what red looks like is decided
in one place, on the device.

Frames are absolute rather than deltas, so a dropped byte or a board reset
self-heals on the next frame.

The CN001 runs no code of ours, so for it the same job -- turning a frame's
meanings into what the keys show -- is done on the host, by `internal/cn001`, and
nowhere else. It is redrawn key by key, only where something changed.

Design decisions worth the context are in [`docs/adr/`](docs/adr/); the
vocabulary is in [`CONTEXT.md`](CONTEXT.md).

## Notes

- The previous firmware on the board (a 4-layer HID keyboard: numpad, media
  keys, mixxx) is saved in [`salvage/`](salvage/). HID is left enabled in
  `boot.py`, so restoring those layers later needs no power cycle.
- Herdr's event backlog is bounded, rate-limited, and not a complete history,
  which is why the daemon reconciles against `session.snapshot` rather than
  trusting the stream, and ignores replayed events entirely. See ADR 0002.
- The Catppuccin pastels herdrcolor uses cannot be shown raw: scaled to idle
  brightness their channel spread is 6-12 counts out of 25, and green against
  teal differs by one -- six dim whites. The firmware saturates them first,
  which puts the closest pair 12 counts apart. See ADR 0010.
- `working` briefly breathed its project colour (ADR 0011) and was reverted a
  day later after testing it on the board: motion spread across the busy states
  does not make a board more informative, it makes the one key that matters
  harder to find. ADR 0012 records what that cost and bought.
- Keys are positions in Herdr's agent list rather than bindings an agent keeps.
  ADR 0002 chose the opposite and ADR 0009 supersedes it: the memory that turned
  out to matter is the position you can see on screen, not a key an agent
  claimed days ago.
- Colour does not wait on that stream at all. Measured against a live session,
  it replays its backlog at ten events a second and then goes quiet -- 140
  seconds, five real status changes, no events -- so agent status is polled with
  `agent.list`, which costs 0.22ms a call. The stream is kept because it is
  instant when it does deliver. See ADR 0005.
- The keypad types two things, the dictation chord held and Return, and only
  when the daemon says to, naming the chord on every hold (protocol 3): the daemon decides hold from tap, the latch and its
  five-minute timeout, for every board at once. The Keybow still lets go by
  itself on a frame that stops naming a microphone and on the host going quiet
  -- a chord left held down by a dead daemon would turn every later keystroke
  into a shortcut. If `adafruit_hid` is missing from the board's `lib/`, the
  grid still works and dictation quietly does not. See ADRs 0006 and 0017.
- The daemon resends the current frame every two seconds even when nothing has
  changed. The board discards a frame older than six seconds and falls back to
  showing no host, so a dead daemon cannot leave stale agent states lit.
- The keypad's port is never waited on for readiness. A daemon that had
  outlived a week of unplugs was once found still writing frames -- so the LEDs
  looked perfect -- while kqueue had stopped reporting its descriptor readable.
  Every press went unread, and because writing still worked, nothing in the log
  said so. On macOS the port is now read on a goroutine of its own with a
  tenth-of-a-second VTIME, so a read returns that often whether or not anything
  says it can; on Windows the read is an overlapped operation that completes
  when a byte arrives. See ADR 0007, which also says what to capture if it ever
  recurs.
- On Windows a read waiting for a key press must never hold up a frame: Go's
  files allow one read or write at a time, which on the first board meant every
  frame waited for a press. The serial port and the CN001 do their own
  overlapped I/O for that reason.
