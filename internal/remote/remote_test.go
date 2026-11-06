package remote

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestTheShapesForgesHandYou(t *testing.T) {
	for remote, want := range map[string]string{
		// The two shapes GitHub itself hands you, with and without the suffix.
		"git@github.com:johnendean/herdrkeys.git":     "https://github.com/johnendean/herdrkeys",
		"https://github.com/johnendean/herdrkeys.git": "https://github.com/johnendean/herdrkeys",
		"https://github.com/johnendean/herdrkeys":     "https://github.com/johnendean/herdrkeys",
		"git@github.com:johnendean/herdrkeys":         "https://github.com/johnendean/herdrkeys",
		// Not a GitHub feature: any host that serves the path it names.
		"git@gitlab.com:group/sub/project.git":     "https://gitlab.com/group/sub/project",
		"git@bitbucket.org:team/repo.git":          "https://bitbucket.org/team/repo",
		"https://codeberg.org/user/repo.git":       "https://codeberg.org/user/repo",
		"git@git.example.internal:infra/tools.git": "https://git.example.internal/infra/tools",
		// Every scheme that names a host.
		"ssh://git@github.com/owner/repo.git": "https://github.com/owner/repo",
		"git://github.com/owner/repo.git":     "https://github.com/owner/repo",
		// 2222 says where sshd listens and nothing about where HTTP does.
		"ssh://git@gitlab.example.com:2222/group/project.git": "https://gitlab.example.com/group/project",
		// A trailing slash is not part of the path.
		"https://github.com/owner/repo/":     "https://github.com/owner/repo",
		"https://github.com/owner/repo.git/": "https://github.com/owner/repo",
	} {
		if got := WebURL(remote); got != want {
			t.Errorf("%q = %q, want %q", remote, got, want)
		}
	}
}

func TestCredentialsNeverReachTheBrowser(t *testing.T) {
	// A remote carrying a token is ordinary. Opening it would put that token
	// in browser history, and in whatever the browser syncs.
	for _, remote := range []string{
		"https://johnendean:ghp_deadbeef@github.com/owner/repo.git",
		"https://token@github.com/owner/repo.git",
	} {
		if got := WebURL(remote); got != "https://github.com/owner/repo" || strings.Contains(got, "@") {
			t.Errorf("%q = %q", remote, got)
		}
	}
}

func TestARemoteWithNoPageIsNotAnError(t *testing.T) {
	for _, remote := range []string{
		"", "   ", "/Users/john/code/herdrkeys", "../sibling-checkout",
		"file:///Volumes/backup/repo.git", "not a remote at all", `C:\Users\john\code\repo`,
	} {
		if got := WebURL(remote); got != "" {
			t.Errorf("%q = %q: the key flashes rather than opening something wrong", remote, got)
		}
	}
}

// -- reading the remote without running git -------------------------------

const plain = "[core]\n\trepositoryformatversion = 0\n[remote \"origin\"]\n" +
	"\turl = git@github.com:johnendean/herdrkeys.git\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n" +
	"[branch \"main\"]\n\tremote = origin\n"

func makeRepo(t *testing.T, root, config string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, ".git", "config"), []byte(config), 0o644)
	return root
}

func TestTheRemoteIsReadStraightOutOfTheConfig(t *testing.T) {
	root := makeRepo(t, t.TempDir(), plain)
	if got := RemoteFromConfig(root); got != "git@github.com:johnendean/herdrkeys.git" {
		t.Fatalf("%q", got)
	}
	page := PageFromConfig(root)
	if page != "https://github.com/johnendean/herdrkeys" || Forge(page) != "github" {
		t.Fatalf("%q", page)
	}
}

func TestAPercentInAURLIsJustACharacter(t *testing.T) {
	root := makeRepo(t, t.TempDir(), "[remote \"origin\"]\n\turl = https://host/o/r%20x.git\n")
	if got := RemoteFromConfig(root); got != "https://host/o/r%20x.git" {
		t.Fatalf("%q", got)
	}
}

func TestAConfigWithCRLFLineEndingsIsRead(t *testing.T) {
	// A Windows checkout's config can carry them.
	root := makeRepo(t, t.TempDir(), strings.ReplaceAll(plain, "\n", "\r\n"))
	if got := RemoteFromConfig(root); got != "git@github.com:johnendean/herdrkeys.git" {
		t.Fatalf("%q", got)
	}
}

func TestASubdirectoryFindsTheRepositoryAboveIt(t *testing.T) {
	root := makeRepo(t, t.TempDir(), plain)
	deep := filepath.Join(root, "src", "herdrkeys")
	os.MkdirAll(deep, 0o755)
	if PageFromConfig(deep) == "" {
		t.Fatal("an agent is rarely sitting in the repository root")
	}
}

func TestCommentsAndOtherRemotesAreNotMistakenForOrigin(t *testing.T) {
	root := makeRepo(t, t.TempDir(), "# url = https://wrong/one.git\n"+
		"[remote \"upstream\"]\n\turl = git@github.com:someone/else.git\n"+
		"[remote \"origin\"]\n\turl = git@github.com:right/one.git\n")
	if got := RemoteFromConfig(root); got != "git@github.com:right/one.git" {
		t.Fatalf("%q", got)
	}
	if RemoteFromConfig(makeRepo(t, t.TempDir(), "[remote \"upstream\"]\n\turl = x:y/z\n")) != "" {
		t.Fatal("no origin, no page")
	}
}

func worktree(t *testing.T) (main, checkout string) {
	t.Helper()
	root := t.TempDir()
	main = makeRepo(t, filepath.Join(root, "main"), plain)
	os.WriteFile(filepath.Join(main, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)
	wt := filepath.Join(main, ".git", "worktrees", "feature")
	os.MkdirAll(wt, 0o755)
	os.WriteFile(filepath.Join(wt, "commondir"), []byte("../..\n"), 0o644)
	os.WriteFile(filepath.Join(wt, "HEAD"), []byte("ref: refs/heads/feature\n"), 0o644)
	checkout = filepath.Join(root, "feature")
	os.MkdirAll(checkout, 0o755)
	os.WriteFile(filepath.Join(checkout, ".git"), []byte("gitdir: "+wt+"\n"), 0o644)
	return main, checkout
}

func TestAWorktreeFindsTheConfigItSharesAndReadsItsOwnHead(t *testing.T) {
	// Herdr creates worktrees itself, so `.git` being a file rather than a
	// directory is ordinary here and must not read as "not a repository".
	main, checkout := worktree(t)
	if got := RemoteFromConfig(checkout); got != "git@github.com:johnendean/herdrkeys.git" {
		t.Fatalf("remote = %q", got)
	}
	if CurrentBranch(checkout) != "feature" || CurrentBranch(main) != "main" {
		t.Fatalf("branches = %q, %q", CurrentBranch(checkout), CurrentBranch(main))
	}
}

func TestOutsideARepositoryThereIsNothing(t *testing.T) {
	dir := t.TempDir()
	if RemoteFromConfig(dir) != "" || PageFromConfig(dir) != "" || CurrentBranch(dir) != "" {
		t.Fatal("not a repository")
	}
	if RemoteFromConfig("") != "" || PageFor("") != "" || CurrentBranch("") != "" {
		t.Fatal("no directory at all")
	}
}

func TestTheBranchIsReadFromHead(t *testing.T) {
	root := makeRepo(t, t.TempDir(), plain)
	os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("ref: refs/heads/feature/thing\n"), 0o644)
	os.MkdirAll(filepath.Join(root, "sub"), 0o755)
	if CurrentBranch(filepath.Join(root, "sub")) != "feature/thing" {
		t.Fatal("from anywhere inside")
	}
	os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("1f2e3d4c5b6a79881f2e3d4c5b6a79881f2e3d4c\n"), 0o644)
	if CurrentBranch(root) != "" {
		t.Fatal("a detached HEAD is no branch")
	}
}

func fakeRun(t *testing.T, out string, err error) *[][]string {
	t.Helper()
	var calls [][]string
	saved := Run
	Run = func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		if name == "gh" && !slices.Contains(env, "GH_PROMPT_DISABLED=1") {
			t.Error("gh must never stop to ask")
		}
		return []byte(out), err
	}
	t.Cleanup(func() { Run = saved })
	return &calls
}

func TestGitIsAskedOnlyWhenTheConfigSaysNothing(t *testing.T) {
	calls := fakeRun(t, "git@github.com:fallback/found.git\n", nil)
	root := t.TempDir()
	inside := makeRepo(t, filepath.Join(root, "checkout"), plain)
	if PageFor(inside) != "https://github.com/johnendean/herdrkeys" || len(*calls) != 0 {
		t.Fatalf("the config answered, so git was not run: %v", *calls)
	}
	elsewhere := filepath.Join(root, "elsewhere")
	os.MkdirAll(elsewhere, 0o755)
	if got := PageFor(elsewhere); got != "https://github.com/fallback/found" || len(*calls) != 1 {
		t.Fatalf("%q, %v: the config said nothing, so git was asked", got, *calls)
	}
}

func TestARealRepositoryResolvesEndToEnd(t *testing.T) {
	// This checkout has an origin on GitHub, so the whole path can be
	// exercised against something real.
	if url := PageFor("."); !strings.HasPrefix(url, "https://") || !strings.HasSuffix(url, "/herdrkeys") {
		t.Fatalf("%q", url)
	}
}

// -- where a press goes ----------------------------------------------------

const page = "https://github.com/johnendean/herdrkeys"

func pull(n, head string) PullRequest { return PullRequest{URL: page + "/pull/" + n, Head: head} }

func TestWhereAPressGoes(t *testing.T) {
	fork := PullRequest{URL: page + "/pull/4", Head: "main", FromFork: true}
	for _, c := range []struct {
		why    string
		branch string
		pulls  []PullRequest
		want   string
	}{
		{"the branch's own pull request wins", "feature", []PullRequest{pull("7", "other"), pull("9", "feature")}, page + "/pull/9"},
		{"another branch's, even one, is not this agent's work", "feature", []PullRequest{pull("7", "other")}, page + "/pulls"},
		{"a fork's branch of the same name is not ours", "main", []PullRequest{fork}, page + "/pulls"},
		{"no open pull requests means the page", "feature", []PullRequest{}, page},
		{"not knowing means the page", "feature", nil, page},
		{"a detached head has no pull request of its own", "", []PullRequest{pull("7", "other")}, page + "/pulls"},
	} {
		if got := Destination(page, c.branch, c.pulls); got != c.want {
			t.Errorf("%s: %q", c.why, got)
		}
	}
}

func TestGhIsAskedAboutThePagesRepository(t *testing.T) {
	calls := fakeRun(t, `[{"url": "`+page+`/pull/9", "headRefName": "feature", "isCrossRepository": false},
		{"url": "`+page+`/pull/4", "headRefName": "main", "isCrossRepository": true}]`, nil)
	pulls, err := OpenPullRequests(page)
	want := []PullRequest{pull("9", "feature"), {URL: page + "/pull/4", Head: "main", FromFork: true}}
	if err != nil || !slices.Equal(pulls, want) {
		t.Fatalf("%v %v", pulls, err)
	}
	argv := (*calls)[0]
	if !slices.Equal(argv[:3], []string{"gh", "pr", "list"}) {
		t.Fatalf("%v", argv)
	}
	if argv[slices.Index(argv, "--repo")+1] != "github.com/johnendean/herdrkeys" ||
		argv[slices.Index(argv, "--state")+1] != "open" {
		t.Fatalf("%v: drafts are open, so they count", argv)
	}
}

func TestEveryWayGhCanFailIsNotKnowing(t *testing.T) {
	for _, c := range []struct {
		out string
		err error
	}{
		{"", errors.New("exit status 1")}, // not logged in, not GitHub, no gh at all, too slow
		{"not json", nil},
		{`[{"url": "x"}]`, nil}, // a shape that was not asked for
	} {
		fakeRun(t, c.out, c.err)
		if _, err := OpenPullRequests(page); err == nil {
			t.Errorf("%q, %v counted as knowing", c.out, c.err)
		}
	}
}

func TestOnlyAForgesOwnPublicHostIsThatForge(t *testing.T) {
	for p, want := range map[string]string{
		"https://github.com/a/b": "github", "https://gitlab.com/group/sub/project": "gitlab",
		"https://bitbucket.org/team/repo": "bitbucket", "https://codeberg.org/someone/thing": "codeberg",
		"https://GitHub.com/a/b": "github",
		// Probably GitLab, and probably GitHub Enterprise -- but a wrong logo
		// is worse than none.
		"https://gitlab.mycorp.com/team/repo": "", "https://github.mycorp.com/team/repo": "",
		"https://notgithub.com/a/b": "", "https://github.com.example.net/a/b": "",
		"https://gist.github.com/a/b": "", "": "",
	} {
		if got := Forge(p); got != want {
			t.Errorf("%q = %q, want %q", p, got, want)
		}
	}
}

func TestGhIsFoundWhereItIsInstalledEvenOffThePath(t *testing.T) {
	// The daemon runs with Herdr's PATH, which on the first Windows machine
	// it ran on did not include GitHub CLI's directory -- so every press
	// opened the page and never the pull request.
	want := Find("gh")
	if want == "gh" {
		t.Skip("gh is not installed here")
	}
	t.Setenv("PATH", t.TempDir())
	if got := Find("gh"); got != want {
		t.Fatalf("found %q off the path, want %q", got, want)
	}
}
