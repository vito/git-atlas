package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Derived is derived/upstream.json: everything generate learns from the
// upstream repository and GitHub. Committed, so checks and compiles are
// reproducible without touching the network for anything that moves.
type Derived struct {
	Upstream UpstreamInfo          `json:"upstream"`
	PRs      map[string]DerivedPR  `json:"prs"`
	APIs     map[string]DerivedAPI `json:"apis"`
	Queue    []QueueItem           `json:"queue"`
}

type UpstreamInfo struct {
	Branch string `json:"branch"`
	SHA    string `json:"sha"`
	Date   string `json:"date"`
}

type DerivedPR struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	State     string `json:"state"` // base | merged | open | closed | unknown
	MergedAt  string `json:"merged_at,omitempty"`
	MergeSHA  string `json:"merge_sha,omitempty"`
	HeadSHA   string `json:"head_sha,omitempty"`
	Base      string `json:"base,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	ClosedAt  string `json:"closed_at,omitempty"`
	Source    string `json:"source"` // git | github | ls-remote | config
}

type DerivedAPI struct {
	Sig       []string `json:"sig"`
	Doc       string   `json:"doc"`
	SchemaSHA string   `json:"schema_sha"`
	AtBase    bool     `json:"at_base"`
	Missing   bool     `json:"missing,omitempty"`
}

type QueueItem struct {
	Kind     string   `json:"kind"` // uncovered | recheck | moved
	PR       int      `json:"pr"`
	Title    string   `json:"title"`
	Date     string   `json:"date,omitempty"`
	SHA      string   `json:"sha,omitempty"`
	Files    []string `json:"files,omitempty"`
	Detail   string   `json:"detail,omitempty"`
	Blocking bool     `json:"blocking"`
}

func LoadDerived(root string) (*Derived, error) {
	b, err := os.ReadFile(root + "/derived/upstream.json")
	if os.IsNotExist(err) {
		return &Derived{PRs: map[string]DerivedPR{}, APIs: map[string]DerivedAPI{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var d Derived
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("derived/upstream.json: %w", err)
	}
	return &d, nil
}

// globRegexp turns a path glob (*, ?, **) into an anchored regexp.
func globRegexp(g string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(g); i++ {
		switch c := g[i]; c {
		case '*':
			if i+1 < len(g) && g[i+1] == '*' {
				i++
				if i+1 < len(g) && g[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

type matcher struct{ inc, exc []*regexp.Regexp }

func newMatcher(inc, exc []string) *matcher {
	m := &matcher{}
	for _, g := range inc {
		m.inc = append(m.inc, globRegexp(g))
	}
	for _, g := range exc {
		m.exc = append(m.exc, globRegexp(g))
	}
	return m
}

func (m *matcher) Match(p string) bool {
	for _, e := range m.exc {
		if e.MatchString(p) {
			return false
		}
	}
	for _, i := range m.inc {
		if i.MatchString(p) {
			return true
		}
	}
	return false
}

// GitHub is an optional API client; nil without a token.
type GitHub struct {
	Token string
	Repo  string
}

type ghPull struct {
	Title          string  `json:"title"`
	State          string  `json:"state"`
	MergedAt       *string `json:"merged_at"`
	MergeCommitSHA string  `json:"merge_commit_sha"`
	CreatedAt      string  `json:"created_at"`
	ClosedAt       *string `json:"closed_at"`
	Head           struct {
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

func (g *GitHub) Pull(n int) (*ghPull, error) {
	req, _ := http.NewRequest("GET", fmt.Sprintf("https://api.github.com/repos/%s/pulls/%d", g.Repo, n), nil)
	req.Header.Set("Authorization", "Bearer "+g.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	c := &http.Client{Timeout: 30 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub pulls/%d: %s: %s", n, resp.Status, strings.TrimSpace(string(b)))
	}
	var p ghPull
	return &p, json.Unmarshal(b, &p)
}

func day(ts string) string {
	if len(ts) >= 10 {
		return ts[:10]
	}
	return ts
}

// covered returns every PR number the atlas accounts for: PR files, their
// stacks and merge vehicles, and skipped PRs.
func covered(d *Data) map[int]string {
	c := map[int]string{}
	for id, p := range d.PRs {
		if n := prNum(id); n > 0 {
			c[n] = "prs/" + id
		}
		for _, s := range p.Stack {
			c[s] = "prs/" + id + " (stack)"
		}
		if p.MergedVia > 0 {
			c[p.MergedVia] = "prs/" + id + " (merged via)"
		}
	}
	for _, s := range d.Skipped {
		c[s.PR] = "skipped"
	}
	return c
}

// Derive recomputes derived data. With refresh, the branch tip is fetched and
// unmerged PRs are looked up on GitHub (or ls-remote without a token);
// otherwise the pinned tip and the previous answers for unmerged PRs are
// reused, so the result is a pure function of the repository contents.
func Derive(d *Data, u *Upstream, prev *Derived, refresh bool, gh *GitHub, warn func(string, ...any)) (*Derived, error) {
	cfg := d.Config
	out := &Derived{PRs: map[string]DerivedPR{}, APIs: map[string]DerivedAPI{}}
	var tip string
	var err error
	if refresh || prev.Upstream.SHA == "" {
		if tip, err = u.FetchBranch(cfg.Branch); err != nil {
			return nil, err
		}
	} else {
		tip = prev.Upstream.SHA
		if err := u.EnsureCommits([]string{tip}); err != nil {
			return nil, err
		}
	}
	if err := u.EnsureCommits([]string{cfg.Cutoff.SHA}); err != nil {
		return nil, err
	}
	log, err := u.FirstParentLog(tip)
	if err != nil {
		return nil, err
	}
	if len(log) == 0 {
		return nil, fmt.Errorf("empty history at %s", tip)
	}
	out.Upstream = UpstreamInfo{Branch: cfg.Branch, SHA: tip, Date: day(log[0].Date)}

	onMain := map[int]Commit{}
	afterCutoff := map[string]bool{}
	reachedCutoff := false
	for _, c := range log {
		if c.SHA == cfg.Cutoff.SHA {
			reachedCutoff = true
		}
		if !reachedCutoff {
			afterCutoff[c.SHA] = true
		}
		if n, _ := c.PR(); n > 0 {
			if _, dup := onMain[n]; !dup {
				onMain[n] = c
			}
		}
	}
	if !reachedCutoff {
		return nil, fmt.Errorf("cutoff %s is not on the first-parent history of %s %s (or the clone is too shallow)", short(cfg.Cutoff.SHA), cfg.Branch, short(tip))
	}
	cutoffDate, err := u.CommitDate(cfg.Cutoff.SHA)
	if err != nil {
		return nil, err
	}

	fromCommit := func(n int, c Commit) DerivedPR {
		_, title := c.PR()
		return DerivedPR{Number: n, Title: title, State: "merged", MergedAt: c.Date, MergeSHA: c.SHA,
			Source: "git"}
	}
	derivePR := func(n int) DerivedPR {
		if c, ok := onMain[n]; ok {
			return fromCommit(n, c)
		}
		key := strconv.Itoa(n)
		old, hadOld := prev.PRs[key]
		if !refresh && hadOld {
			return old
		}
		if refresh && gh != nil {
			p, err := gh.Pull(n)
			if err == nil {
				r := DerivedPR{Number: n, Title: p.Title, State: p.State, HeadSHA: p.Head.SHA, Base: p.Base.Ref,
					CreatedAt: p.CreatedAt, Source: "github"}
				if p.ClosedAt != nil {
					r.ClosedAt = *p.ClosedAt
				}
				if p.MergedAt != nil {
					r.State, r.MergedAt, r.MergeSHA = "merged", *p.MergedAt, p.MergeCommitSHA
				}
				return r
			}
			warn("GitHub lookup of #%d failed (%v); falling back to git", n, err)
		}
		r := old
		r.Number = n
		if !hadOld {
			r.Title, r.State = fmt.Sprintf("#%d (no GitHub token: title unknown)", n), "unknown"
		}
		if head := u.LsRemote(fmt.Sprintf("refs/pull/%d/head", n)); head != "" {
			r.HeadSHA = head
		}
		r.Source = "ls-remote"
		return r
	}

	cov := covered(d)
	want := map[int]bool{}
	for n := range cov {
		want[n] = true
	}
	for _, n := range sortedInts(want) {
		out.PRs[strconv.Itoa(n)] = derivePR(n)
	}
	// Stacks: a PR file standing for several PRs takes its merge from the
	// PR that landed them on the branch.
	for _, id := range sortedKeys(d.PRs) {
		p := d.PRs[id]
		if p.Baseline {
			out.PRs[id] = DerivedPR{Title: p.Title, State: "base", MergedAt: cutoffDate, MergeSHA: cfg.Cutoff.SHA, Source: "config"}
			continue
		}
		if p.MergedVia > 0 {
			via := out.PRs[strconv.Itoa(p.MergedVia)]
			r := via
			r.Number = prNum(id)
			if p.Title != "" {
				r.Title = p.Title
			}
			out.PRs[id] = r
		}
	}

	// Schemas: the branch tip, the cutoff, and unmerged PR heads on demand.
	schemas := map[string]*Schema{}
	schemaAt := func(sha string) (*Schema, error) {
		if s, ok := schemas[sha]; ok {
			return s, nil
		}
		if err := u.EnsureCommits([]string{sha}); err != nil {
			return nil, err
		}
		b, ok, err := u.ReadFile(sha, cfg.Schema)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("%s not found at %s", cfg.Schema, sha)
		}
		s, err := ParseSchema(string(b))
		if err != nil {
			return nil, fmt.Errorf("schema at %s: %w", sha, err)
		}
		schemas[sha] = s
		return s, nil
	}
	tipSchema, err := schemaAt(tip)
	if err != nil {
		return nil, err
	}
	baseSchema, err := schemaAt(cfg.Cutoff.SHA)
	if err != nil {
		return nil, err
	}
	for _, id := range sortedKeys(d.APIs) {
		a := d.APIs[id]
		for _, f := range append([]string{id}, a.Also...) {
			da := DerivedAPI{SchemaSHA: tip}
			def := tipSchema.Fields[f]
			if def == nil && a.RemovedIn != "" {
				def, da.SchemaSHA = baseSchema.Fields[f], cfg.Cutoff.SHA
			}
			if def == nil {
				// Introduced by a PR that has not merged: read its head.
				if p, ok := out.PRs[a.Since]; ok && p.State != "merged" && p.HeadSHA != "" {
					if s, err := schemaAt(p.HeadSHA); err == nil && s.Fields[f] != nil {
						def, da.SchemaSHA = s.Fields[f], p.HeadSHA
					}
				}
			}
			_, da.AtBase = baseSchema.Fields[f]
			if def == nil {
				da.Missing, da.SchemaSHA = true, ""
			} else {
				da.Sig = Signature(def)
				da.Doc = strings.TrimSpace(def.Description)
			}
			out.APIs[f] = da
		}
	}

	// Coverage queue.
	m := newMatcher(cfg.Tracked, cfg.Ignore)
	for _, c := range log {
		if !afterCutoff[c.SHA] {
			continue
		}
		n, title := c.PR()
		if n == 0 || cov[n] != "" {
			continue
		}
		var hits []string
		for _, f := range c.Files {
			if m.Match(f) {
				hits = append(hits, f)
			}
		}
		if len(hits) == 0 {
			continue
		}
		out.Queue = append(out.Queue, QueueItem{Kind: "uncovered", PR: n, Title: title, Date: day(c.Date), SHA: c.SHA, Files: hits, Blocking: true})
	}
	for _, r := range d.Revisions {
		p, ok := out.PRs[r.PR]
		if !ok || r.SHA == "" {
			continue
		}
		full, err := u.Resolve(r.SHA)
		if err != nil {
			continue // reported by validate
		}
		switch {
		case p.State == "merged" && full != p.MergeSHA:
			out.Queue = append(out.Queue, QueueItem{Kind: "recheck", PR: p.Number, Title: p.Title, Date: day(p.MergedAt), SHA: p.MergeSHA,
				Detail:   fmt.Sprintf("revisions/%s/%s.yaml describes %s; the PR merged as %s. Re-read the merged code, then drop `sha:`.", r.Family, r.PR, short(full), short(p.MergeSHA)),
				Blocking: true})
		case p.State != "merged" && p.HeadSHA != "" && full != p.HeadSHA:
			out.Queue = append(out.Queue, QueueItem{Kind: "moved", PR: p.Number, Title: p.Title, SHA: p.HeadSHA,
				Detail: fmt.Sprintf("revisions/%s/%s.yaml describes %s; the PR head is now %s.", r.Family, r.PR, short(full), short(p.HeadSHA))})
		}
	}
	sort.SliceStable(out.Queue, func(i, j int) bool {
		a, b := out.Queue[i], out.Queue[j]
		if a.Kind != b.Kind {
			return a.Kind > b.Kind // uncovered, recheck, moved
		}
		if a.PR != b.PR {
			return a.PR < b.PR
		}
		return a.Detail < b.Detail
	})
	return out, nil
}

func sortedInts(m map[int]bool) []int {
	var out []int
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
