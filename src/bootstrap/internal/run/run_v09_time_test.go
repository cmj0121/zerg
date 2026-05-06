package run

// v0.9 Unit 2 — interpreter tests for std/time. The lazy-zero-on-first-call
// epoch is process-global, so each test that asserts the absolute "first
// call returns 0" rule must reset the epoch before exercising the builtin.
//
// Loose-bound tests for sleep_ms use a 30 ms floor for sleep_ms(50) — same
// margin as PLAN.md §"Time-corpus style" recommends to keep CI stable.

import (
	"strings"
	"testing"
	"time"
)

// resetTimeEpoch restores the uninitialised sentinel so the next now_ms
// call observes "first call returns 0". Tests need this because earlier
// tests may have already initialised the global.
func resetTimeEpoch() {
	timeFirstCallMu.Lock()
	timeFirstCall = time.Time{}
	timeFirstCallMu.Unlock()
}

func TestRunV09TimeNowMsFirstCallZero(t *testing.T) {
	resetTimeEpoch()
	expectV08OK(t, `# requires: v0.9
import "std/time"
print time.now_ms()
`, "0\n")
}

func TestRunV09TimeNowMsMonotonic(t *testing.T) {
	resetTimeEpoch()
	got, err := runV08Main(t, `# requires: v0.9
import "std/time"
a := time.now_ms()
_ := time.sleep_ms(5)
b := time.now_ms()
if b >= a {
    print "ok"
} else {
    print "regressed"
}
`)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if got != "ok\n" {
		t.Fatalf("monotonic check: got %q", got)
	}
}

func TestRunV09TimeSleepMsFloor(t *testing.T) {
	resetTimeEpoch()
	start := time.Now()
	got, err := runV08Main(t, `# requires: v0.9
import "std/time"
print time.sleep_ms(50)
`)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if got != "true\n" {
		t.Fatalf("sleep_ms return: got %q want %q", got, "true\n")
	}
	elapsed := time.Since(start)
	if elapsed < 30*time.Millisecond {
		t.Fatalf("sleep_ms(50) returned in %v; expected >= 30ms", elapsed)
	}
}

func TestRunV09TimeSleepMsNegativeImmediate(t *testing.T) {
	resetTimeEpoch()
	start := time.Now()
	got, err := runV08Main(t, `# requires: v0.9
import "std/time"
print time.sleep_ms(-5)
`)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if got != "true\n" {
		t.Fatalf("sleep_ms return: got %q want %q", got, "true\n")
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("sleep_ms(-5) blocked for %v; expected immediate return", elapsed)
	}
}

func TestRunV09TimeSleepMsZeroImmediate(t *testing.T) {
	resetTimeEpoch()
	got, err := runV08Main(t, `# requires: v0.9
import "std/time"
print time.sleep_ms(0)
`)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if got != "true\n" {
		t.Fatalf("sleep_ms return: got %q want %q", got, "true\n")
	}
}

func TestRunV09TimeUnknownBuiltinUntouched(t *testing.T) {
	// Sanity: a v0.8 program that doesn't import std/time still works after
	// the v0.9 dispatch wedge — confirms the v09 fall-through doesn't
	// shadow existing dispatch.
	resetTimeEpoch()
	expectV08OK(t, `# requires: v0.8
import "std/math"
print math.abs(-7)
`, "7\n")
}

func TestRunV09TimeNowMsIncreasesAfterSleep(t *testing.T) {
	resetTimeEpoch()
	got, err := runV08Main(t, `# requires: v0.9
import "std/time"
a := time.now_ms()
_ := time.sleep_ms(40)
b := time.now_ms()
if b > a {
    print "advanced"
} else {
    print "stalled"
}
`)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if !strings.HasPrefix(got, "advanced") {
		t.Fatalf("expected advanced, got %q", got)
	}
}
