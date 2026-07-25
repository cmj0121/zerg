package build

import (
	"strings"
	"testing"
)

// RUN-based tests for the stdlib `time` module (src/stdlib/time.zg): pure-Zerg wrappers
// over the runtime clock floor. The readings are non-deterministic, so the tests assert
// invariants (a sane epoch, a non-decreasing monotonic clock) rather than exact values.

// TestTimeRuns checks that now() is past a known epoch and monotonic() never goes
// backwards across two reads.
func TestTimeRuns(t *testing.T) {
	got := runProgramRT(t, "import \"time\"\n"+
		"fn main() {\n"+
		"\tprint time.now() > 1700000000\n"+ // seconds; past 2023-11-14
		"\ta := time.monotonic()\n"+
		"\tb := time.monotonic()\n"+
		"\tprint b >= a\n"+
		"}\n")
	if want := "true\ntrue\n"; got != want {
		t.Fatalf("time: got %q, want %q", got, want)
	}
}

// TestTimeLowering pins the emitted C: the clock leaves lower to their sys.c primitives
// and pull in the runtime.
func TestTimeLowering(t *testing.T) {
	code, manifest, diags := Compile("import \"time\"\nfn main() {\n\tprint time.now() + time.monotonic()\n}\n")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	for _, want := range []string{"zrt_time_unix(", "zrt_time_mono(", "zg_time__now(", "zg_time__monotonic("} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted C missing %q:\n%s", want, code)
		}
	}
	if !manifest.NeedsRuntime {
		t.Fatalf("time should need the runtime, got %+v", manifest)
	}
}
