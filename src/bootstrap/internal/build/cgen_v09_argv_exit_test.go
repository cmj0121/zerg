// v0.9 Unit 3 codegen tests — emit + compile + run programs that
// exercise os.argv and os.exit. The compiled binary is invoked via
// exec.Command so the test can assert exit code AND stdout.

package build

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cmj/zerg/src/bootstrap/internal/loader"
	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// buildAndRun materialises src, compiles, executes the binary with the
// given argv, and returns stdout, the exec exit code, and any error.
// Skips when cc is unavailable.
func buildAndRun(t *testing.T, src string, runArgs []string) (stdout string, code int, err error) {
	t.Helper()
	cc := DefaultCC()
	if _, lerr := exec.LookPath(cc); lerr != nil {
		t.Skip("cc not available")
	}
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.zg")
	if werr := os.WriteFile(mainPath, []byte(src), 0o644); werr != nil {
		t.Fatalf("write main.zg: %v", werr)
	}
	bundle, lerr := loader.Load(mainPath)
	if lerr != nil {
		return "", 0, lerr
	}
	if cerr := syntax.CheckBundle(bundle); cerr != nil {
		return "", 0, cerr
	}
	cPath := filepath.Join(dir, "merged.c")
	cFile, ferr := os.Create(cPath)
	if ferr != nil {
		t.Fatalf("create %s: %v", cPath, ferr)
	}
	if eerr := EmitBundle(bundle, cFile); eerr != nil {
		cFile.Close()
		return "", 0, eerr
	}
	cFile.Close()
	binPath := filepath.Join(dir, "merged")
	cmd := exec.Command(cc, "-fwrapv", "-O2", "-o", binPath, cPath, "-lm")
	cmd.Stderr = os.Stderr
	if cerr := cmd.Run(); cerr != nil {
		c, _ := os.ReadFile(cPath)
		t.Fatalf("cc failed: %v\n--- merged.c ---\n%s", cerr, c)
	}
	runCmd := exec.Command(binPath, runArgs...)
	var so bytes.Buffer
	runCmd.Stdout = &so
	runCmd.Stderr = os.Stderr
	runErr := runCmd.Run()
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			return so.String(), ee.ExitCode(), nil
		}
		return so.String(), -1, runErr
	}
	return so.String(), 0, nil
}

func TestV09CgenOsArgvReturnsArgs(t *testing.T) {
	src := `# requires: v0.9
import "std/os"
a := os.argv()
print len(a)
print a[1]
print a[2]
`
	out, code, err := buildAndRun(t, src, []string{"hello", "world"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	want := "3\nhello\nworld\n"
	if out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
}

func TestV09CgenOsExitPropagatesCode(t *testing.T) {
	src := `# requires: v0.9
import "std/os"
print "before"
os.exit(7)
print "after"
`
	out, code, err := buildAndRun(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 7 {
		t.Errorf("exit code = %d, want 7", code)
	}
	if !strings.Contains(out, "before\n") {
		t.Errorf("stdout missing 'before': %q", out)
	}
	if strings.Contains(out, "after") {
		t.Errorf("stdout reached 'after' after os.exit: %q", out)
	}
}

func TestV09CgenOsExitZero(t *testing.T) {
	src := `# requires: v0.9
import "std/os"
print "x"
os.exit(0)
`
	out, code, err := buildAndRun(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if out != "x\n" {
		t.Errorf("stdout = %q, want %q", out, "x\n")
	}
}

func TestV09CgenMainSwapsForArgv(t *testing.T) {
	// A program that uses os.argv must emit `int main(int argc, char **argv)`.
	out, err := emitFromFileSrc(t, `# requires: v0.9
import "std/os"
print len(os.argv())
`)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !strings.Contains(out, "int main(int argc, char **argv)") {
		t.Errorf("emit does not swap main signature for argv\n--- emitted ---\n%s", out)
	}
	if !strings.Contains(out, "__zerg_argc = argc;") {
		t.Errorf("emit missing __zerg_argc seed")
	}
}

// TestV09CgenOsImportSwapsMain pins the v0.14 T2 reality: importing
// std/os now triggers the argv-signature main swap regardless of what
// the user calls. Reason: pure-Zerg os.zg's argv() / env() bodies
// reference the new __builtin accessor primitives (os_argv_at /
// os_envp_at), and the cgen's programUsesArgv / programUsesEnvp
// walker is non-reachability — it fires when ANY fn body across the
// bundle references the primitive, including dead-code paths in the
// imported module. The trade-off is a few bytes of emit overhead
// for an os-importing program that uses only os.exit; a future
// reachability walker can tighten this back down without changing
// any user-visible semantics.
func TestV09CgenOsImportSwapsMain(t *testing.T) {
	out, err := emitFromFileSrc(t, `# requires: v0.9
import "std/os"
os.exit(0)
`)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !strings.Contains(out, "int main(int argc, char **argv)") {
		t.Errorf("os-importing program missing argv signature\n--- emitted ---\n%s", out)
	}
}

func TestV09CgenMainStaysVoidWithoutOsBuiltins(t *testing.T) {
	// A v0.0 program (no std/os import) keeps main(void) and emits no
	// argv/exit/time runtime symbols. Phase 4 Fix 5: extend the negative
	// assertions to cover zerg_time_*, __zerg_argc, __zerg_argv so a
	// regression in the runtime gate is caught at compile-time.
	out, err := emitFromFileSrc(t, `# requires: v0.0
print 42
`)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !strings.Contains(out, "int main(void)") {
		t.Errorf("v0.0 program lost main(void) signature")
	}
	for _, sym := range []string{
		"zerg_os_argv",
		"zerg_os_exit",
		"zerg_os_argv_at",
		"zerg_os_envp_at",
		"zerg_time_clock_us",
		"zerg_time_sleep_ns",
		"zerg_time_epoch",
		"zerg_time_initialised",
		"__zerg_argc",
		"__zerg_argv",
	} {
		if strings.Contains(out, sym) {
			t.Errorf("v0.0 program unexpectedly contains %q in emit", sym)
		}
	}
}

// TestV09CgenTimeRuntimeAbsentForOsEnvOnly pins that a program using
// std/os for env / argv does NOT pull in the std/time runtime. The
// argv + envp primitive runtime IS emitted (importing std/os triggers
// it under v0.14 T2 — see TestV09CgenOsImportSwapsMain) but the time
// runtime stays gated on its own predicate.
func TestV09CgenTimeRuntimeAbsentForOsEnvOnly(t *testing.T) {
	out, err := emitFromFileSrc(t, `# requires: v0.9
import "std/os"
match os.env("PATH") {
    nil => print "unset"
    _   => print "set"
}
`)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	for _, sym := range []string{
		"zerg_time_clock_us",
		"zerg_time_sleep_ns",
		"zerg_time_epoch",
	} {
		if strings.Contains(out, sym) {
			t.Errorf("os.env-only program unexpectedly contains %q in emit", sym)
		}
	}
}

func TestV09CgenOsArgvSingleElementWhenNoArgs(t *testing.T) {
	// Binary launched with no extra args sees argv == [binary_path].
	src := `# requires: v0.9
import "std/os"
print len(os.argv())
`
	out, code, err := buildAndRun(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if out != "1\n" {
		t.Errorf("stdout = %q, want %q", out, "1\n")
	}
}
