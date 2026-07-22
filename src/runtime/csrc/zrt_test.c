/*
 * zrt_test.c - the Zerg test-runner harness (Phase 1i U2).
 *
 * The reporting and summary side of `zerg test`: the compiler-generated driver owns
 * the per-test guard/abort framing (its setjmp must live in main's own activation),
 * and calls these to accumulate the counts and print the human-readable, ASCII-only,
 * deterministic report a conformance golden compares against.
 */
#include "zrt_test.h"

#include <stdio.h>

/* the running counts, reset by zrt_test_begin. A `zerg test` process runs one suite,
 * so process-global state is exactly right; it is never touched by a normal build. */
static int zrt_test_passed;
static int zrt_test_failed;

void zrt_test_begin(void) {
	zrt_test_passed = 0;
	zrt_test_failed = 0;
}

void zrt_test_report_ok(const char *label) {
	printf("test %s ... ok\n", label);
	zrt_test_passed++;
}

void zrt_test_report_fail(const char *label, const char *msg) {
	printf("test %s ... FAILED\n", label);
	if (msg != NULL) {
		printf("    %s\n", msg);
	}
	zrt_test_failed++;
}

int zrt_test_summary(void) {
	int total = zrt_test_passed + zrt_test_failed;
	printf("\ntest result: %d tests; %d passed, %d failed\n", total, zrt_test_passed, zrt_test_failed);
	return zrt_test_failed > 0 ? 1 : 0;
}
