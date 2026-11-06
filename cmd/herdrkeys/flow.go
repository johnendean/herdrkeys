package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/johnendean/herdrkeys/internal/chord"
	"github.com/johnendean/herdrkeys/internal/tui"
)

// flowConfig is where Wispr Flow keeps its settings.
func flowConfig() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("APPDATA"), "Wispr Flow", "config.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", "Wispr Flow", "config.json")
}

// flowKeys is a binding as Flow records it: the codes of the keys held
// together -- virtual-key codes on Windows, key codes on macOS.
type flowKeys []int

// flowBinding is Flow's push-to-talk binding, and false when Flow, its
// config, or the binding cannot be found. Flow records shortcuts as
// `prefs.user.shortcuts`, each combination of codes joined by `+` naming the
// action it does.
func flowBinding() (flowKeys, bool) {
	data, err := os.ReadFile(flowConfig())
	if err != nil {
		return nil, false
	}
	var cfg struct {
		Prefs struct {
			User struct {
				Shortcuts map[string]string `json:"shortcuts"`
			} `json:"user"`
		} `json:"prefs"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return nil, false
	}
	for combo, action := range cfg.Prefs.User.Shortcuts {
		if action != "ptt" {
			continue
		}
		var keys flowKeys
		for _, part := range strings.Split(combo, "+") {
			code, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil {
				return nil, false
			}
			keys = append(keys, code)
		}
		return keys, true
	}
	return nil, false
}

func codeOf(k chord.Key) int {
	if runtime.GOOS == "windows" {
		return int(k.VK)
	}
	return int(k.MacCode)
}

func (f flowKeys) matches(c chord.Chord) bool {
	if len(f) != len(c) {
		return false
	}
	for _, k := range c {
		if !slices.Contains(f, codeOf(k)) {
			return false
		}
	}
	return true
}

// describe names Flow's binding in the chord's own terms where it can.
func (f flowKeys) describe() string {
	var names []string
	for _, code := range f {
		name := ""
		for _, candidate := range []string{"left_control", "right_control", "left_shift", "right_shift",
			"left_alt", "right_alt", "left_gui", "right_gui", "f13", "f14", "f15"} {
			k, _ := chord.Parse(candidate)
			if codeOf(k[0]) == code {
				name = candidate
			}
		}
		if name == "" {
			// Codes past the keyboard's range are what Flow records for a
			// mouse button, which nothing here can hold down.
			name = fmt.Sprintf("code %d", code)
			if code > 0xFF {
				name += " (not a key -- a mouse button?)"
			}
		}
		names = append(names, name)
	}
	return strings.Join(names, "+")
}

func openTUI() (*tui.Device, error) { return tui.Open() }
