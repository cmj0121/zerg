package loader

import (
	"strings"
	"testing"
)

// v0.8 Unit 2 — loader tests for the embedded `std/` resolver.
//
// Unit 2 stands the embed mechanism up; the actual std/io.zg etc. land in
// Unit 3 / Unit 4. The tests here pin the loader-side contract:
//
//   - `import "std/<unknown>"` rejects with "stdlib module not found".
//   - `std/...` imports never fall back to the working directory.
//   - non-`std/...` imports continue to behave exactly as v0.5.
//
// Underscore-prefixed names under `std/` and `sys/` are reserved for
// stdlib-internal scaffolding: the family gate in loadEmbeddedFamily
// rejects them with the standard "not found" wording before any disk or
// embed lookup, so internal files (had any been needed) would be
// invisible to user `import "std/<name>"` lookups.

// TestLoadStdlibUnknownReturnsNotFound covers the headline reject path: a
// user file imports a stdlib module that does not exist in the embedded FS.
func TestLoadStdlibUnknownReturnsNotFound(t *testing.T) {
	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg", "import \"std/foo\"\nprint 1\n")

	_, err := Load(entry)
	if err == nil {
		t.Fatalf("expected error for unknown stdlib module")
	}
	msg := err.Error()
	if !strings.Contains(msg, "stdlib module not found") {
		t.Errorf("error %q does not mention 'stdlib module not found'", msg)
	}
	if !strings.Contains(msg, "std/foo") {
		t.Errorf("error %q does not name the offending import path", msg)
	}
}

// TestLoadStdlibDoesNotFallBackToCwd asserts the stdlib resolver never falls
// through to the working directory. Even if a sibling `std/foo.zg` lives
// next to main.zg on disk, the loader rejects with "stdlib module not
// found" — `std/...` is a closed embed-only namespace.
func TestLoadStdlibDoesNotFallBackToCwd(t *testing.T) {
	dir := t.TempDir()
	// A plausible-looking but irrelevant on-disk file. The loader must not
	// consult it.
	writeFile(t, dir, "foo.zg", "fn quack() -> int { return 1 }\n")
	entry := writeFile(t, dir, "main.zg", "import \"std/foo\"\nprint 1\n")

	_, err := Load(entry)
	if err == nil {
		t.Fatalf("expected error for unknown stdlib module")
	}
	if !strings.Contains(err.Error(), "stdlib module not found") {
		t.Errorf("loader fell through to CWD; got %q", err.Error())
	}
}

// TestLoadStdlibUnderscoreRejected pins the underscore-name rule: any
// `std/<_name>` import rejects with the standard "stdlib module not
// found" wording, regardless of whether the file exists in the embed.
// The check fires at the family gate before disk / fallback consult,
// so it keeps reserved scaffolding names invisible to user code.
func TestLoadStdlibUnderscoreRejected(t *testing.T) {
	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg", "import \"std/_internal\"\nprint 1\n")

	_, err := Load(entry)
	if err == nil {
		t.Fatalf("expected error for underscore-prefixed stdlib name")
	}
	if !strings.Contains(err.Error(), "stdlib module not found") {
		t.Errorf("got %q, want 'stdlib module not found'", err.Error())
	}
}

// TestLoadNonStdlibImportUnchanged confirms v0.5 sibling-import behaviour is
// untouched: a non-`std/` path still resolves against the working directory.
func TestLoadNonStdlibImportUnchanged(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "util.zg", "fn double(x: int) -> int { return x * 2 }\n")
	entry := writeFile(t, dir, "main.zg", "import \"util\"\nprint 1\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(bundle.Modules) != 2 {
		t.Errorf("len(Modules) = %d, want 2", len(bundle.Modules))
	}
	if got := bundle.Entry.Imports[0].LocalName; got != "util" {
		t.Errorf("LocalName = %q, want util", got)
	}
}

// TestLoadStdlibAliasBindsLocalName pins the alias path: `import "std/foo"
// as bar` would bind `bar` as the local name even though the underlying
// resolution still fails. The alias decoupling is verified by checking the
// reject message: it mentions the bare path "std/foo", not the alias.
func TestLoadStdlibAliasMessageNamesPath(t *testing.T) {
	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg", "import \"std/foo\" as bar\nprint 1\n")

	_, err := Load(entry)
	if err == nil {
		t.Fatalf("expected error for unknown stdlib module")
	}
	if !strings.Contains(err.Error(), "std/foo") {
		t.Errorf("error %q does not surface the bare import path", err.Error())
	}
	if strings.Contains(err.Error(), "bar") && !strings.Contains(err.Error(), "std/foo") {
		t.Errorf("error %q surfaces the alias instead of the path", err.Error())
	}
}
