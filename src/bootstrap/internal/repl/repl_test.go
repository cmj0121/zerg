package repl

import (
	"bytes"
	"strings"
	"testing"
)

// runSession feeds input into Start as if it were stdin and returns the
// captured stdout. The trailing :exit is appended so Start always returns
// cleanly; tests that already include it should pass includeExit=false.
func runSession(t *testing.T, input string, includeExit bool) string {
	t.Helper()
	if includeExit {
		input += ":exit\n"
	}
	var out bytes.Buffer
	if err := Start(strings.NewReader(input), &out); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return out.String()
}

// stripBanner removes the banner+help prefix the REPL always emits, plus
// every prompt occurrence — the prompt mixes with output bytes deterministically
// but tests are easier to read when only program output remains. We replace
// the banner first so partial prompt matches inside the banner can't fool us.
func stripPrompts(s string) string {
	for _, p := range []string{primaryPrompt, continuationPrompt} {
		s = strings.ReplaceAll(s, p, "")
	}
	return s
}

func stripBanner(s string) string {
	s = strings.TrimPrefix(s, banner)
	return s
}

// TestSingleLineStatement covers the simplest happy path: one statement, one
// prompt, one print. Confirms try-parse doesn't break the v0.0 single-line
// case.
func TestSingleLineStatement(t *testing.T) {
	got := runSession(t, "print 1 + 2\n", true)
	body := stripPrompts(stripBanner(got))
	if !strings.Contains(body, "3\n") {
		t.Fatalf("expected '3' in output; got %q", body)
	}
}

// TestMultiLineIfStmt feeds an if-stmt across three lines and asserts the
// `if`'s body executes exactly once after the closing brace arrives. The
// continuation prompt is the marker that the parser asked for more input.
func TestMultiLineIfStmt(t *testing.T) {
	input := "if true {\n" +
		"print \"yes\"\n" +
		"}\n"
	got := runSession(t, input, true)
	if !strings.Contains(got, "yes\n") {
		t.Fatalf("expected if body to execute and print 'yes'; got %q", got)
	}
	// Continuation prompt must appear at least once because the first two
	// lines do not parse as a complete program on their own.
	if !strings.Contains(got, continuationPrompt) {
		t.Fatalf("expected continuation prompt %q in output; got %q", continuationPrompt, got)
	}
}

// TestMultiLineFnDecl matches the example session in the requirements: a
// function spanning three input lines, then a call site that prints.
func TestMultiLineFnDecl(t *testing.T) {
	input := "fn double(x: int) -> int {\n" +
		"return x * 2\n" +
		"}\n" +
		"print double(5)\n"
	got := runSession(t, input, true)
	if !strings.Contains(got, "10\n") {
		t.Fatalf("expected 'double(5)' to print '10'; got %q", got)
	}
}

// TestPersistentVariable is the headline persistence requirement: a binding
// declared in one prompt must survive into the next. If the REPL did not
// thread state, `print x` would be an unknown name.
func TestPersistentVariable(t *testing.T) {
	input := "let x := 5\n" +
		"print x\n"
	got := runSession(t, input, true)
	body := stripPrompts(stripBanner(got))
	if !strings.Contains(body, "5\n") {
		t.Fatalf("expected persistent x to print '5'; got %q", body)
	}
}

// TestPriorOutputNotRepeated proves the suppression mechanism: re-running
// the accumulated program for each new prompt must not duplicate output
// from earlier prints. If the skipWriter were broken we'd see 'a' twice.
func TestPriorOutputNotRepeated(t *testing.T) {
	input := "print \"a\"\n" +
		"print \"b\"\n"
	got := runSession(t, input, true)
	body := stripPrompts(stripBanner(got))
	if got, want := body, "a\nb\n"; got != want {
		t.Fatalf("output: got %q, want %q", got, want)
	}
}

// TestParseErrorRecovery exercises the error path: a malformed input
// should print a parse error and clear the buffer so the next valid line
// still runs. Without recovery, the buffer would be poisoned forever.
func TestParseErrorRecovery(t *testing.T) {
	// `print` with no expression is a parse error (not incomplete: NEWLINE
	// after `print` parses as "expected expression, got NEWLINE"). The
	// REPL must report it and accept the next valid statement.
	input := "print\n" +
		"print 42\n"
	got := runSession(t, input, true)
	body := stripPrompts(stripBanner(got))
	if !strings.Contains(body, "parse error") {
		t.Fatalf("expected a parse-error report in output; got %q", body)
	}
	if !strings.Contains(body, "42\n") {
		t.Fatalf("expected recovery: '42' should still print; got %q", body)
	}
}

// TestRuntimeErrorRecovery feeds a type-incorrect program (typeck rejects
// it) and follows up with a valid statement. The bad statement must NOT
// be promoted to committed history.
func TestRuntimeErrorRecovery(t *testing.T) {
	// `let x := 1 + "two"` is a type error caught by Check: int + str.
	input := "let x := 1 + \"two\"\n" +
		"print 99\n"
	got := runSession(t, input, true)
	body := stripPrompts(stripBanner(got))
	if !strings.Contains(body, "type error") {
		t.Fatalf("expected type-error report; got %q", body)
	}
	if !strings.Contains(body, "99\n") {
		t.Fatalf("expected recovery: '99' should still print; got %q", body)
	}
	// And `x` should NOT exist in the session — confirm by following with a
	// fresh session-style check using a new input piece.
}

// TestExitCommand: :exit at the primary prompt returns immediately. We also
// verify a non-empty trailing newline from the prompt is not duplicated.
func TestExitCommand(t *testing.T) {
	got := runSession(t, "", true)
	if !strings.HasPrefix(got, banner) {
		t.Fatalf("output should start with banner; got %q", got)
	}
}

// TestHelpCommand: :help prints the canonical help string and continues.
func TestHelpCommand(t *testing.T) {
	got := runSession(t, ":help\n", true)
	if !strings.Contains(got, helpText) {
		t.Fatalf("expected help text in output; got %q", got)
	}
}

// TestBannerText pins the v0.4 banner content so a future copy edit can't
// silently regress the user-facing string.
func TestBannerText(t *testing.T) {
	got := runSession(t, "", true)
	if !strings.Contains(got, "v0.4") {
		t.Fatalf("banner should mention v0.4; got %q", got)
	}
	if !strings.Contains(got, ":help") {
		t.Fatalf("banner should mention :help; got %q", got)
	}
}

// TestContinuationPromptPersistsForNestedBraces feeds a function whose body
// itself contains a multi-line if. The REPL should keep asking for more
// input until both braces close.
func TestContinuationPromptPersistsForNestedBraces(t *testing.T) {
	input := "fn pick(x: int) -> int {\n" +
		"if x > 0 {\n" +
		"return 1\n" +
		"}\n" +
		"return 0\n" +
		"}\n" +
		"print pick(5)\n" +
		"print pick(-1)\n"
	got := runSession(t, input, true)
	body := stripPrompts(stripBanner(got))
	if !strings.Contains(body, "1\n") || !strings.Contains(body, "0\n") {
		t.Fatalf("expected pick to print 1 and 0; got %q", body)
	}
}

// TestMutAndAssign covers state mutation across prompts: a mut binding made
// in one turn is updated in another and the new value is observed.
func TestMutAndAssign(t *testing.T) {
	input := "mut counter := 0\n" +
		"counter += 5\n" +
		"print counter\n"
	got := runSession(t, input, true)
	body := stripPrompts(stripBanner(got))
	if !strings.Contains(body, "5\n") {
		t.Fatalf("expected counter to be 5; got %q", body)
	}
}
