package loader

import (
	"strings"
	"testing"
)

// The catalog is hand-curated and tiny — these tests pin the invariants
// that the `zerg stdlib` command (and any future consumer) relies on,
// without forcing exact-string golden comparisons that would churn every
// time we tweak a description.

// TestCatalogNonEmpty sanity-checks that Catalog returns at least the
// modules shipped today. The lower bound (7) matches the current set;
// raising it when we add a module is intentional churn.
func TestCatalogNonEmpty(t *testing.T) {
	got := Catalog()
	if len(got) < 7 {
		t.Fatalf("Catalog returned %d entries, want at least 7", len(got))
	}
}

// TestCatalogEntriesValid pins the per-entry contract: non-empty Path,
// non-empty Description, and a path that matches one of the two
// display conventions — bare name (implicit std/*) or `sys/<name>`
// (explicit sys/* prefix). Anything else is a malformed entry.
func TestCatalogEntriesValid(t *testing.T) {
	for i, e := range Catalog() {
		if e.Path == "" {
			t.Errorf("entry %d: empty Path", i)
		}
		if e.Description == "" {
			t.Errorf("entry %d (%s): empty Description", i, e.Path)
		}
		// std/* entries are displayed bare (no prefix); only sys/* keeps
		// its prefix. A path starting with "std/" would mean the entry
		// was authored before the bare-import default landed.
		if strings.HasPrefix(e.Path, "std/") {
			t.Errorf("entry %d (%s): std/* entries are displayed bare; drop the std/ prefix", i, e.Path)
		}
		if strings.Contains(e.Path, "/") && !strings.HasPrefix(e.Path, "sys/") {
			t.Errorf("entry %d (%s): only sys/* keeps an explicit prefix", i, e.Path)
		}
	}
}

// TestCatalogStableOrder pins the documented ordering: bare std/*
// entries first, alphabetical; then sys/* entries, alphabetical. The
// `zerg stdlib` output relies on this for predictable column rendering.
func TestCatalogStableOrder(t *testing.T) {
	entries := Catalog()
	sawSys := false
	var prev string
	var prevFamily string
	for _, e := range entries {
		family := "std"
		if strings.HasPrefix(e.Path, "sys/") {
			family = "sys"
		}
		if family == "sys" {
			sawSys = true
		} else if sawSys {
			t.Errorf("bare std/* entry %s appears after a sys/* entry — families not grouped", e.Path)
		}
		if family == prevFamily && prev != "" && e.Path <= prev {
			t.Errorf("non-alphabetical: %s <= %s within family %s", e.Path, prev, family)
		}
		prev = e.Path
		prevFamily = family
	}
}

// TestCatalogNoDuplicates guards against accidental duplicate registration
// when a module is moved between families or renamed without removing the
// old entry.
func TestCatalogNoDuplicates(t *testing.T) {
	seen := make(map[string]bool)
	for _, e := range Catalog() {
		if seen[e.Path] {
			t.Errorf("duplicate Path: %s", e.Path)
		}
		seen[e.Path] = true
	}
}

// TestCatalogReturnsFreshCopy ensures callers can mutate (sort, filter)
// the returned slice without affecting subsequent calls — the doc
// promises a fresh slice per call.
func TestCatalogReturnsFreshCopy(t *testing.T) {
	a := Catalog()
	if len(a) == 0 {
		t.Skip("empty catalog — no copy semantics to test")
	}
	a[0].Path = "tampered"
	b := Catalog()
	if b[0].Path == "tampered" {
		t.Errorf("Catalog leaked shared state: mutation of first call's slice affected second call")
	}
}
