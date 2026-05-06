package build

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

func TestCQuote(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", `""`},
		{"plain", "hello", `"hello"`},
		{"escaped quote", `say "hi"`, `"say \"hi\""`},
		{"backslash", `a\b`, `"a\\b"`},
		{"newline", "a\nb", `"a\nb"`},
		{"tab", "a\tb", `"a\tb"`},
		{"carriage return", "a\rb", `"a\rb"`},
		{"nul is three-digit octal", "a\x00b", `"a\000b"`},
		{"nul followed by digit stays three-digit", "\x001", `"\0001"`},
		{"low control byte", "\x01", `"\001"`},
		{"del", "\x7f", `"\177"`},
		{"high bit byte passes through", "\xff", "\"\xff\""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := cQuote(c.in)
			if got != c.want {
				t.Fatalf("cQuote(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// mustEmit parses, type-checks, and emits the source — the same pipeline Build
// uses. Tests assert against the full string so they catch both the runtime
// header and the user-code lowering.
func mustEmit(t *testing.T, src string) string {
	t.Helper()
	tokens, err := syntax.Lex([]byte(src))
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	prog, err := syntax.Parse(tokens)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := syntax.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	var buf bytes.Buffer
	if err := Emit(prog, &buf); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	return buf.String()
}

func TestEmitNop(t *testing.T) {
	out := mustEmit(t, "nop\n")
	if !strings.Contains(out, "(void)0;") {
		t.Errorf("nop should lower to (void)0;, got:\n%s", out)
	}
}

func TestEmitRuntimePresent(t *testing.T) {
	// The runtime block must lead the .c file so user code can reference it.
	out := mustEmit(t, "nop\n")
	for _, want := range []string{
		"#include <stdio.h>",
		"typedef struct { const char *data; size_t len; } zerg_str;",
		"static void zerg_print_int",
		"static void zerg_print_float",
		"static void zerg_print_bool",
		"static void zerg_print_str",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing runtime piece %q in:\n%s", want, out)
		}
	}
}

func TestEmitPrintInt(t *testing.T) {
	out := mustEmit(t, "print 42\n")
	if !strings.Contains(out, "zerg_print_int(INT64_C(42));") {
		t.Errorf("print of int should call zerg_print_int with INT64_C; got:\n%s", out)
	}
}

func TestEmitPrintFloat(t *testing.T) {
	out := mustEmit(t, "print 3.14\n")
	if !strings.Contains(out, "zerg_print_float(") {
		t.Errorf("print of float should call zerg_print_float; got:\n%s", out)
	}
}

func TestEmitPrintBool(t *testing.T) {
	out := mustEmit(t, "print true\n")
	if !strings.Contains(out, "zerg_print_bool((_Bool)1);") {
		t.Errorf("print of bool should call zerg_print_bool; got:\n%s", out)
	}
}

func TestEmitPrintStr(t *testing.T) {
	out := mustEmit(t, `print "hi"`+"\n")
	if !strings.Contains(out, `zerg_print_str(zerg_str_lit("hi", 2));`) {
		t.Errorf("print of str should wrap literal in zerg_str_lit; got:\n%s", out)
	}
}

func TestEmitLetMangled(t *testing.T) {
	out := mustEmit(t, "x := 5\n")
	if !strings.Contains(out, "int64_t z_x = INT64_C(5);") {
		t.Errorf("x := 5 should declare int64_t z_x; got:\n%s", out)
	}
}

func TestEmitConstQualifier(t *testing.T) {
	out := mustEmit(t, "const PI := 3.14\n")
	if !strings.Contains(out, "const double z_PI") {
		t.Errorf("const decl should emit C const qualifier; got:\n%s", out)
	}
}

func TestEmitMutAssign(t *testing.T) {
	src := "mut x := 1\nx += 2\n"
	out := mustEmit(t, src)
	if !strings.Contains(out, "z_x += INT64_C(2);") {
		t.Errorf("compound assign on int should emit `+=`; got:\n%s", out)
	}
}

func TestEmitStrConcatAssign(t *testing.T) {
	src := `mut s := "a"` + "\n" + `s += "b"` + "\n"
	out := mustEmit(t, src)
	if !strings.Contains(out, "z_s = zerg_str_concat(z_s, zerg_str_lit") {
		t.Errorf("`+=` on str should call zerg_str_concat; got:\n%s", out)
	}
}

func TestEmitIfChain(t *testing.T) {
	src := `
x := 1
if x == 1 {
    print "one"
} elif x == 2 {
    print "two"
} else {
    print "other"
}
`
	out := mustEmit(t, src)
	if !strings.Contains(out, "if (") || !strings.Contains(out, "} else if (") || !strings.Contains(out, "} else {") {
		t.Errorf("if/elif/else chain missing expected shape; got:\n%s", out)
	}
}

func TestEmitForRange(t *testing.T) {
	src := "for i in 0..3 { print i }\n"
	out := mustEmit(t, src)
	if !strings.Contains(out, "for (int64_t z_i = INT64_C(0); z_i < INT64_C(3); ++z_i) {") {
		t.Errorf("for-in half-open range missing expected shape; got:\n%s", out)
	}
}

func TestEmitForRangeInclusive(t *testing.T) {
	out := mustEmit(t, "for i in 0..=3 { print i }\n")
	if !strings.Contains(out, "z_i <= INT64_C(3)") {
		t.Errorf("inclusive range should compare with `<=`; got:\n%s", out)
	}
}

func TestEmitForCond(t *testing.T) {
	out := mustEmit(t, "mut x := 0\nfor x < 3 { x += 1 }\n")
	if !strings.Contains(out, "while ((z_x < INT64_C(3))) {") {
		t.Errorf("for-cond should lower to a `while`; got:\n%s", out)
	}
}

func TestEmitForInfinite(t *testing.T) {
	out := mustEmit(t, "for { break }\n")
	if !strings.Contains(out, "while (1) {") {
		t.Errorf("infinite for should lower to `while (1)`; got:\n%s", out)
	}
	if !strings.Contains(out, "break;") {
		t.Errorf("unconditional break should emit `break;`; got:\n%s", out)
	}
}

func TestEmitFnDecl(t *testing.T) {
	src := `
fn add(a: int, b: int) -> int {
    return a + b
}
print add(2, 3)
`
	out := mustEmit(t, src)
	addSym := "z_" + mangleModule("main") + "__add"
	if !strings.Contains(out, "static int64_t "+addSym+"(int64_t z_a, int64_t z_b);") {
		t.Errorf("fn decl forward should be emitted; got:\n%s", out)
	}
	if !strings.Contains(out, "static int64_t "+addSym+"(int64_t z_a, int64_t z_b) {") {
		t.Errorf("fn body should follow the same signature; got:\n%s", out)
	}
	if !strings.Contains(out, "return (z_a + z_b);") {
		t.Errorf("return of binary should be parenthesised; got:\n%s", out)
	}
	if !strings.Contains(out, "zerg_print_int("+addSym+"(INT64_C(2), INT64_C(3)));") {
		t.Errorf("call should mangle and pass through helpers; got:\n%s", out)
	}
}

func TestEmitVoidFn(t *testing.T) {
	src := `
fn greet() {
    print "hi"
}
greet()
`
	out := mustEmit(t, src)
	greetSym := "z_" + mangleModule("main") + "__greet"
	if !strings.Contains(out, "static void "+greetSym+"(void);") {
		t.Errorf("void fn should emit `void` return and `(void)` param list; got:\n%s", out)
	}
}

func TestEmitReturnGuard(t *testing.T) {
	src := `
fn pick(b: bool) -> int {
    return 1 if b
    return 0
}
print pick(true)
`
	out := mustEmit(t, src)
	if !strings.Contains(out, "if (z_b) { return INT64_C(1); }") {
		t.Errorf("guarded return should expand to `if (cond) { return ...; }`; got:\n%s", out)
	}
}

func TestEmitStrComparison(t *testing.T) {
	out := mustEmit(t, `print "a" < "b"`+"\n")
	if !strings.Contains(out, "(zerg_str_cmp(zerg_str_lit(\"a\", 1), zerg_str_lit(\"b\", 1)) < 0)") {
		t.Errorf("str ordering should call zerg_str_cmp; got:\n%s", out)
	}
}

func TestEmitFloorDivFloat(t *testing.T) {
	out := mustEmit(t, "print 3.0 // 2.0\n")
	if !strings.Contains(out, "floor(") {
		t.Errorf("float // should call libm floor; got:\n%s", out)
	}
}

func TestEmitFloatMod(t *testing.T) {
	out := mustEmit(t, "print 5.0 % 2.0\n")
	if !strings.Contains(out, "fmod(") {
		t.Errorf("float %% should call libm fmod; got:\n%s", out)
	}
}

func TestEmitXorBool(t *testing.T) {
	out := mustEmit(t, "print true xor false\n")
	if !strings.Contains(out, "((_Bool)") || !strings.Contains(out, "^") {
		t.Errorf("bool xor should cast both sides to _Bool and use `^`; got:\n%s", out)
	}
}

// TestEmitAndCompileSmoke runs the full pipeline end-to-end: emit, compile via
// the same cc the build path uses, execute, and verify stdout. This covers the
// minimum integration check the unit-level tests cannot reach on their own.
func TestEmitAndCompileSmoke(t *testing.T) {
	cc := DefaultCC()
	if _, err := exec.LookPath(cc); err != nil {
		t.Skip("cc not available")
	}
	src := "x := 5\ny := 3\nprint x + y\n"
	out := mustEmit(t, src)

	dir := t.TempDir()
	cPath := filepath.Join(dir, "smoke.c")
	if err := os.WriteFile(cPath, []byte(out), 0o644); err != nil {
		t.Fatalf("write c: %v", err)
	}
	binPath := filepath.Join(dir, "smoke")
	cmd := exec.Command(cc, "-fwrapv", "-O2", "-o", binPath, cPath, "-lm")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("cc: %v\n--- generated ---\n%s", err, out)
	}
	got, err := exec.Command(binPath).Output()
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if string(got) != "8\n" {
		t.Errorf("smoke stdout = %q, want %q\n--- generated ---\n%s", string(got), "8\n", out)
	}
}
