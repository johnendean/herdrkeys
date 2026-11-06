package remote

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// OpenURL opens a URL in the default browser, through the shell, as clicking
// a link would. Best effort, like raising the terminal.
func OpenURL(url string) {
	verb, _ := windows.UTF16PtrFromString("open")
	target, err := windows.UTF16PtrFromString(url)
	if err != nil {
		return
	}
	windows.ShellExecute(0, verb, target, nil, nil, windows.SW_SHOWNORMAL)
}

// hideWindow keeps git and gh from flashing a console window, which a daemon
// with no console of its own would otherwise get for every child.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}
