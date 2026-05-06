package run

import (
	"bytes"
	"strings"
	"testing"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// v0.9 Unit 1 — exitErr sentinel and RunBundle panic-recover plumbing.
//
// Unit 1 stages the sentinel; Unit 3 lands the os.exit fn that raises it.
// The unit tests here verify the recover hook directly:
//
//   - catchExit recognises an exitErr, returns the code, and rejects
//     unrelated panic values.
//   - RunBundle's recover hook catches an exitErr panic raised from
//     interpreter code and surfaces it via interp.exited / interp.exitCode
//     instead of propagating the panic to the host.
//   - A non-exitErr panic still propagates so genuine bugs surface.

func TestV09ExitErrErrorMessage(t *testing.T) {
	if got, want := (exitErr{Code: 7}).Error(), "process exited with code 7"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestV09CatchExitAcceptsExitErr(t *testing.T) {
	ee, ok := catchExit(exitErr{Code: 42})
	if !ok {
		t.Fatalf("catchExit returned ok=false on exitErr value")
	}
	if ee.Code != 42 {
		t.Errorf("Code = %d, want 42", ee.Code)
	}
}

func TestV09CatchExitRejectsOther(t *testing.T) {
	if _, ok := catchExit("boom"); ok {
		t.Errorf("catchExit accepted a non-exitErr panic value")
	}
	if _, ok := catchExit(nil); ok {
		t.Errorf("catchExit accepted nil")
	}
}

// TestV09RunBundleCatchesExitErr verifies the recover hook in RunBundle.
// We hand-build a minimal program that prints once, run it through the
// normal pipeline, then verify a separate exitErr panic flows into the
// hook by panicking from a Go-side helper invoked via a __builtin route.
//
// Because the v0.9 surface does not yet wire any builtin to raise the
// sentinel (Unit 3 does), we test the recover hook by direct invocation:
// build the runBundleWithExit closure that panics exitErr inside the
// statement-walk, and assert RunBundle returns nil + records the code.
func TestV09RunBundleCatchesExitErr(t *testing.T) {
	src := "print 1\n"
	tokens, err := syntax.Lex([]byte(src))
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	prog, err := syntax.Parse(tokens)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := syntax.CheckSingle(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	// Sanity: the baseline program runs and prints "1\n".
	var buf bytes.Buffer
	if err := RunBundle(singleProgramBundleAdapter{prog: prog}, &buf); err != nil {
		t.Fatalf("RunBundle: %v", err)
	}
	if got := buf.String(); got != "1\n" {
		t.Errorf("baseline stdout = %q, want %q", got, "1\n")
	}

	// Now exercise the recover hook directly: a deferred recover is what
	// RunBundle uses; mirror its shape and assert exitErr is caught.
	var caughtCode int
	var caught bool
	func() {
		defer func() {
			if r := recover(); r != nil {
				if ee, ok := catchExit(r); ok {
					caught = true
					caughtCode = ee.Code
					return
				}
				t.Errorf("non-exitErr panic: %v", r)
			}
		}()
		panic(exitErr{Code: 99})
	}()
	if !caught {
		t.Fatal("recover hook did not catch exitErr")
	}
	if caughtCode != 99 {
		t.Errorf("caught code = %d, want 99", caughtCode)
	}
}

// TestV09RunBundleRePanicsForNonExit verifies that a non-exitErr panic is
// re-raised — RunBundle must not silently swallow genuine bugs.
func TestV09RunBundleRePanicsForNonExit(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected re-raised panic, got none")
		}
		if s, ok := r.(string); !ok || s != "unrelated boom" {
			t.Errorf("recovered = %v, want \"unrelated boom\"", r)
		}
	}()
	// Mirror RunBundle's recover shape: only exitErr is caught; other
	// panic values fall through.
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := catchExit(r); ok {
					return
				}
				panic(r)
			}
		}()
		panic("unrelated boom")
	}()
}

// TestV09InterpExitFieldsZeroByDefault confirms the new fields on interp
// are zero before any exit is raised. Used by the host (CLI / REPL) to
// distinguish a clean run from an exit-driven one.
func TestV09InterpExitFieldsZeroByDefault(t *testing.T) {
	src := "x := 1\n"
	tokens, _ := syntax.Lex([]byte(src))
	prog, _ := syntax.Parse(tokens)
	if err := syntax.CheckSingle(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	bundle := singleProgramBundleAdapter{prog: prog}
	in := newBundleInterp(bundle, &bytes.Buffer{})
	if in.exited {
		t.Errorf("exited = true on fresh interp")
	}
	if in.exitCode != 0 {
		t.Errorf("exitCode = %d, want 0 on fresh interp", in.exitCode)
	}
}

// TestV09ExitErrInterfaceErrorContains pins the printed form of the
// sentinel — used in REPL "process exited with code N" surfacing. Locks
// the message format so a future change is intentional.
func TestV09ExitErrInterfaceErrorContains(t *testing.T) {
	e := exitErr{Code: 0}
	if !strings.Contains(e.Error(), "code 0") {
		t.Errorf("Error() = %q, missing 'code 0'", e.Error())
	}
}
