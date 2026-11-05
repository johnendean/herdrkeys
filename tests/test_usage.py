import io
import json
import subprocess
import urllib.error
from datetime import datetime, timezone

import pytest

from herdrkeys import usage
from herdrkeys.usage import CredentialRefused, Limit, Poller, RateLimited, Usage, UsageError

NOW = datetime(2026, 11, 5, 17, 0, tzinfo=timezone.utc).timestamp()
HOUR = 3600.0

# Trimmed from a real response: the endpoint returns many more fields, and
# every one of them but these two is ignored.
RESPONSE = {
    "five_hour": {"utilization": 7.0, "resets_at": "2026-11-05T21:50:00.409228+00:00"},
    "seven_day": {"utilization": 9.0, "resets_at": "2026-11-06T13:00:00.409249+00:00"},
    "seven_day_opus": None,
    "extra_usage": {"is_enabled": False},
}


# -- what a response says ----------------------------------------------------


def test_a_response_gives_the_session_and_weekly_limits():
    read = usage.parse(RESPONSE)
    assert read.session.percent == 7.0
    assert read.weekly.percent == 9.0
    assert read.session.resets_at == datetime(2026, 11, 5, 21, 50, 0, 409228, tzinfo=timezone.utc).timestamp()


def test_no_session_in_progress_is_a_real_answer_not_unknown():
    read = usage.parse({**RESPONSE, "five_hour": None})
    assert read.session is None
    read = usage.parse({**RESPONSE, "five_hour": {"utilization": 0, "resets_at": None}})
    assert read.session == Limit(0.0, None)


@pytest.mark.parametrize(
    "payload",
    [
        None,
        [],
        {},
        {"five_hour": None},
        {**RESPONSE, "five_hour": "lots"},
        {**RESPONSE, "five_hour": {"utilization": "7"}},
        {**RESPONSE, "five_hour": {"utilization": True}},
        {**RESPONSE, "seven_day": {"utilization": 9, "resets_at": "soon"}},
        {**RESPONSE, "seven_day": {"utilization": 9, "resets_at": "2026-11-06T13:00:00"}},
    ],
)
def test_any_other_shape_is_usage_unknown(payload):
    with pytest.raises(UsageError):
        usage.parse(payload)


# -- reading it at a moment ---------------------------------------------------


def test_a_window_that_has_ended_has_reset_without_waiting_to_be_told():
    spent = Usage(Limit(100.0, NOW + 60), Limit(40.0, NOW + 3 * 24 * HOUR))
    assert spent.limited(NOW)
    after = spent.at(NOW + 61)
    assert after.session == Limit(0.0, None)
    assert not spent.limited(NOW + 61)


def test_the_countdown_is_to_the_session_reset_while_nothing_is_spent():
    read = Usage(Limit(30.0, NOW + 2 * HOUR), Limit(50.0, NOW + 3 * 24 * HOUR))
    assert read.free_at(NOW) == NOW + 2 * HOUR


def test_a_spent_weekly_limit_counts_down_to_the_weekly_reset():
    read = Usage(Limit(30.0, NOW + 2 * HOUR), Limit(100.0, NOW + 3 * 24 * HOUR))
    assert read.limited(NOW)
    assert read.free_at(NOW) == NOW + 3 * 24 * HOUR


def test_with_no_session_and_nothing_spent_nothing_counts_down():
    assert Usage(None, Limit(50.0, NOW + HOUR)).free_at(NOW) is None
    assert Usage(Limit(5.0, NOW - 1), Limit(50.0, NOW + HOUR)).free_at(NOW) is None


# -- the credential ----------------------------------------------------------


def credential(token="tok", expires_ms=None):
    oauth = {"accessToken": token}
    if expires_ms is not None:
        oauth["expiresAt"] = expires_ms
    return json.dumps({"claudeAiOauth": oauth})


def runner(returncode=0, stdout=""):
    calls = []

    def run(argv, **kwargs):
        calls.append(argv)
        return subprocess.CompletedProcess(argv, returncode, stdout, "")

    run.calls = calls
    return run


def test_the_token_is_read_from_claude_codes_keychain_item():
    run = runner(stdout=credential("abc", expires_ms=(NOW + HOUR) * 1000))
    assert usage.read_token(run=run, wall=lambda: NOW) == "abc"
    assert run.calls == [["security", "find-generic-password", "-s", "Claude Code-credentials", "-w"]]


def test_a_missing_or_refused_credential_is_not_asked_for_again():
    with pytest.raises(CredentialRefused, match="sign in"):
        usage.read_token(run=runner(returncode=44), wall=lambda: NOW)
    with pytest.raises(CredentialRefused):
        usage.read_token(run=runner(returncode=128), wall=lambda: NOW)
    with pytest.raises(CredentialRefused):
        usage.read_token(run=runner(stdout="not json"), wall=lambda: NOW)


def test_an_expired_token_is_unknown_for_now_not_refused():
    with pytest.raises(UsageError) as caught:
        usage.read_token(run=runner(stdout=credential(expires_ms=(NOW - 1) * 1000)), wall=lambda: NOW)
    assert not isinstance(caught.value, CredentialRefused)


# -- asking Anthropic ---------------------------------------------------------


def test_the_request_carries_the_token_and_the_answer_is_parsed():
    seen = []

    def opener(request, timeout):
        seen.append(request)
        return io.BytesIO(json.dumps(RESPONSE).encode())

    assert usage.parse(usage.fetch("abc", opener=opener)).session.percent == 7.0
    assert seen[0].full_url == usage.URL
    assert seen[0].get_header("Authorization") == "Bearer abc"


def failing(error):
    def opener(request, timeout):
        raise error

    return opener


def http_error(code):
    return urllib.error.HTTPError(usage.URL, code, "no", {}, io.BytesIO(b""))


def test_a_429_is_rate_limited_and_other_failures_are_unknown():
    with pytest.raises(RateLimited):
        usage.fetch("abc", opener=failing(http_error(429)))
    for error in (http_error(401), http_error(500), urllib.error.URLError("offline"), TimeoutError()):
        with pytest.raises(UsageError) as caught:
            usage.fetch("abc", opener=failing(error))
        assert not isinstance(caught.value, (RateLimited, CredentialRefused))
    with pytest.raises(UsageError):
        usage.fetch("abc", opener=lambda request, timeout: io.BytesIO(b"<html>"))


# -- the poller ---------------------------------------------------------------


class Asker:
    def __init__(self, *answers):
        self.answers = list(answers)
        self.asked = 0

    def __call__(self):
        self.asked += 1
        answer = self.answers.pop(0) if len(self.answers) > 1 else self.answers[0]
        if isinstance(answer, Exception):
            raise answer
        return answer


READING = Usage(Limit(7.0, NOW + HOUR), Limit(9.0, NOW + 24 * HOUR))


def inline(work):
    work()


def test_nothing_is_asked_while_nothing_wants_it():
    ask = Asker(READING)
    poller = Poller(ask=ask, spawn=inline)
    poller.tick(0.0, wanted=False)
    assert ask.asked == 0 and poller.reading is None


def test_it_asks_at_most_once_a_minute():
    ask = Asker(READING)
    poller = Poller(ask=ask, spawn=inline)
    poller.tick(0.0, wanted=True)
    assert poller.reading == READING and poller.read_at == 0.0
    poller.tick(59.0, wanted=True)
    assert ask.asked == 1
    poller.tick(60.0, wanted=True)
    assert ask.asked == 2


def test_one_request_at_a_time_and_the_answer_is_collected_on_a_later_tick():
    started = []
    ask = Asker(READING)
    poller = Poller(ask=ask, spawn=started.append)
    poller.tick(0.0, wanted=True)
    poller.tick(120.0, wanted=True)
    assert len(started) == 1, "a second request while one is out"
    started[0]()
    assert poller.reading is None
    poller.tick(121.0, wanted=True)
    assert poller.reading == READING and poller.read_at == 121.0


def test_a_failure_keeps_the_last_reading_and_it_goes_stale_after_five_minutes():
    ask = Asker(READING, UsageError("offline"))
    poller = Poller(ask=ask, spawn=inline)
    poller.tick(0.0, wanted=True)
    for minute in range(1, 6):
        poller.tick(60.0 * minute, wanted=True)
    assert poller.reading == READING
    assert not poller.stale(300.0)
    assert poller.stale(301.0)


def test_a_refused_credential_stops_the_asking_for_the_rest_of_the_run():
    ask = Asker(CredentialRefused("no Claude Code credential"))
    poller = Poller(ask=ask, spawn=inline)
    poller.tick(0.0, wanted=True)
    poller.tick(600.0, wanted=True)
    assert ask.asked == 1
    assert "no Claude Code credential" in poller.refused


def test_a_429_backs_the_polling_off():
    ask = Asker(RateLimited("429"), RateLimited("429"), READING)
    poller = Poller(ask=ask, spawn=inline)
    poller.tick(0.0, wanted=True)
    poller.tick(60.0, wanted=True)
    assert ask.asked == 1, "should wait two minutes after a 429"
    poller.tick(120.0, wanted=True)
    assert ask.asked == 2
    poller.tick(300.0, wanted=True)
    assert ask.asked == 2, "should wait four minutes after a second 429"
    poller.tick(360.0, wanted=True)
    assert ask.asked == 3 and poller.reading == READING


def test_an_unexpected_exception_in_the_worker_is_usage_unknown_not_a_crash():
    poller = Poller(ask=Asker(KeyError("x")), spawn=inline)
    poller.tick(0.0, wanted=True)
    assert poller.reading is None and poller.last_error
