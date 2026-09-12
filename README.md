# herdrkeys

Bind a [Pimoroni Keybow 2040](https://shop.pimoroni.com/products/keybow-2040) to
a running [Herdr](https://herdr.dev) session. The LEDs show what every agent is
doing; the keys jump to them.

```
[12 m mic    ] [13 -        ] [14 -        ] [15 f fn      ]   <- the feature row
[ 8 -        ] [ 9 -        ] [10 -        ] [11 -         ]
[ 4 -        ] [ 5 -        ] [ 6 -        ] [ 7 -         ]
[ 0 w working] [ 1 B blocked] [ 2 i idle   ] [ 3 -         ]
```

## What it does

Every agent Herdr recognises claims a key and keeps it. The key's colour is that
agent's lifecycle state:

| State     | Colour            | Meaning |
| --------- | ----------------- | ------- |
| `idle`    | dim green         | ready for input |
| `working` | amber             | busy |
| `blocked` | red, **blinking** | waiting on you: an approval or a question |
| `done`    | bright green      | finished work you have not looked at yet |
| `unknown` | dim blue          | an agent is there but Herdr cannot classify it |

Focus is shown by **brightness**, never by hue, so a colour only ever means one
thing. Motion is reserved for `blocked`, so movement never becomes background
noise.

Press a key to focus that agent -- and to raise the terminal, because focusing
an agent you cannot see is not focusing it.

One row of four holds no agents. Twelve keys is more grid than the agents have
ever asked for, and the fourth row is worth more as keys that do something else.

The **function key** (slot 15) is one of them. Press it to jump to the next agent
that wants you: `blocked` first, then `done`, wrapping from wherever you are. If
nothing wants you it flashes rather than doing nothing, so you know it heard you.
It is also the only key that shows whether the daemon can see Herdr at all --
dark keys otherwise mean "no agents", which is not the same thing.

The **microphone key** (slot 12) is the other. Hold it to dictate with Wispr
Flow: the keypad holds Right Option down for as long as your finger is down,
which is what Flow's push-to-talk listens for, and lets go when you do. **Tap it
instead and the microphone latches open** until you tap again -- for anything
longer than a sentence, or when you would rather not hold a key down.

Steady bright cyan means held; a **pulsing** cyan means latched, because the only
real risk of latching is forgetting it is on. A latch also gives up on its own
after five minutes: nothing on the board can tell whether Flow is still
listening, and a modifier held down all afternoon would turn every keystroke into
an Option chord.

The keystroke comes from the board itself rather than from the daemon, so it
needs no Accessibility permission -- but the daemon is what tells the board which
key it is, so a keypad showing "no host" will not dictate either.

Slots 13 and 14 are spare and stay dark.

## Install

```bash
herdr plugin install <owner>/herdrkeys
```

That is the whole install. With the keypad plugged in, the daemon writes the
firmware, resets the board so `boot.py` takes effect, and connects -- no venv,
no pip, no replugging. herdrkeys has no dependencies outside the standard
library, so it runs against the system `python3`.

**If your Keybow already carries firmware of your own**, herdrkeys will not
touch it. The keypad stays dark and the log tells you to run:

```bash
herdr plugin action invoke herdrkeys.adopt
```

which copies what is on the board into the plugin's config directory --
timestamped, so it never overwrites an earlier rescue -- and only then takes the
board over. See [ADR 0004](docs/adr/0004-provision-only-with-consent.md).

From a checkout instead:

```bash
make link            # herdr plugin link, for local development
make run             # or run the daemon in the foreground
make install-agent   # optional: supervise it with launchd instead
```

`herdr plugin action invoke herdrkeys.doctor` (or `make doctor`) tells you
which half is unhappy:

```
config file      ~/.config/herdrkeys/config.toml  (absent, using defaults)
slot map         ~/.local/state/herdrkeys/slots.json
herdr socket     ~/.config/herdr/herdr.sock  ok  (4 panes, 2 agent panes)
keybow           /dev/cu.usbmodem11403  (console /dev/cu.usbmodem11401)  ok
daemon           running, pid 35689
terminal app     /Applications/iTerm.app
```

Only one process can hold the keypad's serial port, so when the daemon is
already running `doctor` reports the port as in use rather than probing it.

## Developing without hardware

```bash
make venv   # pytest is the only dependency, and only for the tests
make test   # 127 tests, no keypad and no running Herdr required
make tui    # the whole daemon against a keypad drawn in the terminal;
            # type 0-9 a-f to simulate a key press
```

The interesting logic -- slot assignment, settling, attention ordering -- is a
pure function of plain data, so it is tested against a real 99-event recording
of a live Herdr session in `tests/fixtures/`.

## Configuration

Everything has a working default, and the config file is optional.

herdrkeys can be started three ways -- by a Herdr plugin action, by Herdr's
startup hook, or by hand from a checkout -- and only the first two get
`HERDR_PLUGIN_CONFIG_DIR` in the environment. Rather than let that decide, the
plugin's directory is resolved either way and one precedence applies:

| On disk | In effect |
| --- | --- |
| both files | the plugin's, and `doctor` names the shadowed one |
| only `~/.config/herdrkeys/config.toml` | that one; your settings are never silently dropped |
| only the plugin's | the plugin's |
| neither | the plugin's directory if Herdr made one, else XDG |

`doctor` always prints the file actually in effect. State is deliberately not
resolved this way: the slot map and log always live under
`~/.local/state/herdrkeys/`, so they are in one place whoever started the daemon.

### Named sessions

There is one keypad, so there is one daemon, and it targets Herdr's **default**
session. The startup hook deliberately does not inherit the socket of whichever
server invoked it -- otherwise a named or throwaway session could capture the
keypad just by starting first. To follow a named session instead, set its socket
explicitly:

```toml
socket_path = "~/.config/herdr/sessions/<name>/herdr.sock"
```

```toml
settle_ms         = 300     # how long a state must hold before it lights up
status_poll_ms    = 500     # how often to ask Herdr what every agent is doing
provision         = true    # write firmware to an unprovisioned board
reconcile_seconds = 30      # how often to re-sync against session.snapshot
activate_terminal = true    # raise the terminal when focusing an agent
terminal_app      = ""      # override the auto-detected .app bundle
serial_port       = ""      # override the auto-discovered data port
socket_path       = ""      # override the Herdr socket
```

Keys are numbered row-major with key 0 bottom-left, following PMK's own
`number_to_xy`. To turn the board, set `ROTATION` in `device/code.py` to 0, 90,
180 or 270 degrees anticlockwise from the orientation Pimoroni ships it in, then
`make deploy`. Orientation lives on the device and only there; the host knows
nothing about it and only ever talks in slot numbers.

## How it fits together

```
Keybow 2040 ──usb_cdc data, JSON lines──┐
   code.py: palette + animation only    │
                                        ▼
                                   herdrkeys daemon
                                   slots, settling, attention
                                        │
                                        ▼
                  ~/.config/herdr/herdr.sock, JSON lines
                  agent.list (polled, the status source)
                  events.subscribe (pushed, a bonus)
                  pane.focus on a press (+ agent.focus, ADR 0008)
```

The board is deliberately dumb: it renders frames and reports presses. It holds
no slot map and no policy. A frame is a 16-character string, one character per
key, carrying *meaning* rather than colour -- so what red looks like is decided
in one place, on the device.

Frames are absolute rather than deltas, so a dropped byte or a board reset
self-heals on the next frame.

Design decisions worth the context are in [`docs/adr/`](docs/adr/); the
vocabulary is in [`CONTEXT.md`](CONTEXT.md).

## Notes

- The previous firmware on the board (a 4-layer HID keyboard: numpad, media
  keys, mixxx) is saved in [`salvage/`](salvage/). HID is left enabled in
  `boot.py`, so restoring those layers later needs no power cycle.
- Herdr's event backlog is bounded, rate-limited, and not a complete history,
  which is why the daemon reconciles against `session.snapshot` rather than
  trusting the stream, and ignores replayed events entirely. See ADR 0002.
- Colour does not wait on that stream at all. Measured against a live session,
  it replays its backlog at ten events a second and then goes quiet -- 140
  seconds, five real status changes, no events -- so agent status is polled with
  `agent.list`, which costs 0.22ms a call. The stream is kept because it is
  instant when it does deliver. See ADR 0005.
- The keypad types exactly one keystroke, Right Option, and only for the
  microphone key. It is released on the key coming up, on a second tap when
  latched, five minutes into a latch, on a frame that stops naming that key the
  microphone, and on the host going quiet -- a
  modifier left held down by a dead daemon would turn every later keystroke into
  an Option chord. If `adafruit_hid` is missing from the board's `lib/`, the grid
  still works and dictation quietly does not. See ADR 0006.
- The daemon resends the current frame every two seconds even when nothing has
  changed. The board discards a frame older than six seconds and falls back to
  showing no host, so a dead daemon cannot leave stale agent states lit.
- The keypad is read on every pass of the loop, not only when the selector says
  the port is readable. A daemon that had outlived a week of unplugs was once
  found still writing those frames -- so the LEDs looked perfect -- while kqueue
  had stopped reporting its descriptor readable. Every press went unread, and
  because writing still worked, nothing in the log said so. See ADR 0007, which
  also says what to capture before restarting if it ever recurs.
