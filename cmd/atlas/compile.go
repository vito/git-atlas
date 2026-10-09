package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Palette for PR highlight colors: hand-picked first, then generated hues.
// Assignment is first-free in PR order, so existing colors never move.
var curated = []string{
	"#2f6fed", "#1f9d6b", "#7b4fd6", "#0f8b96", "#d64545", "#8f5b2e",
	"#e8590c", "#2b8a3e", "#ae3ec9", "#1098ad", "#a61e4d", "#3b5bdb",
	"#66a80f", "#9c36b5", "#0b7285", "#c92a2a", "#5f3dc4", "#087f5b",
	"#e67700", "#364fc7", "#862e9c", "#5c940d", "#d6336c", "#1864ab",
}

func hsl(h, s, l float64) string {
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - c/2
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	to := func(v float64) int { return int(math.Round((v + m) * 255)) }
	return fmt.Sprintf("#%02x%02x%02x", to(r), to(g), to(b))
}

func paletteColor(i int) string {
	if i < len(curated) {
		return curated[i]
	}
	j := i - len(curated)
	h := math.Mod(float64(j)*137.508+17, 360)
	l := []float64{0.42, 0.34, 0.50}[j%3]
	return hsl(h, 0.62, l)
}

// orderedPRIDs is baseline first, then by number.
func orderedPRIDs(d *Data) []string {
	ids := sortedKeys(d.PRs)
	sort.SliceStable(ids, func(i, j int) bool { return prNum(ids[i]) < prNum(ids[j]) })
	return ids
}

// AssignColors fills in missing colors and returns the ids it colored.
func AssignColors(d *Data) []string {
	used := map[string]bool{}
	for _, p := range d.PRs {
		if p.Color != "" {
			used[strings.ToLower(p.Color)] = true
		}
	}
	var assigned []string
	next := 0
	for _, id := range orderedPRIDs(d) {
		p := d.PRs[id]
		if p.Color != "" {
			continue
		}
		for used[paletteColor(next)] {
			next++
		}
		p.Color = paletteColor(next)
		used[p.Color] = true
		assigned = append(assigned, id)
	}
	return assigned
}

// ---------- site data ----------

type sitePR struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	State    string   `json:"state"`
	Date     string   `json:"date"`
	URL      string   `json:"url"`
	SHA      string   `json:"sha"`
	Color    string   `json:"color"`
	Summary  string   `json:"summary"`
	Why      string   `json:"why,omitempty"`
	Effects  string   `json:"effects,omitempty"`
	Stack    []string `json:"stack,omitempty"`
	Base     string   `json:"base,omitempty"`
	Families int      `json:"-"`
}

type siteTab struct {
	PR         string            `json:"pr"`
	Where      []ResolvedPointer `json:"where"`
	What       string            `json:"what"`
	Effect     string            `json:"effect,omitempty"`
	SVG        string            `json:"svg,omitempty"`
	Code       []string          `json:"code"`
	Source     string            `json:"source"`
	VerifiedAt string            `json:"verified,omitempty"`
	Notes      string            `json:"notes,omitempty"`
	SHA        string            `json:"sha"`
}

type siteImpl struct {
	Title string    `json:"title"`
	Short string    `json:"short"`
	Tabs  []siteTab `json:"tabs"`
}

type siteSig struct {
	Field string   `json:"field"`
	Sig   []string `json:"sig"`
	Doc   string   `json:"doc"`
}

type siteAPI struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Field     string    `json:"field"`
	Impl      string    `json:"impl"`
	Since     string    `json:"since"`
	Sigs      []siteSig `json:"sigs"`
	Purpose   string    `json:"purpose,omitempty"`
	Note      string    `json:"note,omitempty"`
	Aliases   []string  `json:"aliases,omitempty"`
	RemovedIn string    `json:"removed,omitempty"`
}

type siteCell struct {
	S        string            `json:"s"`
	Note     string            `json:"note,omitempty"`
	Requires string            `json:"req,omitempty"`
	PRs      []string          `json:"prs,omitempty"`
	Evidence []ResolvedPointer `json:"ev,omitempty"`
	AsOf     string            `json:"asof"`
}

type siteRow struct {
	API   string              `json:"api"`
	Cells map[string]siteCell `json:"cells"`
}

type siteFinding struct {
	ID           string            `json:"id"`
	Kind         string            `json:"kind"`
	Title        string            `json:"title"`
	Body         string            `json:"body"`
	Proposal     string            `json:"proposal,omitempty"`
	Size         string            `json:"size"`
	Risk         string            `json:"risk"`
	Status       string            `json:"status"`
	FixedIn      string            `json:"fixed_in,omitempty"`
	DiscoveredIn string            `json:"discovered_in"`
	PRs          []string          `json:"prs,omitempty"`
	APIs         []string          `json:"apis,omitempty"`
	Pointers     []ResolvedPointer `json:"pointers"`
}

type siteSkipped struct {
	PR    int    `json:"pr"`
	Title string `json:"title"`
	Why   string `json:"why"`
}

type siteData struct {
	Meta       map[string]any      `json:"meta"`
	DefaultAPI string              `json:"defaultApi"`
	PRs        []sitePR            `json:"prs"`
	ImplOrder  []string            `json:"implOrder"`
	Impls      map[string]siteImpl `json:"impls"`
	APIs       []siteAPI           `json:"apis"`
	MatrixCols []Mechanism         `json:"matrixCols"`
	Matrix     []siteRow           `json:"matrix"`
	Findings   []siteFinding       `json:"findings"`
	Glossary   []GlossaryEntry     `json:"glossary"`
	Skipped    []siteSkipped       `json:"skipped"`
	Queue      []QueueItem         `json:"queue"`
	Aliases    map[string]string   `json:"aliases"`
}

// pinnedSHA is the commit a PR's revisions describe by default.
func pinnedSHA(dp DerivedPR) string {
	if dp.MergeSHA != "" {
		return dp.MergeSHA
	}
	return dp.HeadSHA
}

func revisionSHA(r *Revision, der *Derived) string {
	if r.SHA != "" {
		return r.SHA
	}
	return pinnedSHA(der.PRs[r.PR])
}

func apiOrder(d *Data) []string {
	rank := map[string]int{}
	for i, t := range d.Config.TypeOrder {
		rank[t] = i
	}
	ids := sortedKeys(d.APIs)
	sort.SliceStable(ids, func(i, j int) bool {
		ti, _, _ := strings.Cut(ids[i], ".")
		tj, _, _ := strings.Cut(ids[j], ".")
		ri, ok := rank[ti]
		if !ok {
			ri = len(rank)
		}
		rj, ok := rank[tj]
		if !ok {
			rj = len(rank)
		}
		if ri != rj {
			return ri < rj
		}
		return ids[i] < ids[j]
	})
	return ids
}

func lines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return []string{}
	}
	return strings.Split(s, "\n")
}

// BuildSite turns data + derived into the JSON the template renders.
func BuildSite(d *Data, der *Derived, res *Resolver) (*siteData, []string) {
	cfg := d.Config
	var problems []string
	sd := &siteData{
		DefaultAPI: cfg.DefaultAPI,
		Impls:      map[string]siteImpl{},
		Queue:      der.Queue,
		Aliases:    map[string]string{},
		MatrixCols: d.Mechanisms,
		Glossary:   d.Glossary,
	}
	links := []map[string]string{}
	for _, l := range cfg.Links {
		links = append(links, map[string]string{"title": l.Title, "url": l.URL})
	}
	sd.Meta = map[string]any{
		"title":    cfg.Title,
		"about":    strings.TrimSpace(cfg.About),
		"upstream": map[string]string{"repo": cfg.GitHubRepo, "branch": der.Upstream.Branch, "sha": der.Upstream.SHA, "date": der.Upstream.Date},
		"cutoff":   map[string]any{"date": cfg.Cutoff.Date, "sha": cfg.Cutoff.SHA, "label": cfg.Cutoff.Label},
		"links":    links,
	}

	// PRs: baseline, merged in merge order, then the rest by number.
	type ord struct {
		id  string
		key string
	}
	var os_ []ord
	for _, id := range orderedPRIDs(d) {
		dp := der.PRs[id]
		k := "2" + fmt.Sprintf("%09d", prNum(id))
		switch {
		case d.PRs[id].Baseline:
			k = "0"
		case dp.State == "merged":
			k = "1" + dp.MergedAt + fmt.Sprintf("%09d", prNum(id))
		}
		os_ = append(os_, ord{id, k})
	}
	sort.SliceStable(os_, func(i, j int) bool { return os_[i].key < os_[j].key })
	for _, o := range os_ {
		p, dp := d.PRs[o.id], der.PRs[o.id]
		sp := sitePR{ID: o.id, Title: dp.Title, State: dp.State, Color: p.Color, Summary: strings.TrimSpace(p.Summary),
			Why: strings.TrimSpace(p.Why), Effects: strings.TrimSpace(p.Effects), Base: dp.Base}
		if p.Title != "" {
			sp.Title = p.Title
		}
		switch dp.State {
		case "merged", "base":
			sp.Date = day(dp.MergedAt)
			sp.SHA = short(dp.MergeSHA)
		default:
			sp.Date = dp.State
			sp.SHA = short(dp.HeadSHA)
		}
		if p.Baseline {
			sp.Date = "≤ " + cfg.Cutoff.Date
		} else {
			url := fmt.Sprintf("https://github.com/%s/pull/%s", cfg.GitHubRepo, o.id)
			if p.MergedVia > 0 {
				url = fmt.Sprintf("https://github.com/%s/pull/%d", cfg.GitHubRepo, p.MergedVia)
			}
			sp.URL = url
		}
		for _, s := range p.Stack {
			sp.Stack = append(sp.Stack, strconv.Itoa(s))
		}
		sd.PRs = append(sd.PRs, sp)
	}

	// Families and their revisions.
	famIDs := sortedKeys(d.Families)
	sort.SliceStable(famIDs, func(i, j int) bool { return d.Families[famIDs[i]].Order < d.Families[famIDs[j]].Order })
	sd.ImplOrder = famIDs
	for _, f := range famIDs {
		sd.Impls[f] = siteImpl{Title: d.Families[f].Title, Short: d.Families[f].Short, Tabs: []siteTab{}}
	}
	for _, r := range d.Revisions {
		impl, ok := sd.Impls[r.Family]
		if !ok {
			continue // reported by validate
		}
		sha := revisionSHA(r, der)
		t := siteTab{PR: r.PR, What: strings.TrimSpace(r.What), Effect: strings.TrimSpace(r.Effect), SVG: r.SVG,
			Code: lines(r.Code), Source: r.Source, Notes: strings.TrimSpace(r.Notes), Where: []ResolvedPointer{}}
		if r.VerifiedAt != nil {
			t.VerifiedAt = *r.VerifiedAt
		}
		for _, p := range r.Pointers {
			rp := res.Resolve(p, sha)
			if rp.Err != "" {
				problems = append(problems, fmt.Sprintf("revisions/%s/%s.yaml: %s: %s", r.Family, r.PR, p, rp.Err))
			}
			t.Where = append(t.Where, rp)
		}
		if full, err := res.U.Resolve(sha); err == nil {
			t.SHA = short(full)
		}
		impl.Tabs = append(impl.Tabs, t)
		sd.Impls[r.Family] = impl
	}

	// APIs.
	for _, id := range apiOrder(d) {
		a := d.APIs[id]
		typ, field, _ := strings.Cut(id, ".")
		sa := siteAPI{ID: id, Type: typ, Field: field, Impl: a.Family, Since: a.Since,
			Purpose: strings.TrimSpace(a.Purpose), Note: strings.TrimSpace(a.Notes), Aliases: a.Aliases, RemovedIn: a.RemovedIn}
		if a.Label != "" {
			sa.Field = a.Label
		}
		for _, f := range append([]string{id}, a.Also...) {
			da := der.APIs[f]
			_, fn, _ := strings.Cut(f, ".")
			sig := da.Sig
			if sig == nil {
				sig = []string{fn + ": (not in the schema)"}
			}
			sa.Sigs = append(sa.Sigs, siteSig{Field: f, Sig: sig, Doc: da.Doc})
		}
		for _, al := range a.Aliases {
			sd.Aliases[al] = id
		}
		sd.APIs = append(sd.APIs, sa)
	}

	// Matrix rows, in API order.
	for _, id := range apiOrder(d) {
		mf, ok := d.Matrix[id]
		if !ok {
			continue
		}
		row := siteRow{API: id, Cells: map[string]siteCell{}}
		for _, mech := range sortedKeys(mf.Cells) {
			c := mf.Cells[mech]
			asOf := c.AsOf
			if asOf == "" {
				asOf = mf.AsOf
			}
			sc := siteCell{S: c.Status, Note: strings.TrimSpace(c.Note), Requires: c.Requires, PRs: c.PRs, AsOf: asOf}
			if full, err := res.U.Resolve(asOf); err == nil {
				sc.AsOf = short(full)
			}
			for _, p := range c.Evidence {
				rp := res.Resolve(p, asOf)
				if rp.Err != "" {
					problems = append(problems, fmt.Sprintf("matrix/%s.yaml: %s: %s: %s", id, mech, p, rp.Err))
				}
				sc.Evidence = append(sc.Evidence, rp)
			}
			row.Cells[mech] = sc
		}
		sd.Matrix = append(sd.Matrix, row)
	}

	// Findings, by id (ids carry their order).
	for _, id := range sortedKeys(d.Findings) {
		f := d.Findings[id]
		sf := siteFinding{ID: id, Kind: f.Kind, Title: f.Title, Body: strings.TrimSpace(f.Claim), Proposal: strings.TrimSpace(f.Proposal),
			Size: f.Size, Risk: f.Risk, Status: f.Status, FixedIn: f.FixedIn, DiscoveredIn: f.DiscoveredIn, PRs: f.PRs, APIs: f.APIs,
			Pointers: []ResolvedPointer{}}
		for _, p := range f.Pointers {
			rp := res.Resolve(p, "")
			if rp.Err != "" {
				problems = append(problems, fmt.Sprintf("findings/%s.yaml: %s: %s", id, p, rp.Err))
			}
			sf.Pointers = append(sf.Pointers, rp)
		}
		sd.Findings = append(sd.Findings, sf)
	}

	for _, s := range d.Skipped {
		sd.Skipped = append(sd.Skipped, siteSkipped{PR: s.PR, Title: der.PRs[strconv.Itoa(s.PR)].Title, Why: strings.TrimSpace(s.Reason)})
	}
	if sd.Queue == nil {
		sd.Queue = []QueueItem{}
	}
	return sd, problems
}

// pointerPairs lists every (sha, path) the site needs, for prefetching.
func pointerPairs(d *Data, der *Derived, u *Upstream) [][2]string {
	seen := map[[2]string]bool{}
	var out [][2]string
	add := func(p Pointer, def string) {
		sha := p.SHA
		if sha == "" {
			sha = def
		}
		full, err := u.Resolve(sha)
		if err != nil {
			return
		}
		k := [2]string{full, p.Path}
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for _, r := range d.Revisions {
		for _, p := range r.Pointers {
			add(p, revisionSHA(r, der))
		}
	}
	for _, mf := range d.Matrix {
		for _, c := range mf.Cells {
			for _, p := range c.Evidence {
				def := c.AsOf
				if def == "" {
					def = mf.AsOf
				}
				add(p, def)
			}
		}
	}
	for _, f := range d.Findings {
		for _, p := range f.Pointers {
			add(p, "")
		}
	}
	return out
}

// pinnedCommits lists every full SHA the data refers to, so they can be
// fetched before resolving.
func pinnedCommits(d *Data, der *Derived) []string {
	var out []string
	for _, dp := range der.PRs {
		out = append(out, dp.MergeSHA, dp.HeadSHA)
	}
	for _, r := range d.Revisions {
		out = append(out, r.SHA)
		for _, p := range r.Pointers {
			out = append(out, p.SHA)
		}
	}
	for _, mf := range d.Matrix {
		out = append(out, mf.AsOf)
		for _, c := range mf.Cells {
			out = append(out, c.AsOf)
			for _, p := range c.Evidence {
				out = append(out, p.SHA)
			}
		}
	}
	for _, f := range d.Findings {
		for _, p := range f.Pointers {
			out = append(out, p.SHA)
		}
	}
	sort.Strings(out)
	return out
}

var scriptClose = regexp.MustCompile(`(?i)</script`)

func RenderHTML(tpl string, sd *siteData) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(sd); err != nil {
		return nil, err
	}
	js := strings.TrimSpace(buf.String())
	// Keep the blob inert inside <script type="application/json">.
	js = scriptClose.ReplaceAllString(js, `<\/script`)
	js = strings.ReplaceAll(js, "<!--", `<\!--`)
	if !strings.Contains(tpl, "__DATA__") {
		return nil, fmt.Errorf("template lacks __DATA__")
	}
	title := sd.Meta["title"].(string)
	out := strings.Replace(tpl, "__DATA__", js, 1)
	out = strings.ReplaceAll(out, "__TITLE__", htmlEscape(title))
	return []byte(out), nil
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

func RenderQueue(d *Data, der *Derived) []byte {
	var b strings.Builder
	b.WriteString("# Coverage queue\n\n")
	b.WriteString("<!-- Generated by `dagger generate`; do not edit. See README.md \"Reading the queue\". -->\n\n")
	fmt.Fprintf(&b, "Upstream: `%s` `%s` @ `%s` (%s). Cutoff: %s.\n\n",
		d.Config.GitHubRepo, der.Upstream.Branch, short(der.Upstream.SHA), der.Upstream.Date, d.Config.Cutoff.Label)
	groups := []struct{ kind, title, help string }{
		{"uncovered", "Merged PRs touching tracked paths, not yet in the atlas",
			"Each needs `data/prs/<n>.yaml` plus revisions, or an entry in `data/skipped.yaml`. Blocks `dagger check`."},
		{"recheck", "Tabs written against a pre-merge head",
			"The PR merged; re-read its tabs at the merge commit and drop their `sha:`. Blocks `dagger check`."},
		{"moved", "Open PRs whose head moved since their tabs were written",
			"Advisory: re-read the new head when convenient, then update the tabs' `sha:`."},
	}
	for _, g := range groups {
		var items []QueueItem
		for _, q := range der.Queue {
			if q.Kind == g.kind {
				items = append(items, q)
			}
		}
		fmt.Fprintf(&b, "## %s (%d)\n\n%s\n\n", g.title, len(items), g.help)
		if len(items) == 0 {
			b.WriteString("Nothing.\n\n")
			continue
		}
		for _, q := range items {
			fmt.Fprintf(&b, "- [#%d](https://github.com/%s/pull/%d) %s", q.PR, d.Config.GitHubRepo, q.PR, q.Title)
			if q.Date != "" {
				fmt.Fprintf(&b, " (%s, `%s`)", q.Date, short(q.SHA))
			}
			b.WriteString("\n")
			if q.Detail != "" {
				fmt.Fprintf(&b, "  - %s\n", q.Detail)
			}
			if len(q.Files) > 0 {
				fs := q.Files
				more := ""
				if len(fs) > 8 {
					more = fmt.Sprintf(", … %d more", len(fs)-8)
					fs = fs[:8]
				}
				fmt.Fprintf(&b, "  - touches `%s`%s\n", strings.Join(fs, "`, `"), more)
			}
		}
		b.WriteString("\n")
	}
	return []byte(strings.TrimRight(b.String(), "\n") + "\n")
}

func RenderDerived(der *Derived) ([]byte, error) {
	if der.Queue == nil {
		der.Queue = []QueueItem{}
	}
	b, err := json.MarshalIndent(der, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
