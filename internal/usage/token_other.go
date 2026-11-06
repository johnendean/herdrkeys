//go:build !darwin

package usage

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// credentialFile is where Claude Code keeps its credential on Windows: a
// plain file, read without any prompt. Installing the plugin with a CN001
// attached is the consent (ADR 0019, as amended).
func credentialFile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", ".credentials.json")
}

// ReadToken is Claude Code's OAuth access token, out of its credential file.
// Read, and never written: Claude Code refreshes it while it runs.
func ReadToken(wall time.Time) (string, error) {
	data, err := os.ReadFile(credentialFile())
	if errors.Is(err, fs.ErrNotExist) {
		return "", &CredentialRefused{"no Claude Code credential at " + credentialFile() + "; sign in to Claude Code"}
	}
	if err != nil {
		return "", &CredentialRefused{"cannot read " + credentialFile() + ": " + err.Error()}
	}
	return TokenFrom(data, wall, credentialFile())
}

// CredentialSource names where the token is read from, for doctor.
func CredentialSource() string { return credentialFile() }
