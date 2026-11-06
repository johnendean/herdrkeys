package tui

import (
	"os"

	"golang.org/x/sys/windows"
)

// rawMode turns off line input and echo on the console, and turns on the
// escape sequences the grid is drawn with.
func rawMode() (func(), error) {
	in, out := windows.Handle(os.Stdin.Fd()), windows.Handle(os.Stdout.Fd())
	var inMode, outMode uint32
	if err := windows.GetConsoleMode(in, &inMode); err != nil {
		return nil, err
	}
	windows.GetConsoleMode(out, &outMode)
	raw := inMode &^ (windows.ENABLE_LINE_INPUT | windows.ENABLE_ECHO_INPUT)
	if err := windows.SetConsoleMode(in, raw); err != nil {
		return nil, err
	}
	windows.SetConsoleMode(out, outMode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
	return func() {
		windows.SetConsoleMode(in, inMode)
		windows.SetConsoleMode(out, outMode)
	}, nil
}
