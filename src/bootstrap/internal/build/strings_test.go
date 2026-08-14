package build

import (
	"strings"
	"testing"
)

// RUN-based tests for the `strings` stdlib module (src/stdlib/strings.zg) — a
// pure-Zerg text utility over the built-in str, reached with `import "strings"`.
// Each program is compiled to C, linked against the runtime under ASan+UBSan, run,
// and asserted for exact stdout AND alloc/free balance (runProgramRTBalanced), so a
// leak in the str-building loops fails too.

// TestStringsPredicates covers has_prefix / has_suffix / contains / index_of, including
// the empty-needle edges (always a prefix/suffix, found at offset 0).
func TestStringsPredicates(t *testing.T) {
	got := runProgramRTBalanced(t, "import \"strings\"\n"+
		"fn main() {\n"+
		"\tprint strings.has_prefix(\"hello\", \"he\")\n"+ // true
		"\tprint strings.has_prefix(\"hello\", \"lo\")\n"+ // false
		"\tprint strings.has_prefix(\"hello\", \"\")\n"+ // true
		"\tprint strings.has_suffix(\"hello\", \"lo\")\n"+ // true
		"\tprint strings.has_suffix(\"hello\", \"\")\n"+ // true
		"\tprint strings.contains(\"banana\", \"nan\")\n"+ // true
		"\tprint strings.index_of(\"hello world\", \"world\")\n"+ // 6
		"\tprint strings.index_of(\"hello\", \"z\")\n"+ // -1
		"\tprint strings.index_of(\"abc\", \"\")\n"+ // 0
		"}\n")
	if want := "true\nfalse\ntrue\ntrue\ntrue\ntrue\n6\n-1\n0\n"; got != want {
		t.Fatalf("strings predicates: got %q, want %q", got, want)
	}
}

// TestStringsSplitJoin covers the split/join inverse pair: interior empty pieces,
// leading/trailing separators, a multi-byte separator, and the no-separator case.
func TestStringsSplitJoin(t *testing.T) {
	got := runProgramRTBalanced(t, "import \"strings\"\n"+
		"fn main() {\n"+
		"\tprint strings.join(strings.split(\"a,b,,c\", \",\"), \"|\")\n"+ // a|b||c
		"\tprint strings.join(strings.split(\",a,\", \",\"), \"|\")\n"+ // |a|
		"\tprint strings.join(strings.split(\"aXXbXXc\", \"XX\"), \"|\")\n"+ // a|b|c
		"\tprint strings.join(strings.split(\"nosep\", \",\"), \"|\")\n"+ // nosep
		"}\n")
	if want := "a|b||c\n|a|\na|b|c\nnosep\n"; got != want {
		t.Fatalf("strings split/join: got %q, want %q", got, want)
	}
}

// TestStringsTransform covers repeat / trim / to_upper / to_lower, with ASCII-only case
// folding passing a non-ASCII byte (é) through unchanged.
func TestStringsTransform(t *testing.T) {
	got := runProgramRTBalanced(t, "import \"strings\"\n"+
		"fn main() {\n"+
		"\tprint strings.repeat(\"ab\", 3)\n"+ // ababab
		"\tprint strings.repeat(\"x\", 0)\n"+ // (empty)
		"\tprint \"[\" + strings.trim(\"  hi \\t\\n\") + \"]\"\n"+ // [hi]
		"\tprint strings.to_upper(\"café!\")\n"+ // CAFé!
		"\tprint strings.to_lower(\"HeLLo\")\n"+ // hello
		"}\n")
	if want := "ababab\n\n[hi]\nCAFé!\nhello\n"; got != want {
		t.Fatalf("strings transform: got %q, want %q", got, want)
	}
}

// TestStringsSplitEmptySepAborts pins that an empty separator is a loud ValueError
// (abort), not a silent surprise.
//
// The KIND is half the assertion, and it is the half that was missing: the test said
// ValueError in its name and checked only the text, while the raise threw a bare `str`
// and the abort line said `strings.split: empty separator` with no kind in front of it.
func TestStringsSplitEmptySepAborts(t *testing.T) {
	out := runProgramRTAbort(t, "import \"strings\"\n"+
		"fn main() {\n"+
		"\tprint strings.join(strings.split(\"abc\", \"\"), \"|\")\n"+
		"}\n")
	if !strings.Contains(out, "ValueError: strings.split: empty separator") {
		t.Fatalf("expected empty-separator abort, got %q", out)
	}
}

// TestStringsCountReplace covers count (non-overlapping) and replace (all occurrences,
// including a no-match pass-through).
func TestStringsCountReplace(t *testing.T) {
	got := runProgramRTBalanced(t, "import \"strings\"\n"+
		"fn main() {\n"+
		"\tprint strings.count(\"banana\", \"a\")\n"+ // 3
		"\tprint strings.count(\"aaaa\", \"aa\")\n"+ // 2 (non-overlapping)
		"\tprint strings.count(\"abc\", \"x\")\n"+ // 0
		"\tprint strings.replace(\"a.b.c\", \".\", \"/\")\n"+ // a/b/c
		"\tprint strings.replace(\"hello\", \"l\", \"L\")\n"+ // heLLo
		"\tprint strings.replace(\"xyz\", \"q\", \"Q\")\n"+ // xyz
		"}\n")
	if want := "3\n2\n0\na/b/c\nheLLo\nxyz\n"; got != want {
		t.Fatalf("strings count/replace: got %q, want %q", got, want)
	}
}

// TestStringsTrimAffixFields covers trim_prefix / trim_suffix (with the no-match
// pass-through) and fields (splitting around whitespace runs, no empty pieces).
func TestStringsTrimAffixFields(t *testing.T) {
	got := runProgramRTBalanced(t, "import \"strings\"\n"+
		"fn main() {\n"+
		"\tprint \"[\" + strings.trim_prefix(\"foobar\", \"foo\") + \"]\"\n"+ // [bar]
		"\tprint \"[\" + strings.trim_prefix(\"foobar\", \"xxx\") + \"]\"\n"+ // [foobar]
		"\tprint \"[\" + strings.trim_suffix(\"foobar\", \"bar\") + \"]\"\n"+ // [foo]
		"\tprint strings.join(strings.fields(\"  the   quick brown  \"), \"|\")\n"+ // the|quick|brown
		"\tprint strings.fields(\"   \").len()\n"+ // 0
		"}\n")
	if want := "[bar]\n[foobar]\n[foo]\nthe|quick|brown\n0\n"; got != want {
		t.Fatalf("strings trim/fields: got %q, want %q", got, want)
	}
}
