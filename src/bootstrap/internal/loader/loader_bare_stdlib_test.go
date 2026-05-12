// Tests for the bare-import → stdlib fall-through.
//
// `import "io"` (no std/ prefix) resolves first against a sibling, then
// falls through to `std/io` when no sibling claims the name. The std/*
// namespace is the implicit default, so users don't have to spell out
// the prefix for stdlib modules. Explicit `import "std/io"` keeps
// working for code that wants to be unambiguous.
package loader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Bare import with no sibling resolves to the stdlib copy under the
// configured stdlib root.
func TestBareImportResolvesToStdlibOnSiblingMiss(t *testing.T) {
	stdlibDir := t.TempDir()
	writeFile(t, stdlibDir, "calc.zg",
		"pub fn add(a: int, b: int) -> int { return a + b }\n")
	defer SetStdlibRootForTest(stdlibDir)()

	userDir := t.TempDir()
	entry := writeFile(t, userDir, "main.zg",
		"import \"calc\"\nprint calc.add(2, 3)\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ri := findResolvedImport(t, bundle.Entry, "calc")
	if !strings.HasSuffix(ri.Target.Path, "calc.zg") {
		t.Errorf("Target.Path = %q, want stdlib calc.zg", ri.Target.Path)
	}
	if !strings.Contains(ri.Target.Path, stdlibDir) {
		t.Errorf("Target.Path = %q does not point under stdlib root %q",
			ri.Target.Path, stdlibDir)
	}
}

// When both a sibling and a stdlib module match the bare name, the
// sibling wins — the v0.5 sibling contract is preserved, the fall-
// through is *fall*-through, not a shadow.
func TestBareImportSiblingShadowsStdlib(t *testing.T) {
	stdlibDir := t.TempDir()
	writeFile(t, stdlibDir, "calc.zg",
		"pub fn add(a: int, b: int) -> int { return 999 }\n")
	defer SetStdlibRootForTest(stdlibDir)()

	userDir := t.TempDir()
	writeFile(t, userDir, "calc.zg",
		"pub fn add(a: int, b: int) -> int { return a + b }\n")
	entry := writeFile(t, userDir, "main.zg",
		"import \"calc\"\nprint calc.add(2, 3)\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ri := findResolvedImport(t, bundle.Entry, "calc")
	if !strings.Contains(ri.Target.Path, userDir) {
		t.Errorf("Target.Path = %q should be the user-dir sibling, not the stdlib copy",
			ri.Target.Path)
	}
	if strings.Contains(string(ri.Target.Source), "999") {
		t.Errorf("resolved to stdlib body (return 999); want sibling body (return a + b)")
	}
}

// Bare import falls through to the registered fallback when neither
// sibling nor on-disk stdlib has the module. Chains together the new
// bare-resolution path with the older disk-miss → fallback hook.
func TestBareImportFallsThroughToFallback(t *testing.T) {
	emptyStdlib := t.TempDir()
	defer SetStdlibRootForTest(emptyStdlib)()
	defer SetStdlibFallbackForTest(func(family, name string) ([]byte, bool) {
		if family == "stdlib" && name == "calc" {
			return []byte("pub fn add(a: int, b: int) -> int { return 100 }\n"), true
		}
		return nil, false
	})()

	userDir := t.TempDir()
	entry := writeFile(t, userDir, "main.zg",
		"import \"calc\"\nprint calc.add(2, 3)\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ri := findResolvedImport(t, bundle.Entry, "calc")
	if !strings.Contains(string(ri.Target.Source), "return 100") {
		t.Errorf("Target.Source = %q, want fallback body", string(ri.Target.Source))
	}
}

// Total miss (no sibling, no stdlib, no fallback) reports the sibling-
// shaped diagnostic — anchored on the user-visible sibling path, no
// "stdlib" jargon. The fall-through is silent on miss.
func TestBareImportTotalMissUsesSiblingWording(t *testing.T) {
	emptyStdlib := t.TempDir()
	defer SetStdlibRootForTest(emptyStdlib)()

	userDir := t.TempDir()
	entry := writeFile(t, userDir, "main.zg",
		"import \"nope\"\nprint 1\n")

	_, err := Load(entry)
	if err == nil {
		t.Fatalf("expected not-found error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, `module "nope" not found at `) {
		t.Errorf("error missing sibling-shaped wording: %s", msg)
	}
	if strings.Contains(msg, "stdlib module not found") {
		t.Errorf("bare-name miss leaked stdlib wording: %s", msg)
	}
	if strings.Contains(msg, "std/nope") {
		t.Errorf("bare-name miss should not mention the std/ prefix the user did not type: %s", msg)
	}
}

// Explicit `import "std/io"` keeps working — the new fall-through is
// additive, not a replacement. Pins back-compat for the 47 .zg files
// that already use the explicit form.
func TestExplicitStdPrefixStillResolves(t *testing.T) {
	stdlibDir := t.TempDir()
	writeFile(t, stdlibDir, "calc.zg",
		"pub fn add(a: int, b: int) -> int { return a + b }\n")
	defer SetStdlibRootForTest(stdlibDir)()

	userDir := t.TempDir()
	entry := writeFile(t, userDir, "main.zg",
		"import \"std/calc\"\nprint calc.add(2, 3)\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ri := findResolvedImport(t, bundle.Entry, "calc")
	if !strings.HasSuffix(ri.Target.Path, "calc.zg") {
		t.Errorf("Target.Path = %q, want stdlib calc.zg", ri.Target.Path)
	}
}

// The bare-name fall-through must NOT consult the sys/* family. sys/*
// modules are platform-specific; reaching them requires the explicit
// `sys/` prefix so users opt in deliberately. A bare `import "path"`
// with sys/path on disk has to miss — silent rescue would let a bare
// name accidentally pull in platform-dependent code.
func TestBareImportDoesNotFallThroughToSys(t *testing.T) {
	stdlibDir := t.TempDir()
	// Plant sys/path on disk so it would resolve under the explicit
	// `sys/path` form. The bare-import path must ignore it.
	if err := os.MkdirAll(filepath.Join(stdlibDir, "sys", "path"), 0o755); err != nil {
		t.Fatalf("mkdir sys/path: %v", err)
	}
	writeFile(t, filepath.Join(stdlibDir, "sys", "path"), "mod.zg",
		"pub fn marker() -> int { return 7 }\n")
	defer SetStdlibRootForTest(stdlibDir)()

	userDir := t.TempDir()
	entry := writeFile(t, userDir, "main.zg",
		"import \"path\"\nprint 1\n")

	_, err := Load(entry)
	if err == nil {
		t.Fatalf("expected not-found error, got nil (bare 'path' must not resolve to sys/path)")
	}
	msg := err.Error()
	if !strings.Contains(msg, `module "path" not found at `) {
		t.Errorf("error missing sibling-shaped wording: %s", msg)
	}
	if strings.Contains(msg, "sys/") {
		t.Errorf("bare-name miss should not mention the sys/ prefix: %s", msg)
	}
}

// Sanity check that the explicit `sys/path` form still works against
// the same fixture used by the negative test above — pins that the
// fall-through restriction is a bare-name-only rule, not a global
// sys/* lockout.
func TestExplicitSysPrefixResolvesAgainstSameFixture(t *testing.T) {
	stdlibDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(stdlibDir, "sys", "path"), 0o755); err != nil {
		t.Fatalf("mkdir sys/path: %v", err)
	}
	writeFile(t, filepath.Join(stdlibDir, "sys", "path"), "mod.zg",
		"pub fn marker() -> int { return 7 }\n")
	defer SetStdlibRootForTest(stdlibDir)()

	userDir := t.TempDir()
	entry := writeFile(t, userDir, "main.zg",
		"import \"sys/path\"\nprint path.marker()\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ri := findResolvedImport(t, bundle.Entry, "path")
	if !strings.Contains(ri.Target.Path, "sys") {
		t.Errorf("Target.Path = %q, want a sys/* disk path", ri.Target.Path)
	}
}

// Leading-underscore names are a stdlib-internal scaffolding convention
// (underscore-prefixed names are reserved). The fall-through must respect that rule so
// internal scaffolding stays invisible even when the user spells the
// bare name: a registered fallback for `_secret` must NOT satisfy a
// bare `import "_secret"`, and the resulting diagnostic stays sibling-
// shaped (no surprise stdlib leak).
func TestBareImportUnderscoreSkipsStdlibFallthrough(t *testing.T) {
	emptyStdlib := t.TempDir()
	defer SetStdlibRootForTest(emptyStdlib)()
	defer SetStdlibFallbackForTest(func(family, name string) ([]byte, bool) {
		if family == "stdlib" && name == "_secret" {
			return []byte("pub fn x() -> int { return 1 }\n"), true
		}
		return nil, false
	})()

	userDir := t.TempDir()
	entry := writeFile(t, userDir, "main.zg",
		"import \"_secret\"\nprint 1\n")

	_, err := Load(entry)
	if err == nil {
		t.Fatalf("expected not-found error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, `module "_secret" not found at `) {
		t.Errorf("underscore bare import should error with sibling-shaped wording; got: %s", msg)
	}
	if strings.Contains(msg, "stdlib") {
		t.Errorf("underscore bare import leaked stdlib wording: %s", msg)
	}
}
