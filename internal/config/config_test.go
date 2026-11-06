package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// env is an environment rooted in a temp dir, with Herdr answering pluginDir
// when asked (or not at all, for "").
func env(t *testing.T, goos, pluginDir string, vars map[string]string) Env {
	t.Helper()
	home := t.TempDir()
	e := Env{
		GOOS: goos,
		Home: home,
		Getenv: func(k string) string {
			return vars[k]
		},
	}
	if pluginDir != "" {
		e.HerdrConfigDir = func() string { return pluginDir }
	}
	return e
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestThePluginsFileWinsWhenBothExist(t *testing.T) {
	plugin := filepath.Join(t.TempDir(), "plugin")
	e := env(t, "darwin", plugin, nil)
	write(t, filepath.Join(plugin, FileName), "")
	write(t, filepath.Join(e.UserConfigDir(), FileName), "")
	loc := e.Resolve()
	if loc.Path != filepath.Join(plugin, FileName) || !loc.Exists {
		t.Fatalf("%+v", loc)
	}
	if !slices.Equal(loc.Shadowed, []string{filepath.Join(e.UserConfigDir(), FileName)}) {
		t.Fatalf("shadowed = %v: doctor names the ignored one", loc.Shadowed)
	}
}

func TestACheckoutWithNoPluginUsesTheUserDir(t *testing.T) {
	e := env(t, "darwin", "", nil)
	write(t, filepath.Join(e.UserConfigDir(), FileName), "")
	if loc := e.Resolve(); loc.Path != filepath.Join(e.Home, ".config", "herdrkeys", FileName) || !loc.Exists {
		t.Fatalf("%+v", loc)
	}
}

func TestThePluginDirIsNotPreferredJustBecauseItCouldExist(t *testing.T) {
	// `plugin config-dir` answers for uninstalled plugins too.
	e := env(t, "darwin", filepath.Join(t.TempDir(), "never-made"), nil)
	write(t, filepath.Join(e.UserConfigDir(), FileName), "")
	if loc := e.Resolve(); loc.Path != filepath.Join(e.UserConfigDir(), FileName) {
		t.Fatalf("%+v: your settings are never silently dropped", loc)
	}
}

func TestAnEmptyPluginDirStillWinsOverNothing(t *testing.T) {
	plugin := t.TempDir()
	e := env(t, "darwin", plugin, nil)
	if loc := e.Resolve(); loc.Path != filepath.Join(plugin, FileName) || loc.Exists {
		t.Fatalf("%+v", loc)
	}
}

func TestWithNothingAnywhereItFallsBackToTheUserDir(t *testing.T) {
	e := env(t, "darwin", "", nil)
	if loc := e.Resolve(); loc.Path != filepath.Join(e.UserConfigDir(), FileName) || loc.Exists {
		t.Fatalf("%+v", loc)
	}
}

func TestTheEnvironmentVariableIsHonouredWhenHerdrSetsIt(t *testing.T) {
	e := env(t, "darwin", "", map[string]string{"HERDR_PLUGIN_CONFIG_DIR": "/from/herdr"})
	if e.PluginConfigDir() != "/from/herdr" {
		t.Fatal(e.PluginConfigDir())
	}
}

func TestWindowsFollowsHerdrsOwnConvention(t *testing.T) {
	e := env(t, "windows", "", map[string]string{
		"APPDATA": `C:\Users\u\AppData\Roaming`, "LOCALAPPDATA": `C:\Users\u\AppData\Local`,
	})
	if got := e.UserConfigDir(); got != filepath.Join(`C:\Users\u\AppData\Roaming`, "herdrkeys") {
		t.Fatalf("config = %q", got)
	}
	if got := e.StateDir(); got != filepath.Join(`C:\Users\u\AppData\Local`, "herdrkeys") {
		t.Fatalf("state = %q", got)
	}
}

func TestStateNeverFollowsThePlugin(t *testing.T) {
	e := env(t, "darwin", "", map[string]string{"HERDR_PLUGIN_CONFIG_DIR": "/plugin", "XDG_STATE_HOME": "/state"})
	if got := e.StateDir(); got != filepath.Join("/state", "herdrkeys") {
		t.Fatalf("state = %q: one log, wherever the daemon was started", got)
	}
}

func TestEverySettingIsRead(t *testing.T) {
	e := env(t, "darwin", "", nil)
	write(t, filepath.Join(e.UserConfigDir(), FileName), `
settle_ms = 250
status_poll_ms = 1000
reconcile_seconds = 10
provision = false
activate_terminal = false
terminal_app = "/Applications/Ghostty.app"
serial_port = "COM7"
socket_path = "~/.config/herdr/sessions/work/herdr.sock"
cn001_brightness = 140
usage = false
dictation_chord = "ctrl+win"
state_path = "ignored: the slot map is gone"
`)
	c := Load(e)
	if c.SettleSeconds != 0.25 || c.StatusPollSeconds != 1 || c.ReconcileSeconds != 10 {
		t.Fatalf("timings %+v", c)
	}
	if c.Provision || c.ActivateTerminal || c.Usage {
		t.Fatalf("flags %+v", c)
	}
	if c.TerminalApp != "/Applications/Ghostty.app" || c.SerialPort != "COM7" {
		t.Fatalf("strings %+v", c)
	}
	if c.SocketPath != filepath.Join(e.Home, ".config/herdr/sessions/work/herdr.sock") {
		t.Fatalf("socket = %q", c.SocketPath)
	}
	if c.CN001Brightness != 100 {
		t.Fatalf("brightness = %d: clamped", c.CN001Brightness)
	}
	if c.DictationChord.String() != "left_control+left_gui" {
		t.Fatalf("chord = %v", c.DictationChord)
	}
	if len(c.Problems) != 0 {
		t.Fatalf("problems = %v", c.Problems)
	}
	if c.SalvageDir != filepath.Join(e.UserConfigDir(), "salvage") {
		t.Fatalf("salvage = %q: beside the config in effect", c.SalvageDir)
	}
}

func TestABadSettingFallsBackAndSaysSo(t *testing.T) {
	e := env(t, "windows", "", map[string]string{"APPDATA": t.TempDir()})
	write(t, filepath.Join(e.UserConfigDir(), FileName), "usage = \"no\"\ndictation_chord = \"a+b\"\n")
	c := Load(e)
	if !c.Usage || c.DictationChord.String() != "left_control+left_gui" {
		t.Fatalf("%+v: defaults kept", c)
	}
	if len(c.Problems) != 2 {
		t.Fatalf("problems = %v", c.Problems)
	}
}

func TestAFileThatDoesNotParseIsAllDefaults(t *testing.T) {
	e := env(t, "darwin", "", nil)
	write(t, filepath.Join(e.UserConfigDir(), FileName), "this is = = not toml")
	c := Load(e)
	if c.SettleSeconds != 0.3 || len(c.Problems) != 1 {
		t.Fatalf("%+v", c)
	}
}
