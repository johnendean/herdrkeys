from herdrkeys.model import FN_SLOT
from herdrkeys.slots import SlotMap


def test_assigns_lowest_free_slot():
    slots = SlotMap()
    assert slots.assign("w1:p1") == 0
    assert slots.assign("w1:p2") == 1
    assert slots.assign("w1:p1") == 0  # idempotent


def test_freed_slot_is_reused_but_survivors_do_not_shift():
    slots = SlotMap()
    for pane in ("a", "b", "c"):
        slots.assign(pane)
    slots.release("b")
    assert slots.slot_of("c") == 2, "an existing binding must not move under your fingers"
    assert slots.assign("d") == 1


def test_function_key_is_never_assigned():
    slots = SlotMap()
    assigned = [slots.assign(f"pane{i}") for i in range(20)]
    assert FN_SLOT not in assigned
    assert assigned[-1] is None, "the grid fills up rather than overflowing onto the fn key"
    assert sorted(s for s in assigned if s is not None) == list(range(15))


def test_gc_drops_only_dead_panes():
    slots = SlotMap()
    slots.assign("alive")
    slots.assign("dead")
    slots.gc({"alive"})
    assert slots.slot_of("alive") == 0
    assert slots.slot_of("dead") is None


def test_round_trips_through_disk(tmp_path):
    path = tmp_path / "slots.json"
    slots = SlotMap()
    slots.assign("w1:p1")
    slots.assign("w2:p7")
    slots.save(path)
    assert SlotMap.load(path).as_dict() == slots.as_dict()


def test_unreadable_or_stale_state_falls_back_to_empty(tmp_path):
    missing = tmp_path / "nope.json"
    assert SlotMap.load(missing).as_dict() == {}
    garbage = tmp_path / "garbage.json"
    garbage.write_text("{not json")
    assert SlotMap.load(garbage).as_dict() == {}
    old = tmp_path / "old.json"
    old.write_text('{"version": 0, "slots": {"0": "w1:p1"}}')
    assert SlotMap.load(old).as_dict() == {}
