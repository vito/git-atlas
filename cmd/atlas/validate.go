package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strings"
)

var (
	hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	isoDay   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	fullSHA  = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

func oneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

func wellFormedXML(s string) error {
	dec := xml.NewDecoder(strings.NewReader(s))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// Validate checks the hand-written data against itself, the derived data and
// the schema. Pointer resolution problems come from BuildSite.
func Validate(d *Data, der *Derived) []string {
	var errs []string
	bad := func(f string, args ...any) { errs = append(errs, fmt.Sprintf(f, args...)) }
	cfg := d.Config

	if cfg.Upstream == "" || cfg.GitHubRepo == "" || cfg.Branch == "" || cfg.Schema == "" {
		bad("atlas.yaml: upstream, github_repo, branch and schema are required")
	}
	if !isoDay.MatchString(cfg.Cutoff.Date) || !fullSHA.MatchString(cfg.Cutoff.SHA) || cfg.Cutoff.Label == "" {
		bad("atlas.yaml: cutoff needs a full sha, its date (YYYY-MM-DD) and a label")
	} else if b, ok := der.PRs["base"]; ok && day(b.MergedAt) != cfg.Cutoff.Date {
		bad("atlas.yaml: cutoff.date is %s but %s was committed on %s", cfg.Cutoff.Date, short(cfg.Cutoff.SHA), day(b.MergedAt))
	}
	if len(cfg.Tracked) == 0 {
		bad("atlas.yaml: tracked must list at least one glob")
	}
	if _, ok := d.APIs[cfg.DefaultAPI]; !ok {
		bad("atlas.yaml: default_api %q is not in data/apis", cfg.DefaultAPI)
	}
	isPR := func(id string) bool { _, ok := d.PRs[id]; return ok }

	// PRs.
	colors := map[string]string{}
	baselines := 0
	for _, id := range sortedKeys(d.PRs) {
		p := d.PRs[id]
		f := "data/prs/" + id + ".yaml"
		if p.Baseline {
			baselines++
			if id != "base" {
				bad("%s: the baseline PR file must be named base.yaml", f)
			}
			if p.Title == "" {
				bad("%s: the baseline needs a title", f)
			}
		} else if prNum(id) <= 0 {
			bad("%s: PR files are named by PR number", f)
		}
		if strings.TrimSpace(p.Summary) == "" {
			bad("%s: summary is required", f)
		}
		if !hexColor.MatchString(p.Color) {
			bad("%s: color %q is not #rrggbb (run dagger generate to assign one)", f, p.Color)
		} else if other, dup := colors[strings.ToLower(p.Color)]; dup {
			bad("%s: color %s is already used by prs/%s", f, p.Color, other)
		} else {
			colors[strings.ToLower(p.Color)] = id
		}
		if len(p.Stack) > 0 && p.MergedVia == 0 {
			bad("%s: a stack needs merged_via (the PR that landed it)", f)
		}
		if p.MergedVia > 0 {
			found := false
			for _, s := range p.Stack {
				found = found || s == p.MergedVia
			}
			if !found {
				bad("%s: merged_via %d is not in stack", f, p.MergedVia)
			}
		}
		if dp, ok := der.PRs[id]; !ok {
			bad("%s: no derived metadata (run dagger generate)", f)
		} else if !oneOf(dp.State, "base", "merged", "open", "closed") {
			bad("%s: derived state is %q (run dagger generate with a GitHub token)", f, dp.State)
		}
	}
	if baselines != 1 {
		bad("data/prs: exactly one PR file must be the baseline, found %d", baselines)
	}

	// Families.
	famUsed, famRevs := map[string]bool{}, map[string]int{}
	for _, a := range d.APIs {
		famUsed[a.Family] = true
	}
	for _, r := range d.Revisions {
		famRevs[r.Family]++
	}
	for _, id := range sortedKeys(d.Families) {
		f := d.Families[id]
		if f.Title == "" || f.Short == "" {
			bad("data/families/%s.yaml: title and short are required", id)
		}
		if !famUsed[id] {
			bad("data/families/%s.yaml: no API uses this family", id)
		}
		if famRevs[id] == 0 {
			bad("data/families/%s.yaml: no revisions under data/revisions/%s/", id, id)
		}
	}

	// APIs.
	aliases := map[string]string{}
	for _, id := range sortedKeys(d.APIs) {
		a := d.APIs[id]
		f := "data/apis/" + id + ".yaml"
		if !strings.Contains(id, ".") {
			bad("%s: API files are named Type.field", f)
		}
		if _, ok := d.Families[a.Family]; !ok {
			bad("%s: family %q does not exist", f, a.Family)
		}
		if a.Since != "pre-cutoff" && !isPR(a.Since) {
			bad("%s: since must be pre-cutoff or a PR in data/prs (got %q)", f, a.Since)
		}
		if a.RemovedIn != "" && !isPR(a.RemovedIn) {
			bad("%s: removed_in %q is not in data/prs", f, a.RemovedIn)
		}
		for _, fld := range append([]string{id}, a.Also...) {
			da, ok := der.APIs[fld]
			switch {
			case !ok:
				bad("%s: %s has no derived signature (run dagger generate)", f, fld)
			case da.Missing && a.RemovedIn == "":
				bad("%s: %s is not in the schema at %s; fix the name or set removed_in", f, fld, short(der.Upstream.SHA))
			case !da.Missing && a.RemovedIn != "" && da.SchemaSHA == der.Upstream.SHA:
				bad("%s: removed_in is set but %s is still in the schema", f, fld)
			}
		}
		if da, ok := der.APIs[id]; ok && !da.Missing {
			if a.Since == "pre-cutoff" && !da.AtBase {
				bad("%s: since pre-cutoff, but %s is not in the schema at the cutoff", f, id)
			}
			if a.Since != "pre-cutoff" && da.AtBase {
				bad("%s: since %s, but %s is already in the schema at the cutoff (use pre-cutoff)", f, a.Since, id)
			}
		}
		for _, al := range a.Aliases {
			if _, clash := d.APIs[al]; clash {
				bad("%s: alias %s is also an API id", f, al)
			}
			if other, dup := aliases[al]; dup {
				bad("%s: alias %s is also claimed by %s", f, al, other)
			}
			aliases[al] = id
		}
	}

	// Revisions.
	for _, r := range d.Revisions {
		f := fmt.Sprintf("data/revisions/%s/%s.yaml", r.Family, r.PR)
		if _, ok := d.Families[r.Family]; !ok {
			bad("%s: family directory %q has no data/families/%s.yaml", f, r.Family, r.Family)
		}
		if !isPR(r.PR) {
			bad("%s: PR %s has no data/prs/%s.yaml", f, r.PR, r.PR)
		}
		if !oneOf(r.Source, "code", "pr-description") {
			bad("%s: source must be code or pr-description", f)
		}
		if r.VerifiedAt != nil && !isoDay.MatchString(*r.VerifiedAt) {
			bad("%s: verified_at must be null or YYYY-MM-DD", f)
		}
		if strings.TrimSpace(r.What) == "" {
			bad("%s: what is required", f)
		}
		if strings.TrimSpace(r.Code) == "" && r.SVG == "" {
			bad("%s: needs code (pseudocode) and/or a diagram (%s.svg)", f, r.PR)
		}
		if len(r.Pointers) == 0 {
			bad("%s: at least one pointer is required", f)
		}
		if dp, ok := der.PRs[r.PR]; ok && !oneOf(dp.State, "merged", "base") && r.SHA == "" {
			bad("%s: #%s is %s; pin the commit this tab describes with sha:", f, r.PR, dp.State)
		}
		if r.SVG != "" {
			if err := wellFormedXML(r.SVG); err != nil {
				bad("%s.svg: not well-formed: %v", strings.TrimSuffix(f, ".yaml"), err)
			}
		}
	}

	// Mechanisms and matrix.
	mechs := map[string]bool{}
	for _, m := range d.Mechanisms {
		if m.ID == "" || m.Name == "" || m.Desc == "" {
			bad("data/mechanisms.yaml: id, name and desc are required (%q)", m.ID)
		}
		if mechs[m.ID] {
			bad("data/mechanisms.yaml: duplicate id %s", m.ID)
		}
		mechs[m.ID] = true
	}
	for _, id := range sortedKeys(d.Matrix) {
		mf := d.Matrix[id]
		f := "data/matrix/" + id + ".yaml"
		if _, ok := d.APIs[id]; !ok {
			bad("%s: %s is not in data/apis", f, id)
		}
		if mf.AsOf == "" {
			bad("%s: as_of (the commit the judgments reflect) is required", f)
		}
		for _, mech := range sortedKeys(mf.Cells) {
			c := mf.Cells[mech]
			if !mechs[mech] {
				bad("%s: %s is not in data/mechanisms.yaml", f, mech)
			}
			if !oneOf(c.Status, "yes", "partial", "gap", "na") {
				bad("%s: %s: status must be yes, partial, gap or na", f, mech)
			}
			if c.Status != "na" && strings.TrimSpace(c.Note) == "" {
				bad("%s: %s: a note is required unless na", f, mech)
			}
			if c.Requires != "" && !isPR(c.Requires) {
				bad("%s: %s: requires %s is not in data/prs", f, mech, c.Requires)
			}
			for _, p := range c.PRs {
				if !isPR(p) {
					bad("%s: %s: PR %s is not in data/prs", f, mech, p)
				}
			}
		}
	}

	// Findings.
	for _, id := range sortedKeys(d.Findings) {
		x := d.Findings[id]
		f := "data/findings/" + id + ".yaml"
		if !oneOf(x.Kind, "conflict", "dup", "lag", "naming") {
			bad("%s: kind must be conflict, dup, lag or naming", f)
		}
		if !oneOf(x.Status, "open", "fixed", "wontfix") {
			bad("%s: status must be open, fixed or wontfix", f)
		}
		if x.Status == "fixed" && !isPR(x.FixedIn) {
			bad("%s: a fixed finding needs fixed_in naming a PR in data/prs", f)
		}
		if x.Status != "fixed" && x.FixedIn != "" {
			bad("%s: fixed_in is set but status is %s", f, x.Status)
		}
		if !isPR(x.DiscoveredIn) {
			bad("%s: discovered_in %q is not in data/prs", f, x.DiscoveredIn)
		}
		if x.Title == "" || strings.TrimSpace(x.Claim) == "" || x.Size == "" || x.Risk == "" {
			bad("%s: title, claim, size and risk are required", f)
		}
		for _, p := range x.PRs {
			if !isPR(p) {
				bad("%s: PR %s is not in data/prs", f, p)
			}
		}
		for _, a := range x.APIs {
			if _, ok := d.APIs[a]; !ok {
				bad("%s: API %s is not in data/apis", f, a)
			}
		}
		for _, p := range x.Pointers {
			if p.SHA == "" {
				bad("%s: pointer %s needs an @sha", f, p)
			}
		}
	}

	// Glossary and skipped.
	terms := map[string]bool{}
	for _, g := range d.Glossary {
		if g.Term == "" || g.Def == "" {
			bad("data/glossary.yaml: term and def are required")
		}
		if terms[g.Term] {
			bad("data/glossary.yaml: duplicate term %q", g.Term)
		}
		terms[g.Term] = true
		if g.PR != "" && !isPR(g.PR) {
			bad("data/glossary.yaml: %q: PR %s is not in data/prs", g.Term, g.PR)
		}
	}
	skipped := map[int]bool{}
	for _, s := range d.Skipped {
		if skipped[s.PR] {
			bad("data/skipped.yaml: #%d listed twice", s.PR)
		}
		skipped[s.PR] = true
		if isPR(fmt.Sprint(s.PR)) {
			bad("data/skipped.yaml: #%d also has data/prs/%d.yaml", s.PR, s.PR)
		}
		if strings.TrimSpace(s.Reason) == "" {
			bad("data/skipped.yaml: #%d needs a reason", s.PR)
		}
	}
	return errs
}
