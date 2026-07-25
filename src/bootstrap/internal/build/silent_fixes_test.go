package build

import (
	"strings"
	"testing"
)

// TestPrintUnknownTypedValueDiagnostic covers the silent-use fix: an Unknown-typed
// value — a str/container method or field result the stdlib has not typed yet — flowing
// into `print` previously emitted no printf at all, so the statement silently vanished
// (`print s.len()  print 42` printed only 42). It must now fail with a clean diagnostic
// rather than disappearing.
func TestPrintUnknownTypedValueDiagnostic(t *testing.T) {
	for _, src := range []string{
		"fn main() {\n\ts := \"hi\"\n\tprint s.len()\n\tprint 42\n}\n",
		"fn main() {\n\ts := \"hi\"\n\tprint s.bogus()\n}\n",
	} {
		code, _, diags := Compile(src)
		if len(diags) == 0 {
			t.Fatalf("printing an unknown-typed value should be rejected, got clean compile of:\n%s", src)
		}
		if code != "" {
			t.Fatalf("no C should be emitted for a rejected program, got:\n%s", code)
		}
		joined := ""
		for _, d := range diags {
			joined += d.Msg
			if strings.Contains(d.Msg, "internal:") {
				t.Fatalf("must be a clean gate, got internal error: %v", diags)
			}
		}
		if !strings.Contains(joined, "cannot use a value of unknown type here") {
			t.Fatalf("expected the unknown-type diagnostic, got: %v", diags)
		}
	}
}

// TestPrintScalarStillWorks guards the byte-identical requirement of the silent-use fix:
// a normal `print` of a scalar or str is unaffected and still lowers to its printf.
func TestPrintScalarStillWorks(t *testing.T) {
	src := "fn main() {\n\tprint \"hi\"\n\tprint 5\n}\n"
	code, _, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("a normal print should compile clean, got: %v", diags)
	}
	for _, want := range []string{`printf("%s\n", "hi");`, `printf("%lld\n", (long long)(5));`} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted C missing %q:\n%s", want, code)
		}
	}
}

// TestIsExprCleanGate covers the `x is T` fix: the type test parses and type-checks but
// has no backend lowering, so it previously fell to the expr() backstop as an
// `internal: unsupported expression node` leak. It must now be a normal user-facing
// Phase gate ("not yet supported") with no `internal:` text.
func TestIsExprCleanGate(t *testing.T) {
	src := "fn main() {\n\tx := 5\n\tprint x is int\n}\n"
	code, _, diags := Compile(src)
	if len(diags) == 0 {
		t.Fatal("`x is T` should be rejected with a clean diagnostic")
	}
	if code != "" {
		t.Fatalf("no C should be emitted for a rejected program, got:\n%s", code)
	}
	joined := ""
	for _, d := range diags {
		joined += d.Msg
		if strings.Contains(d.Msg, "internal:") {
			t.Fatalf("the `is` gate must not leak an internal error, got: %v", diags)
		}
	}
	if !strings.Contains(joined, "the `is` type test is not yet supported") {
		t.Fatalf("expected the `is` not-yet-supported diagnostic, got: %v", diags)
	}
}

// TestEnumExplicitDiscriminantRoundTrip covers the explicit-discriminant fix: a C-style
// enum with explicit `= n` values (`Red = 1; Green; Blue = 10`, Green continuing to 2)
// must construct and match through the real discriminant — not the positional 0,1,2 —
// so a construct-then-match round-trip observes each variant correctly.
func TestEnumExplicitDiscriminantRoundTrip(t *testing.T) {
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	src := "enum C { Red = 1; Green; Blue = 10 }\n" +
		"fn tag(x: C) -> int {\n" +
		"\treturn match x {\n\t\tRed => 100\n\t\tGreen => 200\n\t\tBlue => 300\n\t}\n" +
		"}\n" +
		"fn main() {\n\tprint tag(Red)\n\tprint tag(Green)\n\tprint tag(Blue)\n}\n"
	code, _, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	// Construction and match must both read the explicit tags (1, 2, 10), never 0.
	for _, want := range []string{
		"((zg_C){.tag = 1})",
		"((zg_C){.tag = 2})",
		"((zg_C){.tag = 10})",
		"(zg_x.tag == 1)",
		"(zg_x.tag == 2)",
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted C missing explicit discriminant %q:\n%s", want, code)
		}
	}
	if got := compileAndRun(t, cc, code); got != "100\n200\n300\n" {
		t.Fatalf("enum discriminant round-trip: got %q, want %q", got, "100\n200\n300\n")
	}
}

// TestPayloadEnumDiscriminantRejected covers the review follow-up: an explicit
// discriminant on a payload-bearing enum keeps a positional tag, so it must be rejected
// (a clean diagnostic) rather than silently ignored.
func TestPayloadEnumDiscriminantRejected(t *testing.T) {
	src := "enum Shape {\n\tCircle(int)\n\tSquare = 5\n}\nfn main() {\n\tprint 0\n}\n"
	code, _, diags := Compile(src)
	if len(diags) == 0 {
		t.Fatal("an explicit discriminant on a payload-bearing enum should be rejected")
	}
	if code != "" {
		t.Fatalf("no C should be emitted for a rejected program, got:\n%s", code)
	}
	joined := ""
	for _, d := range diags {
		joined += d.Msg
	}
	if !strings.Contains(joined, "only allowed on a payload-free enum") {
		t.Fatalf("expected the payload-enum discriminant diagnostic, got: %v", diags)
	}
}

// TestBreakIfConditionTypeChecked covers a break-if / continue-if guard: the trailing
// condition must be bool, mirroring return-if — a non-bool was silently accepted.
func TestBreakIfConditionTypeChecked(t *testing.T) {
	for _, src := range []string{
		"fn main() {\n\tmut i := 0\n\tfor {\n\t\ti = i + 1\n\t\tbreak if 5\n\t}\n}\n",
		"fn main() {\n\tmut i := 0\n\tfor {\n\t\ti = i + 1\n\t\tcontinue if 5\n\t\tbreak if i > 3\n\t}\n}\n",
	} {
		_, _, diags := Compile(src)
		found := false
		for _, d := range diags {
			if strings.Contains(d.Msg, "condition must be bool") {
				found = true
			}
		}
		if !found {
			t.Fatalf("a non-bool break/continue-if condition must be rejected, got: %v", diags)
		}
	}
}
