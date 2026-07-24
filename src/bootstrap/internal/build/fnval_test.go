package build

import (
	"strings"
	"testing"
)

// RUN-based tests for first-class NAMED function values (docs/functions.md): a bare
// top-level function name used as a value — passed as an argument, bound to a variable,
// stored in a struct field, returned, and collected in a list — plus the call through
// such a value. Each program is compiled, linked, and executed for exact stdout.

// TestFnValuePassAndCallRuns is the core: a named function passed as an argument and
// called through the parameter (an indirect call through a function value).
func TestFnValuePassAndCallRuns(t *testing.T) {
	got := runProgramRT(t, "fn inc(n: int) -> int {\n\treturn n + 1\n}\n"+
		"fn apply(f: fn(int) -> int, x: int) -> int {\n\treturn f(x)\n}\n"+
		"fn twice(f: fn(int) -> int, x: int) -> int {\n\treturn f(f(x))\n}\n"+
		"fn main() {\n\tprint apply(inc, 5)\n\tprint twice(inc, 10)\n}\n")
	if want := "6\n12\n"; got != want {
		t.Fatalf("function value pass/call: got %q, want %q", got, want)
	}
}

// TestFnValueBindAndReassignRuns covers binding a function to a variable and, for a
// `mut` binding, rebinding it to a different function of the same type.
func TestFnValueBindAndReassignRuns(t *testing.T) {
	got := runProgramRT(t, "fn inc(n: int) -> int {\n\treturn n + 1\n}\n"+
		"fn dbl(n: int) -> int {\n\treturn n * 2\n}\n"+
		"fn main() {\n\tf := inc\n\tprint f(10)\n\tmut g := inc\n\tg = dbl\n\tprint g(10)\n}\n")
	if want := "11\n20\n"; got != want {
		t.Fatalf("function value bind/reassign: got %q, want %q", got, want)
	}
}

// TestFnValueStructFieldRuns covers a function value stored in a struct field and
// called through the field — the dispatch-record shape.
func TestFnValueStructFieldRuns(t *testing.T) {
	got := runProgramRT(t, "fn add(a: int, b: int) -> int {\n\treturn a + b\n}\n"+
		"struct Op {\n\tname: str\n\trun: fn(int, int) -> int\n}\n"+
		"fn main() {\n\to := Op(name: \"add\", run: add)\n\tprint o.run(3, 4)\n\tprint o.name\n}\n")
	if want := "7\nadd\n"; got != want {
		t.Fatalf("function value struct field: got %q, want %q", got, want)
	}
}

// TestFnValueReturnRuns covers returning a function value and calling the result.
func TestFnValueReturnRuns(t *testing.T) {
	got := runProgramRT(t, "fn inc(n: int) -> int {\n\treturn n + 1\n}\n"+
		"fn pick() -> fn(int) -> int {\n\treturn inc\n}\n"+
		"fn main() {\n\tf := pick()\n\tprint f(41)\n}\n")
	if want := "42\n"; got != want {
		t.Fatalf("function value return: got %q, want %q", got, want)
	}
}

// TestFnValueDispatchTableRuns covers a list of function values iterated as a dispatch
// table — the shape a compiler's visitor/handler table takes.
func TestFnValueDispatchTableRuns(t *testing.T) {
	got := runProgramRT(t, "fn inc(n: int) -> int {\n\treturn n + 1\n}\n"+
		"fn dbl(n: int) -> int {\n\treturn n * 2\n}\n"+
		"fn neg(n: int) -> int {\n\treturn -n\n}\n"+
		"fn main() {\n\tops := [inc, dbl, neg]\n\tfor op in ops {\n\t\tprint op(10)\n\t}\n}\n")
	if want := "11\n20\n-10\n"; got != want {
		t.Fatalf("function value dispatch table: got %q, want %q", got, want)
	}
}

// TestFnValueLowering pins the emitted C: a function type is the uniform closure struct
// `{ code, env }`, a named function used as a value is a literal over its env-ignoring
// thunk with a NULL env, and a call goes through the code pointer passing env first.
func TestFnValueLowering(t *testing.T) {
	code, _, diags := Compile("fn inc(n: int) -> int {\n\treturn n + 1\n}\n" +
		"fn apply(f: fn(int) -> int, x: int) -> int {\n\treturn f(x)\n}\n" +
		"fn main() {\n\tprint apply(inc, 5)\n}\n")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	for _, want := range []string{
		"(*code)(void *, int64_t); void *env;", // the fat closure struct typedef
		"zg_fn_0 zg_f",                         // the parameter takes the struct type
		"_cl.code(_cl.env,",                    // the call goes through the code pointer
		"zg_fnthunk",                           // the named function's env-ignoring thunk
		", (void *)0 })",                       // a NULL environment
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted C missing %q:\n%s", want, code)
		}
	}
}

// TestFnValueByRefParamRuns covers a `mut &` parameter surviving into the function
// type: a function value whose type carries the mutable-reference calling convention.
func TestFnValueByRefParamRuns(t *testing.T) {
	got := runProgramRT(t, "fn bump(mut &n: int) {\n\tn = n + 1\n}\n"+
		"fn run(f: fn(mut &int), mut &x: int) {\n\tf(x)\n}\n"+
		"fn main() {\n\tmut v := 41\n\trun(bump, v)\n\tprint v\n}\n")
	if want := "42\n"; got != want {
		t.Fatalf("function value with mut & param: got %q, want %q", got, want)
	}
}

// TestGenericFnValueRejected keeps the mechanism honest: a generic function has no
// single value type until it is monomorphized, so it is not yet a first-class value.
func TestGenericFnValueRejected(t *testing.T) {
	_, _, diags := Compile("fn id[T](x: T) -> T {\n\treturn x\n}\n" +
		"fn main() {\n\tf := id\n\tprint f(5)\n}\n")
	if len(diags) == 0 {
		t.Fatalf("a generic function as a value should be rejected")
	}
	var joined string
	for _, d := range diags {
		joined += d.Msg + "\n"
	}
	if !strings.Contains(joined, "generic function") {
		t.Fatalf("expected a diagnostic about a generic function value, got:\n%s", joined)
	}
}
