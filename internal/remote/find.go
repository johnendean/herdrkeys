package remote

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Find is the program to run for name: whatever PATH finds, or failing that
// the first of the places it is usually installed.
//
// The daemon inherits Herdr's environment, not a shell's, and that PATH can
// be missing what yours has -- on Windows a server started before gh was
// installed, on macOS the bare PATH launchd hands out, which hid Homebrew from
// the Python version of this. Without gh the git key still opens the page,
// so this is the difference between the key reaching a pull request and
// quietly never doing so.
func Find(name string) string {
	if found, err := exec.LookPath(name); err == nil {
		return found
	}
	for _, candidate := range usualPlaces(name) {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return name
}

func usualPlaces(name string) []string {
	if runtime.GOOS != "windows" {
		return []string{
			filepath.Join("/opt/homebrew/bin", name),
			filepath.Join("/usr/local/bin", name),
			filepath.Join("/usr/bin", name),
		}
	}
	programFiles := os.Getenv("ProgramFiles")
	local := os.Getenv("LOCALAPPDATA")
	home := os.Getenv("USERPROFILE")
	exe := name + ".exe"
	var places []string
	switch name {
	case "gh":
		places = []string{
			filepath.Join(programFiles, "GitHub CLI", exe),
			filepath.Join(local, "Programs", "GitHub CLI", exe),
		}
	case "git":
		places = []string{
			filepath.Join(programFiles, "Git", "cmd", exe),
			filepath.Join(local, "Programs", "Git", "cmd", exe),
		}
	}
	return append(places,
		filepath.Join(local, "Microsoft", "WinGet", "Links", exe), // winget
		filepath.Join(home, "scoop", "shims", exe),                // scoop
		filepath.Join(os.Getenv("ProgramData"), "chocolatey", "bin", exe),
	)
}
