// Package remote turns the git remote of a pane's directory into somewhere
// you can go.
//
// Two halves, deliberately separated. WebURL and Destination are pure and
// hold every decision worth arguing about, so they are tested exhaustively and
// need no git, no network and no repository. The git and gh calls and
// OpenURL are the I/O, and are as thin as they can be.
//
// The translation is host-agnostic on purpose. GitHub, GitLab, Bitbucket,
// Codeberg and most self-hosted forges all serve a repository at the same path
// the remote names, so special-casing one host buys nothing and means the key
// silently does nothing the first time it meets another.
package remote

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// git@host:owner/repo -- the scp-like syntax, which is not a URL and so cannot
// be parsed as one. The path is everything after the colon.
var scpLike = regexp.MustCompile(`^(?:[^@/]+@)?([^:/@]+):([^/].*)$`)

var schemeURL = regexp.MustCompile(`(?i)^([a-z][a-z0-9+.-]*)://(?:[^@/]*@)?([^/:]+)(?::\d+)?(/.*)?$`)

// Schemes that name a host a page can be served from. `file://` deliberately
// absent: a local clone has no web page.
var webSchemes = map[string]bool{"https": true, "http": true, "ssh": true, "git": true}

// WebURL is the web page for a git remote, or "" if it does not name one.
//
// "" is a real answer, not a failure: a local-path remote, or no remote at
// all, means there is nothing to open, and the caller says so with a flash.
func WebURL(remote string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return ""
	}
	var host, path string
	if m := schemeURL.FindStringSubmatch(remote); m != nil {
		if !webSchemes[strings.ToLower(m[1])] {
			return "" // file://, and anything else that is not a web host
		}
		// The port is dropped rather than carried over. An ssh remote on
		// 2222 says nothing about which port serves HTTP, and guessing wrong
		// is worse than landing on the default.
		host, path = m[2], m[3]
	} else if m := scpLike.FindStringSubmatch(remote); m != nil {
		if len(m[1]) == 1 {
			// `C:\code\repo` is a Windows path, not host `C`: git itself reads
			// a one-letter "host" as a drive letter, so a local clone.
			return ""
		}
		host, path = m[1], "/"+m[2]
	} else {
		return "" // a bare local path, or something unrecognised
	}
	// Credentials in a remote are common and must never reach a browser: a
	// personal access token in a URL would land in history, and in whatever
	// the browser syncs. The userinfo is matched only to be discarded.
	path = strings.TrimRight(path, "/")
	path = strings.TrimSuffix(path, ".git")
	path = strings.TrimRight(path, "/")
	if host == "" || path == "" {
		return ""
	}
	return "https://" + host + path
}

// -- reading the remote without running git --------------------------------
//
// `git remote get-url` costs 14ms; reading `.git/config` costs 0.02ms. That
// ratio is what decides where each is used. The key's colour is recomputed on
// every frame, so it reads the file; a key press can afford the subprocess.

// gitDir is the `.git` directory governing a path, or "".
func gitDir(cwd string) string {
	start, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(start); err == nil {
		start = resolved
	}
	for dir := start; ; {
		candidate := filepath.Join(dir, ".git")
		info, err := os.Stat(candidate)
		if err == nil {
			if info.IsDir() {
				return candidate
			}
			// A worktree or a submodule: `.git` is a file naming the real
			// directory. Both are ordinary here -- Herdr creates worktrees
			// itself -- so neither can be treated as "not a repository".
			return gitDirFromFile(candidate)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func gitDirFromFile(path string) string {
	text, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(text), "\n") {
		line = strings.TrimSpace(line)
		if target, ok := strings.CutPrefix(line, "gitdir:"); ok {
			target = strings.TrimSpace(target)
			if target == "" {
				return ""
			}
			if filepath.IsAbs(target) {
				return target
			}
			return filepath.Join(filepath.Dir(path), target)
		}
	}
	return ""
}

// configPath is where a git directory keeps its config.
//
// A worktree's own directory holds no config: it points at the repository
// they share through `commondir`, and that is where the remotes live.
func configPath(dir string) string {
	if raw, err := os.ReadFile(filepath.Join(dir, "commondir")); err == nil {
		if target := strings.TrimSpace(string(raw)); target != "" {
			if !filepath.IsAbs(target) {
				target = filepath.Join(dir, target)
			}
			return filepath.Join(target, "config")
		}
	}
	return filepath.Join(dir, "config")
}

// remoteIn pulls one remote's url out of a git config file.
//
// Hand-rolled because git indents its keys with tabs and a `%` in a URL is
// just a character; general INI parsers get both wrong.
func remoteIn(text, remote string) string {
	wanted := `remote "` + remote + `"`
	section := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if section != wanted || !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(key), "url") {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// RemoteFromConfig is a directory's `origin`, read from `.git/config`. No
// subprocess.
//
// Understands plain configuration and nothing more: git's `include.path` and
// `url.<base>.insteadOf` are not followed. That is a deliberate floor, not an
// oversight -- see PageFor, which asks git itself before concluding there is
// nothing here.
func RemoteFromConfig(cwd string) string {
	if cwd == "" {
		return ""
	}
	dir := gitDir(cwd)
	if dir == "" {
		return ""
	}
	text, err := os.ReadFile(configPath(dir))
	if err != nil {
		return ""
	}
	return remoteIn(string(text), "origin")
}

// PageFromConfig is this directory's page, cheaply enough to ask on every
// frame.
//
// Answers from the config file alone. It can therefore say no where a press
// would find something, and the key under-promises rather than over-promises:
// a dark key that turns out to work is a much smaller betrayal than a lit key
// that does nothing.
func PageFromConfig(cwd string) string { return WebURL(RemoteFromConfig(cwd)) }

// forgeHosts are each forge's own public host, and nothing else.
// `gitlab.mycorp.com` is probably GitLab and `github.mycorp.com` probably
// GitHub Enterprise, but a key wearing the wrong logo is worse than one wearing
// none (ADR 0018).
var forgeHosts = map[string]string{
	"github.com":    "github",
	"gitlab.com":    "gitlab",
	"bitbucket.org": "bitbucket",
	"codeberg.org":  "codeberg",
}

// Forge is the forge a page is on, or "" when there is no page or its host is
// not a forge's own public one.
func Forge(page string) string {
	m := schemeURL.FindStringSubmatch(page)
	if m == nil {
		return ""
	}
	return forgeHosts[strings.ToLower(m[2])]
}

// Runner runs a command and returns its output, failing on a non-zero exit.
// A variable so tests can stand in for git and gh.
type Runner func(ctx context.Context, env []string, name string, args ...string) ([]byte, error)

// Run is the real Runner.
var Run Runner = func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	cmd.Stdin = nil
	hideWindow(cmd)
	return cmd.Output()
}

// remoteURL is a directory's `origin` as git itself sees it, or "".
//
// Every failure is the same answer -- not a repository, no such remote, git
// missing, git hanging -- because the key does the same thing in all of them.
func remoteURL(cwd string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := Run(ctx, nil, "git", "-C", cwd, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// PageFor is the page to open for a directory, or "".
//
// The config file first, because it is free and is what the key's colour was
// decided from, so the two agree in every ordinary case. git itself only when
// that finds nothing: it understands includes and `insteadOf` rewrites that
// the parser does not, and 14ms is nothing on a key press. Pressing must
// never be less capable than it was before the key had a colour.
func PageFor(cwd string) string {
	if cwd == "" {
		return ""
	}
	found := RemoteFromConfig(cwd)
	if found == "" {
		found = remoteURL(cwd)
	}
	return WebURL(found)
}

// -- pull requests ---------------------------------------------------------
//
// Asked of `gh`, on a press and never on a frame: it is a network round trip
// of half a second on a good day. Everything here degrades to the page -- no
// `gh`, not logged in, not GitHub, offline, slow -- so the key is never worse
// than it was before it knew about pull requests. See ADR 0013.

// PullRequest is an open pull request.
type PullRequest struct {
	URL  string
	Head string // the branch it was opened from
	// Opened from someone's fork. Its Head names a branch in their
	// repository, which shares nothing with ours but the name -- and forks
	// are full of `main`s.
	FromFork bool
}

// CurrentBranch is the branch a directory has checked out, read from HEAD.
// No subprocess.
//
// "" on a detached HEAD, which is an answer rather than a failure: such a
// checkout has no branch, so it has no pull request of its own.
func CurrentBranch(cwd string) string {
	if cwd == "" {
		return ""
	}
	dir := gitDir(cwd)
	if dir == "" {
		return ""
	}
	head, err := os.ReadFile(filepath.Join(dir, "HEAD"))
	if err != nil {
		return ""
	}
	branch, _ := strings.CutPrefix(strings.TrimSpace(string(head)), "ref: refs/heads/")
	if branch == strings.TrimSpace(string(head)) {
		return ""
	}
	return branch
}

// OpenPullRequests is every open pull request on the repository at page,
// drafts included. False means "could not find out", which the caller
// treats exactly as "there are none".
//
// The repository is named outright from the page rather than left for `gh`
// to infer from the checkout's remotes, so the pull requests are always those
// of the repository the page is -- and `gh` never stops to ask which remote
// is meant.
func OpenPullRequests(page string) ([]PullRequest, bool) {
	repo := page
	if _, after, ok := strings.Cut(page, "://"); ok {
		repo = after
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	env := append(os.Environ(), "GH_PROMPT_DISABLED=1", "NO_COLOR=1")
	out, err := Run(ctx, env, "gh", "pr", "list", "--repo", repo, "--state", "open",
		"--limit", "100", "--json", "url,headRefName,isCrossRepository")
	if err != nil {
		return nil, false
	}
	var rows []struct {
		URL      *string `json:"url"`
		Head     *string `json:"headRefName"`
		FromFork *bool   `json:"isCrossRepository"`
	}
	if json.Unmarshal(out, &rows) != nil {
		return nil, false
	}
	pulls := make([]PullRequest, 0, len(rows))
	for _, row := range rows {
		if row.URL == nil || row.Head == nil || row.FromFork == nil {
			return nil, false // a shape that was not asked for
		}
		pulls = append(pulls, PullRequest{URL: *row.URL, Head: *row.Head, FromFork: *row.FromFork})
	}
	return pulls, true
}

// Destination is where a press on the git key goes: the most specific place
// there is.
//
// The branch's own pull request; failing that, the list of open ones; failing
// that, the page. A pull request for some other branch is never opened on its
// own, however few others there are -- it is not this agent's work. Nor is
// one from a fork that happens to use the same branch name.
//
// Only the first hundred open pull requests are seen. Past that, the agent's
// own can be missed and the list is opened instead, which is the right page
// to be on anyway.
func Destination(page, branch string, pulls []PullRequest) string {
	if len(pulls) == 0 {
		return page
	}
	for _, pull := range pulls {
		if branch != "" && pull.Head == branch && !pull.FromFork {
			return pull.URL
		}
	}
	return page + "/pulls"
}
