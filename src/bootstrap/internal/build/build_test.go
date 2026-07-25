package build

import (
	"strings"
	"testing"
)

func TestCompileSuccess(t *testing.T) {
	code, _, diags := Compile("fn main() {\n  print 1 + 2\n}")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !strings.Contains(code, "int main(void)") {
		t.Fatalf("emitted C missing a C main:\n%s", code)
	}
}

func TestCompileStopsAtParseError(t *testing.T) {
	code, _, diags := Compile("fn f( {")
	if len(diags) == 0 {
		t.Fatalf("expected a parse diagnostic")
	}
	if code != "" {
		t.Fatalf("code should be empty on failure, got %q", code)
	}
}

func TestCompileStopsAtSemaError(t *testing.T) {
	code, _, diags := Compile("fn main() { print undefined_name }")
	if len(diags) == 0 {
		t.Fatalf("expected a sema diagnostic")
	}
	if code != "" {
		t.Fatalf("code should be empty on failure, got %q", code)
	}
}

// TestBundleImportIO covers the stdlib bundle importer (Decision C): `import "io"`
// merges io.zg into the unit, a member call resolves to the namespace-prefixed
// function, the write intrinsics lower to their sys.c primitives, and the manifest
// records the io/runtime dependency.
func TestBundleImportIO(t *testing.T) {
	src := "import \"io\"\n" +
		"fn main() -> Result[nil] {\n" +
		"\tio.println(\"hi\")\n" +
		"\tio.write(\"x\")\n" +
		"\tio.write_int(7)\n" +
		"\treturn nil\n" +
		"}\n"
	code, manifest, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	for _, want := range []string{"zg_io__println(", "zg_io__write(", "zg_io__write_int(", "zrt_write_str("} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted C missing %q:\n%s", want, code)
		}
	}
	if !manifest.NeedsIO || !manifest.NeedsRuntime {
		t.Fatalf("import io should set NeedsIO and NeedsRuntime, got %+v", manifest)
	}
}

// TestBundlePathLastSegment checks the path convention: a module is named by its
// path's last segment, so `import "std/io"` also bundles io.zg.
func TestBundlePathLastSegment(t *testing.T) {
	src := "import \"std/io\"\nfn main() -> Result[nil] {\n\tio.println(\"hi\")\n\treturn nil\n}\n"
	code, _, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !strings.Contains(code, "zg_io__println(") {
		t.Fatalf("std/io should bundle io.zg:\n%s", code)
	}
}

func TestBundleUnknownModule(t *testing.T) {
	_, _, diags := Compile("import \"nope\"\nfn main() { print 1 }")
	if len(diags) == 0 {
		t.Fatalf("expected a diagnostic for an unknown stdlib module")
	}
}

func TestBundleUnknownMember(t *testing.T) {
	_, _, diags := Compile("import \"io\"\nfn main() -> Result[nil] {\n\tio.nope(\"x\")\n\treturn nil\n}\n")
	if len(diags) == 0 {
		t.Fatalf("expected a diagnostic for an unknown module member")
	}
}

// TestNoImportManifestClean guards the additive contract: a value-only program that
// imports nothing sets no io/runtime manifest flag, so it stays byte-identical.
func TestNoImportManifestClean(t *testing.T) {
	_, manifest, diags := Compile("fn main() {\n\tprint 1\n}")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if manifest.NeedsIO || manifest.NeedsRuntime {
		t.Fatalf("a value-only program should need no runtime, got %+v", manifest)
	}
}
