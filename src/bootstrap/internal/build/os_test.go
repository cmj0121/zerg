package build

import (
	"runtime"
	"strings"
	"testing"
)

// RUN-based tests for the stdlib `os` module (src/stdlib/os.zg): pure-Zerg null-safety
// over thin runtime leaves (getenv/exit, and compile-time platform/arch).

// TestOSEnvRuns checks env(): a set variable yields its value, an unset one yields nil
// (so `?? default` takes over). The child inherits the variable via the environment.
func TestOSEnvRuns(t *testing.T) {
	t.Setenv("ZERG_TEST_ENV", "hello")
	got := runProgramRT(t, "import \"os\"\n"+
		"fn main() {\n"+
		"\tprint os.env(\"ZERG_TEST_ENV\") ?? \"unset\"\n"+
		"\tprint os.env(\"ZERG_MISSING_VAR_ABC\") ?? \"unset\"\n"+
		"}\n")
	if want := "hello\nunset\n"; got != want {
		t.Fatalf("os.env: got %q, want %q", got, want)
	}
}

// TestOSPlatformRuns checks platform()/arch() against the host the test runs on, so the
// runtime's compile-time #ifdef mapping stays in step with the real target.
func TestOSPlatformRuns(t *testing.T) {
	wantArch := map[string]string{"arm64": "arm64", "amd64": "x86_64"}[runtime.GOARCH]
	if wantArch == "" {
		t.Skipf("no expected os.arch mapping for GOARCH %q", runtime.GOARCH)
	}
	got := runProgramRT(t, "import \"os\"\nfn main() {\n\tprint os.platform()\n\tprint os.arch()\n}\n")
	if want := runtime.GOOS + "\n" + wantArch + "\n"; got != want {
		t.Fatalf("os.platform/arch: got %q, want %q", got, want)
	}
}

// TestOSExitRuns checks os.exit terminates the process with a non-zero status after its
// preceding output and before anything that follows.
func TestOSExitRuns(t *testing.T) {
	out := runProgramRTAbort(t, "import \"os\"\n"+
		"fn main() {\n\tprint \"before\"\n\tos.exit(2)\n\tprint \"after\"\n}\n")
	if !strings.Contains(out, "before") || strings.Contains(out, "after") {
		t.Fatalf("os.exit: want output before the exit and none after, got %q", out)
	}
}

// TestOSLowering pins the emitted C: the os leaves lower to their sys.c primitives and
// pull in the runtime.
func TestOSLowering(t *testing.T) {
	code, manifest, diags := Compile("import \"os\"\n" +
		"fn main() {\n\tprint os.platform()\n\tprint os.env(\"X\") ?? \"\"\n\tos.exit(0)\n}\n")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	for _, want := range []string{"zrt_platform(", "zrt_getenv(", "zrt_has_env(", "zrt_exit("} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted C missing %q:\n%s", want, code)
		}
	}
	if !manifest.NeedsRuntime {
		t.Fatalf("os should need the runtime, got %+v", manifest)
	}
}
