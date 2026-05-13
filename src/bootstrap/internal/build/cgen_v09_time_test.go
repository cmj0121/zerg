// v0.9 Unit 2 codegen tests — emit + compile + run the std/time runtime.
// Each test asserts the binary's stdout (and timing for sleep_ms) so the
// behaviour matches the interpreter half by construction.

package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// expectV09Build builds + runs the program and returns stdout.
func expectV09Build(t *testing.T, src, want string) {
	t.Helper()
	got, err := buildBundleFromFiles(t, "main.zg", map[string]string{"main.zg": src})
	if err != nil {
		t.Fatalf("build failed: %v\nstdout: %q", err, got)
	}
	if got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestV09CgenTimeNowMsFirstCallZero(t *testing.T) {
	expectV09Build(t, `# requires: v0.9
import "std/time"
print time.now_ms()
`, "0\n")
}

func TestV09CgenTimeNowMsMonotonic(t *testing.T) {
	got, err := buildBundleFromFiles(t, "main.zg", map[string]string{"main.zg": `# requires: v0.9
import "std/time"
a := time.now_ms()
_ := time.sleep_ms(5)
b := time.now_ms()
if b >= a {
    print "ok"
} else {
    print "regressed"
}
`})
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}
	if got != "ok\n" {
		t.Fatalf("monotonic: got %q", got)
	}
}

func TestV09CgenTimeSleepMsFloor(t *testing.T) {
	// In-process timing via now_ms() bracket so process-startup latency
	// doesn't fold into the measurement.
	got, err := buildBundleFromFiles(t, "main.zg", map[string]string{"main.zg": `# requires: v0.9
import "std/time"
warm := time.now_ms()
a := time.now_ms()
slept := time.sleep_ms(50)
b := time.now_ms()
print slept
if b - a >= 30 {
    print "blocked"
} else {
    print "early"
}
print warm
`})
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}
	if !strings.HasPrefix(got, "true\nblocked\n") {
		t.Fatalf("expected prefix 'true\\nblocked\\n', got %q", got)
	}
}

func TestV09CgenTimeSleepMsNegativeImmediate(t *testing.T) {
	// In-process bracket measurement of sleep_ms(-5). Must clamp to 0 and
	// return immediately; tolerance is 25 ms which is huge relative to a
	// no-op nanosleep skip.
	got, err := buildBundleFromFiles(t, "main.zg", map[string]string{"main.zg": `# requires: v0.9
import "std/time"
warm := time.now_ms()
a := time.now_ms()
slept := time.sleep_ms(-5)
b := time.now_ms()
print slept
if b - a < 25 {
    print "fast"
} else {
    print "slow"
}
print warm
`})
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}
	if !strings.HasPrefix(got, "true\nfast\n") {
		t.Fatalf("expected prefix 'true\\nfast\\n', got %q", got)
	}
}

func TestV09CgenTimeSleepMsZeroImmediate(t *testing.T) {
	expectV09Build(t, `# requires: v0.9
import "std/time"
print time.sleep_ms(0)
`, "true\n")
}

func TestV09CgenTimeNowMsAdvancesAfterSleep(t *testing.T) {
	got, err := buildBundleFromFiles(t, "main.zg", map[string]string{"main.zg": `# requires: v0.9
import "std/time"
a := time.now_ms()
_ := time.sleep_ms(40)
b := time.now_ms()
if b > a {
    print "advanced"
} else {
    print "stalled"
}
`})
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}
	if got != "advanced\n" {
		t.Fatalf("expected 'advanced\\n', got %q", got)
	}
}

func TestV09CgenRuntimeGateNotEmittedWithoutTime(t *testing.T) {
	// A v0.0 program without std/time must not pull in the v0.9 time runtime.
	out, err := emitFromFileSrc(t, `# requires: v0.0
print 42
`)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if strings.Contains(out, "zerg_time_clock_us") {
		t.Errorf("v0.0 program emit unexpectedly contains zerg_time_clock_us")
	}
	if strings.Contains(out, "<time.h>") {
		t.Errorf("v0.0 program emit unexpectedly includes <time.h>")
	}
}

func TestV09CgenRuntimeGateEmittedWithTime(t *testing.T) {
	out, err := emitFromFileSrc(t, `# requires: v0.9
import "std/time"
print time.now_ms()
`)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !strings.Contains(out, "zerg_time_clock_us") {
		t.Errorf("v0.9 time program missing zerg_time_clock_us")
	}
	if !strings.Contains(out, "<time.h>") {
		t.Errorf("v0.9 time program missing <time.h>")
	}
}

func emitFromFileSrc(t *testing.T, src string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.zg")
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return emitFromFile(t, entry)
}
