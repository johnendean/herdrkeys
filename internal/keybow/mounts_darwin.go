package keybow

import (
	"os"
	"path/filepath"
	"sort"
)

// mountPoints is everything under /Volumes.
func mountPoints() []string {
	entries, err := os.ReadDir("/Volumes")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		out = append(out, filepath.Join("/Volumes", e.Name()))
	}
	sort.Strings(out)
	return out
}
