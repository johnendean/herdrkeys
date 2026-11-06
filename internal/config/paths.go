// Package config is herdrkeys' configuration, and where things live on disk.
//
// herdrkeys can be launched three ways -- by Herdr as a plugin action, by
// Herdr's startup hook, or by hand from a checkout -- and only the first two
// get HERDR_PLUGIN_CONFIG_DIR in the environment. Keying off that variable
// alone meant the same command read a different config file depending on how
// it was invoked, which is the sort of thing that costs an hour to notice.
//
// So the plugin's directory is resolved whether or not Herdr set the variable,
// and one order of precedence applies everywhere.
//
// State is deliberately not treated this way: it always lives in one place,
// so the log stays in one place no matter who started the daemon.
package config

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// PluginID is herdrkeys' id as a Herdr plugin.
const PluginID = "herdrkeys"

// FileName is the config file's name, wherever it lives.
const FileName = "config.toml"

// Env is the environment, so tests can stand in for it.
type Env struct {
	Getenv func(string) string
	GOOS   string
	Home   string
	// HerdrConfigDir asks Herdr for the plugin's config directory; nil when
	// there is no Herdr to ask.
	HerdrConfigDir func() string
}

// System is the real environment.
func System() Env {
	home, _ := os.UserHomeDir()
	return Env{Getenv: os.Getenv, GOOS: runtime.GOOS, Home: home, HerdrConfigDir: askHerdr}
}

// UserConfigDir is herdrkeys' own config directory: `%APPDATA%\herdrkeys` on
// Windows, following Herdr's own convention there, and
// `$XDG_CONFIG_HOME/herdrkeys` (`~/.config/herdrkeys`) elsewhere.
func (e Env) UserConfigDir() string {
	if e.GOOS == "windows" {
		if appData := e.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, PluginID)
		}
	}
	return filepath.Join(e.xdg("XDG_CONFIG_HOME", ".config"), PluginID)
}

// StateDir is where the log lives: `%LOCALAPPDATA%\herdrkeys` on Windows,
// `$XDG_STATE_HOME/herdrkeys` elsewhere. Never the plugin's state directory:
// one log, wherever the daemon was started from.
func (e Env) StateDir() string {
	if e.GOOS == "windows" {
		if local := e.Getenv("LOCALAPPDATA"); local != "" {
			return filepath.Join(local, PluginID)
		}
	}
	return filepath.Join(e.xdg("XDG_STATE_HOME", filepath.Join(".local", "state")), PluginID)
}

func (e Env) xdg(variable, fallback string) string {
	if dir := e.Getenv(variable); dir != "" {
		return dir
	}
	return filepath.Join(e.Home, fallback)
}

// PluginConfigDir is the plugin's config directory as Herdr itself computes
// it, or "".
//
// Herdr is asked rather than the path rebuilt here, because named sessions
// move Herdr's config root and duplicating that logic would drift.
func (e Env) PluginConfigDir() string {
	if explicit := e.Getenv("HERDR_PLUGIN_CONFIG_DIR"); explicit != "" {
		return explicit
	}
	if e.HerdrConfigDir == nil {
		return ""
	}
	return e.HerdrConfigDir()
}

func askHerdr() string {
	binary := os.Getenv("HERDR_BIN_PATH")
	if binary == "" {
		found, err := exec.LookPath("herdr")
		if err != nil {
			return ""
		}
		binary = found
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, "plugin", "config-dir", PluginID).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Location is the config file in effect.
type Location struct {
	Path   string // whether or not it exists
	Exists bool
	// Shadowed are real files being ignored, for doctor to name.
	Shadowed []string
}

// Resolve picks one config file, preferring the plugin's when Herdr knows of
// it.
//
// A directory Herdr has created counts as the plugin being present, since
// `plugin config-dir` answers for uninstalled plugins too and so cannot be
// used to tell them apart.
func (e Env) Resolve() Location {
	var dirs []string
	if plugin := e.PluginConfigDir(); plugin != "" {
		dirs = append(dirs, plugin)
	}
	dirs = append(dirs, e.UserConfigDir())

	var present []string
	for _, dir := range dirs {
		if isFile(filepath.Join(dir, FileName)) {
			present = append(present, filepath.Join(dir, FileName))
		}
	}
	if len(present) > 0 {
		return Location{Path: present[0], Exists: true, Shadowed: present[1:]}
	}
	for _, dir := range dirs {
		if isDir(dir) {
			return Location{Path: filepath.Join(dir, FileName)}
		}
	}
	return Location{Path: filepath.Join(dirs[len(dirs)-1], FileName)}
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return !errors.Is(err, fs.ErrNotExist)
}
