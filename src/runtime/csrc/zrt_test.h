/*
 * zrt_test.h - the Zerg test-runner harness (Phase 1i U2).
 *
 * Linked ONLY into a `zerg test` binary (never a normal `zerg build`). The
 * compiler-generated test driver (emit's cTestMain) runs each `#[test]` function
 * under the runtime's guard/abort handler and calls these to report each outcome and
 * the final summary.
 *
 * The report is written INCREMENTALLY and UNBUFFERED, through the same `zrt_write`
 * path `io.println` uses (a direct write() syscall, not stdio's block-buffered
 * printf): the driver prints `test <label> ... ` BEFORE running a test and the
 * verdict AFTER, so (a) a test's own `io.println` output interleaves in the right
 * order, and (b) a test that HANGS or crashes still leaves every prior verdict — and
 * the pending test's label — visible rather than trapped in an unflushed buffer.
 * Output is plain ASCII on stdout so a conformance golden can compare it byte for
 * byte:
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

/* zrt_test_start prints `test <label> ... ` (no newline) BEFORE the test runs, so the
 * label is on stdout the instant the test starts — visible even if it then hangs. */
void zrt_test_start(const char *label);

/* zrt_test_ok records a passing test and prints `ok\n`, completing the line
 * zrt_test_start opened. */
void zrt_test_ok(void);

/* zrt_test_fail records a failing test and prints `FAILED\n` followed by the indented
 * message (when non-NULL). msg is the Err message the test's raise carried, read from
 * the runtime by the driver. */
void zrt_test_fail(const char *msg);

/* zrt_test_summary prints the blank-line-separated summary and returns the process
 * exit code: 1 if any test failed, else 0. The driver returns its result from main. */
int zrt_test_summary(void);

#endif /* ZRT_TEST_H */
