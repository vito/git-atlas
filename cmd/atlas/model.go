package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is atlas.yaml: what upstream is tracked and how.
type Config struct {
	Title      string   `yaml:"title"`
	Upstream   string   `yaml:"upstream"`    // git URL of the tracked repository
	GitHubRepo string   `yaml:"github_repo"` // owner/name, for PR URLs and the API
	Branch     string   `yaml:"branch"`
	Schema     string   `yaml:"schema"` // path of the GraphQL SDL in the upstream
	Cutoff     Cutoff   `yaml:"cutoff"`
	Tracked    []string `yaml:"tracked"` // globs; a merged PR touching one must be covered
	Ignore     []string `yaml:"ignore"`  // globs subtracted from tracked
	TypeOrder  []string `yaml:"type_order"`
	DefaultAPI string   `yaml:"default_api"`
	About      string   `yaml:"about"`
	Links      []Link   `yaml:"links"`
}

type Cutoff struct {
	SHA   string `yaml:"sha"`   // full SHA of the last baseline commit on the branch
	Date  string `yaml:"date"`  // its committer date, YYYY-MM-DD (display only)
	Label string `yaml:"label"` // how to say it in prose
}

type Link struct {
	Title string `yaml:"title"`
	URL   string `yaml:"url"`
}

// PRFile is data/prs/<id>.yaml. Everything else about a PR is derived.
type PRFile struct {
	Baseline  bool   `yaml:"baseline,omitempty"`
	Title     string `yaml:"title,omitempty"` // only for the baseline or a stack
	Stack     []int  `yaml:"stack,omitempty"`
	MergedVia int    `yaml:"merged_via,omitempty"`
	Summary   string `yaml:"summary"`
	Why       string `yaml:"why,omitempty"`
	Effects   string `yaml:"effects,omitempty"`
	Color     string `yaml:"color,omitempty"`
}

// APIFile is data/apis/<Type.field>.yaml. Signature and doc are derived.
type APIFile struct {
	Family    string   `yaml:"family"`
	Since     string   `yaml:"since"` // "pre-cutoff" or a PR number
	Label     string   `yaml:"label,omitempty"`
	Also      []string `yaml:"also,omitempty"` // sibling fields documented on this page
	Aliases   []string `yaml:"aliases,omitempty"`
	Purpose   string   `yaml:"purpose,omitempty"`
	Notes     string   `yaml:"notes,omitempty"`
	RemovedIn string   `yaml:"removed_in,omitempty"`
}

// FamilyFile is data/families/<id>.yaml: one implementation shared by fields.
type FamilyFile struct {
	Title string `yaml:"title"`
	Short string `yaml:"short"`
	Order int    `yaml:"order"`
}

// RevisionFile is data/revisions/<family>/<pr>.yaml (+ optional <pr>.svg).
type RevisionFile struct {
	Source     string    `yaml:"source"`        // code | pr-description
	SHA        string    `yaml:"sha,omitempty"` // commit described; default: the PR's merge
	VerifiedAt *string   `yaml:"verified_at"`
	What       string    `yaml:"what"`
	Effect     string    `yaml:"effect,omitempty"`
	Pointers   []Pointer `yaml:"pointers"`
	Notes      string    `yaml:"notes,omitempty"`
	Code       string    `yaml:"code,omitempty"`
}

type Mechanism struct {
	ID   string `yaml:"id" json:"id"`
	Name string `yaml:"name" json:"name"`
	Desc string `yaml:"desc" json:"desc"`
}

// MatrixFile is data/matrix/<Type.field>.yaml.
type MatrixFile struct {
	AsOf  string          `yaml:"as_of"`
	Cells map[string]Cell `yaml:"cells"`
}

type Cell struct {
	Status   string    `yaml:"status"` // yes | partial | gap | na
	Note     string    `yaml:"note,omitempty"`
	Requires string    `yaml:"requires,omitempty"` // holds only once this PR lands
	PRs      []string  `yaml:"prs,omitempty"`      // PRs that changed this cell
	Evidence []Pointer `yaml:"evidence,omitempty"`
	AsOf     string    `yaml:"as_of,omitempty"`
}

// FindingFile is data/findings/<id>.yaml.
type FindingFile struct {
	Kind         string    `yaml:"kind"` // conflict | dup | lag | naming
	Title        string    `yaml:"title"`
	Claim        string    `yaml:"claim"`
	Proposal     string    `yaml:"proposal,omitempty"`
	Size         string    `yaml:"size"`
	Risk         string    `yaml:"risk"`
	Status       string    `yaml:"status"` // open | fixed | wontfix
	FixedIn      string    `yaml:"fixed_in,omitempty"`
	DiscoveredIn string    `yaml:"discovered_in"`
	PRs          []string  `yaml:"prs,omitempty"`
	APIs         []string  `yaml:"apis,omitempty"`
	Pointers     []Pointer `yaml:"pointers"`
}

type GlossaryEntry struct {
	Term string `yaml:"term" json:"term"`
	Def  string `yaml:"def" json:"def"`
	PR   string `yaml:"pr,omitempty" json:"pr,omitempty"`
}

type Skipped struct {
	PR     int    `yaml:"pr"`
	Reason string `yaml:"reason"`
}

// Pointer is "path[:symbol][@sha]", optionally with a note. A missing sha
// means the pinned commit of the entry's PR (see README).
type Pointer struct {
	Path   string
	Symbol string
	SHA    string
	Note   string
}

var shaSuffix = regexp.MustCompile(`@([0-9a-f]{7,40})$`)

func ParsePointer(s string) (Pointer, error) {
	var p Pointer
	s = strings.TrimSpace(s)
	if m := shaSuffix.FindStringSubmatch(s); m != nil {
		p.SHA = m[1]
		s = strings.TrimSuffix(s, m[0])
	}
	path, sym, _ := strings.Cut(s, ":")
	p.Path, p.Symbol = path, sym
	if p.Path == "" || strings.ContainsAny(p.Path, " \t") || strings.ContainsAny(p.Symbol, " \t,()") {
		return p, fmt.Errorf("malformed pointer %q: want path[:symbol][@sha]", s)
	}
	return p, nil
}

func (p Pointer) String() string {
	s := p.Path
	if p.Symbol != "" {
		s += ":" + p.Symbol
	}
	if p.SHA != "" {
		s += "@" + p.SHA
	}
	return s
}

func (p *Pointer) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		q, err := ParsePointer(n.Value)
		if err != nil {
			return fmt.Errorf("line %d: %w", n.Line, err)
		}
		*p = q
		return nil
	case yaml.MappingNode:
		var raw struct {
			At   string `yaml:"at"`
			Note string `yaml:"note"`
		}
		if err := n.Decode(&raw); err != nil {
			return err
		}
		q, err := ParsePointer(raw.At)
		if err != nil {
			return fmt.Errorf("line %d: %w", n.Line, err)
		}
		q.Note = raw.Note
		*p = q
		return nil
	}
	return fmt.Errorf("line %d: pointer must be a string or {at, note}", n.Line)
}

// Revision is a loaded revision file.
type Revision struct {
	RevisionFile
	Family string
	PR     string
	SVG    string
	File   string
}

// Data is everything hand-written.
type Data struct {
	Root       string
	Config     Config
	PRs        map[string]*PRFile
	APIs       map[string]*APIFile
	Families   map[string]*FamilyFile
	Revisions  []*Revision
	Mechanisms []Mechanism
	Matrix     map[string]*MatrixFile
	Findings   map[string]*FindingFile
	Glossary   []GlossaryEntry
	Skipped    []Skipped
}

func decodeStrict(path string, out any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func yamlFiles(dir string) ([]string, error) {
	m, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	sort.Strings(m)
	return m, err
}

func stem(path string) string {
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}

func LoadData(root string) (*Data, error) {
	d := &Data{
		Root:     root,
		PRs:      map[string]*PRFile{},
		APIs:     map[string]*APIFile{},
		Families: map[string]*FamilyFile{},
		Matrix:   map[string]*MatrixFile{},
		Findings: map[string]*FindingFile{},
	}
	if err := decodeStrict(filepath.Join(root, "atlas.yaml"), &d.Config); err != nil {
		return nil, err
	}
	dataDir := filepath.Join(root, "data")
	loadDir := func(sub string, each func(id, path string) error) error {
		files, err := yamlFiles(filepath.Join(dataDir, sub))
		if err != nil {
			return err
		}
		for _, f := range files {
			if err := each(stem(f), f); err != nil {
				return err
			}
		}
		return nil
	}
	if err := loadDir("prs", func(id, f string) error {
		v := &PRFile{}
		d.PRs[id] = v
		return decodeStrict(f, v)
	}); err != nil {
		return nil, err
	}
	if err := loadDir("apis", func(id, f string) error {
		v := &APIFile{}
		d.APIs[id] = v
		return decodeStrict(f, v)
	}); err != nil {
		return nil, err
	}
	if err := loadDir("families", func(id, f string) error {
		v := &FamilyFile{}
		d.Families[id] = v
		return decodeStrict(f, v)
	}); err != nil {
		return nil, err
	}
	if err := loadDir("matrix", func(id, f string) error {
		v := &MatrixFile{}
		d.Matrix[id] = v
		return decodeStrict(f, v)
	}); err != nil {
		return nil, err
	}
	if err := loadDir("findings", func(id, f string) error {
		v := &FindingFile{}
		d.Findings[id] = v
		return decodeStrict(f, v)
	}); err != nil {
		return nil, err
	}
	famDirs, err := filepath.Glob(filepath.Join(dataDir, "revisions", "*"))
	if err != nil {
		return nil, err
	}
	sort.Strings(famDirs)
	for _, fd := range famDirs {
		fam := filepath.Base(fd)
		files, err := yamlFiles(fd)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			r := &Revision{Family: fam, PR: stem(f), File: f}
			if err := decodeStrict(f, &r.RevisionFile); err != nil {
				return nil, err
			}
			svg := strings.TrimSuffix(f, ".yaml") + ".svg"
			if b, err := os.ReadFile(svg); err == nil {
				r.SVG = strings.TrimSpace(string(b))
			}
			d.Revisions = append(d.Revisions, r)
		}
	}
	if err := decodeStrict(filepath.Join(dataDir, "mechanisms.yaml"), &d.Mechanisms); err != nil {
		return nil, err
	}
	if err := decodeStrict(filepath.Join(dataDir, "glossary.yaml"), &d.Glossary); err != nil {
		return nil, err
	}
	if err := decodeStrict(filepath.Join(dataDir, "skipped.yaml"), &d.Skipped); err != nil {
		return nil, err
	}
	return d, nil
}

// PR ids are "base" or a decimal number; they sort baseline-first, then by
// number.
func prNum(id string) int {
	n, err := strconv.Atoi(id)
	if err != nil {
		return -1
	}
	return n
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
