from herdrkeys import activate


def chain(monkeypatch, rows):
    monkeypatch.setattr(activate, "_processes", lambda: rows)


def test_finds_the_terminal_that_is_an_ancestor(monkeypatch):
    # iTerm 3.6: the app itself is up the chain.
    chain(monkeypatch, [
        (10, 9, "herdr"),
        (9, 8, "-zsh"),
        (8, 7, "/usr/bin/login -fpl john /bin/zsh"),
        (7, 6, "iTermServer-3.6.11"),
        (6, 1, "/Applications/iTerm.app/Contents/MacOS/iTerm2"),
    ])
    assert activate.detect_host_app() == "/Applications/iTerm.app"


def test_finds_the_terminal_named_only_in_an_argument(monkeypatch):
    # iTerm 3.7: the session server is detached and lives outside the bundle,
    # so the only trace of the app is in what `login` was asked to run.
    chain(monkeypatch, [
        (30373, 30372, "/Users/john/.local/bin/herdr server"),
        (30372, 30212, "herdr"),
        (30212, 30211, "-zsh"),
        (30211, 30206, "/usr/bin/login -fpl john /Applications/iTerm.app/Contents/MacOS/ShellLauncher --launch_shell"),
        (30206, 1, "/Users/john/Library/Application Support/iTerm2/iTermServer-3.7.2 /Users/john/Library/Application Support/iTerm2/iterm2-daemon-1.socket"),
    ])
    assert activate.detect_host_app() == "/Applications/iTerm.app"


def test_an_app_name_with_spaces_survives(monkeypatch):
    chain(monkeypatch, [
        (3, 2, "herdr"),
        (2, 1, "/Applications/Visual Studio Code.app/Contents/MacOS/Electron"),
    ])
    assert activate.detect_host_app() == "/Applications/Visual Studio Code.app"


def test_no_client_means_no_answer(monkeypatch):
    chain(monkeypatch, [(5, 1, "/Users/john/.local/bin/herdr server")])
    assert activate.detect_host_app() is None
