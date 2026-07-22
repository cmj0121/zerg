/*
 * zrt_test.h - the Zerg test-runner harness (Phase 1i U2).
 *
 * Linked ONLY into a `zerg test` binary (never a normal `zerg build`). The
 * compiler-generated test driver (emit's cTestMain) runs each `#[test]` function
 * under the runtime's guard/abort handler and calls these to report each outcome and
 * the final summary. Output is plain ASCII on stdout so a conformance golden can
 * compare it byte for byte:
 *
 *     test <label> ... ok
 *     test <label> ... FAILED
 *         <message>
 *
 *     test result: <N> tests; <P> passed, <F> failed
 *
 * The process exit code is 1 when any test failed, else 0 (0 for a suite with no
 * tests). These symbols carry the `zrt_` prefix like every other runtime symbol.
 */
#ifndef ZRT_TEST_H
#define ZRT_TEST_H

/* zrt_test_begin resets the pass/fail counters. The driver calls it once before the
 * first test runs. */
void zrt_test_begin(void);

/* zrt_test_report_ok records a passing test and prints `test <label> ... ok`. */
void zrt_test_report_ok(const char *label);

/* zrt_test_report_fail records a failing test and prints `test <label> ... FAILED`
 * followed by the indented message (when non-NULL). msg is the Err message the test's
 * raise carried, read from the runtime by the driver. */
void zrt_test_report_fail(const char *label, const char *msg);

/* zrt_test_summary prints the blank-line-separated summary and returns the process
 * exit code: 1 if any test failed, else 0. The driver returns its result from main. */
int zrt_test_summary(void);

#endif /* ZRT_TEST_H */
