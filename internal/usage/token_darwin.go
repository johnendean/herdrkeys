package usage

import (
	"errors"
	"fmt"
	"os/exec"
	"time"
)

const keychainService = "Claude Code-credentials"

// ReadToken is Claude Code's OAuth access token, out of the Keychain.
//
// `security` may put up macOS's dialog asking whether it may; that dialog is
// the consent (ADR 0019). Whatever stops it -- no item, a refusal, a
// cancelled dialog -- is a CredentialRefused, so it is not asked again.
func ReadToken(wall time.Time) (string, error) {
	out, err := exec.Command("security", "find-generic-password", "-s", keychainService, "-w").Output()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit) && exit.ExitCode() == 44:
		return "", &CredentialRefused{"no Claude Code credential in the Keychain; sign in to Claude Code"}
	case errors.As(err, &exit):
		return "", &CredentialRefused{fmt.Sprintf("the Keychain would not give up Claude Code's credential (security exited %d)", exit.ExitCode())}
	case err != nil:
		return "", &CredentialRefused{"cannot run security: " + err.Error()}
	}
	return TokenFrom(out, wall, "Claude Code's Keychain item")
}

// CredentialSource names where the token is read from, for doctor.
func CredentialSource() string { return "the Keychain item " + keychainService }
