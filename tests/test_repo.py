"""Turning a git remote into a page.

`web_url` is where every decision worth arguing about lives, and it is pure
string work, so it is tested exhaustively here rather than through a daemon.
"""

import pytest

from herdrkeys.repo import page_for, web_url


@pytest.mark.parametrize(
    "remote, expected",
    [
        # The two shapes GitHub itself hands you.
        ("git@github.com:johnendean/herdrkeys.git", "https://github.com/johnendean/herdrkeys"),
        ("https://github.com/johnendean/herdrkeys.git", "https://github.com/johnendean/herdrkeys"),
        # ...and the same repo without the suffix, which is equally valid.
        ("https://github.com/johnendean/herdrkeys", "https://github.com/johnendean/herdrkeys"),
        ("git@github.com:johnendean/herdrkeys", "https://github.com/johnendean/herdrkeys"),
    ],
)
def test_the_shapes_github_hands_you(remote, expected):
    assert web_url(remote) == expected


@pytest.mark.parametrize(
    "remote, expected",
    [
        ("git@gitlab.com:group/sub/project.git", "https://gitlab.com/group/sub/project"),
        ("git@bitbucket.org:team/repo.git", "https://bitbucket.org/team/repo"),
        ("https://codeberg.org/user/repo.git", "https://codeberg.org/user/repo"),
        ("git@git.example.internal:infra/tools.git", "https://git.example.internal/infra/tools"),
    ],
)
def test_any_host_that_serves_the_path_it_names(remote, expected):
    # Not a GitHub feature. The rewrite is host-agnostic, so a self-hosted
    # forge works without anybody adding it to a list.
    assert web_url(remote) == expected


@pytest.mark.parametrize(
    "remote",
    [
        "ssh://git@github.com/owner/repo.git",
        "git://github.com/owner/repo.git",
    ],
)
def test_every_scheme_that_names_a_host_resolves(remote):
    assert web_url(remote) == "https://github.com/owner/repo"


def test_an_ssh_port_is_not_a_web_port():
    # 2222 says where sshd listens and nothing about where HTTP does. Carrying
    # it over would reliably produce a page that does not load.
    assert web_url("ssh://git@gitlab.example.com:2222/group/project.git") == (
        "https://gitlab.example.com/group/project"
    )


@pytest.mark.parametrize(
    "remote",
    [
        "https://johnendean:ghp_deadbeef@github.com/owner/repo.git",
        "https://token@github.com/owner/repo.git",
        "ssh://git@github.com/owner/repo.git",
    ],
)
def test_credentials_never_reach_the_browser(remote):
    # A remote carrying a token is ordinary. Opening it would put that token in
    # browser history, and in whatever the browser syncs.
    url = web_url(remote)
    assert url == "https://github.com/owner/repo"
    assert "@" not in url


@pytest.mark.parametrize(
    "remote",
    [
        None,
        "",
        "   ",
        "/Users/john/code/herdrkeys",          # a local clone
        "../sibling-checkout",
        "file:///Volumes/backup/repo.git",     # no web page exists
        "not a remote at all",
    ],
)
def test_a_remote_with_no_page_is_not_an_error(remote):
    # None is a real answer: the key flashes rather than opening something wrong.
    assert web_url(remote) is None


def test_a_trailing_slash_does_not_become_part_of_the_path():
    assert web_url("https://github.com/owner/repo/") == "https://github.com/owner/repo"
    assert web_url("https://github.com/owner/repo.git/") == "https://github.com/owner/repo"


def test_a_directory_that_is_not_a_repository_has_no_page(tmp_path):
    # Exercises the real git lookup, without a fixture repository: whatever git
    # says, a directory outside a checkout must come back as None.
    assert page_for(str(tmp_path)) is None


def test_no_directory_at_all_has_no_page():
    assert page_for(None) is None
    assert page_for("") is None


def test_a_real_repository_resolves_end_to_end():
    # This checkout has an origin on GitHub, so the whole path -- git lookup and
    # translation -- can be exercised against something real.
    url = page_for(".")
    assert url is not None and url.startswith("https://")
    assert url.endswith("/herdrkeys")
