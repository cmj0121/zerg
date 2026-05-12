package run

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// v0.13 Unit 2 — interpreter rejection for `asm { … }`.
//
// The interpreter cannot execute raw machine code. PLAN pin 6 fixes the
// diagnostic shape: `inline asm requires 'zerg build' (interpreter cannot
// execute machine code)`. The reject anchors on the `asm` keyword so the
// user lands on the exact construct that's keeping `zerg run` from running.
// ---------------------------------------------------------------------------

func TestRunRejectsAsmBlock(t *testing.T) {
	src := "# requires: v0.13\nasm {\n\tmov x0, #0\n\tsvc #0x80\n}\n"
	_, err := runSrc(t, src)
	if err == nil {
		t.Fatalf("Run unexpectedly succeeded; expected interpreter rejection")
	}
	want := "inline asm requires 'zerg build' (interpreter cannot execute machine code)"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not contain %q", err.Error(), want)
	}
}

func TestRunRejectsAsmBlockEvenIfEmpty(t *testing.T) {
	// An empty body is admitted by the parser; the interpreter rejects
	// anyway because the rule is "the construct doesn't run", not "the
	// construct has dangerous content".
	src := "# requires: v0.13\nasm {}\n"
	_, err := runSrc(t, src)
	if err == nil {
		t.Fatalf("Run unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "inline asm requires 'zerg build'") {
		t.Errorf("error %q does not contain the expected prefix", err.Error())
	}
}
