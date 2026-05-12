// Tests for the stdlib disk-miss → bootstrap-fallback chain.
//
// The README documents `std/<name>` and `sys/<name>` as resolving first
// against the on-disk stdlib tree and, on miss, falling through to the
// toolchain's built-in implementation. These tests pin the dispatch:
//
//   - disk miss + no fallback → "<family> module not found"  (unchanged)
//   - disk miss +    fallback → fallback source is parsed and cached
//   - disk hit                → disk wins; fallback is never consulted
//
// SetStdlibRootForTest points the disk root at an empty TempDir so we
// can force the miss path without polluting the real src/std/ tree.
// SetStdlibFallbackForTest injects a synthetic registry per test.
package loader

import (
	"strings"
	"testing"
)

// Disk miss + registered fallback → the fallback source loads and the
// import resolves to a Module whose body came from the registry.
func TestStdlibFallbackResolvesOnDiskMiss(t *testing.T) {
	emptyRoot := t.TempDir()
	defer SetStdlibRootForTest(emptyRoot)()
	defer SetStdlibFallbackForTest(func(family, name string) ([]byte, bool) {
		if family == "stdlib" && name == "fakemod" {
			return []byte("pub fn answer() -> int { return 42 }\n"), true
		}
		return nil, false
	})()

	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg",
		"import \"std/fakemod\"\nprint fakemod.answer()\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ri := findResolvedImport(t, bundle.Entry, "fakemod")
	if ri.Target == nil {
		t.Fatalf("ResolvedImport.Target is nil")
	}
	if !strings.Contains(string(ri.Target.Source), "return 42") {
		t.Errorf("Target.Source = %q, want fallback body", string(ri.Target.Source))
	}
}

// Disk miss + no fallback registered → standard not-found diagnostic.
// Pins the default behaviour: today's empty registry preserves the
// pre-fallback error surface verbatim.
func TestStdlibFallbackMissingStaysNotFound(t *testing.T) {
	emptyRoot := t.TempDir()
	defer SetStdlibRootForTest(emptyRoot)()
	// No SetStdlibFallbackForTest — default returns (nil, false).

	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg",
		"import \"std/nope\"\nprint 1\n")

	_, err := Load(entry)
	if err == nil {
		t.Fatalf("expected not-found error, got nil")
	}
	if !strings.Contains(err.Error(), "stdlib module not found: std/nope") {
		t.Errorf("error missing not-found wording: %s", err.Error())
	}
}

// The sys/* family shares the same dispatch — fallback works there too,
// and the family label is plumbed through so a stdlib registration does
// NOT bleed into sys lookups.
func TestSysFallbackResolvesOnDiskMiss(t *testing.T) {
	emptyRoot := t.TempDir()
	defer SetStdlibRootForTest(emptyRoot)()
	defer SetStdlibFallbackForTest(func(family, name string) ([]byte, bool) {
		if family == "sys" && name == "fakeplat" {
			return []byte("pub fn host() -> int { return 7 }\n"), true
		}
		return nil, false
	})()

	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg",
		"import \"sys/fakeplat\"\nprint fakeplat.host()\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ri := findResolvedImport(t, bundle.Entry, "fakeplat")
	if !strings.Contains(string(ri.Target.Source), "return 7") {
		t.Errorf("Target.Source = %q, want sys fallback body", string(ri.Target.Source))
	}
}

// Family label scoping: a stdlib-only registration must NOT satisfy a
// sys/<same-name> import. Guards against accidental cross-family bleed
// when the fallback registry grows.
func TestFallbackFamilyLabelScoped(t *testing.T) {
	emptyRoot := t.TempDir()
	defer SetStdlibRootForTest(emptyRoot)()
	defer SetStdlibFallbackForTest(func(family, name string) ([]byte, bool) {
		if family == "stdlib" && name == "shared" {
			return []byte("pub fn a() -> int { return 1 }\n"), true
		}
		return nil, false
	})()

	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg",
		"import \"sys/shared\"\nprint 1\n")

	_, err := Load(entry)
	if err == nil {
		t.Fatalf("expected sys not-found error, got nil")
	}
	if !strings.Contains(err.Error(), "sys module not found: sys/shared") {
		t.Errorf("error missing sys not-found wording: %s", err.Error())
	}
}

// Disk hit should win unconditionally — fallback is the disk-miss
// fall-through, never a shadow or override of an on-disk file.
func TestFallbackNotConsultedWhenDiskHits(t *testing.T) {
	stdlibDir := t.TempDir()
	// Plant a real on-disk module.
	writeFile(t, stdlibDir, "real.zg",
		"pub fn disk_value() -> int { return 100 }\n")
	defer SetStdlibRootForTest(stdlibDir)()

	// Register a different fallback for the same name; if the loader
	// consulted it we'd see "return 999".
	defer SetStdlibFallbackForTest(func(family, name string) ([]byte, bool) {
		if family == "stdlib" && name == "real" {
			return []byte("pub fn disk_value() -> int { return 999 }\n"), true
		}
		return nil, false
	})()

	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg",
		"import \"std/real\"\nprint real.disk_value()\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ri := findResolvedImport(t, bundle.Entry, "real")
	if !strings.Contains(string(ri.Target.Source), "return 100") {
		t.Errorf("Target.Source = %q, want on-disk body (fallback leaked)", string(ri.Target.Source))
	}
}

// Reserved leading-underscore names short-circuit BEFORE the disk read
// and therefore also before the fallback consult — registering one
// must remain invisible to user code, matching v0.8's contract.
func TestFallbackUnderscoreNamesStillInvisible(t *testing.T) {
	emptyRoot := t.TempDir()
	defer SetStdlibRootForTest(emptyRoot)()
	defer SetStdlibFallbackForTest(func(family, name string) ([]byte, bool) {
		if family == "stdlib" && name == "_secret" {
			return []byte("pub fn x() -> int { return 1 }\n"), true
		}
		return nil, false
	})()

	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg",
		"import \"std/_secret\"\nprint 1\n")

	_, err := Load(entry)
	if err == nil {
		t.Fatalf("expected not-found error for underscore name, got nil")
	}
	if !strings.Contains(err.Error(), "stdlib module not found") {
		t.Errorf("underscore name should produce standard not-found: %s", err.Error())
	}
}
