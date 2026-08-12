package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/build"
	runtime "github.com/cmj0121/zerg/src/runtime"
)

// buildIter3 compiles src, links it against the materialized runtime, and returns the
// built binary path (skipping when no C compiler is available). Unlike the
// feature-specific harnesses it asserts no manifest flags, so it serves any of the
// completeness-iteration-3 fixes. When sanitize is set the emitted C is compiled with
// AddressSanitizer + UndefinedBehaviorSanitizer, so a tuple/Ref/guard program's
// lowering is exercised for memory and UB errors, not merely a C-string match — the
// gap that let F3 hide behind a substring-only assertion.
func buildIter3(t *testing.T, src string, sanitize bool) string {
	t.Helper()
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("no C compiler")
	}
	code, _, diags := build.Compile(src)
	if len(diags) != 0 {
		t.Fatalf("compile diagnostics: %v\n%s", diags, code)
	}
	dir := t.TempDir()
	cfiles, err := runtime.Materialize(dir)
	if err != nil {
		t.Fatalf("materialize runtime: %v", err)
	}
	cpath := filepath.Join(dir, "prog.c")
	if err := os.WriteFile(cpath, []byte(code), 0o644); err != nil {
		t.Fatalf("write C: %v", err)
	}
	bin := filepath.Join(dir, "prog.bin")
	args := []string{"-std=c11", "-I", dir}
	if sanitize {
		args = append(args, "-fsanitize=address,undefined")
	}
	args = append(args, "-o", bin, cpath)
	args = append(args, cfiles...)
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("cc failed: %v\n%s\n--- generated C ---\n%s", err, out, code)
	}
	return bin
}

// TestVoidBlockStatementRuns is the F1 regression: a void-typed block used in
// STATEMENT position must compile (a baseline lowered it to a harmless statement; the
// carrier value path wrongly declared a `void res;`). It compiles AND runs, so the
// emitted statement-expression is exercised end to end, not just cc-checked.
func TestVoidBlockStatementRuns(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"bare", "fn main() {\n  { print 9 }\n}", "9\n"},
		{"nested", "fn main() {\n  { { print 5 } }\n}", "5\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bin := buildIter3(t, tc.src, false)
			stdout, stderr, err := run(t, bin)
			if err != nil {
				t.Fatalf("run: %v\n%s", err, stderr)
			}
			if stdout != tc.want {
				t.Fatalf("stdout = %q, want %q", stdout, tc.want)
			}
		})
	}
}

// TestUserTupleNameNoCollision is the F2 regression: a user type named `tuple_0`
// mangles to `zg_tuple_0`, the historic tuple-carrier spelling. The carrier must pick
// a disjoint name so both the user struct typedef and the tuple carrier compile. It
// runs, proving the program links and both types coexist.
func TestUserTupleNameNoCollision(t *testing.T) {
	const src = "struct tuple_0 { pub v: int }\n" +
		"fn main() {\n" +
		"  s := tuple_0(v: 7)\n" +
		"  t := (1, 2)\n" +
		"  print s.v\n" +
		"  print t.0\n" +
		"  print t.1\n" +
		"}"
	bin := buildIter3(t, src, true)
	stdout, stderr, err := run(t, bin)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	if stdout != "7\n1\n2\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "7\n1\n2\n")
	}
}

// TestUserResultNamesNoCollision is the F2 assessment of the sibling carrier families:
// a user type named `result_0` / `opt_0` / `either_0` mangles into the same namespace
// as the Result/optional/Either carriers. Each must be kept disjoint. Uses a general
// `Result[int]` (a `zg_result_0` carrier) beside the colliding user types.
func TestUserResultNamesNoCollision(t *testing.T) {
	const src = "struct result_0 { pub a: int }\n" +
		"struct opt_0 { pub b: int }\n" +
		"struct either_0 { pub c: int }\n" +
		"fn ok_val() -> Result[int] {\n  return 5\n}\n" +
		"fn main() -> Result[nil] {\n" +
		"  r := result_0(a: 1)\n" +
		"  o := opt_0(b: 2)\n" +
		"  e := either_0(c: 3)\n" +
		"  print (r.a + o.b + e.c)\n" + // 6
		"  print ok_val()!\n" + // 5
		"  return nil\n" +
		"}"
	bin := buildIter3(t, src, true)
	stdout, stderr, err := run(t, bin)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	if stdout != "6\n5\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "6\n5\n")
	}
}

// TestTryResultNilPropagatesErr is the F3 regression, the most serious: `?` on a
// `Result[nil]` operand must early-return the enclosing Err, not silently swallow it.
// The Err case must print NOTHING before exiting non-zero; the Ok case must pass the
// `?` and print 42 at exit 0. Both are RUN (exit code + stdout), compiled under
// ASan+UBSan — a C-string-only assertion is exactly what hid this bug.
func TestTryResultNilPropagatesErr(t *testing.T) {
	t.Run("err-propagates", func(t *testing.T) {
		const src = "fn f() -> Result[nil] {\n" +
			"  return guard { x: int? = nil; x! }\n" +
			"}\n" +
			"fn main() -> Result[nil] {\n" +
			"  f()?\n" +
			"  print 42\n" +
			"  return nil\n" +
			"}"
		bin := buildIter3(t, src, true)
		stdout, _, err := run(t, bin)
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() == 0 {
			t.Fatalf("want non-zero exit (Err propagated), got err=%v", err)
		}
		if stdout != "" {
			t.Fatalf("stdout = %q, want empty (42 must not print after a propagated Err)", stdout)
		}
	})
	t.Run("ok-passes", func(t *testing.T) {
		const src = "fn f() -> Result[nil] {\n" +
			"  return guard { 5 }\n" +
			"}\n" +
			"fn main() -> Result[nil] {\n" +
			"  f()?\n" +
			"  print 42\n" +
			"  return nil\n" +
			"}"
		bin := buildIter3(t, src, true)
		stdout, stderr, err := run(t, bin)
		if err != nil {
			t.Fatalf("run: %v\n%s", err, stderr)
		}
		if stdout != "42\n" {
			t.Fatalf("stdout = %q, want %q (Ok passes the ?)", stdout, "42\n")
		}
	})
}

// TestGenericTupleRoundTrip is the F4 regression: a tuple element type must be
// substituted through a generic instance, so a generic fn can both RETURN a tuple
// (`dup[T]() -> (T, T)`) and CONSUME one (`firstt[T]((T, int)) -> T`). Runs under
// ASan+UBSan.
func TestGenericTupleRoundTrip(t *testing.T) {
	const src = "fn dup[T](x: T) -> (T, T) {\n" +
		"  return (x, x)\n" +
		"}\n" +
		"fn firstt[T](t: (T, int)) -> T {\n" +
		"  return t.0\n" +
		"}\n" +
		"fn main() {\n" +
		"  print dup(21).0\n" + // 21
		"  print firstt((5, 9))\n" + // 5
		"}"
	bin := buildIter3(t, src, true)
	stdout, stderr, err := run(t, bin)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	if stdout != "21\n5\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "21\n5\n")
	}
}

// TestTupleOfRefDiagnosticHasSpan is the F5 fix: the fail-clean diagnostic for copying
// a tuple that holds a Ref must anchor at the binding's source span, not 0:0.
func TestTupleOfRefDiagnosticHasSpan(t *testing.T) {
	const src = "fn main() {\n" +
		"  r := Ref(1)\n" +
		"  t := (r, 9)\n" +
		"  print t.1\n" +
		"}"
	_, _, diags := build.Compile(src)
	if len(diags) == 0 {
		t.Fatalf("want a fail-clean diagnostic for a tuple-of-Ref copy, got none")
	}
	d := diags[0]
	if d.Span.Start.Line == 0 && d.Span.Start.Col == 0 {
		t.Fatalf("diagnostic span is 0:0, want the binding's source span: %q", d.Msg)
	}
	if d.Span.Start.Line != 3 {
		t.Fatalf("diagnostic at line %d, want line 3 (the `t := (r, 9)` binding): %q", d.Span.Start.Line, d.Msg)
	}
}
