/* conv.c — checked primitive conversions (docs/types.md, "Type Conversion").
 *
 * Zerg converts by RE-CONSTRUCTION, never by reinterpretation: `T(x)` builds a new
 * T from x's value. Narrowing can lose the value, so it is checked like arithmetic —
 * a conversion whose value does not fit the target RAISES OverflowError. These
 * helpers are that check; each aborts through zrt_abort, so `guard { byte(x) }`
 * catches it and yields a Result, exactly as the docs describe.
 *
 * The compiler emits a call here only when the source range is NOT provably inside
 * the target range; a widening conversion (byte -> int, say) is a plain C cast with
 * no call, so it costs nothing.
 *
 * The bounds arrive as arguments rather than being baked in per target type, so one
 * helper per (source signedness, target signedness) pair covers every width in the
 * i8..i64 / u8..u64 family plus int / uint / byte / rune.
 *
 * Nothing here includes <math.h>: the float guards are plain comparisons, which are
 * false for NaN and for an out-of-range infinity, so those abort on the same path as
 * an out-of-range finite value and the runtime needs no libm on the link line.
 */

#include "zergrt.h"

static const char *const kRangeMsg = "OverflowError: integer conversion out of range";
static const char *const kFloatMsg = "OverflowError: float conversion out of range";

int64_t zrt_conv_i_from_i(int64_t v, int64_t lo, int64_t hi) {
	if (v < lo || v > hi) {
		zrt_abort(kRangeMsg);
	}
	return v;
}

uint64_t zrt_conv_u_from_i(int64_t v, uint64_t hi) {
	if (v < 0 || (uint64_t)v > hi) {
		zrt_abort(kRangeMsg);
	}
	return (uint64_t)v;
}

int64_t zrt_conv_i_from_u(uint64_t v, int64_t hi) {
	if (hi < 0 || v > (uint64_t)hi) {
		zrt_abort(kRangeMsg);
	}
	return (int64_t)v;
}

uint64_t zrt_conv_u_from_u(uint64_t v, uint64_t hi) {
	if (v > hi) {
		zrt_abort(kRangeMsg);
	}
	return v;
}

/* float -> integer drops the fractional part (`int(3.7)` is 3 — the intent, not a
 * bug) and raises only when the TRUNCATED value is out of range, or the input is
 * NaN/+-Inf. Testing the open interval (lo-1, hi+1) is what admits 255.7 -> 255 while
 * still rejecting 256.0: truncation is toward zero, so any v in that interval lands
 * back inside [lo, hi] (every integer target straddles zero). NaN and an infinity
 * make both comparisons false and so fall into the abort. The interval also keeps
 * |v| below the target width, which is what makes the C cast itself well-defined. */
int64_t zrt_conv_i_from_f(double v, double lo, double hi) {
	if (!(v > lo - 1.0 && v < hi + 1.0)) {
		zrt_abort(kFloatMsg);
	}
	return (int64_t)v;
}

uint64_t zrt_conv_u_from_f(double v, double hi) {
	if (!(v > -1.0 && v < hi + 1.0)) {
		zrt_abort(kFloatMsg);
	}
	if (v <= 0.0) {
		/* (-1, 0] truncates to zero; casting a negative double to an unsigned type
		 * directly would be undefined, so answer it here. */
		return 0;
	}
	return (uint64_t)v;
}
