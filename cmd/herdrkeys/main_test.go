package main

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/johnendean/herdrkeys/internal/chord"
)

func TestVersionMatchesTheManifest(t *testing.T) {
	data, err := os.ReadFile("../../herdr-plugin.toml")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^version\s*=\s*"([^"]+)"`).FindSubmatch(data)
	if m == nil || string(m[1]) != Version {
		t.Fatalf("manifest version %q, binary %q: the install fetches the release the manifest names", m, Version)
	}
}

func TestEveryCommandTheManifestRunsExists(t *testing.T) {
	data, err := os.ReadFile("../../herdr-plugin.toml")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range regexp.MustCompile(`"\./bin/herdrkeys", "([a-z-]+)"`).FindAllSubmatch(data, -1) {
		if !strings.Contains(usageText, "  "+string(m[1])+" ") {
			t.Errorf("the manifest runs %q, which herdrkeys does not have", m[1])
		}
	}
}

func TestTheVersionCommandSaysWhichBuildThisIs(t *testing.T) {
	var out bytes.Buffer
	if code := cli([]string{"version"}, &out, &out); code != 0 || out.String() != "herdrkeys "+Version+"\n" {
		t.Fatalf("%d %q", code, out.String())
	}
	if code := cli([]string{"nonsense"}, &out, &out); code != 2 {
		t.Fatal("an unknown command is a usage error")
	}
}

func TestStartLeavesTheSessionBehind(t *testing.T) {
	// The daemon must not be bound to whichever session's server ran the
	// hook: there is one set of boards, so there is one daemon, on the
	// default session unless config says otherwise.
	env := withoutSession([]string{"PATH=/bin", "HERDR_SOCKET_PATH=/x.sock", "herdr_session=work", "HOME=/h"})
	if strings.Join(env, " ") != "PATH=/bin HOME=/h" {
		t.Fatalf("%v", env)
	}
}

func TestFlowsBindingIsCheckedAgainstTheChord(t *testing.T) {
	c, _ := chord.Parse("ctrl+win")
	if !(flowKeys{int(c[1].VK), int(c[0].VK)}).matches(c) && !(flowKeys{int(c[1].MacCode), int(c[0].MacCode)}).matches(c) {
		t.Fatal("the same keys in another order are the same binding")
	}
	if (flowKeys{4100}).matches(c) {
		t.Fatal("a mouse button is not a chord")
	}
	if got := (flowKeys{4100}).describe(); !strings.Contains(got, "mouse button") {
		t.Fatalf("%q", got)
	}
}
