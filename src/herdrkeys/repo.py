"""Turning the git remote of a pane's directory into a page you can open.

Two halves, deliberately separated. `web_url` is pure string work and holds
every decision worth arguing about, so it is tested exhaustively and needs no
git, no network and no repository. `remote_url` and `open_url` are the I/O, and
are as thin as they can be.

The translation is host-agnostic on purpose. GitHub, GitLab, Bitbucket,
Codeberg and most self-hosted forges all serve a repository at the same path
the remote names, so special-casing one host buys nothing and means the key
silently does nothing the first time it meets another.
"""

from __future__ import annotations

import os
import re
import subprocess
from pathlib import Path

# git@host:owner/repo -- the scp-like syntax, which is not a URL and so cannot
# be parsed as one. The path is everything after the colon.
_SCP_LIKE = re.compile(r"^(?:(?P<user>[^@/]+)@)?(?P<host>[^:/@]+):(?P<path>[^/].*)$")

# Schemes that name a host we can serve a page from. `file://` deliberately
# absent: a local clone has no web page.
_WEB_SCHEMES = ("https", "http", "ssh", "git")

_SCHEME_URL = re.compile(
    r"^(?P<scheme>[a-z][a-z0-9+.-]*)://"
    r"(?:(?P<userinfo>[^@/]*)@)?"
    r"(?P<host>[^/:]+)"
    r"(?::(?P<port>\d+))?"
    r"(?P<path>/.*)?$",
    re.IGNORECASE,
)


def web_url(remote: str | None) -> str | None:
    """The web page for a git remote, or None if it does not name one.

    None is a real answer, not a failure: a local-path remote, or no remote at
    all, means there is nothing to open, and the caller says so with a flash.
    """
    if not remote:
        return None
    remote = remote.strip()
    if not remote:
        return None

    match = _SCHEME_URL.match(remote)
    if match:
        if match.group("scheme").lower() not in _WEB_SCHEMES:
            return None  # file://, and anything else that is not a web host
        host, path = match.group("host"), match.group("path") or ""
        # The port is dropped rather than carried over. An ssh remote on 2222
        # says nothing about which port serves HTTP, and guessing wrong is
        # worse than landing on the default.
    else:
        match = _SCP_LIKE.match(remote)
        if not match:
            return None  # a bare local path, or something we do not recognise
        host, path = match.group("host"), "/" + match.group("path")

    # Credentials in a remote are common and must never reach a browser: a
    # personal access token in a URL would land in history, and in whatever
    # the browser syncs. The userinfo group is captured only to be discarded.
    path = _tidy_path(path)
    if not host or not path:
        return None
    return f"https://{host}{path}"


def _tidy_path(path: str) -> str:
    path = path.rstrip("/")
    if path.endswith(".git"):
        path = path[: -len(".git")]
    return path.rstrip("/")


# -- reading the remote without running git --------------------------------
#
# `git remote get-url` costs 14ms; reading `.git/config` costs 0.02ms. That
# ratio is what decides where each is used. The key's colour is recomputed on
# every frame, so it reads the file; a key press can afford the subprocess.


def _git_dir(cwd: str) -> Path | None:
    """The `.git` directory governing a path, or None outside a checkout."""
    try:
        start = Path(cwd).resolve()
    except (OSError, ValueError):
        return None
    for directory in (start, *start.parents):
        candidate = directory / ".git"
        try:
            if candidate.is_dir():
                return candidate
            if candidate.is_file():
                # A worktree or a submodule: `.git` is a file naming the real
                # directory. Both are ordinary here -- Herdr creates worktrees
                # itself -- so neither can be treated as "not a repository".
                return _gitdir_from_file(candidate)
        except OSError:
            return None
    return None


def _gitdir_from_file(path: Path) -> Path | None:
    try:
        text = path.read_text()
    except OSError:
        return None
    for line in text.splitlines():
        line = line.strip()
        if line.startswith("gitdir:"):
            target = line.split(":", 1)[1].strip()
            if not target:
                return None
            return Path(target) if os.path.isabs(target) else (path.parent / target)
    return None


def _config_path(git_dir: Path) -> Path:
    """Where a git directory keeps its config.

    A worktree's own directory holds no config: it points at the repository
    they share through `commondir`, and that is where the remotes live.
    """
    commondir = git_dir / "commondir"
    try:
        if commondir.is_file():
            target = commondir.read_text().strip()
            if target:
                common = Path(target) if os.path.isabs(target) else (git_dir / target)
                return common / "config"
    except OSError:
        pass
    return git_dir / "config"


def _remote_in(text: str, remote: str) -> str | None:
    """Pull one remote's url out of a git config file.

    Hand-rolled rather than `configparser`, which mis-reads this format twice
    over: git indents its keys with tabs, which configparser treats as line
    continuations, and a `%` in a URL trips its interpolation.
    """
    wanted = f'remote "{remote}"'
    section = None
    for line in text.splitlines():
        line = line.strip()
        if not line or line[0] in "#;":
            continue
        if line.startswith("[") and line.endswith("]"):
            section = line[1:-1].strip()
            continue
        if section != wanted or "=" not in line:
            continue
        key, _, value = line.partition("=")
        if key.strip().lower() == "url":
            return value.strip() or None
    return None


def remote_from_config(cwd: str | None, *, remote: str = "origin") -> str | None:
    """A directory's remote, read from `.git/config`. No subprocess.

    Understands plain configuration and nothing more: git's `include.path` and
    `url.<base>.insteadOf` are not followed. That is a deliberate floor, not an
    oversight -- see `page_for`, which asks git itself before concluding there
    is nothing here.
    """
    if not cwd:
        return None
    git_dir = _git_dir(cwd)
    if git_dir is None:
        return None
    try:
        text = _config_path(git_dir).read_text()
    except OSError:
        return None
    return _remote_in(text, remote)


def has_page(cwd: str | None, *, remote: str = "origin") -> bool:
    """Whether this directory looks like it has a page, cheaply enough to ask
    on every frame.

    Answers from the config file alone. It can therefore say no where a press
    would find something, and the key under-promises rather than over-promises:
    a dark key that turns out to work is a much smaller betrayal than a lit key
    that does nothing.
    """
    return web_url(remote_from_config(cwd, remote=remote)) is not None


def remote_url(cwd: str, *, remote: str = "origin", timeout: float = 5.0) -> str | None:
    """The URL of a directory's remote, or None if there isn't one.

    Every failure is the same answer -- not a repository, no such remote, git
    missing, git hanging -- because the key does the same thing in all of them.
    """
    try:
        result = subprocess.run(
            ["git", "-C", cwd, "remote", "get-url", remote],
            capture_output=True,
            text=True,
            timeout=timeout,
        )
    except (OSError, subprocess.SubprocessError):
        return None
    if result.returncode != 0:
        return None
    return result.stdout.strip() or None


def page_for(cwd: str | None, *, remote: str = "origin", timeout: float = 5.0) -> str | None:
    """The page to open for a directory, or None if there is not one.

    The config file first, because it is free and is what the key's colour was
    decided from, so the two agree in every ordinary case. git itself only when
    that finds nothing: it understands includes and `insteadOf` rewrites that
    the parser does not, and 14ms is nothing on a key press. Pressing must
    never be less capable than it was before the key had a colour.
    """
    if not cwd:
        return None
    found = remote_from_config(cwd, remote=remote)
    if found is None:
        found = remote_url(cwd, remote=remote, timeout=timeout)
    return web_url(found)


def open_url(url: str, *, timeout: float = 5.0) -> None:
    """Open a URL in the default browser. Best effort, like raising the terminal."""
    try:
        subprocess.run(["open", url], capture_output=True, timeout=timeout)
    except (OSError, subprocess.SubprocessError):
        pass
