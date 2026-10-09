package main

import (
	"strings"
	"testing"
)

func TestGlobRegexp(t *testing.T) {
	m := newMatcher([]string{"core/git*.go", "core/gitref/**", "util/**/x.go"}, []string{"**/*_test.go"})
	for path, want := range map[string]bool{
		"core/git.go":                true,
		"core/git_local_cow.go":      true,
		"core/git_local_test.go":     false,
		"core/schema/git.go":         false,
		"core/gitref/a/b.go":         true,
		"util/x.go":                  true,
		"util/a/b/x.go":              true,
		"core/changeset.go":          false,
		"core/gitref/gitref_test.go": false,
	} {
		if got := m.Match(path); got != want {
			t.Errorf("%s: got %v, want %v", path, got, want)
		}
	}
}

func TestParsePointer(t *testing.T) {
	for in, want := range map[string]Pointer{
		"core/git.go:refJoin@4e9d0b0":                   {Path: "core/git.go", Symbol: "refJoin", SHA: "4e9d0b0"},
		"core/git_local.go:LocalGitRef.Tree":            {Path: "core/git_local.go", Symbol: "LocalGitRef.Tree"},
		"core/git_local_incremental.go":                 {Path: "core/git_local_incremental.go"},
		"engine/snapshots/fsdiff:WalkLayerDeltaChanges": {Path: "engine/snapshots/fsdiff", Symbol: "WalkLayerDeltaChanges"},
	} {
		got, err := ParsePointer(in)
		if err != nil || got != want {
			t.Errorf("%s: got %+v, %v", in, got, err)
		}
		if got.String() != in {
			t.Errorf("%s: round trip %s", in, got)
		}
	}
	for _, in := range []string{"dagql PerCallWhen", "core/git.go:a, b", ""} {
		if _, err := ParsePointer(in); err == nil {
			t.Errorf("%q: expected an error", in)
		}
	}
}

func TestSymbolPatterns(t *testing.T) {
	src := strings.Join([]string{
		"package core",
		"func (s *gitSchema) tree(ctx context.Context) {}",
		"func cowTree[T any](x T) {}",
		"type GitCheckoutBase struct{}",
		"var (",
		"\tgitTreeCacheKey = 1",
		")",
	}, "\n")
	for sym, want := range map[string]bool{
		"gitSchema.tree":   true,
		"cowTree":          true,
		"GitCheckoutBase":  true,
		"gitTreeCacheKey":  true,
		"tree":             true, // a method, any receiver
		"otherSchema.tree": false,
		"missing":          false,
	} {
		found := false
		for _, re := range symbolPatterns(sym) {
			found = found || re.MatchString(src)
		}
		if found != want {
			t.Errorf("%s: got %v, want %v", sym, found, want)
		}
	}
}

func TestPaletteDistinct(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		c := paletteColor(i)
		if !hexColor.MatchString(c) || seen[c] {
			t.Fatalf("palette entry %d (%s) is invalid or repeated", i, c)
		}
		seen[c] = true
	}
}
