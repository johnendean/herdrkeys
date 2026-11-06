//go:build !darwin && !windows

package keybow

// findPorts finds nothing: Linux is not a platform herdrkeys claims
// (ADR 0020).
func findPorts() []Port { return nil }
