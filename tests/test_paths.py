"""One config file, whoever started herdrkeys.

Keying only off HERDR_PLUGIN_CONFIG_DIR meant `herdr plugin action invoke
herdrkeys.doctor` and `./bin/herdrkeys doctor` read different files.
"""

import herdrkeys.paths as paths
from herdrkeys.paths import resolve_config


def setup(monkeypatch, tmp_path, *, plugin_dir=None, env=None):
    monkeypatch.setattr(paths, "xdg_config_dir", lambda: tmp_path / "xdg")
    monkeypatch.setattr(paths, "plugin_config_dir", lambda: plugin_dir)
    monkeypatch.delenv("HERDR_PLUGIN_CONFIG_DIR", raising=False)
    if env:
        monkeypatch.setenv("HERDR_PLUGIN_CONFIG_DIR", str(env))


def write(directory, body="settle_ms = 111\n"):
    directory.mkdir(parents=True, exist_ok=True)
    (directory / "config.toml").write_text(body)
    return directory / "config.toml"


def test_the_plugins_file_wins_when_both_exist(monkeypatch, tmp_path):
    plugin = tmp_path / "plugin"
    setup(monkeypatch, tmp_path, plugin_dir=plugin)
    wanted = write(plugin)
    ignored = write(tmp_path / "xdg")

    location = resolve_config()
    assert location.path == wanted and location.exists
    assert location.shadowed == [ignored], "and the ignored one is reported, not silently dropped"


def test_a_checkout_with_no_plugin_uses_xdg(monkeypatch, tmp_path):
    setup(monkeypatch, tmp_path, plugin_dir=None)
    wanted = write(tmp_path / "xdg")
    assert resolve_config().path == wanted


def test_the_plugin_dir_is_not_preferred_just_because_it_could_exist(monkeypatch, tmp_path):
    # `herdr plugin config-dir` answers for uninstalled plugins too, so a path
    # alone proves nothing. Only a real directory or a real file counts.
    setup(monkeypatch, tmp_path, plugin_dir=tmp_path / "never-created")
    wanted = write(tmp_path / "xdg")
    assert resolve_config().path == wanted


def test_an_empty_plugin_dir_still_wins_over_nothing(monkeypatch, tmp_path):
    plugin = tmp_path / "plugin"
    plugin.mkdir()
    setup(monkeypatch, tmp_path, plugin_dir=plugin)
    location = resolve_config()
    assert location.path == plugin / "config.toml" and not location.exists


def test_with_nothing_anywhere_it_falls_back_to_xdg(monkeypatch, tmp_path):
    setup(monkeypatch, tmp_path, plugin_dir=tmp_path / "plugin")
    location = resolve_config()
    assert location.path == tmp_path / "xdg" / "config.toml"
    assert not location.exists and location.shadowed == []


def test_the_environment_variable_is_honoured_when_herdr_sets_it(monkeypatch, tmp_path):
    monkeypatch.setenv("HERDR_PLUGIN_CONFIG_DIR", str(tmp_path / "from-env"))
    assert paths.plugin_config_dir() == tmp_path / "from-env"


def test_state_never_follows_the_plugin(monkeypatch, tmp_path):
    # The slot map and log must be in one place however the daemon was started.
    monkeypatch.setenv("HERDR_PLUGIN_STATE_DIR", str(tmp_path / "plugin-state"))
    monkeypatch.setenv("XDG_STATE_HOME", str(tmp_path / "xdg-state"))
    assert paths.state_dir() == tmp_path / "xdg-state" / "herdrkeys"
