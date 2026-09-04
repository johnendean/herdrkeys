"""Wiring: Herdr on one side, the keypad on the other, the reducer in between.

The two sides fail independently and routinely -- you unplug the keypad, Herdr
restarts on update -- so the daemon never exits voluntarily. Each side has its
own reconnect loop with capped backoff, and launchd's KeepAlive is only a crash
net. Cycling the process for a routine reconnect would throw away the in-memory
slot map for no reason.
"""

from __future__ import annotations

import logging
import selectors
import time
from pathlib import Path

from . import activate as activate_app
from . import discovery, herdr, reducer
from .config import Config
from .device import Device, DeviceError, SerialDevice
from .herdr_state import HerdrState
from .model import FN_SLOT, Frame
from .settling import Settler
from .slots import SlotMap

log = logging.getLogger("herdrkeys")

# How a running daemon is recognised from outside, so `doctor` can tell "the
# firmware is broken" apart from "the daemon already has the port".
PROCESS_MARKER = "herdrkeys run"

IDLE_TIMEOUT = 1.0

# Frames are only pushed when they change, so a steady session can go minutes
# without one. The board treats silence as the daemon having died, so resend the
# current frame periodically to prove otherwise. Must be comfortably shorter
# than HOST_TIMEOUT in device/code.py.
HEARTBEAT_SECONDS = 2.0

# Herdr replays a backlog on subscribe, and a replayed event can carry the same
# per-pane revision as the snapshot taken alongside it -- Herdr does not bump
# `revision` for every status change. The revision guard drops strictly-older
# updates but cannot drop those, so history folded after a snapshot overwrites
# it and the grid shows a state the agent was in minutes ago.
#
# The replay is also rate-limited: 94 backlog events took about four seconds to
# arrive on a real session, and how many there are depends on how long that
# session has been alive. So the corrective reconcile waits for the stream to go
# quiet rather than for a fixed delay, which would race a longer backlog.
BACKLOG_QUIET_SECONDS = 0.75

# ...but a busy session might never fall quiet, and the grid has to light up
# eventually. Reconcile regardless once this long has passed since connecting.
BACKLOG_MAX_SECONDS = 10.0


class Backoff:
    def __init__(self, minimum: float, maximum: float) -> None:
        self.minimum, self.maximum = minimum, maximum
        self._next_attempt = 0.0
        self._delay = minimum

    def ready(self, now: float) -> bool:
        return now >= self._next_attempt

    def failed(self, now: float) -> None:
        self._next_attempt = now + self._delay
        self._delay = min(self.maximum, self._delay * 2)

    def succeeded(self) -> None:
        self._delay = self.minimum
        self._next_attempt = 0.0

    def wait_for(self, now: float) -> float:
        return max(0.0, self._next_attempt - now)


class Daemon:
    def __init__(self, config: Config, *, clock=time.monotonic, device_factory=None) -> None:
        self.config = config
        self.clock = clock
        self.device_factory = device_factory
        self.socket_path: Path = config.socket_path or herdr.default_socket_path()
        self.state = HerdrState()
        self.slots = SlotMap.load(config.state_path)
        self.settler = Settler(config.settle_seconds)

        self.stream: herdr.EventStream | None = None
        self.device: Device | None = None
        self.last_frame: Frame | None = None
        self._last_push_at = 0.0

        self._herdr_backoff = Backoff(config.reconnect_min_seconds, config.reconnect_max_seconds)
        self._device_backoff = Backoff(config.reconnect_min_seconds, config.reconnect_max_seconds)
        self._next_reconcile = 0.0
        self._backlog_settling = False
        self._last_event_at = 0.0
        self._backlog_deadline = 0.0
        self._host_app: str | None = config.terminal_app

    # -- connections -----------------------------------------------------

    def _connect_herdr(self, now: float) -> None:
        try:
            self.stream = herdr.EventStream(self.socket_path)
            self._reconcile(now)
            self._backlog_settling = True
            self._last_event_at = now
            self._backlog_deadline = now + BACKLOG_MAX_SECONDS
            self._herdr_backoff.succeeded()
            log.info("connected to herdr at %s", self.socket_path)
        except (OSError, herdr.HerdrError) as exc:
            self.stream = None
            self._herdr_backoff.failed(now)
            log.debug("herdr connect failed: %s", exc)

    def _drop_herdr(self, now: float, reason: str) -> None:
        log.warning("herdr connection lost: %s", reason)
        if self.stream is not None:
            self.stream.close()
        self.stream = None
        self.state = HerdrState()
        self._backlog_settling = False
        self._herdr_backoff.failed(now)

    def _connect_device(self, now: float) -> None:
        if self.device_factory is not None:
            self.device = self.device_factory()
            self._device_backoff.succeeded()
            self.last_frame = None
            return
        port = self.config.serial_port or discovery.find_data_port()
        if port is None:
            self._device_backoff.failed(now)
            return
        try:
            self.device = SerialDevice(port)
            self._device_backoff.succeeded()
            self.last_frame = None  # force a full frame onto fresh firmware
            log.info("connected to keypad at %s (%s)", port, self.device.firmware)
        except (OSError, DeviceError) as exc:
            self.device = None
            self._device_backoff.failed(now)
            log.debug("keypad connect failed: %s", exc)

    def _drop_device(self, now: float, reason: str) -> None:
        log.warning("keypad connection lost: %s", reason)
        if self.device is not None:
            self.device.close()
        self.device = None
        self.last_frame = None
        self._device_backoff.failed(now)

    # -- herdr ingest ----------------------------------------------------

    def _reconcile(self, now: float) -> None:
        """Take a snapshot as truth.

        Needed on every reconnect, and periodically: the backlog Herdr replays on
        subscribe is not guaranteed to reach back to session start, so folding
        events alone could leave a pane the daemon never heard about.
        """
        snapshot = herdr.snapshot(self.socket_path)
        self.state.apply_snapshot(snapshot)
        self.slots.gc(self.state.live_pane_ids())
        self._next_reconcile = now + self.config.reconcile_seconds

    def _pump_herdr(self, now: float) -> None:
        if self.stream is None:
            return
        try:
            events = self.stream.read_events()
        except (OSError, herdr.HerdrError) as exc:
            self._drop_herdr(now, str(exc))
            return
        if events:
            self._last_event_at = now
        if self._backlog_settling:
            # Herdr is replaying history. Applying it animates the session's past
            # across the keys -- focus strobing between agents, agents appearing
            # and vanishing -- so drop it. The snapshot taken at connect is
            # already truth, and another one follows once the replay stops, so
            # nothing is lost by ignoring the stream until then.
            events = []
        for event in events:
            self.state.apply_event(event)
        if self._backlog_done(now):
            self._settle_backlog(now)

    def _backlog_done(self, now: float) -> bool:
        if not self._backlog_settling:
            return False
        return now - self._last_event_at >= BACKLOG_QUIET_SECONDS or now >= self._backlog_deadline

    def _settle_backlog(self, now: float) -> None:
        """Re-apply truth once the replayed history has stopped arriving."""
        self._backlog_settling = False
        try:
            self._reconcile(now)
        except (OSError, herdr.HerdrError) as exc:
            self._drop_herdr(now, str(exc))

    # -- key presses -----------------------------------------------------

    def _focus(self, pane_id: str) -> None:
        try:
            herdr.focus_agent(self.socket_path, pane_id)
        except (OSError, herdr.HerdrError) as exc:
            log.warning("focus %s failed: %s", pane_id, exc)
            return
        if not self.config.activate_terminal:
            return
        if self._host_app is None:
            self._host_app = activate_app.detect_host_app()
        if self._host_app:
            activate_app.activate(self._host_app)

    def handle_press(self, slot: int) -> None:
        agent_panes = self.state.agent_panes()
        if slot == FN_SLOT:
            target = reducer.next_attention_target(agent_panes, self.slots, self.settler)
            if target is None:
                if self.device is not None:
                    self.device.flash()  # heard you; nothing wants you
                return
            self._focus(target)
            return
        pane_id = reducer.pane_for_slot(slot, agent_panes, self.slots)
        if pane_id is not None:
            self._focus(pane_id)

    def _pump_device(self, now: float) -> None:
        if self.device is None:
            return
        try:
            presses = self.device.read_presses()
        except DeviceError as exc:
            self._drop_device(now, str(exc))
            return
        for slot in presses:
            self.handle_press(slot)

    # -- rendering -------------------------------------------------------

    def render(self, now: float) -> Frame:
        agent_panes = self.state.agent_panes()
        live = self.state.live_pane_ids()
        for pane_id, pane in agent_panes.items():
            self.settler.observe(pane_id, pane.state, now)
        self.settler.retain(live)
        self.settler.tick(now)
        if reducer.sync_slots(agent_panes, self.slots, live):
            self.slots.save(self.config.state_path)
        return reducer.render(agent_panes, self.slots, self.settler, connected=self.stream is not None)

    def _push(self, frame: Frame, now: float) -> None:
        if self.device is None:
            return
        due = now - self._last_push_at >= HEARTBEAT_SECONDS
        if frame == self.last_frame and not due:
            return
        try:
            self.device.send_frame(frame)
        except DeviceError as exc:
            self._drop_device(now, str(exc))
            return
        self.last_frame = frame
        self._last_push_at = now

    # -- loop ------------------------------------------------------------

    def _timeout(self, now: float) -> float:
        candidates = [IDLE_TIMEOUT]
        if self.device is not None:
            candidates.append(max(0.0, self._last_push_at + HEARTBEAT_SECONDS - now))
        settle = self.settler.next_deadline(now)
        if settle is not None:
            candidates.append(settle)
        if self.stream is None:
            candidates.append(self._herdr_backoff.wait_for(now))
        else:
            candidates.append(max(0.0, self._next_reconcile - now))
            if self._backlog_settling:
                candidates.append(max(0.0, self._last_event_at + BACKLOG_QUIET_SECONDS - now))
                candidates.append(max(0.0, self._backlog_deadline - now))
        if self.device is None:
            candidates.append(self._device_backoff.wait_for(now))
        return max(0.01, min(candidates))

    def run_forever(self) -> None:
        log.info("herdrkeys starting; socket=%s state=%s", self.socket_path, self.config.state_path)
        while True:
            now = self.clock()

            if self.stream is None and self._herdr_backoff.ready(now):
                self._connect_herdr(now)
            if self.device is None and self._device_backoff.ready(now):
                self._connect_device(now)
            if self.stream is not None and now >= self._next_reconcile:
                try:
                    self._reconcile(now)
                except (OSError, herdr.HerdrError) as exc:
                    self._drop_herdr(now, str(exc))

            selector = selectors.DefaultSelector()
            if self.stream is not None:
                selector.register(self.stream.fileno(), selectors.EVENT_READ, "herdr")
            if self.device is not None:
                selector.register(self.device.fileno(), selectors.EVENT_READ, "device")
            try:
                ready = selector.select(self._timeout(now))
            finally:
                selector.close()

            now = self.clock()
            woken = {key.data for key, _ in ready}
            if "herdr" in woken:
                self._pump_herdr(now)
            elif self._backlog_done(now):
                self._settle_backlog(now)
            if "device" in woken:
                self._pump_device(now)

            self._push(self.render(self.clock()), now)
