package build

import (
	"strings"
	"testing"
)

// TestPtrOpsCompileAndRun is the Phase 1h U1 end-to-end oracle: the raw-pointer
// intrinsics — addr(x), p.store(v), p.load(), and p.offset(n) — inside an
// `unsafe { }` block-expression must compile to pure C and run, round-tripping a
// value through a typed pointer.
func TestPtrOpsCompileAndRun(t *testing.T) {
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	src := "fn main() {\n" +
		"\tunsafe {\n" +
		"\t\tx: int = 42\n" +
		"\t\tp: ptr[int] = addr(x)\n" +
		"\t\tp.store(9)\n" +
		"\t\tprint p.load()\n" +
		"\t\tq: ptr[int] = p.offset(0)\n" +
		"\t\tprint q.load()\n" +
		"\t}\n" +
		"}\n"
	code, _, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if got := compileAndRun(t, cc, code); got != "9\n9\n" {
		t.Fatalf("ptr round-trip: got %q, want %q", got, "9\n9\n")
	}
}

// TestPtrCtypeAndOps checks the U1 lowering shape: a `ptr[int]` lowers to `int64_t*`,
// addr is `&`, load derefs, store assigns through the deref, and offset is C pointer
// arithmetic — no runtime helper involved.
func TestPtrCtypeAndOps(t *testing.T) {
	src := "fn main() {\n" +
		"\tunsafe {\n" +
		"\t\tx: int = 1\n" +
		"\t\tp: ptr[int] = addr(x)\n" +
		"\t\tp.store(2)\n" +
		"\t\tprint p.load()\n" +
		"\t\tq: ptr[int] = p.offset(1)\n" +
		"\t}\n" +
		"}\n"
	code, _, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	for _, want := range []string{
		"int64_t* zg_p = (&(zg_x));",
		"((*(zg_p)) = (2));",
		"(*(zg_p))",
		"int64_t* zg_q = ((zg_p) + (1));",
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted C missing %q:\n%s", want, code)
		}
	}
}

// TestPtrNullTestIsSafe checks that a null test `p == 0` is SAFE (not in group 12's
// list of unsafe ptr ops), so a program may hold and test a pointer without
// dereferencing; it lowers to the native C comparison.
func TestPtrNullTestIsSafe(t *testing.T) {
	src := "fn is_null(p: ptr[int]) -> bool {\n" +
		"\treturn p == 0\n" +
		"}\n" +
		"fn main() { print 0 }\n"
	code, _, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("a `p == 0` null test should be safe, got diagnostics: %v", diags)
	}
	if !strings.Contains(code, "(zg_p == 0)") {
		t.Fatalf("null test should lower to a native comparison:\n%s", code)
	}
}

// TestPtrCast checks the U1 casts `ptr(p)` and `ptr[T](p)`: each is an unsafe,
// pure-C cast to `void*` / `T*`.
func TestPtrCast(t *testing.T) {
	src := "fn main() {\n" +
		"\tunsafe {\n" +
		"\t\tx: int = 5\n" +
		"\t\tp: ptr[int] = addr(x)\n" +
		"\t\tr: ptr = ptr(p)\n" +
		"\t\tq: ptr[int] = ptr[int](r)\n" +
		"\t\tprint q.load()\n" +
		"\t}\n" +
		"}\n"
	code, _, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	for _, want := range []string{
		"void* zg_r = ((void*)(zg_p));",
		"int64_t* zg_q = ((int64_t*)(zg_r));",
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted C missing %q:\n%s", want, code)
		}
	}
}

// TestMutableGlobalCompileAndRun is the Phase 1h U2 end-to-end oracle: a module-level
// `unsafe { }` group holding a `mut` global and an unsafe `fn` that mutates it. The
// global is initialized once, the group fn is emitted and callable from unsafe code,
// and a write persists across calls.
func TestMutableGlobalCompileAndRun(t *testing.T) {
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	src := "unsafe {\n" +
		"\tmut counter: int = 0\n" +
		"\tfn bump() -> int {\n" +
		"\t\tcounter = counter + 1\n" +
		"\t\treturn counter\n" +
		"\t}\n" +
		"}\n" +
		"fn main() {\n" +
		"\tunsafe {\n" +
		"\t\tbump()\n" +
		"\t\tbump()\n" +
		"\t\tprint bump()\n" +
		"\t}\n" +
		"}\n"
	code, _, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	for _, want := range []string{
		"int64_t zg_counter;",     // file-scope global
		"int64_t zg_bump(void) {", // group fn emitted
		"zg_counter = (zg_counter + 1);",
		"zg_counter = 0;", // one-time init
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted C missing %q:\n%s", want, code)
		}
	}
	if got := compileAndRun(t, cc, code); got != "3\n" {
		t.Fatalf("mutable global: got %q, want %q", got, "3\n")
	}
}

// TestUnsafeOpsRejectedInSafeCode collects the Phase 1h unsafe-gating diagnostics:
// every raw operation is legal only inside an `unsafe { }` context, and calling an
// unsafe group fn from safe code is rejected — while a plain top-level `mut` (outside
// a group) stays the safe-code error it already was.
func TestUnsafeOpsRejectedInSafeCode(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "addr",
			src:  "fn main() {\n\tx: int = 1\n\tp: ptr[int] = addr(x)\n\tprint 0\n}\n",
		},
		{
			name: "load",
			src:  "fn read(p: ptr[int]) -> int {\n\treturn p.load()\n}\nfn main() { print 0 }\n",
		},
		{
			name: "store",
			src:  "fn write(p: ptr[int]) {\n\tp.store(1)\n}\nfn main() { print 0 }\n",
		},
		{
			name: "offset",
			src:  "fn step(p: ptr[int]) -> ptr[int] {\n\treturn p.offset(1)\n}\nfn main() { print 0 }\n",
		},
		{
			name: "call-unsafe-fn",
			src:  "unsafe {\n\tfn danger() -> int { return 1 }\n}\nfn main() { print danger() }\n",
		},
		{
			name: "plain-top-level-mut",
			src:  "mut counter := 0\nfn main() { print 0 }\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, diags := Compile(tc.src)
			if len(diags) == 0 {
				t.Fatalf("%s should be rejected outside unsafe, but compiled clean", tc.name)
			}
		})
	}
}

// TestPtrTypeInSignatureIsSafe checks that naming a `ptr`/`ptr[T]` type in a
// parameter or return position is safe (only OPERATIONS are unsafe): a function that
// merely passes a pointer through, without operating on it, compiles clean.
func TestPtrTypeInSignatureIsSafe(t *testing.T) {
	src := "fn passthrough(p: ptr[int]) -> ptr[int] {\n" +
		"\treturn p\n" +
		"}\n" +
		"fn main() { print 0 }\n"
	_, _, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("a ptr type in a signature should be safe, got diagnostics: %v", diags)
	}
}
