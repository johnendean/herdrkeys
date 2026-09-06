import re
from pathlib import Path

import pytest

from herdrkeys import daemon as daemon_module
from herdrkeys.config import Config
from herdrkeys.daemon import Backoff, Daemon
from herdrkeys.device import FakeDevice
from herdrkeys.model import FEATURE_SLOTS, FN_SLOT, MIC_SLOT, AgentState


@pytest.fixture
def rig(tmp_path, monkeypatch):
    """A daemon with a fake keypad and no real socket underneath it."""
    focused = []
    monkeypatch.setattr(daemon_module.herdr, "focus_agent", lambda path, pane_id: focused.append(pane_id))
    monkeypatch.setattr(daemon_module.activate_app, "detect_host_app", lambda: "/Applications/Test.app")
    activated = []
    monkeypatch.setattr(daemon_module.activate_app, "activate", lambda app: activated.append(app))

    config = Config(settle_seconds=0.0, activate_terminal=True, state_path=tmp_path / "slots.json")
    device = FakeDevice()
    instance = Daemon(config, device_factory=lambda: device)
    instance.device = device
    return instance, device, focused, activated


def load(instance, panes, focused_pane_id):
    instance.state.apply_snapshot({"panes": panes, "focused_pane_id": focused_pane_id})
    instance.render(0.0)
    instance.render(1.0)  # a second pass so the settler commits


def agent_pane(pane_id, status, revision=1, focused=False):
    return {"pane_id": pane_id, "agent": "claude", "agent_status": status,
            "revision": revision, "focused": focused}


def test_pressing_a_key_focuses_that_agent_and_raises_the_terminal(rig):
    instance, _device, focused, activated = rig
    load(instance, [agent_pane("w1:p1", "idle"), agent_pane("w2:p1", "working")], "w1:p1")

    instance.handle_press(1)
    assert focused == ["w2:p1"]
    assert activated == ["/Applications/Test.app"], "focusing what you cannot see is not focusing it"


def test_pressing_an_empty_key_does_nothing_at_all(rig):
    instance, device, focused, _ = rig
    load(instance, [agent_pane("w1:p1", "idle")], "w1:p1")
    instance.handle_press(9)
    assert focused == [] and device.flashes == 0


def test_the_function_key_goes_to_the_blocked_agent(rig):
    instance, _device, focused, _ = rig
    load(
        instance,
        [agent_pane("w1:p1", "working"), agent_pane("w2:p1", "blocked"), agent_pane("w3:p1", "done")],
        "w1:p1",
    )
    instance.handle_press(FN_SLOT)
    assert focused == ["w2:p1"], "blocked outranks done"


def test_the_function_key_flashes_when_nothing_wants_you(rig):
    instance, device, focused, _ = rig
    load(instance, [agent_pane("w1:p1", "working")], "w1:p1")
    instance.handle_press(FN_SLOT)
    assert focused == []
    assert device.flashes == 1, "silence is ambiguous; a flash says 'heard you'"


def test_activation_can_be_switched_off(rig):
    instance, _device, focused, activated = rig
    instance.config.activate_terminal = False
    load(instance, [agent_pane("w1:p1", "idle")], "w1:p1")
    instance.handle_press(0)
    assert focused == ["w1:p1"] and activated == []


def test_pressing_the_mic_key_does_nothing_on_the_host(rig):
    # The keystroke is the board's job. The host must not focus an agent, must
    # not flash "nothing wants you", and must not care that the press happened.
    instance, device, focused, _ = rig
    load(instance, [agent_pane("w1:p1", "idle"), agent_pane("w2:p1", "blocked")], "w1:p1")

    instance.handle_press(MIC_SLOT)
    assert focused == [] and device.flashes == 0


def test_no_feature_key_can_focus_an_agent(rig):
    instance, _device, focused, _ = rig
    load(instance, [agent_pane(f"w{i}:p1", "idle") for i in range(1, 13)], "w1:p1")
    for slot in FEATURE_SLOTS:
        if slot != FN_SLOT:
            instance.handle_press(slot)
    assert focused == []


def test_the_slot_map_survives_a_restart(rig, tmp_path):
    instance, _device, _focused, _ = rig
    load(instance, [agent_pane("w1:p1", "idle"), agent_pane("w2:p1", "idle")], "w1:p1")
    assert instance.config.state_path.exists()

    revived = Daemon(instance.config, device_factory=lambda: FakeDevice())
    assert revived.slots.slot_of("w2:p1") == 1


def test_a_frame_is_only_pushed_when_it_changes(rig):
    instance, device, _focused, _ = rig
    load(instance, [agent_pane("w1:p1", "idle")], "w1:p1")

    frame = instance.render(2.0)
    instance._push(frame, 2.0)
    instance._push(instance.render(3.0), 3.0)
    assert len(device.frames) == 1

    instance.state.apply_snapshot({"panes": [agent_pane("w1:p1", "blocked", revision=2)],
                                  "focused_pane_id": "w1:p1"})
    instance._push(instance.render(4.0), 4.0)
    assert len(device.frames) == 2
    assert device.last_frame.keys[0] == "B"


def test_disconnected_from_herdr_is_visible_on_the_function_key(rig):
    instance, _device, _focused, _ = rig
    assert instance.stream is None
    assert instance.render(0.0).keys[FN_SLOT] == "x"


def test_settled_state_reaches_the_frame(rig):
    instance, _device, _focused, _ = rig
    load(instance, [agent_pane("w1:p1", "done")], None)
    assert instance.state.agent_panes()["w1:p1"].state is AgentState.DONE
    assert instance.render(2.0).keys[0] == "d"


# -- reconnect backoff ---------------------------------------------------


def test_backoff_widens_on_failure_and_resets_on_success():
    backoff = Backoff(0.5, 4.0)
    assert backoff.ready(0.0)
    backoff.failed(0.0)
    assert not backoff.ready(0.4) and backoff.ready(0.5)
    backoff.failed(0.5)
    assert not backoff.ready(1.4) and backoff.ready(1.5)
    backoff.succeeded()
    assert backoff.ready(1.5)


def test_backoff_is_capped():
    backoff = Backoff(0.5, 2.0)
    for _ in range(10):
        backoff.failed(0.0)
    assert backoff.wait_for(0.0) <= 2.0


def test_the_backlog_is_re_reconciled_once_the_stream_goes_quiet(rig, monkeypatch):
    # The replay is rate limited -- ~94 events over four seconds on a real
    # session, growing with session age -- so a fixed delay would race it. The
    # second snapshot is what picks up anything that changed while it drained.
    instance, _device, _focused, _ = rig
    instance.config.reconcile_seconds = 30.0
    snapshots = []

    class Stream:
        def __init__(self, path, **kwargs):
            pass

        def fileno(self):
            return 0

        def read_events(self):
            return []

        def close(self):
            pass

    def snapshot(path):
        snapshots.append(True)
        status = "idle" if len(snapshots) == 1 else "blocked"
        return {"panes": [agent_pane("w1:p1", status, revision=36)], "focused_pane_id": None}

    monkeypatch.setattr(daemon_module.herdr, "EventStream", Stream)
    monkeypatch.setattr(daemon_module.herdr, "snapshot", snapshot)

    instance._connect_herdr(100.0)
    assert len(snapshots) == 1 and instance._backlog_settling
    assert instance.state.panes["w1:p1"].state is AgentState.IDLE

    instance._pump_herdr(100.5)  # quiet, but not long enough
    assert instance._backlog_settling and len(snapshots) == 1

    instance._pump_herdr(101.0)  # quiet for longer than BACKLOG_QUIET_SECONDS
    assert not instance._backlog_settling
    assert len(snapshots) == 2
    assert instance.state.panes["w1:p1"].state is AgentState.BLOCKED, "picks up what changed"


def test_losing_herdr_clears_the_backlog_wait(rig, monkeypatch):
    instance, _device, _focused, _ = rig
    instance._backlog_settling = True
    instance.stream = None
    instance._drop_herdr(0.0, "gone")
    assert not instance._backlog_settling


def test_same_revision_updates_are_not_discarded():
    # Herdr does not bump `revision` for every status change: a pane observed at
    # revision 36 was `working` in the event backlog and `idle` in a snapshot
    # taken later. Filtering on `<=` would drop the newer value.
    from herdrkeys.herdr_state import HerdrState

    state = HerdrState()
    state.apply_snapshot({"panes": [agent_pane("w1:p1", "working", revision=36)],
                          "focused_pane_id": None})
    state.apply_event({"data": {"type": "pane_updated", "pane": agent_pane("w1:p1", "idle", revision=36)}})
    assert state.panes["w1:p1"].state is AgentState.IDLE


def test_an_unchanged_frame_is_resent_as_a_heartbeat(rig):
    # Frames are only pushed on change, so a steady session can go minutes
    # without one -- and the board reads silence as the daemon having died.
    instance, device, _focused, _ = rig
    load(instance, [agent_pane("w1:p1", "idle")], "w1:p1")

    instance._push(instance.render(10.0), 10.0)
    assert len(device.frames) == 1

    instance._push(instance.render(11.0), 11.0)
    assert len(device.frames) == 1, "unchanged and not yet due"

    instance._push(instance.render(12.5), 12.5)
    assert len(device.frames) == 2, "unchanged but due"
    assert device.frames[-1] == device.frames[0]


def test_the_heartbeat_keeps_the_loop_awake(rig):
    instance, _device, _focused, _ = rig
    instance._last_push_at = 100.0
    assert instance._timeout(100.0) <= daemon_module.HEARTBEAT_SECONDS


def test_heartbeat_is_faster_than_the_boards_staleness_timeout():
    # The board discards a frame nobody has refreshed; if these two ever cross,
    # a healthy session would flicker to "no host".
    device_source = (Path(__file__).parent.parent / "device" / "code.py").read_text()
    match = re.search(r"^HOST_TIMEOUT = ([\d.]+)", device_source, re.M)
    assert match, "device/code.py must declare HOST_TIMEOUT"
    assert daemon_module.HEARTBEAT_SECONDS < float(match.group(1)) / 2


def test_history_is_discarded_rather_than_applied(rig, monkeypatch):
    # Replaying the backlog would walk the session's past across the keys: focus
    # strobing between agents every ~50ms, agents appearing and vanishing.
    # Observed live before this gate existed. The connect-time snapshot is
    # already truth, so the grid can light up at once and simply ignore history.
    instance, device, _focused, _ = rig
    history = [{"data": {"type": "pane_focused", "pane_id": "w2:p1", "workspace_id": "w2"}}]

    class Stream:
        def __init__(self, path, **kwargs):
            pass

        def fileno(self):
            return 0

        def read_events(self):
            return [history.pop(0)] if history else []

        def close(self):
            pass

    monkeypatch.setattr(daemon_module.herdr, "EventStream", Stream)
    monkeypatch.setattr(
        daemon_module.herdr,
        "snapshot",
        lambda path: {"panes": [agent_pane("w1:p1", "idle", focused=True)], "focused_pane_id": "w1:p1"},
    )

    instance._connect_herdr(0.0)
    assert instance._backlog_settling
    instance._push(instance.render(0.1), 0.1)
    assert len(device.frames) == 1, "the connect snapshot is truth; show it at once"

    instance._pump_herdr(0.2)  # a replayed focus change arrives
    assert instance.state.focused_pane_id == "w1:p1", "history must not move focus"

    instance._pump_herdr(1.5)  # quiet: reconcile and start trusting the stream
    assert not instance._backlog_settling
    instance.state.apply_event(
        {"data": {"type": "pane_focused", "pane_id": "w9:p9", "workspace_id": "w9"}}
    )
    assert instance.state.focused_pane_id == "w9:p9", "live events are applied normally"


def test_a_busy_session_still_lights_up_eventually(rig, monkeypatch):
    # If events never stop arriving, waiting for quiet would wait forever.
    instance, _device, _focused, _ = rig
    monkeypatch.setattr(daemon_module.herdr, "snapshot", lambda path: {"panes": [], "focused_pane_id": None})
    instance._backlog_settling = True
    instance._last_event_at = 100.0
    instance._backlog_deadline = 100.0 + daemon_module.BACKLOG_MAX_SECONDS

    assert not instance._backlog_done(100.5), "still arriving, deadline not reached"
    assert instance._backlog_done(100.0 + daemon_module.BACKLOG_MAX_SECONDS)


def test_a_stale_descriptor_drops_the_keypad_rather_than_the_daemon(rig, monkeypatch):
    # kqueue refuses a descriptor that has gone stale -- the keypad unplugged, or
    # re-enumerated by a deploy -- with EINVAL, at registration rather than at
    # read time. Uncaught, that killed a daemon documented as never exiting
    # voluntarily, and the keypad stayed dark until someone noticed.
    instance, device, _focused, _ = rig

    class Stop(Exception):
        """Ends run_forever from the clock, since nothing else ever does."""

    def stale_fileno():
        raise OSError(22, "Invalid argument")

    device.fileno = stale_fileno
    instance._herdr_backoff = Backoff(0.0, 0.0)
    instance._device_backoff = Backoff(0.0, 0.0)
    monkeypatch.setattr(daemon_module.herdr, "EventStream", lambda path, **kw: (_ for _ in ()).throw(OSError("no herdr")))

    ticks = iter([0.0, 0.01, 0.02, 0.03])

    def clock():
        try:
            return next(ticks)
        except StopIteration:
            raise Stop

    instance.clock = clock
    with pytest.raises(Stop):
        instance.run_forever()

    assert instance.device is None, "the keypad was dropped; the daemon kept running"
    assert device.closed


# -- status polling ------------------------------------------------------


def agent_row(pane_id, status, revision=1, focused=False):
    """One row of `agent.list`, shaped as Herdr returns it."""
    return {"pane_id": pane_id, "agent": "claude", "agent_status": status,
            "revision": revision, "focused": focused}


def test_status_comes_from_the_poll_when_no_event_ever_arrives(rig, monkeypatch):
    # Measured live: the stream replays its backlog at ten events a second and
    # then goes quiet, delivering nothing for the next two minutes while five
    # real status changes happened. Colour cannot wait on it. See ADR 0005.
    instance, device, _focused, _ = rig
    load(instance, [agent_pane("w1:p1", "working")], "w1:p1")
    monkeypatch.setattr(daemon_module.herdr, "agents", lambda path: [agent_row("w1:p1", "blocked", focused=True)])

    instance._poll_status(10.0)
    instance._push(instance.render(10.0), 10.0)
    assert device.last_frame.keys[0] == "B", "no event arrived, and the key changed anyway"


def test_the_poll_paces_itself_and_keeps_the_loop_awake(rig, monkeypatch):
    instance, _device, _focused, _ = rig
    monkeypatch.setattr(daemon_module.herdr, "agents", lambda path: [])
    instance.stream = object()
    instance.config.status_poll_seconds = 0.5
    instance._last_push_at = 100.0
    instance._next_reconcile = 130.0

    instance._poll_status(100.0)
    assert instance._next_status_poll == 100.5
    assert instance._timeout(100.0) == 0.5, "the loop must wake for its own poll"


def test_an_agent_leaving_goes_dark_but_keeps_its_slot(rig, monkeypatch):
    # `agent.list` only lists agent panes, so absence means the agent left --
    # never that the pane did. Slots outlive agents (ADR 0002).
    instance, _device, _focused, _ = rig
    load(instance, [agent_pane("w1:p1", "idle"), agent_pane("w2:p1", "working")], "w1:p1")
    assert instance.slots.slot_of("w2:p1") == 1
    monkeypatch.setattr(daemon_module.herdr, "agents", lambda path: [agent_row("w1:p1", "idle", focused=True)])

    instance._poll_status(10.0)
    assert instance.render(10.0).keys[1] == "-"
    assert instance.slots.slot_of("w2:p1") == 1, "restarting an agent in place keeps its key"


def test_an_agent_the_poll_finds_first_gets_a_slot(rig, monkeypatch):
    instance, _device, _focused, _ = rig
    load(instance, [agent_pane("w1:p1", "idle")], "w1:p1")
    monkeypatch.setattr(
        daemon_module.herdr,
        "agents",
        lambda path: [agent_row("w1:p1", "idle", focused=True), agent_row("w5:p2", "working")],
    )

    instance._poll_status(10.0)
    frame = instance.render(10.0)
    assert instance.slots.slot_of("w5:p2") == 1
    assert frame.keys[1] == "w", "a new agent need not wait for the next reconcile"


def test_the_poll_releases_focus_it_can_see_has_moved(rig, monkeypatch):
    # Focus is brightness. `agent.list` cannot name a shell pane, but a row
    # saying "not me" is enough to stop a key claiming focus it lost.
    instance, _device, _focused, _ = rig
    load(instance, [agent_pane("w1:p1", "idle", focused=True)], "w1:p1")
    assert instance.render(10.0).keys[0] == "I"

    monkeypatch.setattr(daemon_module.herdr, "agents", lambda path: [agent_row("w1:p1", "idle")])
    instance._poll_status(10.0)
    assert instance.render(10.0).keys[0] == "i"


def test_a_failed_poll_drops_herdr_like_any_other_request(rig, monkeypatch):
    instance, _device, _focused, _ = rig

    class Stream:
        def close(self):
            pass

    instance.stream = Stream()

    def gone(path):
        raise daemon_module.herdr.HerdrError("agent.list: connection closed before a reply")

    monkeypatch.setattr(daemon_module.herdr, "agents", gone)
    instance._poll_status(5.0)
    assert instance.stream is None, "the function key must show Herdr is gone"


def test_a_replayed_event_cannot_undo_a_poll():
    # The revision guard drops updates older than what we hold, so the poll has
    # to leave the high-water revision behind it -- a row carrying a lower one
    # would otherwise let stale history win the moment the replay resumed.
    from herdrkeys.herdr_state import HerdrState

    state = HerdrState()
    state.apply_snapshot({"panes": [agent_pane("w1:p1", "working", revision=36)], "focused_pane_id": None})
    state.apply_agents([agent_row("w1:p1", "blocked", revision=0)])
    state.apply_event({"data": {"type": "pane_updated", "pane": agent_pane("w1:p1", "idle", revision=12)}})
    assert state.panes["w1:p1"].state is AgentState.BLOCKED


# -- provisioning --------------------------------------------------------


@pytest.fixture
def board(tmp_path, monkeypatch):
    """A daemon with no keypad attached, and a fake board on a fake drive."""
    drive = tmp_path / "CIRCUITPY"
    drive.mkdir()
    calls = {"copied": [], "reset": []}
    monkeypatch.setattr(daemon_module.provision, "copy_firmware", lambda d: calls["copied"].append(d))
    monkeypatch.setattr(daemon_module.provision, "hard_reset", lambda p: calls["reset"].append(p) or True)
    monkeypatch.setattr(
        daemon_module.discovery, "find_ports",
        lambda: [type("P", (), {"device": "/dev/console", "interface": 0})()],
    )
    return drive, calls


def set_board(monkeypatch, drive, state):
    from herdrkeys.provision import Board

    monkeypatch.setattr(
        daemon_module.provision, "inspect",
        lambda *a, **k: None if state is None else Board(drive=drive, state=state),
    )


def test_a_board_running_our_firmware_is_provisioned(rig, board, monkeypatch):
    from herdrkeys.provision import DriveState

    instance, _device, _focused, _ = rig
    drive, calls = board
    set_board(monkeypatch, drive, DriveState.OURS)

    instance._maybe_provision(0.0)
    assert calls["copied"] == [drive]
    assert calls["reset"] == ["/dev/console"], "boot.py needs a re-enumeration to take effect"


def test_an_empty_board_is_provisioned(rig, board, monkeypatch):
    from herdrkeys.provision import DriveState

    instance, _device, _focused, _ = rig
    drive, calls = board
    set_board(monkeypatch, drive, DriveState.BLANK)
    instance._maybe_provision(0.0)
    assert calls["copied"] == [drive]


def test_somebody_elses_board_is_never_written_to(rig, board, monkeypatch, caplog):
    from herdrkeys.provision import DriveState

    instance, _device, _focused, _ = rig
    drive, calls = board
    set_board(monkeypatch, drive, DriveState.FOREIGN)

    with caplog.at_level("WARNING"):
        instance._maybe_provision(0.0)
    assert calls["copied"] == [] and calls["reset"] == []
    assert "adopt" in caplog.text, "and the user is told how to take it over deliberately"


def test_the_refusal_is_not_repeated_every_retry(rig, board, monkeypatch, caplog):
    from herdrkeys.provision import DriveState

    instance, _device, _focused, _ = rig
    drive, _calls = board
    set_board(monkeypatch, drive, DriveState.FOREIGN)

    with caplog.at_level("WARNING"):
        for tick in range(0, 600, 60):
            instance._provision_backoff.succeeded()
            instance._maybe_provision(float(tick))
    assert caplog.text.count("leaving it alone") == 1, "a reconnect loop must not become a log flood"


def test_provisioning_can_be_switched_off(rig, board, monkeypatch):
    from herdrkeys.provision import DriveState

    instance, _device, _focused, _ = rig
    drive, calls = board
    set_board(monkeypatch, drive, DriveState.OURS)
    instance.config.provision = False
    instance._maybe_provision(0.0)
    assert calls["copied"] == []


def test_no_keybow_attached_does_nothing_at_all(rig, monkeypatch):
    instance, _device, _focused, _ = rig
    monkeypatch.setattr(daemon_module.discovery, "find_ports", lambda: [])
    called = []
    monkeypatch.setattr(daemon_module.provision, "inspect", lambda *a, **k: called.append(True))
    instance._maybe_provision(0.0)
    assert called == [], "no board means nothing to inspect"
