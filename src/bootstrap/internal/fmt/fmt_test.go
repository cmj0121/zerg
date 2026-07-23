package fmt

import (
	"strings"
	"testing"
)

// mustRoundTrip runs the oracle on src and returns the canonical text.
func mustRoundTrip(t *testing.T, src string) string {
	t.Helper()
	out, err := RoundTrip(src)
	if err != nil {
		t.Fatalf("round-trip failed:\n--- source ---\n%s\n--- error ---\n%v", src, err)
	}
	return out
}

// TestCanonicalForm checks the deterministic layout rules: one statement per
// line, canonical spacing, tab indentation, and precedence-preserving parens.
func TestCanonicalForm(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "spacing and indent",
			src:  "fn main()  {\n    x:=1+2*3\n    print   x\n}",
			want: "fn main() {\n\tx := 1 + 2 * 3\n\tprint x\n}\n",
		},
		{
			name: "semicolons become newlines",
			src:  "fn main() { a := 1; b := 2; print a + b }",
			want: "fn main() {\n\ta := 1\n\tb := 2\n\tprint a + b\n}\n",
		},
		{
			name: "parens survive by precedence",
			src:  "fn main() {\n\tprint (1 + 2) * 3\n\tprint -(4 + 5)\n\tprint 1 - (2 - 3)\n}",
			want: "fn main() {\n\tprint (1 + 2) * 3\n\tprint -(4 + 5)\n\tprint 1 - (2 - 3)\n}\n",
		},
		{
			name: "redundant parens are dropped",
			src:  "fn main() {\n\tprint (1) + ((2 * 3))\n}",
			want: "fn main() {\n\tprint 1 + 2 * 3\n}\n",
		},
		{
			name: "signature spacing",
			src:  "fn add( a :int , b : int )->int {\n\treturn a + b\n}",
			want: "fn add(a: int, b: int) -> int {\n\treturn a + b\n}\n",
		},
		{
			name: "typed and mutable bindings",
			src:  "fn main() {\n\tmut n:=0\n\tr : float = 2\n\tconst k := 10\n\tn = n + 1\n}",
			want: "fn main() {\n\tmut n := 0\n\tr: float = 2\n\tconst k := 10\n\tn = n + 1\n}\n",
		},
		{
			name: "if else chain",
			src:  "fn main() {\n\tif true { nop } else if false { nop } else { nop }\n}",
			want: "fn main() {\n\tif true {\n\t\tnop\n\t} else if false {\n\t\tnop\n\t} else {\n\t\tnop\n\t}\n}\n",
		},
		{
			name: "match arms align to single spaces",
			src:  "fn f(n: int) -> int {\n\treturn match n {\n\t\t0          => 0\n\t\tx if x < 0 => -1\n\t\t_          => 1\n\t}\n}",
			want: "fn f(n: int) -> int {\n\treturn match n {\n\t\t0 => 0\n\t\tx if x < 0 => -1\n\t\t_ => 1\n\t}\n}\n",
		},
		{
			name: "return variants",
			src:  "fn f(n: int) -> int {\n\treturn if n < 0\n\treturn n if n > 10\n\treturn 0\n}",
			want: "fn f(n: int) -> int {\n\treturn if n < 0\n\treturn n if n > 10\n\treturn 0\n}\n",
		},
		{
			name: "float surface preserved verbatim",
			src:  "fn main() {\n\tprint 2.0\n\tprint 6.022e23\n}",
			want: "fn main() {\n\tprint 2.0\n\tprint 6.022e23\n}\n",
		},
		{
			name: "integer base and grouping preserved",
			src:  "fn main() {\n\tprint 0xFF + 1_000\n\tprint 0b1100 & 0b1010\n\tprint 0o755\n}",
			want: "fn main() {\n\tprint 0xFF + 1_000\n\tprint 0b1100 & 0b1010\n\tprint 0o755\n}\n",
		},
		{
			name: "pub fn keyword preserved",
			src:  "pub fn add(a: int, b: int) -> int {\n\treturn a + b\n}",
			want: "pub fn add(a: int, b: int) -> int {\n\treturn a + b\n}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustRoundTrip(t, tc.src); got != tc.want {
				t.Fatalf("canonical form mismatch\n--- got ---\n%q\n--- want ---\n%q", got, tc.want)
			}
		})
	}
}

// TestTriviaSurvival proves the DESIGN §2 cases survive the round-trip: a
// comment line between two statements, a same-line trailing comment, a '##' doc
// block above a fn, and a collapsed blank-line run.
func TestTriviaSurvival(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "comment between two statements",
			src:  "fn main() {\n\ta := 1\n\t# a note between statements\n\tb := 2\n\tprint a + b\n}",
			want: "fn main() {\n\ta := 1\n\t# a note between statements\n\tb := 2\n\tprint a + b\n}\n",
		},
		{
			name: "trailing same-line comment",
			src:  "fn main() {\n\tx := 1      # inline note\n\tprint x\n}",
			want: "fn main() {\n\tx := 1 # inline note\n\tprint x\n}\n",
		},
		{
			name: "doc block above a fn",
			src:  "## Greets the world.\n## Twice, even.\nfn main() {\n\tnop\n}",
			want: "## Greets the world.\n## Twice, even.\nfn main() {\n\tnop\n}\n",
		},
		{
			name: "blank-line run collapses to one",
			src:  "fn main() {\n\ta := 1\n\n\n\n\tb := 2\n\tprint a + b\n}",
			want: "fn main() {\n\ta := 1\n\n\tb := 2\n\tprint a + b\n}\n",
		},
		{
			name: "header comment then blank then fn",
			src:  "# a file header\n\nfn main() {\n\tnop\n}",
			want: "# a file header\n\nfn main() {\n\tnop\n}\n",
		},
		{
			name: "dangling comment before closing brace",
			src:  "fn main() {\n\tnop\n\t# closing thoughts\n}",
			want: "fn main() {\n\tnop\n\t# closing thoughts\n}\n",
		},
		{
			name: "comment between declarations",
			src:  "fn a() {\n\tnop\n}\n\n# a separator\nfn b() {\n\tnop\n}",
			want: "fn a() {\n\tnop\n}\n\n# a separator\nfn b() {\n\tnop\n}\n",
		},
		{
			name: "match arm lead and trail comments",
			src:  "fn f(n: int) -> int {\n\treturn match n {\n\t\t# zero case\n\t\t0 => 0    # nothing\n\t\t_ => 1\n\t}\n}",
			want: "fn f(n: int) -> int {\n\treturn match n {\n\t\t# zero case\n\t\t0 => 0 # nothing\n\t\t_ => 1\n\t}\n}\n",
		},
		{
			name: "comment at end of file",
			src:  "fn main() {\n\tnop\n}\n# the end",
			want: "fn main() {\n\tnop\n}\n# the end\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustRoundTrip(t, tc.src); got != tc.want {
				t.Fatalf("trivia mismatch\n--- got ---\n%q\n--- want ---\n%q", got, tc.want)
			}
		})
	}
}

// TestIntegerBasesVerbatim pins FIX 1: a formatter must never collapse an
// author's base or grouping to decimal.
func TestIntegerBasesVerbatim(t *testing.T) {
	for _, lexeme := range []string{"0xFF", "0b1100", "0b1010", "0o755", "1_000", "0xDE_AD", "42"} {
		src := "fn main() {\n\tprint " + lexeme + "\n}"
		out := mustRoundTrip(t, src)
		if !strings.Contains(out, lexeme) {
			t.Errorf("integer literal %q was not reprinted verbatim:\n%s", lexeme, out)
		}
	}
}

// TestFloatSurfaceVerbatim pins the float-literal surface: a formatter must never
// rewrite the author's exponent form or '_' grouping to a canonical float (the
// same source-preservation rule as integer bases).
func TestFloatSurfaceVerbatim(t *testing.T) {
	for _, lexeme := range []string{"1.5e3", "1_000.5", "1e10", "100000000.0", "6.022e23", "3.14"} {
		src := "fn main() {\n\tprint " + lexeme + "\n}"
		out := mustRoundTrip(t, src)
		if !strings.Contains(out, lexeme) {
			t.Errorf("float literal %q was not reprinted verbatim:\n%s", lexeme, out)
		}
	}
}

// TestInteriorCommentRejected pins FIX 3: a comment inside an expression is not
// preserved in iteration 1, so it must be loudly rejected — never silently
// dropped. RoundTrip fails and Format returns a conservation diagnostic.
func TestInteriorCommentRejected(t *testing.T) {
	src := "fn main() {\n\tprint 1 +\n\t# interior comment\n\t2\n}"
	// the source parses cleanly (the comment is valid trivia)...
	if _, diags := Format(src); len(diags) == 0 {
		t.Fatalf("Format must refuse a source whose interior comment would be lost")
	}
	// ...but the oracle and the formatter both refuse to drop it silently.
	if _, err := RoundTrip(src); err == nil {
		t.Fatalf("RoundTrip must fail when a comment is attached to no node")
	}
	out, diags := Format(src)
	if out != "" {
		t.Fatalf("rejected source must produce no output, got %q", out)
	}
	if len(diags) == 0 || !strings.Contains(diags[0].Error(), "would be lost") {
		t.Fatalf("expected a 'would be lost' diagnostic, got %v", diags)
	}
}

// TestConservationPassesForAttachedComments makes sure the conservation check
// does not fire on comments that ARE attached (statement-level and trailing).
func TestConservationPassesForAttachedComments(t *testing.T) {
	src := "fn main() {\n\t# lead\n\tx := 1 # trail\n\tprint x\n}"
	if _, err := RoundTrip(src); err != nil {
		t.Fatalf("attached comments must pass conservation: %v", err)
	}
}

// TestFormatReportsDiagnostics ensures broken source is refused, not mangled.
func TestFormatReportsDiagnostics(t *testing.T) {
	out, diags := Format("fn f( {")
	if len(diags) == 0 {
		t.Fatalf("expected diagnostics for broken source")
	}
	if out != "" {
		t.Fatalf("broken source must not produce output, got %q", out)
	}
	if _, err := RoundTrip("fn f( {"); err == nil {
		t.Fatalf("RoundTrip should fail on broken source")
	}
}

// TestQuote covers the string re-quoting rules.
func TestQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", `"plain"`},
		{"a\tb\nc", `"a\tb\nc"`},
		{`quote " and \`, `"quote \" and \\"`},
		{"\x01", `"\u{1}"`},
		{"héllo ☺", `"héllo ☺"`},
	}
	for _, tc := range cases {
		if got := quote(tc.in); got != tc.want {
			t.Errorf("quote(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
	// each quoted form must survive its own round-trip
	for _, tc := range cases {
		src := "fn main() {\n\tprint " + tc.want + "\n}"
		mustRoundTrip(t, src)
	}
}

// TestFloatText keeps float literals lexing as floats.
func TestFloatText(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{2, "2.0"},
		{3.14, "3.14"},
		{6.022e23, "6.022e+23"},
		{0.5, "0.5"},
	}
	for _, tc := range cases {
		if got := floatText(tc.in); got != tc.want {
			t.Errorf("floatText(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestIdempotenceOnCanonicalInput formats already-canonical text and demands
// byte identity on the first pass.
func TestIdempotenceOnCanonicalInput(t *testing.T) {
	canon := "# header\nfn main() {\n\tx := 1 # inline\n\n\tprint x\n}\n"
	got, diags := Format(canon)
	if len(diags) != 0 {
		t.Fatalf("canonical text should parse cleanly: %v", diags)
	}
	if got != canon {
		t.Fatalf("canonical text must be a fixpoint\n--- got ---\n%q\n--- want ---\n%q", got, canon)
	}
}

// TestPrintNeverEmitsSemicolons is the '; is never a separator' rule.
func TestPrintNeverEmitsSemicolons(t *testing.T) {
	out := mustRoundTrip(t, "fn main() { a := 1; b := 2; print a + b }")
	if strings.Contains(out, ";") {
		t.Fatalf("canonical output must not contain ';':\n%s", out)
	}
}
