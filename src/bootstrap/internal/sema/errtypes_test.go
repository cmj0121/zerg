package sema

import "testing"

// TestBuiltinErrorConstructors covers the fixed, built-in error taxonomy (docs/code/errors.md,
// GRAMMAR group 8): a named kind is a constructor `Name(msg) -> Err` and requires one str
// message.
func TestBuiltinErrorConstructors(t *testing.T) {
	t.Run("a kind constructs an Err from a message", func(t *testing.T) {
		wantOK(t, "fn f() -> Err {\n  return ValueError(\"bad\")\n}")
	})
	t.Run("every documented kind is nameable", func(t *testing.T) {
		for _, k := range []string{
			"ValueError", "OverflowError", "IOError", "EncodingError", "IndexError", "KeyError",
			"DeadlockError", "SendOnClosedError",
		} {
			wantOK(t, "fn f() -> Err {\n  return "+k+"(\"m\")\n}")
		}
	})
	t.Run("the message is required and a str", func(t *testing.T) {
		wantErr(t, "fn f() -> Err {\n  return ValueError(1)\n}", "cannot use")
	})
	t.Run("the sentinel kind is not a constructor", func(t *testing.T) {
		wantErr(t, "fn f() -> Err {\n  return StopIteration(\"m\")\n}", "StopIteration")
	})
}

// TestErrKindTableMirrorsTheRuntime pins the two questions the table answers separately.
// The integer is the whole contract with the runtime: it is what `zrt_err.kind` holds, so
// a value that drifts from src/runtime/csrc/zergrt.h ZRT_ERR_* silently makes a runtime
// abort and a hand-written `raise` of the same name two different errors. And the second
// column says which names a program may BUILD: every kind is an `is` target, but
// StopIteration is the runtime's end-of-stream sentinel, and a channel tells a clean close
// from a crash by that kind alone — a forgeable one would let a crash pass for a clean end.
func TestErrKindTableMirrorsTheRuntime(t *testing.T) {
	cases := []struct {
		name string
		kind int
		ctor bool
	}{
		{"ValueError", 1, true},
		{"OverflowError", 2, true},
		{"IOError", 3, true},
		{"EncodingError", 4, true},
		{"IndexError", 5, true},
		{"KeyError", 6, true},
		{"DeadlockError", 7, true},
		{"SendOnClosedError", 8, true},
		{"StopIteration", 9, false},
		{"DivideByZeroError", 10, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, ok := ErrKind(tc.name)
			if !ok || k != tc.kind {
				t.Errorf("ErrKind(%q) = %d, %v; want %d, true", tc.name, k, ok, tc.kind)
			}
			if _, ok := ErrCtorKind(tc.name); ok != tc.ctor {
				t.Errorf("ErrCtorKind(%q) constructible = %v, want %v", tc.name, ok, tc.ctor)
			}
		})
	}
	t.Run("a name outside the taxonomy is neither", func(t *testing.T) {
		if _, ok := ErrKind("NopeError"); ok {
			t.Error("ErrKind accepted a name the taxonomy does not have")
		}
		if _, ok := ErrCtorKind("NopeError"); ok {
			t.Error("ErrCtorKind accepted a name the taxonomy does not have")
		}
	})
}

// TestErrorRaiseAndInspect covers raising a built-in error and inspecting it: `raise`
// takes an Err, `is <Kind>` tests it, and `.message()` reads it (docs/code/errors.md).
func TestErrorRaiseAndInspect(t *testing.T) {
	t.Run("raise takes a built-in error", func(t *testing.T) {
		wantOK(t, "fn f(bad: bool) -> int {\n  if bad {\n    raise IOError(\"down\")\n  }\n  return 0\n}")
	})
	t.Run("is tests an Err's kind", func(t *testing.T) {
		wantOK(t, "fn f(e: Err) -> bool {\n  return e is ValueError\n}")
	})
	t.Run("message reads a str", func(t *testing.T) {
		wantOK(t, "fn f(e: Err) -> str {\n  return e.message()\n}")
	})
}

// TestErrorResultMatch covers destructuring a guard's Result with `Left`/`Right` and
// distinguishing the caught error by kind — the observable path errors flow through
// (docs/code/errors.md: `match r { Left(v) => …; Right(e) => … }`).
func TestErrorResultMatch(t *testing.T) {
	src := "fn risky() -> int {\n  raise ValueError(\"x\")\n}\n" +
		"fn f() -> int {\n  r := guard { risky() }\n  return match r {\n" +
		"    Left(v) => v\n    Right(e) => if e is ValueError { 1 } else { 0 }\n  }\n}"
	t.Run("Left and Right destructure a Result", func(t *testing.T) {
		wantOK(t, src)
	})
	t.Run("an Either subject needs both sides or a catch-all", func(t *testing.T) {
		wantErr(t, "fn risky() -> int {\n  raise ValueError(\"x\")\n}\n"+
			"fn f() -> int {\n  return match guard { risky() } {\n    Left(v) => v\n  }\n}",
			"the Right case")
	})
}
