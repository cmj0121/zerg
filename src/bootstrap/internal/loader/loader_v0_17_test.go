// v0.17 stdlib-nesting loader tests.
//
// The math/big landing forces the import-path validator to admit one
// segment of nesting (`"math/big"`). The widened rule is:
//
//   - bare identifier:           OK (sibling-first probe + std-fallback)
//   - `<seg>/<seg>`:             OK (stdlib-only resolution)
//   - deeper or empty segments:  reject with the v0.5 wording
//
// The `LocalName` for a slash-bearing import binds to the last segment
// — `import "math/big"` binds as `big`. Aliases (`as <name>`) override
// the default binding unchanged.
package loader

import (
	"strings"
	"testing"
)

// TestLoadBareNestedStdlibResolves drives the bare-import path:
// `import "math/big"` finds the embedded `math/big.zg` via the std/*
// fall-through. The user file types no `std/` prefix.
func TestLoadBareNestedStdlibResolves(t *testing.T) {
	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg",
		"import \"math/big\"\nprint 1\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ri := findResolvedImport(t, bundle.Entry, "big")
	if !strings.HasSuffix(ri.Target.Path, "math/big.zg") {
		t.Errorf("Target.Path = %q, want suffix math/big.zg", ri.Target.Path)
	}
}

// TestLoadStdPrefixedNestedStdlibResolves drives the explicit-prefix
// path: `import "std/math/big"` resolves identically. The shared
// fall-through and the explicit-prefix path land on the same module
// pointer when both names appear (covered separately by the loader's
// dedup machinery; this test just confirms the prefix-form resolves).
func TestLoadStdPrefixedNestedStdlibResolves(t *testing.T) {
	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg",
		"import \"std/math/big\"\nprint 1\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ri := findResolvedImport(t, bundle.Entry, "big")
	if !strings.HasSuffix(ri.Target.Path, "math/big.zg") {
		t.Errorf("Target.Path = %q, want suffix math/big.zg", ri.Target.Path)
	}
}

// TestLoadBareNestedDefaultLocalName pins the LocalName derivation:
// no `as` clause, slash-bearing path → binding is the last segment.
// Without this rule the loader would bind the import as the literal
// path string `"math/big"`, which is not a valid Zerg identifier and
// would break typeck on the importing module's first reference.
func TestLoadBareNestedDefaultLocalName(t *testing.T) {
	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg",
		"import \"math/big\"\nprint 1\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := bundle.Entry.Imports[0].LocalName
	if got != "big" {
		t.Errorf("LocalName = %q, want %q", got, "big")
	}
}

// TestLoadNestedAliasOverridesDefault confirms the `as` clause still
// wins over the new last-segment default. The widening must not
// regress the alias surface.
func TestLoadNestedAliasOverridesDefault(t *testing.T) {
	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg",
		"import \"math/big\" as bigm\nprint 1\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := bundle.Entry.Imports[0].LocalName
	if got != "bigm" {
		t.Errorf("LocalName = %q, want %q", got, "bigm")
	}
}

// TestLoadTwoSegmentDeepNestingRejects pins the upper bound: three
// segments (`"math/foo/bar"`) reject with the v0.5 invalid-path
// wording. v0.17 deliberately caps nesting at one segment; deeper
// layouts stay available for later minors as a strict superset.
func TestLoadTwoSegmentDeepNestingRejects(t *testing.T) {
	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg",
		"import \"math/foo/bar\"\nprint 1\n")

	_, err := Load(entry)
	if err == nil {
		t.Fatalf("expected error for over-nested import path")
	}
	if !strings.Contains(err.Error(), "invalid module path") {
		t.Errorf("error %q does not name the invalid-path rule", err.Error())
	}
}

// TestLoadEmptySegmentNestingRejects pins the empty-segment branch:
// `"math//big"` reports as invalid (no silent collapse to `math/big`).
// Leading and trailing slashes share the same rejection path.
func TestLoadEmptySegmentNestingRejects(t *testing.T) {
	cases := []string{"math//big", "/big", "math/"}
	for _, p := range cases {
		dir := t.TempDir()
		entry := writeFile(t, dir, "main.zg",
			"import \""+p+"\"\nprint 1\n")

		_, err := Load(entry)
		if err == nil {
			t.Fatalf("expected error for empty-segment path %q", p)
		}
		if !strings.Contains(err.Error(), "invalid module path") {
			t.Errorf("path %q: error %q does not name the invalid-path rule", p, err.Error())
		}
	}
}

// TestLoadStdPrefixedDeepNestingRejects mirrors the bare-path deep-
// nesting test under the explicit-prefix form. The post-prefix
// portion is what the family gate validates, so the rule applies
// uniformly across both spellings.
func TestLoadStdPrefixedDeepNestingRejects(t *testing.T) {
	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg",
		"import \"std/math/foo/bar\"\nprint 1\n")

	_, err := Load(entry)
	if err == nil {
		t.Fatalf("expected error for over-nested std-prefixed import")
	}
	if !strings.Contains(err.Error(), "stdlib module not found") {
		t.Errorf("error %q does not surface the not-found wording", err.Error())
	}
}

// TestIsValidStdlibPathUnit pins the helper directly. Boundary cases
// (empty, leading/trailing slash, double-slash, leading digit on a
// segment, identifier with digits past the first byte) are easy to
// regress with a naive HasPrefix; pin them explicitly.
func TestIsValidStdlibPathUnit(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"math", true},
		{"math/big", true},
		{"math/big_decimal", true},
		{"math_big/decimal", true},
		{"a/b/c", false},
		{"a//b", false},
		{"/a", false},
		{"a/", false},
		{"1math/big", false},
		{"math/1big", false},
		{"_math/big", true},
		{"math/_big", true},
		{"math-big", false},
	}
	for _, c := range cases {
		if got := isValidStdlibPath(c.in); got != c.want {
			t.Errorf("isValidStdlibPath(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestLastPathSegmentUnit pins the helper directly. A path with no
// slash returns itself; otherwise the substring after the final
// slash. Empty strings and edge cases included.
func TestLastPathSegmentUnit(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"math", "math"},
		{"math/big", "big"},
		{"a/b/c", "c"},
		{"trailing/", ""},
		{"/leading", "leading"},
	}
	for _, c := range cases {
		if got := lastPathSegment(c.in); got != c.want {
			t.Errorf("lastPathSegment(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
