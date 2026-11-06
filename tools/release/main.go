// Command release builds every release binary reproducibly and writes
// checksums.txt.
//
//	go run ./tools/release          build into dist/ and rewrite checksums.txt
//	go run ./tools/release -check   build, and fail if checksums.txt disagrees
//
// The install checks each downloaded binary against checksums.txt in the
// commit it cloned, so the sums must be committed before the release is tagged,
// which means before CI has built anything. That only works if a build is
// reproducible: the same source and the same Go give the same bytes on any
// machine. CI runs `-check` on the tag and refuses to publish on any difference.
// See ADR 0020.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// targets are the platforms the manifest claims (ADR 0020): macOS and Windows,
// each on both architectures. Every one builds without cgo, from any machine.
var targets = []struct{ goos, goarch string }{
	{"darwin", "amd64"}, {"darwin", "arm64"},
	{"windows", "amd64"}, {"windows", "arm64"},
}

func main() {
	check := flag.Bool("check", false, "fail if checksums.txt does not match a fresh build")
	flag.Parse()
	if err := run(*check); err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}

// pinnedGo is the exact toolchain go.mod names. Another Go version produces
// different bytes, so building with one would publish sums no one can verify.
func pinnedGo() (string, error) {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return "", err
	}
	m := regexp.MustCompile(`(?m)^go (\S+)`).FindSubmatch(data)
	if m == nil {
		return "", fmt.Errorf("no go line in go.mod")
	}
	return "go" + string(m[1]), nil
}

func run(check bool) error {
	want, err := pinnedGo()
	if err != nil {
		return err
	}
	if runtime.Version() != want {
		return fmt.Errorf("built with %s, but go.mod pins %s: release builds must use exactly that toolchain", runtime.Version(), want)
	}
	if err := os.MkdirAll("dist", 0o755); err != nil {
		return err
	}

	var lines []string
	for _, t := range targets {
		name := fmt.Sprintf("herdrkeys-%s-%s", t.goos, t.goarch)
		if t.goos == "windows" {
			name += ".exe"
		}
		out := filepath.Join("dist", name)
		cmd := exec.Command("go", "build",
			"-trimpath", "-buildvcs=false",
			"-ldflags", "-s -w -buildid=",
			"-o", out, "./cmd/herdrkeys")
		cmd.Env = append(cleanEnv(),
			"CGO_ENABLED=0", "GOOS="+t.goos, "GOARCH="+t.goarch,
			"GOAMD64=v1", "GOARM64=v8.0", "GOFLAGS=", "GOTOOLCHAIN=local")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("building %s: %w", name, err)
		}
		data, err := os.ReadFile(out)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		lines = append(lines, hex.EncodeToString(sum[:])+"  "+name)
		fmt.Println("built", out)
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i][66:] < lines[j][66:] })
	sums := strings.Join(lines, "\n") + "\n"

	if !check {
		return os.WriteFile("checksums.txt", []byte(sums), 0o644)
	}
	committed, err := os.ReadFile("checksums.txt")
	if err != nil {
		return err
	}
	if string(committed) != sums {
		return fmt.Errorf("checksums.txt does not match this build:\n--- committed\n%s--- built\n%s", committed, sums)
	}
	fmt.Println("checksums.txt matches")
	return nil
}

// cleanEnv drops settings that would change the output, so the caller's
// environment cannot leak into a release build.
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(key) {
		case "CGO_ENABLED", "GOOS", "GOARCH", "GOAMD64", "GOARM64", "GOFLAGS", "GOTOOLCHAIN", "GOEXPERIMENT":
			continue
		}
		env = append(env, kv)
	}
	return env
}
