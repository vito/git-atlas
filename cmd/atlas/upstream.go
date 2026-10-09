package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Upstream is a blobless, shallow bare clone of the tracked repository.
// Blobs are fetched lazily (promisor remote) when a file is read.
type Upstream struct {
	Dir   string
	URL   string
	Since string // shallow boundary date

	mu       sync.Mutex
	have     map[string]bool
	resolved map[string]string
}

func OpenUpstream(dir, url, since string) (*Upstream, error) {
	u := &Upstream{Dir: dir, URL: url, Since: since, have: map[string]bool{}, resolved: map[string]string{}}
	if _, err := os.Stat(dir + "/HEAD"); err != nil {
		cmd := exec.Command("git", "clone", "--quiet", "--bare", "--filter=blob:none",
			"--shallow-since="+since, "--no-tags", url, dir)
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("clone %s: %w", url, err)
		}
	}
	return u, nil
}

func (u *Upstream) git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(), "GIT_DIR="+u.Dir, "GIT_TERMINAL_PROMPT=0", "TZ=UTC")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// FetchBranch updates refs/heads/<branch> from the remote and returns its SHA.
func (u *Upstream) FetchBranch(branch string) (string, error) {
	if _, err := u.git("fetch", "--quiet", "--filter=blob:none", "--shallow-since="+u.Since, "--no-tags",
		"origin", "+refs/heads/"+branch+":refs/heads/"+branch); err != nil {
		return "", err
	}
	out, err := u.git("rev-parse", "refs/heads/"+branch)
	return strings.TrimSpace(out), err
}

func (u *Upstream) HasCommit(sha string) bool {
	_, err := u.git("cat-file", "-e", sha+"^{commit}")
	return err == nil
}

// EnsureCommits fetches full SHAs that are not present yet (PR heads, merge
// commits off the first-parent window).
func (u *Upstream) EnsureCommits(shas []string) error {
	var missing []string
	seen := map[string]bool{}
	for _, s := range shas {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		if len(s) == 40 && !u.HasCommit(s) {
			missing = append(missing, s)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	args := append([]string{"fetch", "--quiet", "--filter=blob:none", "--shallow-since=" + u.Since, "--no-tags", "--no-write-fetch-head", "origin"}, missing...)
	_, err := u.git(args...)
	return err
}

// Resolve expands a (possibly abbreviated) commit SHA.
func (u *Upstream) Resolve(sha string) (string, error) {
	u.mu.Lock()
	if r, ok := u.resolved[sha]; ok {
		u.mu.Unlock()
		return r, nil
	}
	u.mu.Unlock()
	out, err := u.git("rev-parse", "--verify", "--quiet", sha+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("commit %s not found in the upstream clone (fetch it, or use a full SHA)", sha)
	}
	r := strings.TrimSpace(out)
	u.mu.Lock()
	u.resolved[sha] = r
	u.mu.Unlock()
	return r, nil
}

// ReadFile returns the contents of path at sha; ok is false when the path
// does not exist there.
func (u *Upstream) ReadFile(sha, path string) ([]byte, bool, error) {
	ls, err := u.git("ls-tree", sha, "--", path)
	if err != nil {
		return nil, false, err
	}
	f := strings.Fields(ls)
	if len(f) < 3 || f[1] != "blob" {
		return nil, false, nil
	}
	var lastErr error
	for i := 0; i < 3; i++ { // lazy blob fetches can race; retry
		out, err := u.git("cat-file", "blob", f[2])
		if err == nil {
			return []byte(out), true, nil
		}
		lastErr = err
		time.Sleep(time.Duration(i+1) * 300 * time.Millisecond)
	}
	return nil, false, lastErr
}

// CommitDate is a commit's committer date (UTC, RFC 3339).
func (u *Upstream) CommitDate(sha string) (string, error) {
	out, err := u.git("log", "-1", "--date=format-local:%Y-%m-%dT%H:%M:%SZ", "--format=%cd", sha)
	return strings.TrimSpace(out), err
}

// ListDir returns the files directly in dir at sha, or nil if dir is not a
// directory there.
func (u *Upstream) ListDir(sha, dir string) []string {
	out, err := u.git("ls-tree", "--name-only", sha+":"+strings.TrimSuffix(dir, "/"))
	if err != nil {
		return nil
	}
	var fs []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l != "" {
			fs = append(fs, strings.TrimSuffix(dir, "/")+"/"+l)
		}
	}
	return fs
}

type Commit struct {
	SHA     string
	Parents []string
	Date    string // committer date, YYYY-MM-DD (UTC)
	Subject string
	Body    string
	Files   []string // changed against the first parent
}

var (
	mergeSubject  = regexp.MustCompile(`^Merge pull request #(\d+) from (\S+)`)
	squashSubject = regexp.MustCompile(`^(.*) \(#(\d+)\)$`)
)

// PR returns the PR number and title a first-parent commit merged, if any.
func (c Commit) PR() (int, string) {
	if m := mergeSubject.FindStringSubmatch(c.Subject); m != nil {
		var n int
		fmt.Sscanf(m[1], "%d", &n)
		title := strings.TrimSpace(strings.SplitN(strings.TrimSpace(c.Body), "\n", 2)[0])
		return n, title
	}
	if m := squashSubject.FindStringSubmatch(c.Subject); m != nil {
		var n int
		fmt.Sscanf(m[2], "%d", &n)
		return n, m[1]
	}
	return 0, ""
}

// FirstParentLog lists first-parent commits reachable from tip, newest
// first, with the files each changed against its first parent.
func (u *Upstream) FirstParentLog(tip string) ([]Commit, error) {
	out, err := u.git("log", "--first-parent", "--diff-merges=first-parent", "--name-only",
		"--date=format-local:%Y-%m-%dT%H:%M:%SZ", "--format=%x1e%H%x1f%P%x1f%cd%x1f%s%x1f%b%x1f", tip)
	if err != nil {
		return nil, err
	}
	var cs []Commit
	for _, rec := range strings.Split(out, "\x1e") {
		if strings.TrimSpace(rec) == "" {
			continue
		}
		parts := strings.SplitN(rec, "\x1f", 6)
		if len(parts) < 6 {
			continue
		}
		c := Commit{SHA: parts[0], Parents: strings.Fields(parts[1]), Date: parts[2], Subject: parts[3], Body: parts[4]}
		for _, l := range strings.Split(parts[5], "\n") {
			if l = strings.TrimSpace(l); l != "" {
				c.Files = append(c.Files, l)
			}
		}
		cs = append(cs, c)
	}
	return cs, nil
}

// LsRemote returns the SHA a remote ref points at, or "".
func (u *Upstream) LsRemote(ref string) string {
	out, err := u.git("ls-remote", "origin", ref)
	if err != nil {
		return ""
	}
	f := strings.Fields(out)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}
