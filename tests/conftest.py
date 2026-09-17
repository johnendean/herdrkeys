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
