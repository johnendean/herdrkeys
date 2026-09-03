import pytest

from herdrkeys import daemon as daemon_module
from herdrkeys.config import Config
from herdrkeys.daemon import Backoff, Daemon
from herdrkeys.device import FakeDevice
from herdrkeys.model import FN_SLOT, AgentState


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
    # Replayed backlog can carry the same revision as the snapshot taken next to
    # it, so history folded afterwards silently wins -- and the replay is rate
    # limited, ~94 events over four seconds on a real session, so a fixed delay
    # would race it. Observed live: two agents that were idle and working both
    # rendered a state they had been in minutes earlier.
    instance, _device, _focused, _ = rig
    snapshots = []

    class Stream:
        def __init__(self, path, **kwargs):
            self.batches = [
                [{"data": {"type": "pane_updated", "pane": agent_pane("w1:p1", "working", revision=36)}}],
                [],
                [],
            ]

        def fileno(self):
            return 0

        def read_events(self):
            return self.batches.pop(0) if self.batches else []

        def close(self):
            pass

    def snapshot(path):
        snapshots.append(True)
        return {"panes": [agent_pane("w1:p1", "idle", revision=36)], "focused_pane_id": None}

    monkeypatch.setattr(daemon_module.herdr, "EventStream", Stream)
    monkeypatch.setattr(daemon_module.herdr, "snapshot", snapshot)

    instance._connect_herdr(100.0)
    assert len(snapshots) == 1 and instance._backlog_settling

    instance._pump_herdr(100.1)  # replayed history overwrites the truth
    assert instance.state.panes["w1:p1"].state is AgentState.WORKING
    assert instance._backlog_settling, "still arriving; do not reconcile yet"

    instance._pump_herdr(100.5)  # quiet, but not long enough
    assert instance._backlog_settling

    instance._pump_herdr(101.0)  # quiet for longer than BACKLOG_QUIET_SECONDS
    assert not instance._backlog_settling
    assert len(snapshots) == 2
    assert instance.state.panes["w1:p1"].state is AgentState.IDLE, "truth wins in the end"


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
