package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cmj/zerg/src/bootstrap/internal/loader"
	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// v0.9 Unit 3 — interpreter dispatch for std/os.argv and std/os.exit.

// runV09ArgvExitMain writes mainSrc to a temp main.zg, loads via the
// public Bundle pipeline, and returns the stdout + (exitCode, exited)
// pair from RunBundleWithOptions.
func runV09ArgvExitMain(t *testing.T, mainSrc string, opts Options) (string, int, bool, error) {
	t.Helper()
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.zg")
	if err := os.WriteFile(entry, []byte(mainSrc), 0o644); err != nil {
		t.Fatalf("write main.zg: %v", err)
	}
	bundle, err := loader.Load(entry)
	if err != nil {
		return "", 0, false, err
	}
	if err := syntax.CheckBundle(bundle); err != nil {
		return "", 0, false, err
	}
	var buf bytes.Buffer
	code, exited, runErr := RunBundleWithOptions(bundle, &buf, opts)
	return buf.String(), code, exited, runErr
}

func TestV09InterpOsArgvReturnsSlice(t *testing.T) {
	out, code, exited, err := runV09ArgvExitMain(t, `# requires: v0.9
import "std/os"
a := os.argv()
print len(a)
print a[0]
print a[1]
`, Options{Argv: []string{"prog.zg", "hello"}})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if exited {
		t.Errorf("exited=true on a clean program")
	}
	if code != 0 {
		t.Errorf("code=%d, want 0", code)
	}
	want := "2\nprog.zg\nhello\n"
	if out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
}

func TestV09InterpOsExitNonZero(t *testing.T) {
	out, code, exited, err := runV09ArgvExitMain(t, `# requires: v0.9
import "std/os"
print "before"
os.exit(7)
print "after"
`, Options{Argv: []string{"x"}})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !exited {
		t.Errorf("exited=false; expected exit-driven termination")
	}
	if code != 7 {
		t.Errorf("code=%d, want 7", code)
	}
	if !strings.Contains(out, "before\n") {
		t.Errorf("stdout missing 'before': %q", out)
	}
	if strings.Contains(out, "after") {
		t.Errorf("stdout reached 'after' after os.exit: %q", out)
	}
}

func TestV09InterpOsExitZero(t *testing.T) {
	_, code, exited, err := runV09ArgvExitMain(t, `# requires: v0.9
import "std/os"
os.exit(0)
`, Options{Argv: []string{"x"}})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !exited {
		t.Errorf("exited=false; expected exit-driven termination even on code 0")
	}
	if code != 0 {
		t.Errorf("code=%d, want 0", code)
	}
}

func TestV09InterpOsArgvEmptyOpts(t *testing.T) {
	// No Argv supplied — argv() returns an empty list.
	out, _, _, err := runV09ArgvExitMain(t, `# requires: v0.9
import "std/os"
print len(os.argv())
`, Options{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out != "0\n" {
		t.Errorf("stdout = %q, want %q", out, "0\n")
	}
}

// TestV09InterpOsExitInsideFnDrainsCalls verifies that os.exit raised
// from inside a fn-call frame still surfaces at the top-level boundary.
func TestV09InterpOsExitInsideFnDrainsCalls(t *testing.T) {
	out, code, exited, err := runV09ArgvExitMain(t, `# requires: v0.9
import "std/os"
fn die(n: int) -> never {
    os.exit(n)
}
print "before"
die(13)
print "after"
`, Options{Argv: []string{"x"}})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !exited || code != 13 {
		t.Errorf("(exited, code) = (%v, %d), want (true, 13)", exited, code)
	}
	if !strings.Contains(out, "before\n") {
		t.Errorf("stdout missing 'before': %q", out)
	}
	if strings.Contains(out, "after") {
		t.Errorf("stdout reached 'after': %q", out)
	}
}

// TestV09InterpSpawnExitSurfacesCode pins Phase 4 Fix 2: an os.exit
// raised inside a spawned goroutine cannot panic across the goroutine
// boundary, so the spawn-recover stashes the code on a bundle-shared
// coordinator. RunBundleWithOptions consults the coordinator after
// spawnWg.Wait() (or earlier, on the next main-path stmt boundary) and
// surfaces (exited=true, code=N) — matching cgen's libc-exit semantics
// where the first thread to call exit() takes the whole process down.
func TestV09InterpSpawnExitSurfacesCode(t *testing.T) {
	out, code, exited, err := runV09ArgvExitMain(t, `# requires: v0.9
import "std/os"
import "std/time"

fn worker() {
    time.sleep_ms(10)
    os.exit(5)
}

spawn worker()
time.sleep_ms(100)
print "should not print"
`, Options{Argv: []string{"x"}})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !exited || code != 5 {
		t.Errorf("(exited, code) = (%v, %d), want (true, 5)", exited, code)
	}
	if strings.Contains(out, "should not print") {
		t.Errorf("stdout leaked past spawn-exit: %q", out)
	}
}
