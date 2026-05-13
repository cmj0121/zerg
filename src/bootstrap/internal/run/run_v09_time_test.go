package run

// v0.9 Unit 2 — interpreter tests for std/time. v0.14 T2 moved the
// lazy-zero-on-first-call epoch from process-global Go state into the
// per-interpreter module-level mut binding `epoch_us` in src/std/time.zg.
// Each test that calls into time.now_ms() spins up a fresh interpreter
// (via expectV08OK / runV08Main), so the epoch resets implicitly between
// tests — the explicit resetTimeEpoch helper that the pre-T2 tests
// required is no longer needed.
//
// Loose-bound tests for sleep_ms use a 30 ms floor for sleep_ms(50) — same
// margin as PLAN.md §"Time-corpus style" recommends to keep CI stable.

import (
	"strings"
	"testing"
	"time"
)

func TestRunV09TimeNowMsFirstCallZero(t *testing.T) {
	expectV08OK(t, `# requires: v0.9
import "std/time"
print time.now_ms()
`, "0\n")
}

func TestRunV09TimeNowMsMonotonic(t *testing.T) {
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
	expectV08OK(t, `# requires: v0.8
import "std/math"
print math.abs(-7)
`, "7\n")
}

func TestRunV09TimeNowMsIncreasesAfterSleep(t *testing.T) {
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
