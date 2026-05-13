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
	// v0.14 layout refactor: sys/path migrated from sys/path/mod.zg to
	// the flat sys/path.zg form. The loader's per-module resolution
	// picks whichever layout the module ships, so this test pins the
	// flat-form path to lock in the migration. sys/syscall (still
	// dir-with-per-host-variants) is tested separately.
	if !strings.Contains(ri.Target.Path, "sys/path.zg") &&
		!strings.Contains(ri.Target.Path, `sys\path.zg`) {
		t.Errorf("Target.Path = %q, want suffix sys/path.zg", ri.Target.Path)
	}
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

// Underscore-prefixed names mirror the std/* reserved-scaffolding rule so any
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

// Guards against an accidental rename / move of the embedded sys/ tree
// that would silently turn every sys/* import into a miss with no
// other signal. The toolchain now ships the stdlib via //go:embed; the
// check fires the default fallback for a known sys/* module and asserts
// it returns non-empty source.
func TestSysTreeReachable(t *testing.T) {
	src, ok := defaultStdlibFallback("sys", "path")
	if !ok {
		t.Fatal("embedded sys/path module not reachable via defaultStdlibFallback")
	}
	if len(src) == 0 {
		t.Fatal("embedded sys/path module has empty source")
	}
}

// v0.14 per-host sys/* module selection: on a host whose (goos, goarch)
// pair matches platformArchs × platformSuffixes, the loader prefers
// `sys/<name>/mod_<goos>_<goarch>.zg` over the generic `mod.zg`. Tests
// the on-disk path via $ZERG_STDLIB so the host-override pair drives a
// real os.ReadFile.
func TestSysModulePathPrefersHostVariant(t *testing.T) {
	dir := t.TempDir()
	restoreStdlib := SetStdlibRootForTest(dir)
	defer restoreStdlib()
	restoreGoos := SetHostPlatformForTest("macos")
	defer restoreGoos()
	restoreGoarch := SetHostArchForTest("arm64")
	defer restoreGoarch()

	// Lay down both files; the variant must win.
	if err := os.MkdirAll(filepath.Join(dir, "sys", "demo"), 0o755); err != nil {
		t.Fatalf("mkdir sys/demo: %v", err)
	}
	writeFile(t, filepath.Join(dir, "sys", "demo"), "mod.zg",
		"# requires: v0.14\npub fn marker() -> int {\n\treturn 0\n}\n")
	writeFile(t, filepath.Join(dir, "sys", "demo"), "mod_macos_arm64.zg",
		"# requires: v0.14\npub fn marker() -> int {\n\treturn 1\n}\n")

	got := sysModulePath("demo")
	if !strings.HasSuffix(got, filepath.Join("sys", "demo", "mod_macos_arm64.zg")) {
		t.Errorf("sysModulePath = %q, want suffix sys/demo/mod_macos_arm64.zg", got)
	}
}

// When only the generic mod.zg exists on disk, sysModulePath falls
// through to it even if the host has a recognised (goos, goarch). The
// fallback rule keeps sys/path (platform-neutral mod.zg only) working
// on every supported host without requiring a per-arch shim.
func TestSysModulePathFallsBackToModZg(t *testing.T) {
	dir := t.TempDir()
	restoreStdlib := SetStdlibRootForTest(dir)
	defer restoreStdlib()
	restoreGoos := SetHostPlatformForTest("macos")
	defer restoreGoos()
	restoreGoarch := SetHostArchForTest("arm64")
	defer restoreGoarch()

	if err := os.MkdirAll(filepath.Join(dir, "sys", "neutral"), 0o755); err != nil {
		t.Fatalf("mkdir sys/neutral: %v", err)
	}
	writeFile(t, filepath.Join(dir, "sys", "neutral"), "mod.zg",
		"# requires: v0.14\npub fn marker() -> int {\n\treturn 0\n}\n")

	got := sysModulePath("neutral")
	if !strings.HasSuffix(got, filepath.Join("sys", "neutral", "mod.zg")) {
		t.Errorf("sysModulePath = %q, want suffix sys/neutral/mod.zg", got)
	}
}

// v0.14 layout refactor: sysModulePath probes three forms — flat
// sys/<name>.zg, sys/<name>/mod_<host>.zg, sys/<name>/mod.zg — and
// uses whichever the module ships. The flat form is the simplest
// shape, right for platform-neutral single-file modules.
func TestSysModulePathPrefersFlat(t *testing.T) {
	dir := t.TempDir()
	restoreStdlib := SetStdlibRootForTest(dir)
	defer restoreStdlib()
	restoreGoos := SetHostPlatformForTest("macos")
	defer restoreGoos()
	restoreGoarch := SetHostArchForTest("arm64")
	defer restoreGoarch()

	if err := os.MkdirAll(filepath.Join(dir, "sys"), 0o755); err != nil {
		t.Fatalf("mkdir sys: %v", err)
	}
	writeFile(t, filepath.Join(dir, "sys"), "flatmod.zg",
		"# requires: v0.14\npub fn marker() -> int {\n\treturn 7\n}\n")

	got := sysModulePath("flatmod")
	if !strings.HasSuffix(got, filepath.Join("sys", "flatmod.zg")) {
		t.Errorf("sysModulePath = %q, want suffix sys/flatmod.zg", got)
	}
}

// When a module ships BOTH flat sys/<name>.zg AND a dir sys/<name>/,
// the resolver prefers the flat form silently. A future authoring
// rule may reject the ambiguity outright; this test pins the
// preference until that rule lands.
func TestSysModulePathFlatBeatsDir(t *testing.T) {
	dir := t.TempDir()
	restoreStdlib := SetStdlibRootForTest(dir)
	defer restoreStdlib()
	restoreGoos := SetHostPlatformForTest("macos")
	defer restoreGoos()
	restoreGoarch := SetHostArchForTest("arm64")
	defer restoreGoarch()

	// Both forms present.
	if err := os.MkdirAll(filepath.Join(dir, "sys", "ambig"), 0o755); err != nil {
		t.Fatalf("mkdir sys/ambig: %v", err)
	}
	writeFile(t, filepath.Join(dir, "sys"), "ambig.zg",
		"# requires: v0.14\npub fn marker() -> int {\n\treturn 1\n}\n")
	writeFile(t, filepath.Join(dir, "sys", "ambig"), "mod.zg",
		"# requires: v0.14\npub fn marker() -> int {\n\treturn 2\n}\n")

	got := sysModulePath("ambig")
	if !strings.HasSuffix(got, filepath.Join("sys", "ambig.zg")) {
		t.Errorf("sysModulePath = %q, want flat sys/ambig.zg to win over sys/ambig/mod.zg", got)
	}
}

// An unrecognised host arch (sentinel "none") disables variant
// resolution entirely — sysModulePath returns the generic mod.zg
// path regardless of which variant files may exist.
func TestSysModulePathSkipsVariantOnUnknownHost(t *testing.T) {
	dir := t.TempDir()
	restoreStdlib := SetStdlibRootForTest(dir)
	defer restoreStdlib()
	restoreGoos := SetHostPlatformForTest("macos")
	defer restoreGoos()
	restoreGoarch := SetHostArchForTest("none")
	defer restoreGoarch()

	if err := os.MkdirAll(filepath.Join(dir, "sys", "demo"), 0o755); err != nil {
		t.Fatalf("mkdir sys/demo: %v", err)
	}
	writeFile(t, filepath.Join(dir, "sys", "demo"), "mod.zg",
		"# requires: v0.14\npub fn marker() -> int {\n\treturn 0\n}\n")
	writeFile(t, filepath.Join(dir, "sys", "demo"), "mod_macos_arm64.zg",
		"# requires: v0.14\npub fn marker() -> int {\n\treturn 1\n}\n")

	got := sysModulePath("demo")
	if !strings.HasSuffix(got, filepath.Join("sys", "demo", "mod.zg")) {
		t.Errorf("sysModulePath = %q, want suffix sys/demo/mod.zg (variant skipped on unknown host)", got)
	}
}
