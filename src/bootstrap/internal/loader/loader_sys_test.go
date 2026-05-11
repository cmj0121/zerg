// Tests for the v0.14 `sys/*` import family. Mirrors the std/* loader
// tests' shape: confirm the prefix routes to the on-disk stdlib tree,
// the directory-module `mod.zg` convention resolves correctly, the
// local-binding name defaults to the post-prefix component, and miss
// cases produce the uniform "sys module not found" diagnostic without
// falling through to sibling resolution.
package loader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSysPathResolves(t *testing.T) {
	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg",
		"import \"sys/path\"\nprint 1\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ri := findResolvedImport(t, bundle.Entry, "path")
	if !strings.Contains(ri.Target.Path, "sys/path/mod.zg") &&
		!strings.Contains(ri.Target.Path, `sys\path\mod.zg`) {
		t.Errorf("Target.Path = %q, want suffix sys/path/mod.zg", ri.Target.Path)
	}
	// v0.14 binds the short-name to `path`, not `path/mod` — cycle
	// diagnostics name modules by short-name and `path/mod.zg imports …`
	// would read oddly. The post-prefix component is the canonical name.
	if ri.Target.ShortName != "path" {
		t.Errorf("Target.ShortName = %q, want %q", ri.Target.ShortName, "path")
	}
}

func TestLoadSysAliased(t *testing.T) {
	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg",
		"import \"sys/path\" as p\nprint 1\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ri := bundle.Entry.Imports[0]
	if ri.LocalName != "p" {
		t.Errorf("LocalName = %q, want %q", ri.LocalName, "p")
	}
}

// The miss case must NOT fall through to the sibling-resolution path —
// that would emit a different "module ... not found" wording naming a
// non-existent working-directory file path.
func TestLoadSysMissingProducesSysDiagnostic(t *testing.T) {
	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg",
		"import \"sys/doesnotexist\"\nprint 1\n")

	_, err := Load(entry)
	if err == nil {
		t.Fatalf("expected miss error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "sys module not found: sys/doesnotexist") {
		t.Errorf("error missing 'sys module not found: sys/doesnotexist': %s", msg)
	}
	if strings.Contains(msg, "doesnotexist.zg") &&
		!strings.Contains(msg, "/mod.zg") {
		t.Errorf("sys miss should not name a sibling .zg path; got: %s", msg)
	}
}

func TestLoadSysBarePrefixRejected(t *testing.T) {
	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg",
		"import \"sys/\"\nprint 1\n")

	_, err := Load(entry)
	if err == nil {
		t.Fatalf("expected miss error, got nil")
	}
	if !strings.Contains(err.Error(), "sys module not found") {
		t.Errorf("error missing 'sys module not found': %s", err.Error())
	}
}

// Underscore-prefixed names mirror the std/_placeholder rule so any
// future internal-only scaffolding files stay invisible to user code.
func TestLoadSysUnderscorePrefixedRejected(t *testing.T) {
	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg",
		"import \"sys/_internal\"\nprint 1\n")

	_, err := Load(entry)
	if err == nil {
		t.Fatalf("expected miss error, got nil")
	}
	if !strings.Contains(err.Error(), "sys module not found") {
		t.Errorf("error missing 'sys module not found': %s", err.Error())
	}
}

// Smoke test for the "all toolchain-shipped modules can import each
// other" invariant: a user program importing both families ends up
// with both modules in the bundle.
func TestLoadSysAndStdCoexist(t *testing.T) {
	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg",
		"import \"std/strings\"\nimport \"sys/path\"\nprint 1\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	short := map[string]bool{}
	for _, m := range bundle.Modules {
		short[m.ShortName] = true
	}
	for _, want := range []string{"main", "strings", "path"} {
		if !short[want] {
			t.Errorf("missing module ShortName %q (have: %v)", want, short)
		}
	}
}

// Parse-time smoke check against accidental drift in mod.zg: every edit
// to the module body gets validated through the same parser the loader
// uses, so a bad commit is caught here rather than at first import.
func TestSysPathModParses(t *testing.T) {
	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg",
		"import \"sys/path\"\nprint 1\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ri := findResolvedImport(t, bundle.Entry, "path")
	if ri.Target.Program == nil {
		t.Fatalf("sys/path module has no parsed program")
	}
	if len(ri.Target.Program.Statements) == 0 {
		t.Errorf("sys/path top-level statement list is empty")
	}
}

// Guards against an accidental rename / move of src/std/sys/ that would
// silently turn every sys/* import into a miss with no other signal.
func TestSysTreeReachable(t *testing.T) {
	root := stdlibRoot()
	if root == "" {
		t.Fatal("stdlibRoot() returned empty; src/std/ discovery failed")
	}
	sysDir := filepath.Join(root, "sys")
	entries, err := os.ReadDir(sysDir)
	if err != nil {
		t.Fatalf("read %s: %v", sysDir, err)
	}
	if len(entries) == 0 {
		t.Fatalf("%s is empty", sysDir)
	}
	found := false
	for _, e := range entries {
		if e.Name() == "path" && e.IsDir() {
			found = true
		}
	}
	if !found {
		t.Errorf("%s does not contain a `path/` directory; entries: %v", sysDir, entries)
	}
}
