# 7. The keypad is read every pass, not on a selector edge

Date: 2026-09-12

## Status

Accepted

## Context

A keypad displayed every agent correctly and did nothing when pressed. The
daemon had been up for two days and had survived roughly seven keypad
reconnects in that time.

What the evidence showed, in the order it arrived:

* Herdr's own server log carried `herdrkeys:agent.focus` requests from earlier
  the same day, each `outcome="ok"` and each focusing a workspace. So the slot
  map, the API call and the socket were all fine, and had been working hours
  before.
* A controlled test -- 150 seconds, presses throughout -- produced **no focus
  request at all**.
* The daemon's serial descriptor advanced **exactly 48 bytes every two
  seconds**, with no keys touched: the heartbeat frame, being written. Nothing
  was being read.
* With the daemon stopped, a raw listener on the data port captured **33 clean
  key messages** -- slots 0, 1, 2, 3, 12 and 15, matching the keys pressed. The
  board, the firmware and the wire protocol were all healthy. `doctor`
  handshaked with the board on the first try.
* The same daemon code, freshly started, read and focused **every** press.

The process was deaf. It was writing to its descriptor perfectly well and never
reading from it. `_pump_device` ran only when `selectors.select()` named the
device, which made that one edge the single point of failure for the whole input
path -- and because writing still worked, nothing ever surfaced an error. The
LEDs stayed correct for as long as the daemon lived, which is precisely what made
this hard to see: every visible signal said the keypad was healthy.

**What stopped kqueue reporting that descriptor readable was not established.**
The wedged process's state died when it was killed. It had been through a week of
unplugs and re-enumerations; the log shows the descriptor churning, including
drops on `ENXIO` and one `EINVAL` raised out of the registration call itself.
That is a plausible neighbourhood for the fault and not a proof.

## Decision

Read the keypad on every pass of the loop, whatever the selector reported.

The registration stays. It is still what makes the loop wake promptly on a press
rather than waiting out a timeout, and losing that would cost latency on every
key. It is simply no longer the only thing that can cause a read.

This is safe because reads already never block: the port is opened `O_NONBLOCK`
with `VMIN = 0` and `VTIME = 0`, and `EAGAIN` comes back as `b""`. And it is
cheap because the loop already wakes at least once per status poll (500ms) and
once per heartbeat (2s) regardless of traffic.

## Consequences

The worst case changes from *presses stop forever, silently, until a human
restarts the daemon* to *a press arrives up to one poll late*. The cost is one
extra `read` per pass, returning `EAGAIN`.

This is the same instinct as ADR 0005, and as absolute frames: where a push
channel and a pull channel can both answer the question, do not let the push
channel be the only one that can. ADR 0005 stopped trusting Herdr's event stream
to volunteer status; this stops trusting kqueue to volunteer readability. Frames
are absolute for the same reason -- a dropped byte self-heals on the next one
rather than needing anybody to notice.

The unproven root cause is now much cheaper to be wrong about, but it is still
unproven. **If a keypad ever again lights up correctly and ignores presses on a
daemon carrying this change**, the selector is exonerated and the fault is lower
down -- the descriptor itself, or the port. Capture this *before* restarting,
because restarting destroys the evidence:

```bash
lsof /dev/cu.usbmodem*            # is anything else holding the data port?
lsof -p "$(pgrep -f 'herdrkeys run')" -a -d 4   # offset: writes advance it 48b/2s
```

then stop the daemon and listen to the port raw, to establish whether the board
is still emitting presses at all. That last step is what separated "the board
stopped sending" from "the daemon stopped reading", and it was the one that
settled this.
