// Tests for the v0.13 platform-suffix file-resolution rule.
//
// A `.zg` file whose basename ends with `_macos.zg` or `_linux.zg` is
// compiled only on the matching host. Sibling-import resolution probes the
// host-suffixed name before falling back to the unsuffixed name; both
// existing on a matching host is an ambiguity error. The wrong-host suffix
// alone is invisible — sibling resolution falls through to the standard
// not-found path. Entry-file mismatch gets the dedicated wrong-platform
// diagnostic before any read happens.
//
// Tests pin the host via SetHostPlatformForTest so they run on any
// developer machine (CI on macOS for the real corpus; these unit tests on
// any host).
package loader

import (
	"strings"
	"testing"
)

// Sibling import resolves to the _macos.zg form when the host is macOS.
func TestSiblingPicksMacosSuffixOnDarwin(t *testing.T) {
	restore := SetHostPlatformForTest("macos")
	defer restore()

	dir := t.TempDir()
	// Only the _macos.zg form exists — must be picked.
	writeFile(t, dir, "helper_macos.zg",
		"pub fn greet() -> int { return 42 }\n")
	entry := writeFile(t, dir, "main.zg",
		"import \"helper\"\nprint helper.greet()\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ri := findResolvedImport(t, bundle.Entry, "helper")
	if !strings.HasSuffix(ri.Target.Path, "helper_macos.zg") {
		t.Errorf("Target.Path = %q, want suffix helper_macos.zg", ri.Target.Path)
	}
}

// Sibling import prefers _macos.zg over the unsuffixed form when both exist
// — wait, that's the ambiguity case. The non-ambiguous variant is: only the
// host-suffixed form exists (covered above) OR only the unsuffixed form
// exists (covered here).
func TestSiblingFallsBackToUnsuffixedWhenOnlyOneExists(t *testing.T) {
	restore := SetHostPlatformForTest("macos")
	defer restore()

	dir := t.TempDir()
	writeFile(t, dir, "helper.zg",
		"pub fn greet() -> int { return 7 }\n")
	entry := writeFile(t, dir, "main.zg",
		"import \"helper\"\nprint helper.greet()\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ri := findResolvedImport(t, bundle.Entry, "helper")
	if !strings.HasSuffix(ri.Target.Path, "helper.zg") ||
		strings.HasSuffix(ri.Target.Path, "_macos.zg") ||
		strings.HasSuffix(ri.Target.Path, "_linux.zg") {
		t.Errorf("Target.Path = %q, want unsuffixed helper.zg", ri.Target.Path)
	}
}

// Ambiguity: both helper.zg and helper_macos.zg exist on a macOS host.
func TestSiblingAmbiguityWhenBothFormsExist(t *testing.T) {
	restore := SetHostPlatformForTest("macos")
	defer restore()

	dir := t.TempDir()
	writeFile(t, dir, "helper.zg",
		"pub fn greet() -> int { return 1 }\n")
	writeFile(t, dir, "helper_macos.zg",
		"pub fn greet() -> int { return 2 }\n")
	entry := writeFile(t, dir, "main.zg",
		"import \"helper\"\nprint helper.greet()\n")

	_, err := Load(entry)
	if err == nil {
		t.Fatalf("expected ambiguity error, got nil")
	}
	want := `module "helper" matches both helper.zg and helper_macos.zg; choose one`
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error missing %q\n%s", want, err.Error())
	}
}

// Wrong-host sibling is invisible: only helper_linux.zg exists on macOS →
// standard not-found diagnostic, NOT a wrong-platform diagnostic.
func TestSiblingWrongHostOnlyProducesNotFound(t *testing.T) {
	restore := SetHostPlatformForTest("macos")
	defer restore()

	dir := t.TempDir()
	writeFile(t, dir, "helper_linux.zg",
		"pub fn greet() -> int { return 9 }\n")
	entry := writeFile(t, dir, "main.zg",
		"import \"helper\"\nprint helper.greet()\n")

	_, err := Load(entry)
	if err == nil {
		t.Fatalf("expected not-found error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, `module "helper" not found`) {
		t.Errorf("error missing 'module \"helper\" not found': %s", msg)
	}
	if strings.Contains(msg, "gated to") {
		t.Errorf("sibling miss should not use wrong-platform wording; got: %s", msg)
	}
}

// Entry-file wrong-platform: foo_linux.zg on macOS rejects with the
// dedicated diagnostic before any read happens.
func TestEntryWrongPlatform(t *testing.T) {
	restore := SetHostPlatformForTest("macos")
	defer restore()

	dir := t.TempDir()
	entry := writeFile(t, dir, "main_linux.zg",
		"print 1\n")

	_, err := Load(entry)
	if err == nil {
		t.Fatalf("expected wrong-platform error, got nil")
	}
	want := "entry file main_linux.zg is gated to linux but host is macos"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error missing %q\n%s", want, err.Error())
	}
}

// Entry-file matching platform loads fine: main_macos.zg on macOS works.
func TestEntryMatchingPlatformLoads(t *testing.T) {
	restore := SetHostPlatformForTest("macos")
	defer restore()

	dir := t.TempDir()
	entry := writeFile(t, dir, "main_macos.zg",
		"print 1\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// The entry's canonical Name stays "main" regardless of the on-disk
	// filename — that's the v0.5 rule and v0.13 must not perturb it.
	if bundle.Entry.Name != "main" {
		t.Errorf("Entry.Name = %q, want %q", bundle.Entry.Name, "main")
	}
}

// Stdlib carve-out sanity: an `import "std/io"` on a macOS host resolves
// to the embedded std/io module unchanged. The suffix table must NOT be
// consulted for stdlib paths; if it were, we'd see "stdlib module not
// found: std/io_macos" or similar weirdness.
func TestStdlibUnaffectedBySuffixTable(t *testing.T) {
	restore := SetHostPlatformForTest("macos")
	defer restore()

	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg",
		"import \"std/io\"\nprint 1\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ri := findResolvedImport(t, bundle.Entry, "io")
	// Embedded stdlib path layout is `stdlib/<name>.zg` (see embed.go).
	// The carve-out's contract is that this path is what we get back even
	// when the host has a recognized platform-suffix string set; if the
	// suffix table were consulted, the embed lookup would fall through to
	// "stdlib module not found" or to a synthetic "stdlib/io_macos.zg" miss.
	if !strings.Contains(ri.Target.Path, "stdlib/io.zg") &&
		!strings.Contains(ri.Target.Path, "stdlib\\io.zg") {
		t.Errorf("Target.Path = %q, want a stdlib/io.zg embed path", ri.Target.Path)
	}
}

// Unsupported host (hostPlatform == "") skips the suffix probe entirely:
// only the unsuffixed form is considered, _macos.zg and _linux.zg files
// are invisible for sibling resolution.
func TestSiblingUnsupportedHostSkipsSuffix(t *testing.T) {
	restore := SetHostPlatformForTest("none")
	defer restore()

	dir := t.TempDir()
	// Only the suffixed form exists. On an unsupported host nothing
	// resolves to it — we should get a not-found, not an ambiguity, not
	// an accidental hit.
	writeFile(t, dir, "helper_macos.zg",
		"pub fn greet() -> int { return 1 }\n")
	entry := writeFile(t, dir, "main.zg",
		"import \"helper\"\nprint helper.greet()\n")

	_, err := Load(entry)
	if err == nil {
		t.Fatalf("expected not-found error on unsupported host, got nil")
	}
	if !strings.Contains(err.Error(), `module "helper" not found`) {
		t.Errorf("error missing not-found wording: %s", err.Error())
	}
}

// fileSuffixPlatform unit checks. The boundary cases (literal "_macos.zg",
// double suffix, no extension) are easy to get wrong with a naive
// HasSuffix — pin them explicitly.
func TestFileSuffixPlatformBoundaries(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"main.zg", ""},
		{"main_macos.zg", "macos"},
		{"main_linux.zg", "linux"},
		{"main_macos_linux.zg", "linux"}, // last suffix wins
		{"_macos.zg", ""},                // no module-name stem
		{"_linux.zg", ""},                //
		{"macos_notes.zg", ""},           // suffix is .zg, not _macos.zg
		{"main_windows.zg", ""},          // unsupported platform → ignored
		{"helper.txt", ""},               // non-zg basename
		{"helper_macos", ""},             // missing .zg extension
	}
	for _, c := range cases {
		if got := fileSuffixPlatform(c.in); got != c.want {
			t.Errorf("fileSuffixPlatform(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// SetHostPlatformForTest must round-trip through its restore func.
func TestSetHostPlatformForTestRoundtrip(t *testing.T) {
	// Capture the pre-test value via a no-op override.
	original := hostPlatform()

	restore1 := SetHostPlatformForTest("macos")
	if hostPlatform() != "macos" {
		t.Errorf("override macos: hostPlatform() = %q", hostPlatform())
	}
	restore2 := SetHostPlatformForTest("linux")
	if hostPlatform() != "linux" {
		t.Errorf("override linux: hostPlatform() = %q", hostPlatform())
	}
	restore2()
	if hostPlatform() != "macos" {
		t.Errorf("after restore2: hostPlatform() = %q, want macos", hostPlatform())
	}
	restore1()
	if hostPlatform() != original {
		t.Errorf("after restore1: hostPlatform() = %q, want %q", hostPlatform(), original)
	}
}
