package build

import "testing"

// RUN-based tests for the `ascii` stdlib module (src/stdlib/ascii.zg) — single-byte
// ASCII classification reached with `import "ascii"`. Each program is compiled to C,
// linked under ASan+UBSan, run, and asserted for exact stdout (runProgramRT). The module
// allocates nothing, so there is no heap balance to check — ASan/UBSan cover the reads.

// TestAsciiPredicates walks a sample of representative bytes and prints each predicate,
// pinning the letter/digit/hex/space boundaries (including that 'a'/'f' are hex digits
// but 'Z'/'g' are not, and that '_' and '#' are neither alpha nor alnum).
func TestAsciiPredicates(t *testing.T) {
	got := runProgramRT(t, "import \"ascii\"\n"+
		"fn show(b: byte) {\n"+
		"\tprint ascii.is_digit(b)\n"+
		"\tprint ascii.is_alpha(b)\n"+
		"\tprint ascii.is_alnum(b)\n"+
		"\tprint ascii.is_space(b)\n"+
		"\tprint ascii.is_upper(b)\n"+
		"\tprint ascii.is_lower(b)\n"+
		"\tprint ascii.is_hex_digit(b)\n"+
		"}\n"+
		"fn main() {\n"+
		"\tshow(byte(57))\n"+ // '9': digit, alnum, hex
		"\tshow(byte(97))\n"+ // 'a': alpha, lower, alnum, hex
		"\tshow(byte(90))\n"+ // 'Z': alpha, upper, alnum
		"\tshow(byte(32))\n"+ // ' ': space
		"\tshow(byte(95))\n"+ // '_': none
		"}\n")
	want := "" +
		"true\nfalse\ntrue\nfalse\nfalse\nfalse\ntrue\n" + // '9'
		"false\ntrue\ntrue\nfalse\nfalse\ntrue\ntrue\n" + // 'a'
		"false\ntrue\ntrue\nfalse\ntrue\nfalse\nfalse\n" + // 'Z'
		"false\nfalse\nfalse\ntrue\nfalse\nfalse\nfalse\n" + // ' '
		"false\nfalse\nfalse\nfalse\nfalse\nfalse\nfalse\n" // '_'
	if got != want {
		t.Fatalf("ascii predicates: got %q, want %q", got, want)
	}
}

// TestAsciiSpaceSet pins the full C-isspace whitespace set (tab..CR and space) and that
// a non-whitespace control byte (NUL) is excluded.
func TestAsciiSpaceSet(t *testing.T) {
	got := runProgramRT(t, "import \"ascii\"\n"+
		"fn main() {\n"+
		"\tprint ascii.is_space(byte(9))\n"+ // tab
		"\tprint ascii.is_space(byte(10))\n"+ // LF
		"\tprint ascii.is_space(byte(11))\n"+ // VT
		"\tprint ascii.is_space(byte(12))\n"+ // FF
		"\tprint ascii.is_space(byte(13))\n"+ // CR
		"\tprint ascii.is_space(byte(32))\n"+ // space
		"\tprint ascii.is_space(byte(0))\n"+ // NUL — not space
		"\tprint ascii.is_space(byte(8))\n"+ // BS — not space
		"}\n")
	if want := "true\ntrue\ntrue\ntrue\ntrue\ntrue\nfalse\nfalse\n"; got != want {
		t.Fatalf("ascii is_space set: got %q, want %q", got, want)
	}
}

// TestAsciiFoldAndValue covers the byte case folds (with pass-through of a non-letter)
// and the digit_val / hex_val readouts including their -1 sentinels.
func TestAsciiFoldAndValue(t *testing.T) {
	got := runProgramRT(t, "import \"ascii\"\n"+
		"fn main() {\n"+
		"\tprint int(ascii.fold_upper(byte(97)))\n"+ // 'a' -> 'A' = 65
		"\tprint int(ascii.fold_lower(byte(90)))\n"+ // 'Z' -> 'z' = 122
		"\tprint int(ascii.fold_upper(byte(35)))\n"+ // '#' unchanged = 35
		"\tprint ascii.digit_val(byte(55))\n"+ // '7' -> 7
		"\tprint ascii.digit_val(byte(65))\n"+ // 'A' -> -1
		"\tprint ascii.hex_val(byte(97))\n"+ // 'a' -> 10
		"\tprint ascii.hex_val(byte(70))\n"+ // 'F' -> 15
		"\tprint ascii.hex_val(byte(71))\n"+ // 'G' -> -1
		"}\n")
	if want := "65\n122\n35\n7\n-1\n10\n15\n-1\n"; got != want {
		t.Fatalf("ascii fold/value: got %q, want %q", got, want)
	}
}
