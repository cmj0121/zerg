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
let a := os.argv()
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

func TestV09CgenMainStaysVoidForExitOnly(t *testing.T) {
	// A program that uses only os.exit (NOT os.argv) keeps main(void).
	out, err := emitFromFileSrc(t, `# requires: v0.9
import "std/os"
os.exit(0)
`)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !strings.Contains(out, "int main(void)") {
		t.Errorf("exit-only program lost main(void) signature\n--- emitted ---\n%s", out)
	}
	if strings.Contains(out, "int main(int argc") {
		t.Errorf("exit-only program got argv signature swap")
	}
}

func TestV09CgenMainStaysVoidWithoutOsBuiltins(t *testing.T) {
	// A v0.0 program (no std/os import) keeps main(void) and emits no
	// argv/exit runtime symbols.
	out, err := emitFromFileSrc(t, `# requires: v0.0
print 42
`)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !strings.Contains(out, "int main(void)") {
		t.Errorf("v0.0 program lost main(void) signature")
	}
	if strings.Contains(out, "zerg_os_argv") {
		t.Errorf("v0.0 program unexpectedly contains zerg_os_argv runtime")
	}
	if strings.Contains(out, "zerg_os_exit") {
		t.Errorf("v0.0 program unexpectedly contains zerg_os_exit runtime")
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
