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
