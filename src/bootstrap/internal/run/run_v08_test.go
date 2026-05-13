// Tests for the v0.8 std-module interpreter dispatch (Unit 3). Each test
// materialises a v0.8 main.zg under TempDir, imports the relevant std
// module, exercises one __builtin, and asserts the resulting Value via
// stdout. The std modules themselves are toolchain-shipped via the embed
// FS stood up in Unit 2, so the tests only need to write the entry file.
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

// runV08Main writes main.zg under TempDir, loads via the public Bundle
// pipeline (loader → CheckBundle → RunBundle), and returns stdout + any
// error. The harness mirrors run_v05_test.go's runBundleFiles but is
// tailored to v0.8 single-entry programs that import std modules from the
// embed FS — no extra fixture files needed beyond main.zg.
func runV08Main(t *testing.T, mainSrc string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.zg")
	if err := os.WriteFile(entry, []byte(mainSrc), 0o644); err != nil {
		t.Fatalf("write main.zg: %v", err)
	}
	bundle, err := loader.Load(entry)
	if err != nil {
		return "", err
	}
	if err := syntax.CheckBundle(bundle); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := RunBundle(bundle, &buf); err != nil {
		return buf.String(), err
	}
	return buf.String(), nil
}

func expectV08OK(t *testing.T, mainSrc, want string) {
	t.Helper()
	got, err := runV08Main(t, mainSrc)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if got != want {
		t.Fatalf("stdout mismatch\n got: %q\nwant: %q", got, want)
	}
}

func expectV08Err(t *testing.T, mainSrc, want string) {
	t.Helper()
	_, err := runV08Main(t, mainSrc)
	if err == nil {
		t.Fatalf("expected error containing %q, got success", want)
	}
	if want != "" && !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not contain %q", err.Error(), want)
	}
}

// ---------------------------------------------------------------------------
// std/math
// ---------------------------------------------------------------------------

func TestRunV08MathAbs(t *testing.T) {
	expectV08OK(t, `# requires: v0.8
import "std/math"
print math.abs(-5)
print math.abs(7)
print math.abs(0)
`, "5\n7\n0\n")
}

func TestRunV08MathMin(t *testing.T) {
	expectV08OK(t, `# requires: v0.8
import "std/math"
print math.min(3, 7)
print math.min(7, 3)
print math.min(-1, -2)
`, "3\n3\n-2\n")
}

func TestRunV08MathMax(t *testing.T) {
	expectV08OK(t, `# requires: v0.8
import "std/math"
print math.max(3, 7)
print math.max(7, 3)
print math.max(-1, -2)
`, "7\n7\n-1\n")
}

func TestRunV08MathGcd(t *testing.T) {
	expectV08OK(t, `# requires: v0.8
import "std/math"
print math.gcd(12, 18)
print math.gcd(0, 0)
print math.gcd(0, 5)
print math.gcd(5, 0)
print math.gcd(-12, 18)
`, "6\n0\n5\n5\n6\n")
}

// ---------------------------------------------------------------------------
// std/strings
// ---------------------------------------------------------------------------

func TestRunV08StringsTrim(t *testing.T) {
	expectV08OK(t, `# requires: v0.8
import "std/strings"
print strings.trim("  hello  ")
print strings.trim("\t\nzerg\n")
print strings.trim("noop")
`, "hello\nzerg\nnoop\n")
}

func TestRunV08StringsStartsWithEndsWithContains(t *testing.T) {
	expectV08OK(t, `# requires: v0.8
import "std/strings"
print strings.starts_with("hello", "he")
print strings.starts_with("hello", "lo")
print strings.ends_with("hello", "lo")
print strings.ends_with("hello", "he")
print strings.contains("hello", "ell")
print strings.contains("hello", "zz")
`, "true\nfalse\ntrue\nfalse\ntrue\nfalse\n")
}

func TestRunV08StringsReplace(t *testing.T) {
	expectV08OK(t, `# requires: v0.8
import "std/strings"
print strings.replace("foo bar foo", "foo", "baz")
print strings.replace("aaa", "aa", "b")
print strings.replace("abc", "x", "y")
`, "baz bar baz\nba\nabc\n")
}

func TestRunV08StringsToUpperToLower(t *testing.T) {
	expectV08OK(t, `# requires: v0.8
import "std/strings"
print strings.to_upper("hello")
print strings.to_lower("HELLO")
print strings.to_upper("AbCdE")
print strings.to_lower("AbCdE")
`, "HELLO\nhello\nABCDE\nabcde\n")
}

func TestRunV08StringsSplit(t *testing.T) {
	expectV08OK(t, `# requires: v0.8
import "std/strings"
xs := strings.split("a,b,c", ",")
print xs
`, "[ a, b, c ]\n")
}

func TestRunV08StringsSplitEmptySepPanics(t *testing.T) {
	expectV08Err(t, `# requires: v0.8
import "std/strings"
xs := strings.split("abc", "")
print xs
`, "split: empty separator")
}

func TestRunV08StringsJoin(t *testing.T) {
	expectV08OK(t, `# requires: v0.8
import "std/strings"
xs := strings.split("a,b,c", ",")
print strings.join(xs, "-")
print strings.join(xs, "")
`, "a-b-c\nabc\n")
}

func TestRunV08StringsParseIntOk(t *testing.T) {
	expectV08OK(t, `# requires: v0.8
import "std/strings"
print strings.parse_int("42")
print strings.parse_int("  -7  ")
print strings.parse_int("+9")
print strings.parse_int("-0")
`, "Result.Ok(42)\nResult.Ok(-7)\nResult.Ok(9)\nResult.Ok(0)\n")
}

func TestRunV08StringsParseIntErr(t *testing.T) {
	expectV08OK(t, `# requires: v0.8
import "std/strings"
print strings.parse_int("")
print strings.parse_int("   ")
print strings.parse_int("abc")
print strings.parse_int("12x")
print strings.parse_int("99999999999999999999999")
`, "Result.Err(ParseError.Empty)\nResult.Err(ParseError.Empty)\nResult.Err(ParseError.InvalidDigit)\nResult.Err(ParseError.InvalidDigit)\nResult.Err(ParseError.Overflow)\n")
}

// ---------------------------------------------------------------------------
// std/io
// ---------------------------------------------------------------------------

func TestRunV08IoReadFileOk(t *testing.T) {
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
	expectV08OK(t, src, "Result.Ok(hello, file\n)\n")
}

func TestRunV08IoReadFileNotFound(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "no-such-file.txt")
	src := `# requires: v0.8
import "std/io"
r := io.read_file("` + missing + `")
print r
`
	expectV08OK(t, src, "Result.Err(IoError.NotFound)\n")
}

func TestRunV08IoWriteFileOk(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.txt")
	src := `# requires: v0.8
import "std/io"
r := io.write_file("` + target + `", "written")
print r
`
	expectV08OK(t, src, "Result.Ok(true)\n")
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "written" {
		t.Errorf("file contents = %q, want %q", got, "written")
	}
}

func TestRunV08IoWriteFilePermissionDenied(t *testing.T) {
	// Writing to a path under a read-only directory triggers
	// fs.ErrPermission. POSIX: 0o500 dir → write-attempts EACCES.
	dir := t.TempDir()
	roDir := filepath.Join(dir, "ro")
	if err := os.Mkdir(roDir, 0o500); err != nil {
		t.Fatalf("mkdir ro: %v", err)
	}
	defer os.Chmod(roDir, 0o700)
	target := filepath.Join(roDir, "out.txt")
	src := `# requires: v0.8
import "std/io"
r := io.write_file("` + target + `", "data")
print r
`
	expectV08OK(t, src, "Result.Err(IoError.PermissionDenied)\n")
}

// ---------------------------------------------------------------------------
// std/os
// ---------------------------------------------------------------------------

func TestRunV08OsEnvSome(t *testing.T) {
	t.Setenv("ZERG_TEST_V08_VAR", "abc")
	expectV08OK(t, `# requires: v0.8
import "std/os"
print os.env("ZERG_TEST_V08_VAR")
`, "abc\n")
}

func TestRunV08OsEnvNone(t *testing.T) {
	t.Setenv("ZERG_TEST_V08_VAR", "abc")
	os.Unsetenv("ZERG_TEST_V08_VAR")
	expectV08OK(t, `# requires: v0.8
import "std/os"
print os.env("ZERG_TEST_V08_VAR")
`, "nil\n")
}
