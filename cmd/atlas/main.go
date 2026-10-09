// Command atlas derives, validates and compiles the git atlas.
//
//	atlas generate [-refresh]   rewrite derived/, QUEUE.md and index.html
//	atlas check <name>          validate | fresh | coverage
//
// Both read the hand-written data under data/ and atlas.yaml. The upstream
// repository is cloned (blobless, shallow) into -upstream on first use.
// GITHUB_TOKEN, when set, lets -refresh read unmerged PRs from the API.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type outputs map[string][]byte

type build struct {
	data     *Data
	derived  *Derived
	out      outputs
	problems []string // pointer resolution problems
	colored  []string
}

func warnf(f string, args ...any) { fmt.Fprintf(os.Stderr, "warning: "+f+"\n", args...) }

func run(root, upstreamDir string, refresh bool) (*build, error) {
	d, err := LoadData(root)
	if err != nil {
		return nil, err
	}
	b := &build{data: d, out: outputs{}}
	b.colored = AssignColors(d)
	for _, id := range b.colored {
		path := filepath.Join("data", "prs", id+".yaml")
		orig, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			return nil, err
		}
		s := string(orig)
		if !strings.HasSuffix(s, "\n") {
			s += "\n"
		}
		b.out[path] = []byte(s + fmt.Sprintf("color: %q\n", d.PRs[id].Color))
	}
	prev, err := LoadDerived(root)
	if err != nil {
		return nil, err
	}
	cutoff, err := time.Parse("2006-01-02", d.Config.Cutoff.Date)
	if err != nil {
		return nil, fmt.Errorf("atlas.yaml: cutoff.date: %w", err)
	}
	since := cutoff.AddDate(0, 0, -30).Format("2006-01-02")
	u, err := OpenUpstream(upstreamDir, d.Config.Upstream, since)
	if err != nil {
		return nil, err
	}
	var gh *GitHub
	if tok := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); tok != "" && refresh {
		gh = &GitHub{Token: tok, Repo: d.Config.GitHubRepo}
	} else if refresh {
		warnf("no GITHUB_TOKEN: unmerged PRs keep their previous title/state; heads come from ls-remote")
	}
	der, err := Derive(d, u, prev, refresh, gh, warnf)
	if err != nil {
		return nil, err
	}
	b.derived = der
	if err := u.EnsureCommits(pinnedCommits(d, der)); err != nil {
		warnf("fetching pinned commits: %v", err)
	}
	res := NewResolver(u, d.Config.GitHubRepo)
	res.Prefetch(pointerPairs(d, der, u))
	sd, problems := BuildSite(d, der, res)
	b.problems = problems
	tpl, err := os.ReadFile(filepath.Join(root, "site", "template.html"))
	if err != nil {
		return nil, err
	}
	html, err := RenderHTML(string(tpl), sd)
	if err != nil {
		return nil, err
	}
	b.out["index.html"] = html
	b.out["QUEUE.md"] = RenderQueue(d, der)
	dj, err := RenderDerived(der)
	if err != nil {
		return nil, err
	}
	b.out[filepath.Join("derived", "upstream.json")] = dj
	return b, nil
}

func (o outputs) paths() []string {
	var ps []string
	for p := range o {
		ps = append(ps, p)
	}
	sort.Strings(ps)
	return ps
}

func main() {
	fs := flag.NewFlagSet("atlas", flag.ExitOnError)
	root := fs.String("root", ".", "repository root")
	up := fs.String("upstream", "", "upstream clone directory (default <root>/tmp/upstream.git)")
	refresh := fs.Bool("refresh", false, "generate: fetch the branch tip and re-read unmerged PRs")
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: atlas generate [-refresh] | atlas check validate|fresh|coverage")
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	sub := ""
	if cmd == "check" {
		if len(args) == 0 {
			fmt.Fprintln(os.Stderr, "usage: atlas check validate|fresh|coverage")
			os.Exit(2)
		}
		sub, args = args[0], args[1:]
	}
	fs.Parse(args)
	if *up == "" {
		*up = filepath.Join(*root, "tmp", "upstream.git")
	}
	b, err := run(*root, *up, cmd == "generate" && *refresh)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	switch cmd {
	case "generate":
		for _, p := range b.out.paths() {
			full := filepath.Join(*root, p)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(1)
			}
			if err := os.WriteFile(full, b.out[p], 0o644); err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(1)
			}
		}
		for _, id := range b.colored {
			fmt.Printf("assigned %s to #%s\n", b.data.PRs[id].Color, id)
		}
		for _, p := range b.problems {
			warnf("%s", p)
		}
		blocking := 0
		for _, q := range b.derived.Queue {
			if q.Blocking {
				blocking++
			}
		}
		fmt.Printf("upstream %s @ %s; %d PRs; queue: %d blocking, %d advisory (QUEUE.md)\n",
			b.derived.Upstream.Branch, short(b.derived.Upstream.SHA), len(b.data.PRs), blocking, len(b.derived.Queue)-blocking)
	case "check":
		var fails []string
		switch sub {
		case "validate":
			fails = append(Validate(b.data, b.derived), b.problems...)
		case "fresh":
			for _, p := range b.out.paths() {
				cur, err := os.ReadFile(filepath.Join(*root, p))
				if err != nil || !bytes.Equal(cur, b.out[p]) {
					fails = append(fails, fmt.Sprintf("%s is stale: run dagger generate and commit the result", p))
				}
			}
		case "coverage":
			for _, q := range b.derived.Queue {
				if q.Blocking {
					fails = append(fails, fmt.Sprintf("%s #%d %s %s", q.Kind, q.PR, q.Title, q.Detail))
				}
			}
			if len(fails) > 0 {
				fails = append(fails, "see QUEUE.md and README.md \"Reading the queue\"")
			}
		default:
			fmt.Fprintln(os.Stderr, "unknown check", sub)
			os.Exit(2)
		}
		for _, f := range fails {
			fmt.Println("FAIL", f)
		}
		if len(fails) > 0 {
			fmt.Printf("%d problem(s)\n", len(fails))
			os.Exit(1)
		}
		fmt.Println("ok")
	default:
		fmt.Fprintln(os.Stderr, "unknown command", cmd)
		os.Exit(2)
	}
}
