// Command herdrkeys binds a Pimoroni Keybow 2040, a Soomfon CN001, or both, to
// a running Herdr session: the keys show what every agent is doing, and
// pressing one jumps to it.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// Version must equal `version` in herdr-plugin.toml, which is also the
// release tag the install fetches. A test holds the two together.
const Version = "0.2.0"

const usageText = `usage: herdrkeys [-v] <command>

  run       run the daemon in the foreground (the default)
  start     start the daemon detached, unless this build is already running
  stop      stop the running daemon cleanly, blanking the boards
  doctor    check everything the daemon depends on
  adopt     take over a Keybow, saving any firmware already on it
  deploy    write herdrkeys firmware to a Keybow already running it
  tui       run the daemon against a keypad drawn in the terminal
  version   print the version
`

func main() { os.Exit(cli(os.Args[1:], os.Stdout, os.Stderr)) }

func cli(args []string, stdout, stderr io.Writer) int {
	verbose := false
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "-v", "--verbose":
			verbose = true
		case "-h", "--help":
			fmt.Fprint(stdout, usageText)
			return 0
		default:
			fmt.Fprint(stderr, usageText)
			return 2
		}
		args = args[1:]
	}
	command := "run"
	if len(args) > 0 {
		command = args[0]
	}
	commands := map[string]func() int{
		"run":     func() int { return run(verbose, stderr) },
		"start":   func() int { return start(stdout, stderr) },
		"stop":    func() int { return stop(stdout) },
		"doctor":  func() int { return doctor(stdout) },
		"adopt":   func() int { return adopt(stdout) },
		"deploy":  func() int { return deploy(stdout) },
		"tui":     func() int { return runTUI(verbose, stderr) },
		"version": func() int { fmt.Fprintln(stdout, "herdrkeys", Version); return 0 },
		// Asked by a running daemon on macOS: could a process started now
		// type? Only a fresh one is told about a grant (ADR 0017).
		"can-type": canTypeCommand,
	}
	fn, ok := commands[command]
	if !ok {
		fmt.Fprint(stderr, usageText)
		return 2
	}
	return fn()
}
