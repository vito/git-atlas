package main

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// Resolver checks code pointers against upstream files and finds the line a
// symbol is defined on, for links.
type Resolver struct {
	U    *Upstream
	Repo string // owner/name, for blob URLs

	mu    sync.Mutex
	files map[string]*fileAt
}

type fileAt struct {
	once sync.Once
	body string
	ok   bool
	err  error
}

type ResolvedPointer struct {
	Text string `json:"t"`
	URL  string `json:"u,omitempty"`
	Note string `json:"n,omitempty"`
	SHA  string `json:"-"`
	Err  string `json:"-"`
}

func NewResolver(u *Upstream, repo string) *Resolver {
	return &Resolver{U: u, Repo: repo, files: map[string]*fileAt{}}
}

func (r *Resolver) file(sha, path string) (string, bool, error) {
	key := sha + ":" + path
	r.mu.Lock()
	f, ok := r.files[key]
	if !ok {
		f = &fileAt{}
		r.files[key] = f
	}
	r.mu.Unlock()
	f.once.Do(func() {
		b, ok, err := r.U.ReadFile(sha, path)
		f.body, f.ok, f.err = string(b), ok, err
	})
	return f.body, f.ok, f.err
}

// Prefetch reads files concurrently; lazy blob fetches are round trips.
func (r *Resolver) Prefetch(pairs [][2]string) {
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, p := range pairs {
		wg.Add(1)
		sem <- struct{}{}
		go func(sha, path string) {
			defer wg.Done()
			defer func() { <-sem }()
			r.file(sha, path)
		}(p[0], p[1])
	}
	wg.Wait()
}

func symbolPatterns(sym string) []*regexp.Regexp {
	q := regexp.QuoteMeta
	if recv, name, ok := strings.Cut(sym, "."); ok {
		return []*regexp.Regexp{
			regexp.MustCompile(`(?m)^func \(\w*\s*\*?` + q(recv) + `(\[[^\]]*\])?\) ` + q(name) + `[\[(]`),
		}
	}
	return []*regexp.Regexp{
		regexp.MustCompile(`(?m)^func ` + q(sym) + `[\[(]`),
		regexp.MustCompile(`(?m)^func \([^)]*\) ` + q(sym) + `[\[(]`),
		regexp.MustCompile(`(?m)^(type|var|const) ` + q(sym) + `\b`),
		regexp.MustCompile(`(?m)^\t` + q(sym) + `\s+(=|[*\[\]A-Za-z])`), // in a type/var/const block
	}
}

// Resolve checks p (defaulting its SHA to def) and returns display text, a
// blob URL with the definition's line, and an error string if it does not
// resolve.
func (r *Resolver) Resolve(p Pointer, def string) ResolvedPointer {
	out := ResolvedPointer{Text: p.Path, Note: p.Note}
	if p.Symbol != "" {
		out.Text += ":" + p.Symbol
	}
	sha := p.SHA
	if sha == "" {
		sha = def
	}
	if sha == "" {
		out.Err = "no commit to resolve against"
		return out
	}
	full, err := r.U.Resolve(sha)
	if err != nil {
		out.Err = err.Error()
		return out
	}
	out.SHA = full
	if p.SHA != "" {
		out.Text += " @" + short(full)
	}
	body, ok, err := r.file(full, p.Path)
	if err != nil {
		out.Err = err.Error()
		return out
	}
	path := p.Path
	if !ok {
		// A package directory: look for the symbol in its Go files.
		files := r.U.ListDir(full, p.Path)
		if files == nil {
			out.Err = fmt.Sprintf("%s does not exist at %s", p.Path, short(full))
			return out
		}
		if p.Symbol == "" {
			out.URL = fmt.Sprintf("https://github.com/%s/tree/%s/%s", r.Repo, full, p.Path)
			return out
		}
		for _, f := range files {
			if !strings.HasSuffix(f, ".go") || strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, _, _ := r.file(full, f)
			for _, re := range symbolPatterns(p.Symbol) {
				if re.MatchString(b) {
					body, path, ok = b, f, true
					break
				}
			}
			if ok {
				break
			}
		}
		if !ok {
			out.Err = fmt.Sprintf("%s is not defined in package %s at %s", p.Symbol, p.Path, short(full))
			return out
		}
	}
	url := fmt.Sprintf("https://github.com/%s/blob/%s/%s", r.Repo, full, path)
	if p.Symbol == "" {
		out.URL = url
		return out
	}
	idx := -1
	if strings.HasSuffix(path, ".go") {
		for _, re := range symbolPatterns(p.Symbol) {
			if loc := re.FindStringIndex(body); loc != nil {
				idx = loc[0]
				break
			}
		}
	} else {
		idx = strings.Index(body, p.Symbol)
	}
	if idx < 0 {
		out.Err = fmt.Sprintf("%s is not defined in %s at %s", p.Symbol, p.Path, short(full))
		out.URL = url
		return out
	}
	out.URL = fmt.Sprintf("%s#L%d", url, strings.Count(body[:idx], "\n")+1)
	return out
}
