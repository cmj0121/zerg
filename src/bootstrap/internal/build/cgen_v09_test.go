package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// v0.9 Unit 1 — cgen surface for `-> never` fn-decls.
//
// The C type for `never` is `void`; the fn-decl carries
// `__attribute__((noreturn))` so the C compiler accepts the absence of a
// return statement and the call-site does not need a phantom value at the
// boundary. Unit 1 only stages the attribute; Unit 3 lands the os.exit fn
// that exercises it through a real call path.

// emitForTest writes src to a TempDir as main.zg, runs the public emit
// pipeline, and returns the merged C source. Skips when the lex/parse/
// typeck pipeline rejects (e.g. missing toolchain features in the
// requested-version comment).
func emitForTest(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.zg")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	out, err := emitFromFile(t, path)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	return out
}

func TestV09CgenNeverFnEmitsNoreturn(t *testing.T) {
	// A pure-Zerg `-> never` fn (an infinite loop, the only way to inhabit
	// the contract before Unit 3) emits with the noreturn attribute. We
	// don't compile/run because the only producer is an infinite loop;
	// the source-level assertion is enough at Unit 1.
	src := "fn diverge() -> never { for { } }\n" +
		"print 1\n"
	out := emitForTest(t, src)
	// The fn signature must carry the attribute. Match permissively
	// (any whitespace) so a future formatter change doesn't break us.
	if !strings.Contains(out, "__attribute__((noreturn))") {
		t.Errorf("emitted C does not contain __attribute__((noreturn))\n--- emitted ---\n%s", out)
	}
	// And the prefix must precede the diverge fn name (mangled).
	idx := strings.Index(out, "__attribute__((noreturn))")
	if idx < 0 {
		t.FailNow()
	}
	tail := out[idx:]
	if !strings.Contains(tail, "diverge") {
		t.Errorf("noreturn attribute does not precede diverge fn\n--- tail ---\n%s", tail)
	}
}

func TestV09CgenNeverFnReturnsVoid(t *testing.T) {
	// `never` lowers to C void at the return-position in the signature.
	src := "fn diverge() -> never { for { } }\n" +
		"print 1\n"
	out := emitForTest(t, src)
	// Look for the substring "noreturn)) void " in the diverge sig.
	if !strings.Contains(out, "noreturn)) void ") {
		t.Errorf("emitted C does not lower never to void\n--- emitted ---\n%s", out)
	}
}

func TestV09CgenNonNeverFnHasNoNoreturn(t *testing.T) {
	// A v0.0-style program without `-> never` must NOT receive the
	// attribute on any user fn. The runtime emits a `noreturn` helper
	// (zerg_not_implemented) that is unrelated; we narrow the check to
	// "noreturn)) void " followed by a user-fn-mangle prefix `z_main_`
	// to confirm none of the user fns picked up the attribute.
	src := "fn add(a: int, b: int) -> int { return a + b }\n" +
		"print add(1, 2)\n"
	out := emitForTest(t, src)
	if strings.Contains(out, "noreturn)) void z_main_") ||
		strings.Contains(out, "noreturn)) int64_t z_main_") {
		t.Errorf("non-never program leaked noreturn attribute on a user fn\n--- emitted ---\n%s", out)
	}
}
