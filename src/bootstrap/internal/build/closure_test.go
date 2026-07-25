package build

import (
	"strings"
	"testing"
)

// RUN-based tests for non-capturing closure literals (docs/functions.md): a
// `fn(...) { }` used as a value lifts to a top-level function. Each program is
// compiled, linked, and executed for exact stdout.

// TestClosureInlineArgRuns is the canonical higher-order idiom: a closure passed
// directly to a function that calls it.
func TestClosureInlineArgRuns(t *testing.T) {
	got := runProgramRT(t, "fn apply(f: fn(int) -> int, x: int) -> int {\n\treturn f(x)\n}\n"+
		"fn main() {\n\tprint apply(fn(n: int) -> int { return n + 1 }, 10)\n}\n")
	if want := "11\n"; got != want {
		t.Fatalf("inline closure argument: got %q, want %q", got, want)
	}
}

// TestClosureBoundAndCalledRuns covers a closure bound to a variable and called.
func TestClosureBoundAndCalledRuns(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n"+
		"\tg := fn(n: int) -> int { return n * n }\n"+
		"\tprint g(7)\n}\n")
	if want := "49\n"; got != want {
		t.Fatalf("bound closure: got %q, want %q", got, want)
	}
}

// TestClosureMultilineBodyRuns covers a multi-line closure body — with local mutation,
// which docs/functions.md permits (only CAPTURED state is immutable) — passed inline,
// exercising both the lift and the brace-block ASI.
func TestClosureMultilineBodyRuns(t *testing.T) {
	got := runProgramRT(t, "fn apply(f: fn(int) -> int, x: int) -> int {\n\treturn f(x)\n}\n"+
		"fn main() {\n"+
		"\tprint apply(fn(n: int) -> int {\n"+
		"\t\tmut acc := n\n"+
		"\t\tacc = acc * 2\n"+
		"\t\treturn acc + 1\n"+
		"\t}, 10)\n"+
		"}\n")
	if want := "21\n"; got != want {
		t.Fatalf("multi-line closure: got %q, want %q", got, want)
	}
}

// TestClosureDispatchTableRuns covers a list of closures iterated as a dispatch table.
func TestClosureDispatchTableRuns(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n"+
		"\tops := [fn(n: int) -> int { return n + 1 }, fn(n: int) -> int { return n * 2 }]\n"+
		"\tfor op in ops {\n\t\tprint op(10)\n\t}\n}\n")
	if want := "11\n20\n"; got != want {
		t.Fatalf("closure dispatch table: got %q, want %q", got, want)
	}
}

// TestClosureRefersTopLevelLifts covers that a reference to a top-level function (not a
// capture) keeps a closure liftable.
func TestClosureRefersTopLevelLifts(t *testing.T) {
	got := runProgramRT(t, "fn helper(n: int) -> int { return n * 3 }\n"+
		"fn main() {\n"+
		"\tf := fn(n: int) -> int { return helper(n) + 1 }\n"+
		"\tprint f(10)\n}\n")
	if want := "31\n"; got != want {
		t.Fatalf("closure over a top-level name: got %q, want %q", got, want)
	}
}

// TestClosureInnerLocalNotCaptured covers that a closure's own inner binding — even one
// shadowing an enclosing name — is not a capture.
func TestClosureInnerLocalNotCaptured(t *testing.T) {
	got := runProgramRT(t, "fn apply(f: fn(int) -> int, x: int) -> int {\n\treturn f(x)\n}\n"+
		"fn main() {\n"+
		"\ty := 999\n"+
		"\tprint apply(fn(n: int) -> int {\n\t\ty := n + 1\n\t\treturn y\n\t}, 10)\n}\n")
	if want := "11\n"; got != want {
		t.Fatalf("closure inner local: got %q, want %q", got, want)
	}
}

// TestClosureLifting pins the emitted C: the closure becomes a top-level `zg_lambda_0`
// function taking the uniform closure env parameter (ignored, since it captures
// nothing), and its use site is a closure literal over it with a NULL env.
func TestClosureLifting(t *testing.T) {
	code, _, diags := Compile("fn apply(f: fn(int) -> int, x: int) -> int {\n\treturn f(x)\n}\n" +
		"fn main() {\n\tprint apply(fn(n: int) -> int { return n + 1 }, 5)\n}\n")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	for _, want := range []string{
		"int64_t zg_lambda_0(void *zg_env, int64_t",
		"(void)zg_env;",
		"zg_lambda_0, (void *)0 })",
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted C missing %q:\n%s", want, code)
		}
	}
}

// TestCapturingClosureGated keeps the boundary of what capture supports: a `mut`
// variable can never be captured (the grammar captures immutable captures, and the
// value cannot change through the capture). An immutable capture — POD or NON-POD —
// works (see the RUN tests; a non-POD capture is retained into the refcounted env box).
func TestCapturingClosureGated(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{
			"a mut capture",
			"fn main() {\n\tmut acc := 0\n\tf := fn(n: int) -> int { return n + acc }\n\tprint f(5)\n}\n",
			"cannot capture the mutable variable",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, diags := Compile(tc.src)
			if len(diags) == 0 {
				t.Fatalf("expected a diagnostic for %s", tc.name)
			}
			var joined string
			for _, d := range diags {
				joined += d.Msg + "\n"
			}
			if !strings.Contains(joined, tc.want) {
				t.Fatalf("expected a diagnostic mentioning %q, got:\n%s", tc.want, joined)
			}
		})
	}
}
