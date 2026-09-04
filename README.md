# herdrkeys

Bind a [Pimoroni Keybow 2040](https://shop.pimoroni.com/products/keybow-2040) to
a running [Herdr](https://herdr.dev) session. The LEDs show what every agent is
doing; the keys jump to them.

```
[12 -        ] [13 -        ] [14 -        ] [15 f fn      ]
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

The **function key** (slot 15) never holds an agent. Press it to jump to the
next agent that wants you: `blocked` first, then `done`, wrapping from wherever
you are. If nothing wants you it flashes rather than doing nothing, so you know
it heard you. It is also the only key that shows whether the daemon can see
Herdr at all -- dark keys otherwise mean "no agents", which is not the same
thing.

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
make test   # 87 tests, no keypad and no running Herdr required
make tui    # the whole daemon against a keypad drawn in the terminal;
            # type 0-9 a-f to simulate a key press
```

The interesting logic -- slot assignment, settling, attention ordering -- is a
pure function of plain data, so it is tested against a real 99-event recording
of a live Herdr session in `tests/fixtures/`.

## Configuration

Everything has a working default; `~/.config/herdrkeys/config.toml` is optional.

```toml
settle_ms         = 300     # how long a state must hold before it lights up
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
                  events.subscribe (pushed) + agent.focus
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
- The daemon resends the current frame every two seconds even when nothing has
  changed. The board discards a frame older than six seconds and falls back to
  showing no host, so a dead daemon cannot leave stale agent states lit.
