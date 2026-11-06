//go:build !darwin && !windows

package tui

// rawMode leaves the terminal as it is: input arrives a line at a time.
func rawMode() (func(), error) { return func() {}, nil }
