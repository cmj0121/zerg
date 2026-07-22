package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeEntry writes src to a temp entry file and returns its path, for the
// path-taking CompileTests / CompileProgram.
func writeEntry(t *testing.T, src string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "entry.zg")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	return p
}

// TestBuildIgnoresTestFunction is the Phase 1i isolation guarantee: adding a
// `#[test]` function to a program leaves a normal `zerg build`'s emitted C
// byte-identical. The two sources differ only by the test function (the import and
// everything else is held constant), so any difference would be the test leaking
// into the build.
func TestBuildIgnoresTestFunction(t *testing.T) {
	withTest := "import \"testing\"\n\n#[test]\nfn test_thing() {\n  testing.assert(true)\n}\n\n" +
		"fn main() {\n  testing.assert(true)\n  print 42\n}\n"
	noTest := "import \"testing\"\n\n" +
		"fn main() {\n  testing.assert(true)\n  print 42\n}\n"

	a, _, da := Compile(withTest)
	b, _, db := Compile(noTest)
	if len(da) != 0 || len(db) != 0 {
		t.Fatalf("unexpected diagnostics: %v / %v", da, db)
	}
	if a != b {
		t.Fatalf("a #[test] function changed the build output:\n--- with ---\n%s\n--- without ---\n%s", a, b)
	}
}

// TestCompileTestsEmitsDriver checks CompileTests keeps the `#[test]` functions and
// emits a test-driver translation unit: the runtime header, the test harness header,
// and the generated `int main(void)` that runs each test and returns the summary. The
// Manifest always reports NeedsRuntime.
func TestCompileTestsEmitsDriver(t *testing.T) {
	src := "import \"testing\"\n\n#[test]\nfn test_ok() {\n  testing.assert(true)\n}\n"
	entry := writeEntry(t, src)
	code, manifest, diags := CompileTests(entry)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !manifest.NeedsRuntime {
		t.Fatalf("a test binary must report NeedsRuntime")
	}
	for _, want := range []string{"#include \"zrt_test.h\"", "int main(void) {", "zrt_test_begin();", "zrt_test_summary()"} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted test driver missing %q:\n%s", want, code)
		}
	}
}

// TestCompileTestsReportsShapeError checks a malformed `#[test]` (a parameter) is a
// compile diagnostic under `zerg test`, so the driver reports it and exits non-zero.
func TestCompileTestsReportsShapeError(t *testing.T) {
	entry := writeEntry(t, "#[test]\nfn test_bad(x: int) {\n  nop\n}\n")
	_, _, diags := CompileTests(entry)
	if len(diags) == 0 {
		t.Fatalf("a malformed #[test] must be reported")
	}
}
