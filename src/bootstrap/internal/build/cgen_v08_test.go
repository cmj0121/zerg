// v0.8 Unit 4 codegen tests — emit + compile + run the embedded C runtime
// for each std/* builtin. Where possible the assertion is on stdout (the
// parity surface) rather than the C source; programUsesV08 size-guard
// uses substring matching against the emitted source so a stray runtime
// emission in a v0.0–v0.7 program is caught.

package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// expectV08Build builds the single-entry program and asserts stdout. The
// fixture writes main.zg under TempDir; std modules are toolchain-shipped
// via embed.FS so no extra files needed.
func expectV08Build(t *testing.T, src, want string) {
	t.Helper()
	got, err := buildBundleFromFiles(t, "main.zg", map[string]string{"main.zg": src})
	if err != nil {
		t.Fatalf("build failed: %v\nstdout: %q", err, got)
	}
	if got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

// expectV08BuildEnv variant sets an env var on the binary's process so
// os.env tests can exercise both the Some and None paths.
func expectV08BuildEnv(t *testing.T, src, want string, env map[string]string) {
	t.Helper()
	cc := DefaultCC()
	if _, err := exec.LookPath(cc); err != nil {
		t.Skip("cc not available")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.zg"), []byte(src), 0o644); err != nil {
		t.Fatalf("write main.zg: %v", err)
	}
	cPath, binPath := filepath.Join(dir, "p.c"), filepath.Join(dir, "p")
	out, err := emitFromFile(t, filepath.Join(dir, "main.zg"))
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if err := os.WriteFile(cPath, []byte(out), 0o644); err != nil {
		t.Fatalf("write c: %v", err)
	}
	cmd := exec.Command(cc, "-fwrapv", "-pthread", "-O2", "-o", binPath, cPath, "-lm")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("cc: %v\n--- C ---\n%s", err, out)
	}
	run := exec.Command(binPath)
	run.Env = os.Environ()
	for k, v := range env {
		run.Env = append(run.Env, k+"="+v)
	}
	got, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v; stdout=%q", err, got)
	}
	if string(got) != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

// emitFromFile loads + checks + emits a single-entry zerg file via the
// public Build pipeline; returns the merged C source.
func emitFromFile(t *testing.T, entry string) (string, error) {
	t.Helper()
	var buf strings.Builder
	if err := EmitSource(entry, &builderWriter{&buf}); err != nil {
		return "", err
	}
	return buf.String(), nil
}

type builderWriter struct{ b *strings.Builder }

func (w *builderWriter) Write(p []byte) (int, error) { return w.b.Write(p) }

// ---------------------------------------------------------------------------
// std/math
// ---------------------------------------------------------------------------

func TestV08CgenMathAbs(t *testing.T) {
	expectV08Build(t, `# requires: v0.8
import "std/math"
print math.abs(-5)
print math.abs(7)
print math.abs(0)
`, "5\n7\n0\n")
}

func TestV08CgenMathMin(t *testing.T) {
	expectV08Build(t, `# requires: v0.8
import "std/math"
print math.min(3, 7)
print math.min(-1, -2)
`, "3\n-2\n")
}

func TestV08CgenMathMax(t *testing.T) {
	expectV08Build(t, `# requires: v0.8
import "std/math"
print math.max(3, 7)
print math.max(-1, -2)
`, "7\n-1\n")
}

func TestV08CgenMathGcd(t *testing.T) {
	expectV08Build(t, `# requires: v0.8
import "std/math"
print math.gcd(12, 18)
print math.gcd(0, 0)
print math.gcd(0, 5)
print math.gcd(-12, 18)
`, "6\n0\n5\n6\n")
}

// ---------------------------------------------------------------------------
// std/strings
// ---------------------------------------------------------------------------

func TestV08CgenStringsTrim(t *testing.T) {
	expectV08Build(t, `# requires: v0.8
import "std/strings"
print strings.trim("  hello  ")
print strings.trim("noop")
`, "hello\nnoop\n")
}

func TestV08CgenStringsStartsWith(t *testing.T) {
	expectV08Build(t, `# requires: v0.8
import "std/strings"
print strings.starts_with("hello world", "hello")
print strings.starts_with("hello", "world")
print strings.starts_with("anything", "")
`, "true\nfalse\ntrue\n")
}

func TestV08CgenStringsEndsWith(t *testing.T) {
	expectV08Build(t, `# requires: v0.8
import "std/strings"
print strings.ends_with("hello world", "world")
print strings.ends_with("hello", "world")
`, "true\nfalse\n")
}

func TestV08CgenStringsContains(t *testing.T) {
	expectV08Build(t, `# requires: v0.8
import "std/strings"
print strings.contains("hello world", "lo wo")
print strings.contains("hello", "xyz")
print strings.contains("any", "")
`, "true\nfalse\ntrue\n")
}

func TestV08CgenStringsReplace(t *testing.T) {
	expectV08Build(t, `# requires: v0.8
import "std/strings"
print strings.replace("aaa", "aa", "b")
print strings.replace("hello world", "world", "zerg")
print strings.replace("noop", "x", "y")
`, "ba\nhello zerg\nnoop\n")
}

func TestV08CgenStringsToUpper(t *testing.T) {
	expectV08Build(t, `# requires: v0.8
import "std/strings"
print strings.to_upper("hello, World!")
`, "HELLO, WORLD!\n")
}

func TestV08CgenStringsToLower(t *testing.T) {
	expectV08Build(t, `# requires: v0.8
import "std/strings"
print strings.to_lower("HELLO, World!")
`, "hello, world!\n")
}

// to_upper/to_lower on non-ASCII passes through unchanged byte-for-byte.
func TestV08CgenStringsToUpperNonAsciiPassthrough(t *testing.T) {
	expectV08Build(t, `# requires: v0.8
import "std/strings"
print strings.to_upper("café")
`, "CAFé\n")
}

func TestV08CgenStringsSplit(t *testing.T) {
	expectV08Build(t, `# requires: v0.8
import "std/strings"
parts := strings.split("a,b,c", ",")
print parts
`, "[ a, b, c ]\n")
}

func TestV08CgenStringsJoin(t *testing.T) {
	expectV08Build(t, `# requires: v0.8
import "std/strings"
parts := strings.split("a,b,c", ",")
print strings.join(parts, "-")
`, "a-b-c\n")
}

func TestV08CgenStringsParseIntOk(t *testing.T) {
	expectV08Build(t, `# requires: v0.8
import "std/strings"
print strings.parse_int("42")
print strings.parse_int("  -7  ")
print strings.parse_int("+0")
`, "Result.Ok(42)\nResult.Ok(-7)\nResult.Ok(0)\n")
}

func TestV08CgenStringsParseIntErr(t *testing.T) {
	expectV08Build(t, `# requires: v0.8
import "std/strings"
print strings.parse_int("")
print strings.parse_int("abc")
print strings.parse_int("99999999999999999999999")
`, "Result.Err(ParseError.Empty)\nResult.Err(ParseError.InvalidDigit)\nResult.Err(ParseError.Overflow)\n")
}

// ---------------------------------------------------------------------------
// std/io
// ---------------------------------------------------------------------------

func TestV08CgenIoReadFileOk(t *testing.T) {
	cc := DefaultCC()
	if _, err := exec.LookPath(cc); err != nil {
		t.Skip("cc not available")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fixture.txt")
	if err := os.WriteFile(fixture, []byte("hello, file\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	src := `# requires: v0.8
import "std/io"
r := io.read_file("` + fixture + `")
print r
`
	expectV08Build(t, src, "Result.Ok(hello, file\n)\n")
}

func TestV08CgenIoReadFileNotFound(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "no-such-file.txt")
	src := `# requires: v0.8
import "std/io"
r := io.read_file("` + missing + `")
print r
`
	expectV08Build(t, src, "Result.Err(IoError.NotFound)\n")
}

func TestV08CgenIoWriteFileOk(t *testing.T) {
	cc := DefaultCC()
	if _, err := exec.LookPath(cc); err != nil {
		t.Skip("cc not available")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "out.txt")
	src := `# requires: v0.8
import "std/io"
r := io.write_file("` + target + `", "written")
print r
`
	expectV08Build(t, src, "Result.Ok(true)\n")
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "written" {
		t.Errorf("file contents = %q, want %q", got, "written")
	}
}

// ---------------------------------------------------------------------------
// std/os
// ---------------------------------------------------------------------------

func TestV08CgenOsEnvSome(t *testing.T) {
	expectV08BuildEnv(t, `# requires: v0.8
import "std/os"
print os.env("ZERG_V08_CGEN_VAR")
`, "Option.Some(present)\n", map[string]string{"ZERG_V08_CGEN_VAR": "present"})
}

func TestV08CgenOsEnvNone(t *testing.T) {
	expectV08BuildEnv(t, `# requires: v0.8
import "std/os"
print os.env("ZERG_V08_CGEN_NEVER_SET")
`, "Option.None\n", map[string]string{})
}

// ---------------------------------------------------------------------------
// Codegen size guard — v0.0–v0.7 programs do NOT contain v0.8 symbols.
// ---------------------------------------------------------------------------

func TestV08CgenRuntimeAbsentWithoutBuiltin(t *testing.T) {
	out := mustEmit(t, "print 1\n")
	for _, banned := range []string{
		"zerg_io_read_file",
		"zerg_io_write_file",
		"zerg_strings_split",
		"zerg_strings_join",
		"zerg_strings_trim",
		"zerg_strings_replace",
		"zerg_strings_parse_int",
		"zerg_math_abs",
		"zerg_os_env",
		"zerg_io_str_or_err",
		"zerg_parse_int_result",
		"zerg_os_env_result",
	} {
		if strings.Contains(out, banned) {
			t.Errorf("v0.8 symbol %q leaked into v0.0 program emit", banned)
		}
	}
}

// TestV08CgenRuntimePresentWithBuiltin — using any v0.8 builtin pulls in
// the runtime helpers. The canary import was math until v0.14 retired
// the __builtin math shim into pure Zerg; strings is the smallest still-
// shimmed v0.8 family that exercises the same runtime-wiring path.
func TestV08CgenRuntimePresentWithBuiltin(t *testing.T) {
	src := `# requires: v0.8
import "std/strings"
print strings.trim("  hi  ")
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.zg"), []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	out, err := emitFromFile(t, filepath.Join(dir, "main.zg"))
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	for _, want := range []string{
		"static zerg_str zerg_strings_trim(",
		"zerg_io_str_or_err",
		"zerg_strings_split",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("v0.8 runtime missing %q in:\n%s", want, out)
		}
	}
}

// TestV08CgenListStrShapeForceMonomorphized — a program that calls
// strings.split (returns list[str]) must emit the zerg_list_zerg_str
// shape's helpers even though the user code never literal-constructs a
// list[str]. The runtime references zerg_list_zerg_str_push, so the
// shape must be present before the runtime block.
func TestV08CgenListStrShapeForceMonomorphized(t *testing.T) {
	src := `# requires: v0.8
import "std/strings"
print strings.trim("  hi  ")
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.zg"), []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	out, err := emitFromFile(t, filepath.Join(dir, "main.zg"))
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	for _, want := range []string{
		"zerg_list_zerg_str",
		"zerg_list_zerg_str_push",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in v0.8 emit; got:\n%s", want, out)
		}
	}
	// The list helper must appear BEFORE the runtime's reference to it.
	pushDef := strings.Index(out, "static void zerg_list_zerg_str_push(")
	runtimeRef := strings.Index(out, "static zerg_list_zerg_str zerg_strings_split(")
	if pushDef < 0 || runtimeRef < 0 {
		t.Fatalf("missing markers; pushDef=%d runtimeRef=%d", pushDef, runtimeRef)
	}
	if pushDef > runtimeRef {
		t.Errorf("zerg_list_zerg_str_push should precede zerg_strings_split definition")
	}
}
