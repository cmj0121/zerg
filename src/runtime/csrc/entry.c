/*
 * entry.c - the Zerg runtime program-entry shim.
 *
 * Used only by a `fn main() -> Result[nil]` program (the additive entry path);
 * a value-only nil/int main keeps the Phase 0 `main` untouched. The setjmp that
 * arms the root abort handler lives here, in zrt_run's own activation, so its
 * longjmp landing pad stays valid for the whole run - the reason the handler
 * cannot be hidden behind a plain function call.
 */
#include "zergrt.h"

int zrt_run(zrt_main_fn fn) {
	zrt_frame frame;
	zrt_fault_init(); /* a stack overflow from here on dies with its name */
	zrt_handler_push(&frame);
	if (setjmp(frame.buf) == 0) {
		zrt_result_nil r = fn();
		zrt_unwind_to(frame.mark); /* run any top-level defers on the Ok path */
		zrt_handler_pop(&frame);
		return zrt_result_is_err(r) ? 1 : 0;
	}
	/* abort landed here: zrt_abort already ran cleanups down to frame.mark */
	zrt_handler_pop(&frame);
	return 1;
}

/* zrt_run_args is zrt_run for a `fn main(args: list[str]) -> Result[nil]` program: it
 * arms the same root abort handler and hands main the args list. main takes the list
 * BY VALUE and frees it as its own parameter at scope exit, so on the abort path the
 * unwind that already ran cleanups down to frame.mark has freed it too. */
int zrt_run_args(zrt_main_args_fn fn, zrt_list args) {
	zrt_frame frame;
	zrt_fault_init(); /* a stack overflow from here on dies with its name */
	zrt_handler_push(&frame);
	if (setjmp(frame.buf) == 0) {
		zrt_result_nil r = fn(args);
		zrt_unwind_to(frame.mark);
		zrt_handler_pop(&frame);
		return zrt_result_is_err(r) ? 1 : 0;
	}
	zrt_handler_pop(&frame);
	return 1;
}

/* zrt_main_run_nil / _int / _args are zrt_run for the three entry shapes that answer no
 * Result - `fn main()`, `fn main() -> int`, and `fn main(args)`. They exist because those
 * shapes had no root handler at all: the emitted `int main` called the body directly, so
 * an uncaught abort reached zrt_unwind_abort with g_tls.handler still NULL, took its
 * exit(1) last resort, and left the cleanup stack standing. Every pending `defer` at the
 * top level was skipped on the one path that most needs them to run.
 *
 * They are here rather than emitted as C text for the reason zrt_run is here: this bracket
 * is a protocol - push, arm, unwind to the mark, pop on BOTH paths - and a copy of it in
 * generated text is the one copy no compiler in this repository ever checks.
 *
 * The three differ only in what the body answers, which is exactly how zrt_sched_main_nil
 * and _int differ; the emitter picks between the two families by whether the program is
 * concurrent, and by nothing else. */
int zrt_main_run_nil(void (*fn)(void)) {
	zrt_frame frame;
	zrt_fault_init(); /* a stack overflow from here on dies with its name */
	zrt_handler_push(&frame);
	if (setjmp(frame.buf) == 0) {
		fn();
		zrt_unwind_to(frame.mark);
		zrt_handler_pop(&frame);
		return 0;
	}
	zrt_handler_pop(&frame);
	return 1;
}

/* zrt_main_run_int carries main's value out as the process's status; an abort answers 1,
 * which is the status that path already had. */
int zrt_main_run_int(int64_t (*fn)(void)) {
	zrt_frame frame;
	zrt_fault_init(); /* a stack overflow from here on dies with its name */
	zrt_handler_push(&frame);
	if (setjmp(frame.buf) == 0) {
		int64_t rc = fn();
		zrt_unwind_to(frame.mark);
		zrt_handler_pop(&frame);
		return (int)rc;
	}
	zrt_handler_pop(&frame);
	return 1;
}

/* zrt_main_run_args hands the args list on, on the same terms as zrt_run_args: main owns
 * the list as its own parameter, so the unwind frees it on either path. */
int zrt_main_run_args(void (*fn)(zrt_list), zrt_list args) {
	zrt_frame frame;
	zrt_fault_init(); /* a stack overflow from here on dies with its name */
	zrt_handler_push(&frame);
	if (setjmp(frame.buf) == 0) {
		fn(args);
		zrt_unwind_to(frame.mark);
		zrt_handler_pop(&frame);
		return 0;
	}
	zrt_handler_pop(&frame);
	return 1;
}

/* zrt_main_run_args_int is the fourth corner: args in, an exit code out. Both compilers
 * used to drop that code - `fn main(args) -> int` returning 2 exited 0 - because the entry
 * they emitted called main for effect and answered 0 itself. There is nothing special about
 * the shape; it was simply the corner neither of them had a spelling for. */
int zrt_main_run_args_int(int64_t (*fn)(zrt_list), zrt_list args) {
	zrt_frame frame;
	zrt_fault_init(); /* a stack overflow from here on dies with its name */
	zrt_handler_push(&frame);
	if (setjmp(frame.buf) == 0) {
		int64_t rc = fn(args);
		zrt_unwind_to(frame.mark);
		zrt_handler_pop(&frame);
		return (int)rc;
	}
	zrt_handler_pop(&frame);
	return 1;
}
