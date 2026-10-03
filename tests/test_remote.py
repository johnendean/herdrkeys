"""Turning a git remote into a page.

`web_url` is where every decision worth arguing about lives, and it is pure
string work, so it is tested exhaustively here rather than through a daemon.
"""

import pytest

from herdrkeys import remote
from herdrkeys.remote import (
    PullRequest,
    current_branch,
    destination,
    has_page,
    open_pull_requests,
    page_for,
    remote_from_config,
    web_url,
)


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


# -- reading the remote without running git -------------------------------


def make_repo(root, config_text):
    """A checkout as far as this code is concerned: a .git dir with a config."""
    git_dir = root / ".git"
    git_dir.mkdir(parents=True)
    (git_dir / "config").write_text(config_text)
    return root


PLAIN = """\
[core]
\trepositoryformatversion = 0
[remote "origin"]
\turl = git@github.com:johnendean/herdrkeys.git
\tfetch = +refs/heads/*:refs/remotes/origin/*
[branch "main"]
\tremote = origin
"""


def test_the_remote_is_read_straight_out_of_the_config(tmp_path):
    make_repo(tmp_path, PLAIN)
    assert remote_from_config(str(tmp_path)) == "git@github.com:johnendean/herdrkeys.git"


def test_tab_indented_keys_are_read(tmp_path):
    # The reason this is hand-parsed: configparser reads git's tab indentation
    # as line continuations and returns nothing useful.
    make_repo(tmp_path, PLAIN)
    assert has_page(str(tmp_path))


def test_a_percent_in_a_url_is_not_interpolation(tmp_path):
    # The other reason: configparser would try to expand this and raise.
    make_repo(tmp_path, '[remote "origin"]\n\turl = https://host/o/r%20x.git\n')
    assert remote_from_config(str(tmp_path)) == "https://host/o/r%20x.git"


def test_a_subdirectory_finds_the_repository_above_it(tmp_path):
    make_repo(tmp_path, PLAIN)
    deep = tmp_path / "src" / "herdrkeys"
    deep.mkdir(parents=True)
    assert has_page(str(deep)), "an agent is rarely sitting in the repository root"


def test_comments_and_other_remotes_are_not_mistaken_for_origin(tmp_path):
    make_repo(
        tmp_path,
        '# url = https://wrong/one.git\n'
        '[remote "upstream"]\n\turl = git@github.com:someone/else.git\n'
        '[remote "origin"]\n\turl = git@github.com:right/one.git\n',
    )
    assert remote_from_config(str(tmp_path)) == "git@github.com:right/one.git"


def test_a_repository_with_no_origin_has_no_page(tmp_path):
    make_repo(tmp_path, '[remote "upstream"]\n\turl = git@github.com:someone/else.git\n')
    assert remote_from_config(str(tmp_path)) is None


def test_a_worktree_finds_the_config_it_shares(tmp_path):
    # Herdr creates worktrees itself, so `.git` being a file rather than a
    # directory is ordinary here and must not read as "not a repository".
    main = make_repo(tmp_path / "main", PLAIN)
    worktree_git = main / ".git" / "worktrees" / "feature"
    worktree_git.mkdir(parents=True)
    (worktree_git / "commondir").write_text("../..\n")

    checkout = tmp_path / "feature"
    checkout.mkdir()
    (checkout / ".git").write_text(f"gitdir: {worktree_git}\n")

    assert has_page(str(checkout))
    assert remote_from_config(str(checkout)) == "git@github.com:johnendean/herdrkeys.git"


def test_a_directory_outside_any_repository_reads_as_no_page(tmp_path):
    assert remote_from_config(str(tmp_path)) is None
    assert has_page(str(tmp_path)) is False


def test_no_directory_at_all_reads_as_no_page():
    assert remote_from_config(None) is None
    assert has_page(None) is False


def test_the_colour_never_promises_more_than_a_press_delivers(tmp_path):
    # The invariant that matters. has_page reads only the config file, so it
    # may say no where a press finds something -- but it must never say yes
    # where a press finds nothing, which would be a key that lies.
    make_repo(tmp_path, PLAIN)
    assert has_page(str(tmp_path)) is True
    assert page_for(str(tmp_path)) is not None


def test_git_is_asked_only_when_the_config_says_nothing(tmp_path, monkeypatch):
    # The config parser understands plain remotes and nothing else. Rather than
    # grow it into a git implementation, a press falls through to git, so the
    # key is never less capable than it was before it had a colour.
    import herdrkeys.remote as remote_module

    asked = []
    monkeypatch.setattr(
        remote_module,
        "remote_url",
        lambda cwd, **kw: asked.append(cwd) or "git@github.com:fallback/found.git",
    )

    inside = make_repo(tmp_path / "checkout", PLAIN)
    assert page_for(str(inside)) == "https://github.com/johnendean/herdrkeys"
    assert asked == [], "the config answered, so git was not run"

    # A sibling of the checkout, not a child: a child would walk up and find
    # the repository above it, which is the whole point of the walk.
    bare = tmp_path / "elsewhere"
    bare.mkdir()
    assert page_for(str(bare)) == "https://github.com/fallback/found"
    assert asked == [str(bare)], "the config said nothing, so git was asked"


# -- where a press goes ----------------------------------------------------

PAGE = "https://github.com/johnendean/herdrkeys"


def pull(number, head):
    return PullRequest(f"{PAGE}/pull/{number}", head)


def test_the_branchs_own_pull_request_wins():
    assert destination(PAGE, "feature", [pull(7, "other"), pull(9, "feature")]) == f"{PAGE}/pull/9"


def test_only_other_branches_pull_requests_means_the_list():
    # Even one. A pull request for another branch is not this agent's work, so
    # it is never opened on its own.
    assert destination(PAGE, "feature", [pull(7, "other")]) == f"{PAGE}/pulls"


def test_a_forks_branch_of_the_same_name_is_not_ours():
    # Someone's fork opened a pull request from their `main`. Ours is `main`
    # too, and that pull request is still not this agent's work.
    theirs = PullRequest(f"{PAGE}/pull/4", "main", from_fork=True)
    assert destination(PAGE, "main", [theirs]) == f"{PAGE}/pulls"


def test_no_open_pull_requests_means_the_page():
    assert destination(PAGE, "feature", []) == PAGE


def test_not_knowing_means_the_page():
    assert destination(PAGE, "feature", None) == PAGE


def test_a_detached_head_has_no_pull_request_of_its_own():
    assert destination(PAGE, None, [pull(7, "other")]) == f"{PAGE}/pulls"


def test_the_branch_is_read_from_head(tmp_path):
    make_repo(tmp_path, PLAIN)
    (tmp_path / ".git" / "HEAD").write_text("ref: refs/heads/feature/thing\n")
    (tmp_path / "sub").mkdir()
    assert current_branch(str(tmp_path)) == "feature/thing"
    assert current_branch(str(tmp_path / "sub")) == "feature/thing", "from anywhere inside"


def test_a_detached_head_is_no_branch(tmp_path):
    make_repo(tmp_path, PLAIN)
    (tmp_path / ".git" / "HEAD").write_text("1f2e3d4c5b6a79881f2e3d4c5b6a79881f2e3d4c\n")
    assert current_branch(str(tmp_path)) is None


def test_a_worktree_reads_its_own_head(tmp_path):
    # Herdr's worktrees are the usual case: each has its own HEAD, in its own
    # git dir, and that is the branch whose pull request matters.
    main = make_repo(tmp_path / "main", PLAIN)
    (main / ".git" / "HEAD").write_text("ref: refs/heads/main\n")
    worktree_git = main / ".git" / "worktrees" / "feature"
    worktree_git.mkdir(parents=True)
    (worktree_git / "HEAD").write_text("ref: refs/heads/feature\n")
    (worktree_git / "commondir").write_text("../..\n")
    checkout = tmp_path / "feature"
    checkout.mkdir()
    (checkout / ".git").write_text(f"gitdir: {worktree_git}\n")

    assert current_branch(str(checkout)) == "feature"
    assert current_branch(str(main)) == "main"


def test_outside_a_repository_there_is_no_branch(tmp_path):
    assert current_branch(str(tmp_path)) is None
    assert current_branch(None) is None


# -- asking gh -------------------------------------------------------------


class Ran:
    def __init__(self, returncode=0, stdout=""):
        self.returncode, self.stdout = returncode, stdout


def test_gh_is_asked_about_the_pages_repository_without_prompting(monkeypatch):
    calls = []

    def fake_run(argv, **kw):
        calls.append((argv, kw))
        return Ran(stdout='[{"url": "%s/pull/9", "headRefName": "feature", "isCrossRepository": false},'
                          ' {"url": "%s/pull/4", "headRefName": "main", "isCrossRepository": true}]' % (PAGE, PAGE))

    monkeypatch.setattr(remote.subprocess, "run", fake_run)

    assert open_pull_requests(PAGE) == [
        pull(9, "feature"),
        PullRequest(f"{PAGE}/pull/4", "main", from_fork=True),
    ]
    argv, kw = calls[0]
    assert argv[:3] == ["gh", "pr", "list"]
    assert argv[argv.index("--repo") + 1] == "github.com/johnendean/herdrkeys"
    assert argv[argv.index("--state") + 1] == "open", "drafts are open, so they count"
    assert kw["env"]["GH_PROMPT_DISABLED"] == "1"
    assert kw["timeout"] <= 3.0


@pytest.mark.parametrize(
    "outcome",
    [
        Ran(returncode=1),                     # not logged in, not GitHub, no such repo
        Ran(stdout="not json"),
        Ran(stdout='[{"url": "x"}]'),          # a shape we did not ask for
    ],
)
def test_every_way_gh_can_fail_is_not_knowing(monkeypatch, outcome):
    monkeypatch.setattr(remote.subprocess, "run", lambda argv, **kw: outcome)
    assert open_pull_requests(PAGE) is None


@pytest.mark.parametrize("error", [FileNotFoundError("gh"), remote.subprocess.TimeoutExpired("gh", 3.0)])
def test_no_gh_or_a_slow_one_is_not_knowing(monkeypatch, error):
    def fake_run(argv, **kw):
        raise error

    monkeypatch.setattr(remote.subprocess, "run", fake_run)
    assert open_pull_requests(PAGE) is None
