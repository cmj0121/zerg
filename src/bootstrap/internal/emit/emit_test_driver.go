package emit

// This file carries the Phase 1i U2 test driver — the C backend's `zerg test` entry.
// Instead of the ordinary `cMain`, a test translation unit ends in a generated
// `int main(void)` that runs every discovered `#[test]` function under the runtime's
// guard/abort handler (the same setjmp landing pad `guard { }` and `zrt_run` use) and
// reports pass/fail through the small zrt_test.c harness. A test that completes
// normally passes; one whose assertion RAISES (an uncaught abort) is caught here and
// reported as a failure with the Err's message.
//
// The whole path is additive: it fires only for EmitTests (testMode), so a normal
// `zerg build` never emits any of it and stays byte-identical.

import (
	"fmt"

	"github.com/cmj0121/zerg/src/bootstrap/internal/diag"
	"github.com/cmj0121/zerg/src/bootstrap/internal/mono"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
)

// EmitTests lowers a monomorphized test program to C: the `zerg test` counterpart of
// Emit (Phase 1i U2). It renders every function instance exactly as Emit does, then a
// generated test-driver `main` that runs the given `#[test]` signatures in order. The
// returned Manifest always reports NeedsRuntime (the driver needs the abort handler);
// the driver's reporting/summary helpers link from zrt_test.c.
func EmitTests(prog *mono.Program, tests []*sema.FuncSig) (string, Manifest, []diag.Diagnostic) {
	e := &emitter{prog: prog, info: prog.Info, testMode: true, tests: tests}
	e.program()
	if !e.diags.Empty() {
		return "", Manifest{}, e.diags.Items()
	}
	return e.sb.String(), Manifest{
		NeedsRuntime: true, Concurrency: e.concurrency, NeedsResult: e.needsResult,
		NeedsIO: e.needsIO, NeedsFormat: e.needsFormat, NeedsFFI: e.needsFFI,
	}, nil
}

// cTestMain emits the generated test-driver entry (Phase 1i U2). It runs each
// module's init first (Phase 1g S3), resets the harness counters, runs every test in
// deterministic order (U3), and returns the harness summary's exit code (1 if any
// test failed, else 0 — including 0 when there are no tests).
func (e *emitter) cTestMain() {
	e.line("int main(void) {")
	e.indent++
	e.emitInitCalls()
	e.line("zrt_test_begin();")
	for _, t := range e.tests {
		e.emitTestRun(t)
	}
	e.line("return zrt_test_summary();")
	e.indent--
	e.line("}")
}

// emitTestRun emits one test's protected run. It installs a fresh `catch` abort
// handler and arms its setjmp in main's own activation (as guardExpr / zrt_run do),
// so a raise inside the test longjmps back here rather than aborting the process. On
// the setjmp==0 path the test ran to completion: a `nil` test is simply called, a
// `Result[nil]` test additionally fails when it returns an Err; the scope's cleanups
// unwind and the test is reported ok. On the setjmp!=0 path the test raised: the
// carried Err's message is read back and the test is reported FAILED.
func (e *emitter) emitTestRun(t *sema.FuncSig) {
	label := cString(t.TestLabel)
	fn := "zg_" + t.Name
	e.line("{")
	e.indent++
	e.line("zrt_frame zg_tf;")
	e.line("zrt_handler_push_catch(&zg_tf);")
	e.line("if (setjmp(zg_tf.buf) == 0) {")
	e.indent++
	if isResultNil(t.Ret) {
		e.line(fmt.Sprintf("zrt_result_nil zg_tr = %s();", fn))
		e.line("if (zrt_result_is_err(zg_tr)) { zrt_raise_err(zrt_err_new(\"test returned an Err value\")); }")
	} else {
		e.line(fmt.Sprintf("%s();", fn))
	}
	e.line("zrt_unwind_to(zg_tf.mark);")
	e.line("zrt_handler_pop(&zg_tf);")
	e.line(fmt.Sprintf("zrt_test_report_ok(%s);", label))
	e.indent--
	e.line("} else {")
	e.indent++
	e.line("zrt_handler_pop(&zg_tf);")
	e.line(fmt.Sprintf("zrt_test_report_fail(%s, zrt_taken_err().msg);", label))
	e.indent--
	e.line("}")
	e.indent--
	e.line("}")
}
