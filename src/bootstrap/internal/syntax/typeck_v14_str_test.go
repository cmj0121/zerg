package syntax

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// v0.14 str ↔ list[byte] bridge primitives.
//
// Three new builtins: `len(str)` (extended from v0.2 list-only),
// `bytes(s: str) -> list[byte]`, and `to_str(buf: list[byte]) -> str`.
// All three admit method-form syntax (`s.len()`, `s.bytes()`,
// `buf.to_str()`) via the same dispatch path that handles list builtins.
// ---------------------------------------------------------------------------

// `len(s)` on a str returns int (byte count, v0.14 reading). The
// previous-version rejection (TestCheckLenOnStrRejected) is gone — see
// typeck_v02_test.go's TestCheckLenOnStrOK for the positive version.
func TestCheckStrLenMethodOK(t *testing.T) {
	prog := checkSrc(t, "s := \"hello\"\nn := s.len()\n")
	let := prog.Statements[1].(*LetStmt)
	if let.Value.Type() != TInt() {
		t.Fatalf("Type = %s, want int", let.Value.Type())
	}
}

func TestCheckStrBytesMethodOK(t *testing.T) {
	// s.bytes() returns list[byte]. The synthetic CallExpr dispatch
	// stamps the method-call expression with the registry return type
	// (list[byte]); the method-form just inverts the call shape.
	prog := checkSrc(t, "s := \"hello\"\nb := s.bytes()\n")
	let := prog.Statements[1].(*LetStmt)
	got := let.Value.Type()
	if got == nil || got.Kind != TypeList || got.Element == nil || got.Element.Kind != TypeByte {
		t.Fatalf("Type = %v, want list[byte]", got)
	}
}

func TestCheckListByteToStrMethodOK(t *testing.T) {
	// buf.to_str() on a list[byte] returns str.
	prog := checkSrc(t, "buf: list[byte] = ['h', 'i']\ns := buf.to_str()\n")
	let := prog.Statements[1].(*LetStmt)
	if let.Value.Type() != TStr() {
		t.Fatalf("Type = %s, want str", let.Value.Type())
	}
}

// list[int].to_str() rejects: the cgen has no contract for non-byte
// element types and the registry pins tByte placeholder element.
func TestCheckListIntToStrRejected(t *testing.T) {
	src := "xs := [1, 2, 3]\ns := xs.to_str()\n"
	checkErr(t, src, "list[byte]")
}

// `bytes` is a reserved builtin name — user fn decls collide.
func TestCheckUserRedefineBytesRejected(t *testing.T) {
	src := "fn bytes() -> int {\nreturn 0\n}\n"
	checkErr(t, src, "cannot redefine built-in 'bytes'")
}

// Same reservation rule for to_str.
func TestCheckUserRedefineToStrRejected(t *testing.T) {
	src := "fn to_str() -> int {\nreturn 0\n}\n"
	checkErr(t, src, "cannot redefine built-in 'to_str'")
}

// Free-function form of bytes/to_str works too (parallel to len /
// push / clone, which all admit both `f(xs)` and `xs.f()` shapes).
func TestCheckBytesFreeCallOK(t *testing.T) {
	prog := checkSrc(t, "s := \"hi\"\nb := bytes(s)\n")
	let := prog.Statements[1].(*LetStmt)
	got := let.Value.Type()
	if got == nil || got.Kind != TypeList || got.Element == nil || got.Element.Kind != TypeByte {
		t.Fatalf("Type = %v, want list[byte]", got)
	}
}

func TestCheckToStrFreeCallOK(t *testing.T) {
	prog := checkSrc(t, "buf: list[byte] = ['h']\ns := to_str(buf)\n")
	let := prog.Statements[1].(*LetStmt)
	if let.Value.Type() != TStr() {
		t.Fatalf("Type = %s, want str", let.Value.Type())
	}
}

// Wrong-type rejection through the free-call shape mirrors the method-
// form rule: bytes(int) and to_str(int) both reject. The diagnostic
// wording comes from the generic param-matching loop in checkCall, not
// from a custom message.
func TestCheckBytesOnIntRejected(t *testing.T) {
	checkErr(t, "x := 5\nb := bytes(x)\n", "expected str")
}

func TestCheckToStrOnStrRejected(t *testing.T) {
	// Mirror image: to_str takes list[byte], not str. A user reaching
	// for "str → str identity" would write s itself; passing a str into
	// to_str surfaces the type mismatch.
	checkErr(t, "s := \"hi\"\nb := to_str(s)\n", "expected list[byte]")
}

// Method-form on a non-receiver-capable type rejects through the
// dispatchMethodCall fall-through. Pins that we did NOT accidentally
// admit `int.bytes()` or `int.to_str()` by lifting the type-check.
func TestCheckIntBytesMethodRejected(t *testing.T) {
	src := "x := 5\nb := x.bytes()\n"
	err := strings.ToLower(checkErrCapture(t, src))
	if !strings.Contains(err, "method") {
		t.Errorf("error = %q, want diagnostic mentioning method", err)
	}
}

// checkErrCapture runs Check on src and returns the error message text.
// Used by tests that want a substring search without the strict-fatal
// behaviour of checkErr (which fails the test on a missing substring).
func checkErrCapture(t *testing.T, src string) string {
	t.Helper()
	tokens, err := Lex([]byte(src))
	if err != nil {
		t.Fatalf("Lex(%q): %v", src, err)
	}
	prog, err := Parse(tokens)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	err = Check(prog)
	if err == nil {
		t.Fatalf("Check(%q) succeeded, expected error", src)
	}
	return err.Error()
}
