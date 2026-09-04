"""The startup hook must not bind the daemon to whatever server ran it.

There is one keypad, so there is one daemon. When Herdr invokes the startup
hook it exports HERDR_SOCKET_PATH for its own server, and inheriting that meant
a named or throwaway session could capture the keypad simply by starting first,
leaving it showing a session nobody was looking at. Seen for real: a headless
test server started the daemon and it bound to that session's socket.
"""

import os
import stat
import subprocess
import time
from pathlib import Path

import pytest

SCRIPT = Path(__file__).parent.parent / "bin" / "herdrkeys-start"


def stub(directory: Path, name: str, body: str) -> None:
    path = directory / name
    path.write_text(body)
    path.chmod(path.stat().st_mode | stat.S_IEXEC | stat.S_IXGRP | stat.S_IXOTH)


@pytest.fixture
def fake_env(tmp_path):
    """A PATH where python3 records its environment and no daemon is running."""
    binaries = tmp_path / "bin"
    binaries.mkdir()
    record = tmp_path / "invocation"
    stub(binaries, "python3", f'#!/bin/sh\necho "SOCKET=[${{HERDR_SOCKET_PATH-}}] SESSION=[${{HERDR_SESSION-}}]" > {record}\n')
    stub(binaries, "pgrep", "#!/bin/sh\nexit 1\n")  # nothing already running

    env = dict(os.environ)
    env["PATH"] = f"{binaries}:{env['PATH']}"
    env["XDG_STATE_HOME"] = str(tmp_path / "state")
    return env, record


def run_start(env, record, timeout=10.0):
    subprocess.run([str(SCRIPT)], env=env, capture_output=True, text=True, timeout=timeout, check=True)
    deadline = time.monotonic() + 5
    while time.monotonic() < deadline:  # the daemon is launched detached
        if record.is_file():
            return record.read_text().strip()
        time.sleep(0.05)
    raise AssertionError("the start script never launched anything")


def test_the_launching_sessions_socket_is_not_inherited(fake_env):
    env, record = fake_env
    env["HERDR_SOCKET_PATH"] = "/somewhere/sessions/throwaway/herdr.sock"
    env["HERDR_SESSION"] = "throwaway"
    assert run_start(env, record) == "SOCKET=[] SESSION=[]"


def test_a_plain_launch_is_unaffected(fake_env):
    env, record = fake_env
    env.pop("HERDR_SOCKET_PATH", None)
    env.pop("HERDR_SESSION", None)
    assert run_start(env, record) == "SOCKET=[] SESSION=[]"


def test_it_refuses_to_start_a_second_daemon(tmp_path):
    binaries = tmp_path / "bin"
    binaries.mkdir()
    stub(binaries, "python3", "#!/bin/sh\nexit 0\n")
    stub(binaries, "pgrep", "#!/bin/sh\nexit 0\n")  # one is already running
    env = dict(os.environ)
    env["PATH"] = f"{binaries}:{env['PATH']}"
    env["XDG_STATE_HOME"] = str(tmp_path / "state")

    result = subprocess.run([str(SCRIPT)], env=env, capture_output=True, text=True, timeout=10)
    assert result.returncode == 0, "install must not fail because it is already running"
    assert "already running" in result.stdout


def test_it_fails_loudly_without_python(tmp_path):
    binaries = tmp_path / "bin"
    binaries.mkdir()
    stub(binaries, "pgrep", "#!/bin/sh\nexit 1\n")
    env = dict(os.environ)
    env["PATH"] = str(binaries)  # nothing else, so no python3 anywhere
    env["XDG_STATE_HOME"] = str(tmp_path / "state")

    result = subprocess.run([str(SCRIPT)], env=env, capture_output=True, text=True, timeout=10)
    assert result.returncode != 0, "a missing interpreter should fail the install, not pass quietly"
    assert "python3" in result.stderr
