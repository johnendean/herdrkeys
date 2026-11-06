"""The guards in conftest.py must fail a test, not merely protect the machine.

A guard that raises inside a worker thread is caught there: the test passes and
nobody learns it needed a fake (issue #29). So each guard is checked from the
outside, by running a test that trips it under this same conftest.
"""

from pathlib import Path

pytest_plugins = ["pytester"]

CONFTEST = (Path(__file__).parent / "conftest.py").read_text()


def test_a_test_that_lets_the_real_poller_ask_for_usage_fails(pytester):
    pytester.makeconftest(CONFTEST)
    pytester.makepyfile(
        """
        from herdrkeys import usage

        def test_forgets_its_fake():
            poller = usage.Poller()  # the real ask, on a real thread
            poller.tick(0.0, wanted=True)
        """
    )
    result = pytester.runpytest_inprocess("-p", "no:cacheprovider")
    result.assert_outcomes(errors=1, passed=1)
    result.stdout.fnmatch_lines(["*a test tried to ask Anthropic for usage*"])


def test_a_test_with_its_own_fake_is_left_alone(pytester):
    pytester.makeconftest(CONFTEST)
    pytester.makepyfile(
        """
        from herdrkeys import usage

        def test_brings_its_own():
            poller = usage.Poller(ask=lambda: usage.Usage(None, None), spawn=lambda work: work())
            poller.tick(0.0, wanted=True)
            assert poller.reading == usage.Usage(None, None)
        """
    )
    pytester.runpytest_inprocess("-p", "no:cacheprovider").assert_outcomes(passed=1)
