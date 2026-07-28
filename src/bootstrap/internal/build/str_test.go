package build

import (
	"strings"
	"testing"
)

// RUN-based tests for the `str` operators docs/code/collections.md gives the type — Add
// (`a + b` concatenates into a new str) and Ord (lexicographic by code point, with Eq
// underneath). Each program is compiled, linked against the materialized runtime under
// ASan+UBSan, and executed, so a passing test asserts a clean exit and exact stdout.

// TestStrConcatRuns covers `str` Add: '+' joins two strings into a new one, and folds
// left-to-right across a chain. The empty string is an identity.
func TestStrConcatRuns(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n"+
		"\ta := \"foo\"\n\tb := \"bar\"\n"+
		"\tprint a + b\n"+
		"\tprint \"a\" + \"b\" + \"c\"\n"+
		"\tprint \"\" + \"x\"\n"+
		"\tprint a + \"\"\n"+
		"}\n")
	if want := "foobar\nabc\nx\nfoo\n"; got != want {
		t.Fatalf("str concatenation: got %q, want %q", got, want)
	}
}

// TestStrEqualityIsContentNotPointer is the regression that motivated the lowering: a
// str comparison must compare CONTENT. Both operands here are built at run time
// through zrt_str_concat, so they are distinct pointers holding equal bytes — the
// native C `==` the backend used to emit answered false.
func TestStrEqualityIsContentNotPointer(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n"+
		"\ta := \"foo\" + \"bar\"\n"+
		"\tb := \"foobar\"\n"+
		"\tprint a == b\n"+
		"\tprint a != b\n"+
		"\tc := \"x\" + \"y\"\n"+
		"\td := \"x\" + \"y\"\n"+
		"\tprint c == d\n"+
		"}\n")
	if want := "true\nfalse\ntrue\n"; got != want {
		t.Fatalf("str equality must compare content: got %q, want %q", got, want)
	}
}

// TestStrOrderingRuns covers `str` Ord: it sorts lexicographically by code point, so
// "apple" < "banana" and a prefix precedes the string that extends it.
func TestStrOrderingRuns(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n"+
		"\tprint \"apple\" < \"banana\"\n"+
		"\tprint \"banana\" < \"apple\"\n"+
		"\tprint \"foo\" < \"foobar\"\n"+
		"\tprint \"abc\" <= \"abc\"\n"+
		"\tprint \"abc\" >= \"abc\"\n"+
		"\tprint \"b\" > \"a\"\n"+
		"}\n")
	if want := "true\nfalse\ntrue\ntrue\ntrue\ntrue\n"; got != want {
		t.Fatalf("str ordering: got %q, want %q", got, want)
	}
}

// TestStrGenericBoundRuns covers the blessed Eq/Ord registration `str` gained once its
// comparison became content-correct: a `[T: Ord]` / `[T: Eq]` generic accepts a str
// operand, and the instance substitution routes it to the same strcmp lowering.
func TestStrGenericBoundRuns(t *testing.T) {
	got := runProgramRT(t, "fn largest[T: Ord](a: T, b: T) -> T {\n"+
		"\tif a > b {\n\t\treturn a\n\t}\n\treturn b\n}\n"+
		"fn same[T: Eq](a: T, b: T) -> bool {\n\treturn a == b\n}\n"+
		"fn main() {\n"+
		"\tprint largest(\"apple\", \"banana\")\n"+
		"\tprint largest(3, 9)\n"+
		"\tprint same(\"x\" + \"y\", \"xy\")\n"+
		"\tprint same(1, 2)\n"+
		"}\n")
	if want := "banana\n9\ntrue\nfalse\n"; got != want {
		t.Fatalf("str under a generic bound: got %q, want %q", got, want)
	}
}

// TestStrConcatLowering pins the emitted C: '+' becomes zrt_str_concat and a
// comparison becomes strcmp against 0 — never a bare pointer compare.
func TestStrConcatLowering(t *testing.T) {
	code, manifest, diags := Compile("fn main() {\n\ta := \"x\" + \"y\"\n\tprint a == \"xy\"\n}\n")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	for _, want := range []string{"zrt_str_concat(", "strcmp("} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted C missing %q:\n%s", want, code)
		}
	}
	// zrt_str_concat rides in the always-linked fmt.c, so concatenation needs the runtime.
	if !manifest.NeedsRuntime {
		t.Fatalf("str concatenation should set NeedsRuntime, got %+v", manifest)
	}
}

// TestStrNonAddArithRejected checks that opening '+' to str did not open the rest of
// the arithmetic family: only Add is specified for str.
func TestStrNonAddArithRejected(t *testing.T) {
	for _, op := range []string{"-", "*", "/", "%"} {
		src := "fn main() {\n\tprint \"a\" " + op + " \"b\"\n}\n"
		if _, _, diags := Compile(src); len(diags) == 0 {
			t.Fatalf("operator %q on two strings should be rejected", op)
		}
	}
}

// TestStrMixedOperandRejected checks that a str never silently pairs with a number:
// there is no implicit conversion, so `"a" + 1` stays a diagnostic.
func TestStrMixedOperandRejected(t *testing.T) {
	for _, src := range []string{
		"fn main() {\n\tprint \"a\" + 1\n}\n",
		"fn main() {\n\tprint 1 + \"a\"\n}\n",
		"fn main() {\n\tprint \"a\" < 1\n}\n",
	} {
		if _, _, diags := Compile(src); len(diags) == 0 {
			t.Fatalf("a mixed str/number operand should be rejected: %s", src)
		}
	}
}
