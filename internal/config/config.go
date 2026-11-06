package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/johnendean/herdrkeys/internal/chord"
)

// Config is everything herdrkeys can be told. Everything has a working
// default, and the file is optional.
type Config struct {
	SettleSeconds       float64
	StatusPollSeconds   float64
	ReconcileSeconds    float64
	ReconnectMinSeconds float64
	ReconnectMaxSeconds float64
	ActivateTerminal    bool
	TerminalApp         string // "" is auto-detected
	SocketPath          string // "" is Herdr's default session
	SerialPort          string // "" is auto-discovered
	SalvageDir          string
	Provision           bool
	// The CN001's panel, 0-100. Its LCD is far brighter than the Keybow's
	// LEDs at the same meaning, and the room decides how bright is right.
	CN001Brightness int
	// The CN001's strip shows the Claude plan's usage, asked of Anthropic
	// once a minute with Claude Code's own credential (ADR 0019).
	Usage bool
	// The keys held down while the microphone is open (ADR 0017).
	DictationChord chord.Chord

	// Where this came from, for doctor.
	Location Location
	// Problems are settings that were present and could not be used. Each
	// falls back to its default rather than stopping the daemon.
	Problems []string
}

// Defaults is the configuration with no file at all.
func Defaults() Config {
	return Config{
		SettleSeconds:       0.3,
		StatusPollSeconds:   0.5,
		ReconcileSeconds:    30,
		ReconnectMinSeconds: 0.5,
		ReconnectMaxSeconds: 15,
		ActivateTerminal:    true,
		Provision:           true,
		CN001Brightness:     80,
		Usage:               true,
		DictationChord:      chord.Default(),
	}
}

// Load reads the config file in effect for this environment.
func Load(env Env) Config {
	location := env.Resolve()
	config := Defaults()
	config.Location = location
	config.SalvageDir = filepath.Join(filepath.Dir(location.Path), "salvage")
	data, err := os.ReadFile(location.Path)
	if err != nil {
		return config
	}
	config.apply(data, env)
	return config
}

func (c *Config) apply(data []byte, env Env) {
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		c.Problems = append(c.Problems, fmt.Sprintf("%s does not parse, so every setting is its default: %v", c.Location.Path, err))
		return
	}
	number := func(key string, into *float64, scale float64) {
		if v, ok := raw[key]; ok {
			switch n := v.(type) {
			case int64:
				*into = float64(n) * scale
			case float64:
				*into = n * scale
			default:
				c.Problems = append(c.Problems, fmt.Sprintf("%s should be a number", key))
			}
		}
	}
	flag := func(key string, into *bool) {
		if v, ok := raw[key]; ok {
			if b, ok := v.(bool); ok {
				*into = b
			} else {
				c.Problems = append(c.Problems, fmt.Sprintf("%s should be true or false", key))
			}
		}
	}
	text := func(key string, into *string) {
		if v, ok := raw[key]; ok {
			if s, ok := v.(string); ok {
				*into = s
			} else {
				c.Problems = append(c.Problems, fmt.Sprintf("%s should be a string", key))
			}
		}
	}

	number("settle_ms", &c.SettleSeconds, 0.001)
	number("status_poll_ms", &c.StatusPollSeconds, 0.001)
	number("reconcile_seconds", &c.ReconcileSeconds, 1)
	flag("provision", &c.Provision)
	flag("activate_terminal", &c.ActivateTerminal)
	flag("usage", &c.Usage)
	text("terminal_app", &c.TerminalApp)
	text("serial_port", &c.SerialPort)
	var socket, salvage string
	text("socket_path", &socket)
	if socket != "" {
		c.SocketPath = expandHome(socket, env.Home)
	}
	text("salvage_dir", &salvage)
	if salvage != "" {
		c.SalvageDir = expandHome(salvage, env.Home)
	}
	brightness := float64(c.CN001Brightness)
	number("cn001_brightness", &brightness, 1)
	c.CN001Brightness = int(max(0, min(100, brightness)))
	var spelled string
	text("dictation_chord", &spelled)
	if spelled != "" {
		parsed, err := chord.Parse(spelled)
		if err != nil {
			c.Problems = append(c.Problems, err.Error())
		} else {
			c.DictationChord = parsed
		}
	}
	// `state_path` is accepted and ignored: it configured the slot map, which
	// no longer exists (ADR 0009). An old config file must not stop the
	// daemon starting.
}

func expandHome(path, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		return filepath.Join(home, path[2:])
	}
	return path
}
