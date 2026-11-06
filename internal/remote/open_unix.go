//go:build !windows

package remote

import (
	"context"
	"os/exec"
	"time"
)

// OpenURL opens a URL in the default browser. Best effort, like raising the
// terminal.
func OpenURL(url string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	exec.CommandContext(ctx, "open", url).Run()
}

func hideWindow(*exec.Cmd) {}
