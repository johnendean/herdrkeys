"""Keep every test out of the real state directory.

The daemon deletes the slot map an older version left behind (ADR 0009), and it
does that on the way into `run_forever` -- so the two tests that drive the loop
reach outside the checkout and delete the file belonging to whoever is running
the tests. Found by running them on a live machine and watching a real
`~/.local/state/herdrkeys/slots.json` disappear.

Redirecting the variable per test would fix those two and leave the next one to
be found the same way, so it is done here, for all of them, once.
"""

import pytest


@pytest.fixture(autouse=True)
def isolated_state_dir(tmp_path, monkeypatch):
    monkeypatch.setenv("XDG_STATE_HOME", str(tmp_path / "state"))


@pytest.fixture(autouse=True)
def no_real_keystrokes(monkeypatch):
    """The same lesson for typing: a test must never press a key on this machine.

    The daemon types for a board that cannot (ADR 0017), and on a machine that
    has granted the permission a stray real `HostTypist` would hold down Right
    Option for real. Every test hands the daemon a fake; this makes forgetting
    to a failure rather than a keystroke.
    """
    from herdrkeys import macos

    def refuse(self, *args, **kwargs):
        raise AssertionError("a test tried to post a real keystroke")

    monkeypatch.setattr(macos.HostTypist, "_post", refuse)
