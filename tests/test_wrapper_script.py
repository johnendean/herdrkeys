"""The wrapper must find a Python new enough to run herdrkeys.

launchd starts the agent with a bare PATH, where `python3` is the Command Line
Tools' 3.9 -- older than `tomllib`, so the daemon died at import and launchd
restarted it every ten seconds. Seen for real on the first `make install-agent`.
"""

import os
import stat
import subprocess
from pathlib import Path

SCRIPT = Path(__file__).parent.parent / "bin" / "herdrkeys"


def python(directory: Path, name: str, *, new_enough: bool) -> None:
    """A stand-in interpreter: answers the version check, then says who it is."""
    path = directory / name
    verdict = 0 if new_enough else 1
    path.write_text(f'#!/bin/sh\nif [ "$1" = "-c" ]; then exit {verdict}; fi\necho "{name}"\n')
    path.chmod(path.stat().st_mode | stat.S_IEXEC | stat.S_IXGRP | stat.S_IXOTH)


def run(tmp_path, *pythons):
    binaries = tmp_path / "bin"
    binaries.mkdir()
    for name, new_enough in pythons:
        python(binaries, name, new_enough=new_enough)
    env = {"PATH": f"{binaries}:/usr/bin:/bin", "HOME": str(tmp_path), "HERDRKEYS_PYTHON_DIRS": ""}
    return subprocess.run([str(SCRIPT), "run"], env=env, capture_output=True, text=True, timeout=10)


def test_it_uses_python3_when_that_is_new_enough(tmp_path):
    result = run(tmp_path, ("python3", True), ("python3.12", True))
    assert result.stdout.strip() == "python3"


def test_it_passes_over_a_python3_too_old_for_herdrkeys(tmp_path):
    result = run(tmp_path, ("python3", False), ("python3.12", True))
    assert result.stdout.strip() == "python3.12"


def test_with_nothing_new_enough_it_says_so(tmp_path):
    result = run(tmp_path, ("python3", False))
    assert result.returncode != 0
    assert "3.11" in result.stderr, "a version too old should be named, not left as an import error"
