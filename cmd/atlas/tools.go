package main

// Agent-facing helpers: tracked-diff and find-symbol. They read the same
// upstream clone as generate and check, and print text for a model to read.

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// prCommits finds what a PR changed: on the branch, its merge commit against
// the first parent; otherwise its head against the merge base with the branch.
func prCommits(d *Data, u *Upstream, prev *Derived, n int) (base, head, label string, err error) {
	tip, err := u.FetchBranch(d.Config.Branch)
	if err != nil {
		return "", "", "", err
	}
	log, err := u.FirstParentLog(tip)
	if err != nil {
		return "", "", "", err
	}
	for _, c := range log {
		if m, title := c.PR(); m == n {
			if len(c.Parents) == 0 {
				return "", "", "", fmt.Errorf("#%d: merge commit %s has no parent in the clone", n, short(c.SHA))
			}
			return c.Parents[0], c.SHA, fmt.Sprintf("#%d %s\nmerged %s as %s (diff against its first parent %s)", n, title, day(c.Date), short(c.SHA), short(c.Parents[0])), nil
		}
	}
	head = u.LsRemote(fmt.Sprintf("refs/pull/%d/head", n))
	if head == "" {
		return "", "", "", fmt.Errorf("#%d is not on %s's first-parent history since the clone's horizon, and has no refs/pull/%d/head", n, d.Config.Branch, n)
	}
	if err := u.EnsureCommits([]string{head}); err != nil {
		return "", "", "", err
	}
	mb, err := u.git("merge-base", tip, head)
	if err != nil {
		return "", "", "", fmt.Errorf("#%d: no merge base between %s and head %s in the shallow clone: %w", n, short(tip), short(head), err)
	}
	base = strings.TrimSpace(mb)
	title := prev.PRs[strconv.Itoa(n)].Title
	return base, head, fmt.Sprintf("#%d %s\nnot on %s: head %s (diff against the merge base %s). Pin tabs with sha: %s", n, title, d.Config.Branch, short(head), short(base), head), nil
}

// pathspecs turns globs into git pathspecs.
func pathspecs(include, exclude []string) []string {
	var out []string
	for _, g := range include {
		out = append(out, ":(glob)"+g)
	}
	for _, g := range exclude {
		out = append(out, ":(glob,exclude)"+g)
	}
	return out
}

func trackedDiff(d *Data, u *Upstream, prev *Derived, n int, paths []string, statOnly bool, maxLines int) (string, error) {
	base, head, label, err := prCommits(d, u, prev, n)
	if err != nil {
		return "", err
	}
	include := d.Config.Tracked
	if len(paths) > 0 {
		include = paths
	}
	spec := pathspecs(include, d.Config.Ignore)
	stat, err := u.git(append([]string{"diff", "--stat=120", base, head, "--"}, spec...)...)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\npaths: %s (minus %s)\n\n", label, strings.Join(include, " "), strings.Join(d.Config.Ignore, " "))
	if strings.TrimSpace(stat) == "" {
		b.WriteString("No changes under these paths.\n")
		return b.String(), nil
	}
	b.WriteString(stat)
	if statOnly {
		return b.String(), nil
	}
	patch, err := u.git(append([]string{"diff", "--no-color", base, head, "--"}, spec...)...)
	if err != nil {
		return "", err
	}
	lines := strings.Split(patch, "\n")
	b.WriteString("\n")
	if maxLines > 0 && len(lines) > maxLines {
		b.WriteString(strings.Join(lines[:maxLines], "\n"))
		fmt.Fprintf(&b, "\n\n... %d more lines. Narrow with paths, or read files at %s.\n", len(lines)-maxLines, short(head))
		return b.String(), nil
	}
	b.WriteString(patch)
	return b.String(), nil
}

// Default scope of find-symbol: the Go code a git pointer plausibly names.
var symbolScope = []string{"core/**", "engine/**", "dagql/**", "util/**", "cmd/**"}

var receiverRe = regexp.MustCompile(`^func \([A-Za-z0-9_]*\s*\*?([A-Za-z0-9_]+)`)

// findSymbol lists where name is defined at commit, as ready-made pointers.
// name is "Func", "Type.method", or a type/var/const.
func findSymbol(u *Upstream, name, commit string, scope []string) (string, error) {
	if len(scope) == 0 {
		scope = symbolScope
	}
	full, err := u.Resolve(commit)
	if err != nil {
		if err := u.EnsureCommits([]string{commit}); err == nil {
			full, err = u.Resolve(commit)
		}
		if err != nil {
			return "", err
		}
	}
	// Prefetch the Go blobs in scope in one round trip; git grep would
	// otherwise fetch them lazily, one by one.
	m := newMatcher(scope, []string{"**/*_test.go", "**/testdata/**"})
	ls, err := u.git("ls-tree", "-r", full)
	if err != nil {
		return "", err
	}
	var oids, files []string
	for _, l := range strings.Split(ls, "\n") {
		meta, path, ok := strings.Cut(l, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) < 3 || f[1] != "blob" || !strings.HasSuffix(path, ".go") || !m.Match(path) {
			continue
		}
		files = append(files, path)
		oids = append(oids, f[2])
	}
	if err := u.prefetchBlobs(oids); err != nil {
		return "", err
	}
	q := regexp.QuoteMeta
	var pats []string
	recv, meth, isMethod := strings.Cut(name, ".")
	if isMethod {
		pats = []string{`^func \([A-Za-z0-9_]*[[:space:]]*\*?` + q(recv) + `(\[[^]]*\])?\) ` + q(meth) + `[[(]`}
	} else {
		pats = []string{
			`^func ` + q(name) + `[[(]`,
			`^func \([^)]*\) ` + q(name) + `[[(]`,
			`^(type|var|const) ` + q(name) + `([^A-Za-z0-9_]|$)`,
			`^[[:space:]]+` + q(name) + `[[:space:]]+(=|[*[A-Za-z])`,
		}
	}
	args := []string{"grep", "-n", "-E"}
	for _, p := range pats {
		args = append(args, "-e", p)
	}
	args = append(args, full, "--")
	args = append(args, files...)
	out, err := u.git(args...)
	if err != nil && strings.TrimSpace(out) != "" {
		return "", err
	}
	var b strings.Builder
	hits := 0
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		// <sha>:<path>:<line>:<text>
		rest := strings.TrimPrefix(sc.Text(), full+":")
		path, rest, _ := strings.Cut(rest, ":")
		line, text, _ := strings.Cut(rest, ":")
		sym := name
		if !isMethod {
			if mm := receiverRe.FindStringSubmatch(text); mm != nil {
				sym = mm[1] + "." + name
			}
		}
		hits++
		fmt.Fprintf(&b, "%s:%s@%s   (line %s: %s)\n", path, sym, short(full), line, strings.TrimSpace(text))
	}
	if hits == 0 {
		return fmt.Sprintf("%s is not defined in %s at %s (searched %d Go files, tests excluded).\n", name, strings.Join(scope, " "), short(full), len(files)), nil
	}
	return fmt.Sprintf("%d definition(s) of %s at %s:\n%s", hits, name, short(full), b.String()), nil
}

// prefetchBlobs fetches the blobs the clone lacks, in one request.
func (u *Upstream) prefetchBlobs(oids []string) error {
	if len(oids) == 0 {
		return nil
	}
	cmd := []string{"cat-file", "--batch-check=%(objectname)", "--batch-all-objects"}
	have := map[string]bool{}
	if out, err := u.git(cmd...); err == nil {
		for _, l := range strings.Split(out, "\n") {
			have[strings.TrimSpace(l)] = true
		}
	}
	var missing []string
	for _, o := range oids {
		if !have[o] {
			missing = append(missing, o)
		}
	}
	for len(missing) > 0 {
		batch := missing
		if len(batch) > 2000 {
			batch = batch[:2000]
		}
		missing = missing[len(batch):]
		args := append([]string{"-c", "fetch.negotiationAlgorithm=noop", "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "--filter=blob:none", "origin"}, batch...)
		if _, err := u.git(args...); err != nil {
			return fmt.Errorf("prefetching %d blobs: %w", len(batch), err)
		}
	}
	return nil
}

func toolMain(cmd string, args []string) {
	fs := newFlags(cmd)
	root := fs.String("root", ".", "repository root")
	up := fs.String("upstream", "", "upstream clone directory (default <root>/tmp/upstream.git)")
	statOnly := fs.Bool("stat", false, "tracked-diff: only the diffstat")
	maxLines := fs.Int("max-lines", 2000, "tracked-diff: truncate the patch (0 = no limit)")
	paths := fs.String("paths", "", "comma-separated globs replacing the default paths")
	fs.Parse(args)
	if *up == "" {
		*up = *root + "/tmp/upstream.git"
	}
	var globs []string
	for _, p := range strings.Split(*paths, ",") {
		if p = strings.TrimSpace(p); p != "" {
			globs = append(globs, p)
		}
	}
	fail := func(err error) {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	d, err := LoadData(*root)
	if err != nil {
		fail(err)
	}
	prev, err := LoadDerived(*root)
	if err != nil {
		fail(err)
	}
	u, err := OpenUpstream(*up, d.Config.Upstream, cloneSince(d))
	if err != nil {
		fail(err)
	}
	var out string
	switch cmd {
	case "tracked-diff":
		if fs.NArg() != 1 {
			fail(fmt.Errorf("usage: atlas tracked-diff [-stat] [-paths globs] <pr>"))
		}
		n, err := strconv.Atoi(strings.TrimPrefix(fs.Arg(0), "#"))
		if err != nil {
			fail(fmt.Errorf("PR must be a number: %q", fs.Arg(0)))
		}
		out, err = trackedDiff(d, u, prev, n, globs, *statOnly, *maxLines)
		if err != nil {
			fail(err)
		}
	case "find-symbol":
		if fs.NArg() != 2 {
			fail(fmt.Errorf("usage: atlas find-symbol [-paths globs] <name> <commit>"))
		}
		out, err = findSymbol(u, fs.Arg(0), fs.Arg(1), globs)
		if err != nil {
			fail(err)
		}
	}
	fmt.Print(out)
}
