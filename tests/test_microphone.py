"""The one microphone, decided on the host for every board (ADR 0017).

A held modifier is the worst thing herdrkeys can leave behind on a machine, so
every way the microphone closes is a test of its own.
"""

from herdrkeys.microphone import LATCH_TAP_SECONDS, LATCH_TIMEOUT, Microphone


class FakeTypist:
    """Records what it was told to hold and type, which is the whole question."""

    def __init__(self):
        self.holding = False
        self.returns = 0

    def hold(self, on: bool) -> None:
        self.holding = on

    def type_return(self) -> None:
        self.returns += 1


def test_holding_the_key_holds_the_hotkey_until_it_comes_up():
    mic, keybow = Microphone(), FakeTypist()

    mic.down(keybow, 0.0)
    assert keybow.holding and mic.state == "open"

    mic.up(keybow, 2.0)
    assert not keybow.holding, "a hold ends when your finger does"
    assert mic.state == "closed"


def test_a_tap_latches_the_microphone_open_and_another_tap_ends_it():
    mic, keybow = Microphone(), FakeTypist()

    mic.down(keybow, 0.0)
    mic.up(keybow, LATCH_TAP_SECONDS / 2)
    assert keybow.holding and mic.state == "latched"

    mic.down(keybow, 10.0)
    assert not keybow.holding and mic.state == "closed"
    mic.up(keybow, 10.1)
    assert not keybow.holding, "the finger that closed it must not reopen it"


def test_a_latch_does_not_outlive_its_timeout():
    mic, keybow = Microphone(), FakeTypist()
    mic.down(keybow, 0.0)
    mic.up(keybow, 0.1)

    mic.tick(0.1 + LATCH_TIMEOUT - 1)
    assert keybow.holding, "still inside the timeout"

    mic.tick(0.1 + LATCH_TIMEOUT + 1)
    assert not keybow.holding and mic.state == "closed"


def test_a_long_hold_is_not_cut_off_by_the_latch_timeout():
    mic, keybow = Microphone(), FakeTypist()
    mic.down(keybow, 0.0)

    mic.tick(LATCH_TIMEOUT + 1)
    assert keybow.holding, "a finger on the key is its own timeout"


def test_return_types_on_the_board_it_was_pressed_on():
    mic, keybow, host = Microphone(), FakeTypist(), FakeTypist()

    mic.press_return(host)
    assert host.returns == 1 and keybow.returns == 0


def test_return_while_the_microphone_is_open_closes_it_and_types_nothing():
    # Flow inserts what it heard some time after it stops listening, so a
    # Return now would send the prompt without it.
    mic, keybow = Microphone(), FakeTypist()
    mic.down(keybow, 0.0)

    mic.press_return(keybow)
    assert not keybow.holding and keybow.returns == 0
    assert mic.state == "closed"

    mic.press_return(keybow)
    assert keybow.returns == 1, "the next press sends"


def test_return_on_one_board_closes_a_microphone_held_by_the_other():
    mic, keybow, host = Microphone(), FakeTypist(), FakeTypist()
    mic.down(keybow, 0.0)
    mic.up(keybow, 0.1)  # latched on the Keybow

    mic.press_return(host)
    assert not keybow.holding, "there is one microphone, whoever holds it"
    assert host.returns == 0 and keybow.returns == 0


def test_only_one_board_ever_holds_the_hotkey():
    mic, keybow, host = Microphone(), FakeTypist(), FakeTypist()
    mic.down(keybow, 0.0)

    mic.down(host, 1.0)
    mic.up(host, 1.1)
    assert keybow.holding and not host.holding, "a second holder is a second thing to forget to let go"
    assert mic.state == "open"

    mic.up(keybow, 3.0)
    assert not keybow.holding and not host.holding


def test_losing_the_board_that_holds_it_closes_the_microphone():
    mic, keybow, host = Microphone(), FakeTypist(), FakeTypist()
    mic.down(host, 0.0)
    mic.up(host, 0.1)

    mic.lost(keybow)
    assert host.holding, "a board that held nothing has nothing to give back"

    mic.lost(host)
    assert not host.holding and mic.state == "closed"
