/*
 * zrt_test.c - the Zerg test-runner harness (Phase 1i U2).
 *
 * The reporting and summary side of `zerg test`: the compiler-generated driver owns
 * the per-test guard/abort framing (its setjmp must live in main's own activation),
 * and calls these to accumulate the counts and print the human-readable, ASCII-only,
 * deterministic report a conformance golden compares against.
 *
 * Output goes through the runtime's `zrt_write*` primitives (a direct, UNBUFFERED
 * write() syscall — the same path `io.println` uses), NOT stdio's block-buffered
 * printf. That keeps the report ordered with any output a test itself writes, and
 * leaves every completed verdict on stdout even if a later test hangs or crashes with
 * no chance to flush a buffer.
 */
#include "zrt_test.h"

#include "zergrt.h"

/* the running counts, reset by zrt_test_begin. A `zerg test` process runs one suite,
 * so process-global state is exactly right; it is never touched by a normal build. */
static int zrt_test_passed;
static int zrt_test_failed;

void zrt_test_begin(void) {
	zrt_test_passed = 0;
	zrt_test_failed = 0;
}

void zrt_test_start(const char *label) {
	zrt_write_str(1, "test ");
	zrt_write_str(1, label);
	zrt_write_str(1, " ... ");
}

void zrt_test_ok(void) {
	zrt_write_str(1, "ok\n");
	zrt_test_passed++;
}

void zrt_test_fail(const char *msg) {
	zrt_write_str(1, "FAILED\n");
	if (msg != NULL) {
		zrt_write_str(1, "    ");
		zrt_write_str(1, msg);
		zrt_write_str(1, "\n");
	}
	zrt_test_failed++;
}

int zrt_test_summary(void) {
	int total = zrt_test_passed + zrt_test_failed;
	zrt_write_str(1, "\ntest result: ");
	zrt_write_int(1, total);
	zrt_write_str(1, " tests; ");
	zrt_write_int(1, zrt_test_passed);
	zrt_write_str(1, " passed, ");
	zrt_write_int(1, zrt_test_failed);
	zrt_write_str(1, " failed\n");
	return zrt_test_failed > 0 ? 1 : 0;
}
