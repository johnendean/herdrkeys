"""How much of the Claude plan's rate limits has gone, for the CN001's strip.

Asked of Anthropic, not Herdr: the same endpoint Claude Code's own `/usage`
calls, with Claude Code's own OAuth token, read from the Keychain and never
written back (ADR 0019). The endpoint is undocumented, so anything it says
that does not parse is "usage unknown", never a failure of the daemon.

Parsing is pure; `read_token` and `fetch` are the only parts that reach out,
and `Poller` is what the daemon holds: it asks off the loop's thread, at most
once a minute, and only while it is told something wants the answer.
"""

from __future__ import annotations

import json
import logging
import math
import subprocess
import threading
import time
import urllib.error
import urllib.request
from concurrent.futures import Future
from datetime import datetime
from typing import NamedTuple

log = logging.getLogger("herdrkeys")

URL = "https://api.anthropic.com/api/oauth/usage"
KEYCHAIN_SERVICE = "Claude Code-credentials"
REQUEST_TIMEOUT = 10.0

POLL_SECONDS = 60.0
MAX_POLL_SECONDS = 15 * 60.0  # how far a 429 backs the polling off
STALE_SECONDS = 5 * 60.0


class Limit(NamedTuple):
    """One rate limit: the share of its window spent, and when the window ends."""

    percent: float
    resets_at: float | None  # seconds since the epoch; None with no window running

    @property
    def spent(self) -> bool:
        return self.percent >= 100

    def at(self, wall: float) -> Limit:
        """This limit as it stands at `wall`: a window that has ended has reset."""
        if self.resets_at is not None and wall >= self.resets_at:
            return Limit(0.0, None)
        return self


class Usage(NamedTuple):
    session: Limit | None  # the 5-hour limit
    weekly: Limit | None

    def at(self, wall: float) -> Usage:
        return Usage(
            self.session.at(wall) if self.session else None,
            self.weekly.at(wall) if self.weekly else None,
        )

    def free_at(self, wall: float) -> float | None:
        """When work can next start, if a limit is spent; else when the session resets.

        None when nothing is counting down: no session in progress and nothing
        spent.
        """
        now = self.at(wall)
        spent = [limit.resets_at for limit in (now.session, now.weekly) if limit and limit.spent and limit.resets_at]
        if spent:
            return max(spent)
        return now.session.resets_at if now.session else None

    def limited(self, wall: float) -> bool:
        now = self.at(wall)
        return any(limit and limit.spent for limit in (now.session, now.weekly))


class UsageError(Exception):
    """Usage is unknown this time. Asking again later may work."""


class CredentialRefused(UsageError):
    """No credential to be had. Asking again would only raise the Keychain dialog again."""


class RateLimited(UsageError):
    pass


def _limit(value: object, name: str) -> Limit | None:
    if value is None:
        return None
    if not isinstance(value, dict):
        raise UsageError(f"unexpected {name}: {type(value).__name__}")
    percent = value.get("utilization")
    # bool is an int, and True is not a percentage.
    if isinstance(percent, bool) or not isinstance(percent, (int, float)):
        raise UsageError(f"unexpected {name} utilization: {percent!r}")
    # JSON's NaN and Infinity, and numbers too big for a float, all parse; none
    # is a percentage, and drawing one would crash the strip (issue #27).
    try:
        percent = float(percent)
    except OverflowError:
        percent = math.inf
    if not math.isfinite(percent):
        raise UsageError(f"unexpected {name} utilization: not a finite number")
    resets = value.get("resets_at")
    if resets is None:
        return Limit(percent, None)
    if not isinstance(resets, str):
        raise UsageError(f"unexpected {name} resets_at: {resets!r}")
    try:
        moment = datetime.fromisoformat(resets)
    except ValueError as exc:
        raise UsageError(f"unexpected {name} resets_at: {resets!r}") from exc
    if moment.tzinfo is None:
        raise UsageError(f"{name} resets_at has no timezone: {resets!r}")
    return Limit(percent, moment.timestamp())


def parse(payload: object) -> Usage:
    """The usage a response describes. Raises UsageError for any other shape."""
    if not isinstance(payload, dict) or "five_hour" not in payload or "seven_day" not in payload:
        raise UsageError("unexpected response: no five_hour or seven_day")
    return Usage(_limit(payload["five_hour"], "five_hour"), _limit(payload["seven_day"], "seven_day"))


def token_from(credential: str, wall: float) -> str:
    """The access token in Claude Code's Keychain item, if it is still good."""
    try:
        oauth = json.loads(credential)["claudeAiOauth"]
        token = oauth["accessToken"]
    except (ValueError, KeyError, TypeError) as exc:
        raise CredentialRefused("Claude Code's Keychain item has no OAuth token") from exc
    if not isinstance(token, str) or not token:
        raise CredentialRefused("Claude Code's Keychain item has no OAuth token")
    expires = oauth.get("expiresAt")
    if isinstance(expires, (int, float)) and not isinstance(expires, bool) and expires / 1000 <= wall:
        raise UsageError("Claude Code's token has expired; it refreshes it when it next runs")
    return token


def read_token(*, run=subprocess.run, wall=time.time) -> str:
    """Claude Code's OAuth access token, out of the Keychain.

    `security` may put up macOS's dialog asking whether it may; that dialog is
    the consent (ADR 0019). Whatever stops it -- no item, a refusal, a
    cancelled dialog -- is a CredentialRefused, so it is not asked again.
    """
    try:
        result = run(
            ["security", "find-generic-password", "-s", KEYCHAIN_SERVICE, "-w"],
            capture_output=True,
            text=True,
        )
    except OSError as exc:
        raise CredentialRefused(f"cannot run security: {exc}") from exc
    if result.returncode == 44:
        raise CredentialRefused("no Claude Code credential in the Keychain; sign in to Claude Code")
    if result.returncode != 0:
        raise CredentialRefused(f"the Keychain would not give up Claude Code's credential (security exited {result.returncode})")
    return token_from(result.stdout, wall())


def fetch(token: str, *, opener=urllib.request.urlopen) -> object:
    request = urllib.request.Request(
        URL,
        headers={
            "Authorization": f"Bearer {token}",
            "anthropic-beta": "oauth-2025-04-20",
            "User-Agent": "herdrkeys",
        },
    )
    try:
        with opener(request, timeout=REQUEST_TIMEOUT) as response:
            return json.load(response)
    except urllib.error.HTTPError as exc:
        if exc.code == 429:
            raise RateLimited("asked too often (HTTP 429)") from exc
        if exc.code in (401, 403):
            raise UsageError(f"token not accepted (HTTP {exc.code}); Claude Code refreshes it when it next runs") from exc
        raise UsageError(f"HTTP {exc.code}") from exc
    except (urllib.error.URLError, TimeoutError, OSError) as exc:
        raise UsageError(f"cannot reach Anthropic: {exc}") from exc
    except ValueError as exc:
        raise UsageError("response was not JSON") from exc


def current() -> Usage:
    """Usage as Anthropic has it now. Blocks; raises UsageError."""
    return parse(fetch(read_token()))


def _in_background(work) -> None:
    threading.Thread(target=work, name="usage", daemon=True).start()


class Poller:
    """The last usage known, kept fresh while something wants it.

    Asks at most once every `POLL_SECONDS`, one request at a time, off the
    caller's thread; `tick` collects the answer. Nothing is asked while nothing
    wants it, and nothing at all once the credential has been refused.
    """

    def __init__(self, *, ask=None, spawn=_in_background) -> None:
        self._ask = ask or (lambda: current())  # looked up when asked, so a test can stand in for it
        self._spawn = spawn
        self.reading: Usage | None = None
        self.read_at: float | None = None
        self.refused: str | None = None  # why, once the credential is out of reach
        self.last_error: str | None = None
        self._interval = POLL_SECONDS
        self._next_ask = 0.0
        self._asking: Future | None = None
        self._asked_at = 0.0  # when the request out now, or the last one, started

    def stale(self, now: float) -> bool:
        return self.read_at is not None and now - self.read_at > STALE_SECONDS

    def tick(self, now: float, *, wanted: bool) -> None:
        self._collect(now)
        if not wanted or self.refused or self._asking is not None or now < self._next_ask:
            return
        asking: Future = Future()
        self._asking = asking
        self._asked_at = now
        self._next_ask = now + self._interval

        def work() -> None:
            try:
                asking.set_result(self._ask())
            except Exception as exc:  # a thread that dies silently would never ask again
                asking.set_exception(exc)

        try:
            self._spawn(work)
        except RuntimeError as exc:
            self._asking = None
            log.warning("usage: could not start asking: %s", exc)
            return
        self._collect(now)

    def _collect(self, now: float) -> None:
        asking = self._asking
        if asking is None or not asking.done():
            return
        self._asking = None
        error = asking.exception()
        if error is None:
            self.reading, self.read_at = asking.result(), now
            # Back to once a minute from when this request started -- not at
            # the backed-off deadline set when it did, which after a run of
            # 429s left a good reading to go stale before the next (#28).
            self._interval = POLL_SECONDS
            self._next_ask = self._asked_at + POLL_SECONDS
            if self.last_error is not None:
                log.info("usage: known again")
            self.last_error = None
            return
        reason = str(error) if isinstance(error, UsageError) else f"{type(error).__name__}: {error}"
        if isinstance(error, CredentialRefused):
            self.refused = reason
            log.warning("usage: %s -- the strip stays as it is until the daemon restarts", reason)
            return
        if isinstance(error, RateLimited):
            self._interval = min(MAX_POLL_SECONDS, self._interval * 2)
            self._next_ask = now + self._interval
        if reason != self.last_error:
            self.last_error = reason
            log.warning("usage unknown: %s", reason)
