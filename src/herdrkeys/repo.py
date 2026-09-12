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

import re
import subprocess

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
    """The page to open for a directory, or None if there is not one."""
    if not cwd:
        return None
    return web_url(remote_url(cwd, remote=remote, timeout=timeout))


def open_url(url: str, *, timeout: float = 5.0) -> None:
    """Open a URL in the default browser. Best effort, like raising the terminal."""
    try:
        subprocess.run(["open", url], capture_output=True, timeout=timeout)
    except (OSError, subprocess.SubprocessError):
        pass
