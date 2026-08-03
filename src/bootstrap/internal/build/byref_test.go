package build

import (
	"strings"
	"testing"
)

// RUN-based tests for the `mut &` mutable-reference parameter (GRAMMAR group 5,
// docs/core/memory.md) — Zerg's one explicit by-ref path. Each program is compiled, linked
// against the runtime under ASan+UBSan, and executed, so a passing test asserts the
// caller actually observes the callee's write.

// TestByRefWritesBackRuns is the core of the feature: the callee mutates the caller's
// variable in place, and the caller sees it.
func TestByRefWritesBackRuns(t *testing.T) {
	got := runProgramRT(t, "fn bump(mut &n: int) {\n\tn = n + 1\n}\n"+
		"fn main() {\n\tmut x := 5\n\tbump(x)\n\tprint x\n}\n")
	if want := "6\n"; got != want {
		t.Fatalf("by-ref writeback: got %q, want %q", got, want)
	}
}

// TestByRefSwapRuns exercises two distinct `mut &` parameters in one call — the shape
// the no-aliasing guarantee exists for.
func TestByRefSwapRuns(t *testing.T) {
	got := runProgramRT(t, "fn swap(mut &p: int, mut &q: int) {\n\tt := p\n\tp = q\n\tq = t\n}\n"+
		"fn main() {\n\tmut a := 1\n\tmut b := 2\n\tswap(a, b)\n\tprint a\n\tprint b\n}\n")
	if want := "2\n1\n"; got != want {
		t.Fatalf("by-ref swap: got %q, want %q", got, want)
	}
}

// TestByRefPassOnwardRuns covers the one onward move the docs allow: a `mut &`
// parameter may be passed to another `mut &` parameter, forwarding the same storage.
func TestByRefPassOnwardRuns(t *testing.T) {
	got := runProgramRT(t, "fn inner(mut &n: int) {\n\tn = n * 2\n}\n"+
		"fn outer(mut &n: int) {\n\tinner(n)\n}\n"+
		"fn main() {\n\tmut x := 21\n\touter(x)\n\tprint x\n}\n")
	if want := "42\n"; got != want {
		t.Fatalf("by-ref pass-onward: got %q, want %q", got, want)
	}
}

// TestByRefStructRuns covers a whole struct behind the reference, and a struct FIELD
// as the argument — both are lvalue paths rooted at a mut binding.
func TestByRefStructRuns(t *testing.T) {
	got := runProgramRT(t, "struct P {\n\tx: int\n\ty: int\n}\n"+
		"fn shift(mut &p: P) {\n\tp.x = p.x + 10\n}\n"+
		"fn bump(mut &n: int) {\n\tn = n + 1\n}\n"+
		"fn main() {\n\tmut p := P(x: 1, y: 2)\n\tshift(p)\n\tprint p.x\n\tprint p.y\n"+
		"\tbump(p.y)\n\tprint p.y\n}\n")
	if want := "11\n2\n3\n"; got != want {
		t.Fatalf("by-ref struct: got %q, want %q", got, want)
	}
}

// TestByRefReturnsACopyRuns pins that a value position COPIES the current value rather
// than leaking the reference: the returned int is an ordinary value.
func TestByRefReturnsACopyRuns(t *testing.T) {
	got := runProgramRT(t, "fn take(mut &n: int) -> int {\n\tn = n + 1\n\treturn n\n}\n"+
		"fn main() {\n\tmut x := 1\n\ty := take(x)\n\tprint x\n\tprint y\n}\n")
	if want := "2\n2\n"; got != want {
		t.Fatalf("by-ref return copy: got %q, want %q", got, want)
	}
}

// TestByRefContractRejected covers the caller-side contract: the argument must be a
// `mut` lvalue, and two `mut &` arguments must not name the same storage.
func TestByRefContractRejected(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{
			"an immutable binding",
			"fn bump(mut &n: int) {\n\tn = n + 1\n}\nfn main() {\n\tx := 5\n\tbump(x)\n}\n",
			"declare it `mut`",
		},
		{
			"a literal",
			"fn bump(mut &n: int) {\n\tn = n + 1\n}\nfn main() {\n\tbump(5)\n}\n",
			"needs a variable to write back to",
		},
		{
			"the same variable twice",
			"fn f(mut &a: int, mut &b: int) {\n\ta = b\n}\nfn main() {\n\tmut x := 5\n\tf(x, x)\n}\n",
			"must not alias",
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

// TestByRefLowering pins the emitted C: the parameter is a pointer in BOTH the
// prototype and the definition (a mismatch is a cc error), and the call takes an
// address rather than copying.
func TestByRefLowering(t *testing.T) {
	code, _, diags := Compile("fn bump(mut &n: int) {\n\tn = n + 1\n}\n" +
		"fn main() {\n\tmut x := 5\n\tbump(x)\n}\n")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	for _, want := range []string{
		"void zg_bump(int64_t*);",
		"void zg_bump(int64_t* zg_n)",
		"(*zg_n) = (zrt_add_i64((int64_t)((*zg_n)), (int64_t)(1)));",
		"zg_bump(&zg_x);",
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted C missing %q:\n%s", want, code)
		}
	}
}

// TestByValueParamUnchanged is the control: a plain parameter still passes by value,
// so the caller's variable is untouched and the emitted C keeps its old shape.
func TestByValueParamUnchanged(t *testing.T) {
	got := runProgramRT(t, "fn bump(n: int) -> int {\n\treturn n + 1\n}\n"+
		"fn main() {\n\tmut x := 5\n\tprint bump(x)\n\tprint x\n}\n")
	if want := "6\n5\n"; got != want {
		t.Fatalf("by-value parameter: got %q, want %q", got, want)
	}
	code, _, _ := Compile("fn bump(n: int) -> int {\n\treturn n + 1\n}\n" +
		"fn main() {\n\tmut x := 5\n\tprint bump(x)\n}\n")
	if strings.Contains(code, "int64_t*") || strings.Contains(code, "&zg_x") {
		t.Fatalf("a by-value parameter must not take a pointer:\n%s", code)
	}
}

// TestMutFnReceiverGated covers the gate on `mut fn`: GRAMMAR makes it the `mut &this`
// case, but the receiver is still passed by value, so it must fail loudly rather than
// silently discard the mutation.
func TestMutFnReceiverGated(t *testing.T) {
	_, _, diags := Compile("struct C {\n\tn: int\n}\n" +
		"impl C {\n\tmut fn bump(d: int) {\n\t\tthis.n = this.n + d\n\t}\n}\n" +
		"fn main() {\n\tmut c := C(n: 1)\n\tc.bump(1)\n}\n")
	if len(diags) == 0 {
		t.Fatalf("a `mut fn` receiver mutation should be gated, not silently dropped")
	}
}
