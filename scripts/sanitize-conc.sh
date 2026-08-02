#!/usr/bin/env bash
#
# sanitize-conc.sh — run the concurrency corpus with the sanitizers switched on.
#
# `make corpus` runs these same cases and checks what they PRINT. That catches a lost
# wake-up and a doubly-delivered value, because both change the arithmetic — and it is
# blind to everything that goes wrong without changing the answer: a coroutine stack freed
# while another fiber is still on it, a channel's waiter list read after the channel died,
# a buffer that the scheduler forgot on the way out. Those are precisely the mistakes the
# termination and abort paths make, and a program with one of them prints the right number
# and exits zero until the day it does not.
#
# So the same cases are built again here against AddressSanitizer and UndefinedBehaviour-
# Sanitizer, and on Linux against LeakSanitizer as well. It is a separate script rather
# than a corpus flag because the sanitizers need their own link line: `zerg build` decides
# the whole cc invocation itself and has nowhere to put a flag, so the C is emitted and
# compiled here instead.
#
# Each case still has to print the right thing. A sanitizer run that comes back clean while
# the program answers wrongly has proved nothing worth having.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

ZERG="${ZERG:-$ROOT/bin/zerg}"
CC="${CC:-cc}"
RT="src/runtime/csrc"

# The same number of repetitions the corpus uses, for the same reason: a concurrent program
# is a function of its source AND of an interleaving the scheduler picks fresh each run, so
# one clean run says very little. The sanitizers make each run slower, not slow — these are
# millisecond programs.
# SCHEDULES is how many seeded single-worker interleavings each case is run under, and RUNS
# how many unseeded multi-worker ones. They are separate numbers because they buy different
# things: a schedule is a repro and a run is a search, and 10 of either was not enough — the
# day this went to 150 it found three runtime bugs, one of which failed about one run in
# twenty and had passed every CI run this project had ever done.
#
# THE TWO NUMBERS ARE NOT INTERCHANGEABLE, and the split hid a whole class. 60 of the 70
# runs a case gets are `ZRT_WORKERS=1` — a seeded schedule IS single-worker, that is what
# makes it a repro — so only 10 of them can reach a bug that needs two threads at once. CI
# found a SEGV in `wq_pop` that way, on run 3 of 10, and 1560 runs here never saw it again:
# a loaded CI runner with fewer cores preempts where an idle desktop does not.
#
# So the unseeded half is the one that searches, and it is the one that was small. PARALLEL
# runs the multi-worker half several instances at once, which is the pressure that made the
# difference — an oversubscribed CPU, not more repetitions of an idle one.
# ASan's FAKE STACK is turned OFF while the runtime's own trace is compiled in, because the
# trace reasons about STACKS and the fake stack puts locals on the heap — a waiter that ASan
# has moved is one the instrument cannot say anything true about. This is a precondition of
# the measurement, not a fix: see below.
export ASAN_OPTIONS="${ASAN_OPTIONS:-detect_stack_use_after_return=0}"

# The fake stack was once believed to BE the bug, and the reason is a
# theory that was TESTED AND DISPROVEN — written down so it is not tried again.
#
# With it on, ASan moves a local whose address escapes into a heap-backed frame from a
# PER-THREAD pool. This scheduler is M:N, so a `zrt_waiter` allocated in one worker's fake
# stack is read from another, and that looked like a complete explanation for the SEGVs CI
# reports: always multi-worker, never on the seeded single-worker half where nothing
# migrates, never under clang (whose ASan leaves this off) and always under GCC (whose
# turns it on).
#
# It is not the explanation. With `detect_stack_use_after_return=0` set explicitly, CI still
# SEGVs in `wq_pop` from the same `chan_close` on the same case. What the fake stack DID
# explain is the false positives from this repo's own instrument: a check that a queued
# waiter lies inside a live coroutine stack is meaningless when ASan has put the waiter on
# the heap, and that check has been removed (see zergrt.h).

SCHEDULES="${SCHEDULES:-${REPS:-60}}"
RUNS="${RUNS:-${REPS:-30}}"
PARALLEL="${PARALLEL:-4}"

[ -x "$ZERG" ] || {
	printf 'sanitize-conc: %s is not built — run `make build` first\n' "$ZERG" >&2
	exit 2
}
[ -d test-data/codegen ] || {
	printf 'sanitize-conc: test-data submodule not initialized (git submodule update --init)\n' >&2
	exit 2
}

# The runtime units to link, derived from the tree by REMOVING the alternates rather than
# by listing the keepers. A list of keepers is what went stale last time: the runtime grew
# sched.c and chan.c and the list did not, so the check linked a runtime with no scheduler
# in it. Subtracting means a new unit is picked up on the day it appears, and only a new
# ALTERNATIVE — a second implementation of a slot that already has one — needs a line here.
rt_sources() {
	# thread_pthread.c is this slot's implementation on every platform CI runs on; the
	# context switch is chosen by architecture, exactly as the compiler's own rt_units()
	# chooses it. zrt_test.c is the C suite's harness and belongs to no program.
	ls "$RT"/*.c | grep -Ev 'thread_win32|thread_none|ctx_ucontext|zrt_test'
	case "$(uname -m)" in
	arm64 | aarch64) printf '%s/ctx_arm64.S\n' "$RT" ;;
	x86_64 | amd64) printf '%s/ctx_x86_64.S\n' "$RT" ;;
	*) printf '%s/ctx_ucontext.c\n' "$RT" ;;
	esac
}

# LeakSanitizer does not exist on macOS — asking for it there aborts the process before
# main. So the leak half of this gate is the Linux half, and the macOS run is an
# address/UB run that says as much rather than pretending to check for leaks.
case "$(uname -s)" in
Linux)
	export ASAN_OPTIONS="detect_leaks=1"
	LEAKS="on"
	;;
*)
	export ASAN_OPTIONS="detect_leaks=0"
	LEAKS="off — LeakSanitizer is not available on $(uname -s)"
	;;
esac

WORK="$(mktemp -d "${TMPDIR:-/tmp}/zerg-sanitize.XXXXXX")" || exit 2

printf 'sanitize-conc: address + undefined, leak detection %s, %s seeded single-worker schedules + %s multi-worker runs per case\n\n' "$LEAKS" "$SCHEDULES" "$RUNS"

fail=0
cases=0
# CASES narrows the sweep to one or a few cases, which is what a rare multi-worker race
# needs: the seeded half is single-worker by construction, so the only runs that can reach
# an interleaving bug are the unseeded ones, and hammering one case is the way to get them.
for src in ${CASES:-test-data/codegen/conc_*.zg}; do
	name="$(basename "$src" .zg)"
	cases=$((cases + 1))

	if ! "$ZERG" build --emit c "$src" >"$WORK/$name.c" 2>"$WORK/$name.emit.log"; then
		printf 'EMIT   %s\n' "$name"
		head -5 "$WORK/$name.emit.log"
		fail=1
		continue
	fi

	# shellcheck disable=SC2046  # the source list is one path per line and has no spaces
	# NOTE the -DZRT_TRACE path is compiled ONLY here: `make -C src/runtime build` does not
	# define it, so a trace-only mistake reads as a green runtime build and a red gate.
	# -DZRT_TRACE, ON by default here, arms the runtime's LIVE-WAITER INVARIANT: a waiter
	# lives on the stack of the coroutine that parked, so a queue still pointing at one
	# whose stack is being freed is a hand-off into unmapped memory. That is the SEGV in
	# wq_pop CI has reported three times and no local run has reproduced — and the
	# invariant catches the run that MAKES the mistake rather than the one that trips over
	# it, which is a different and much larger set of runs.
	#
	# It prints nothing unless it fires; the verbose per-event log is a second switch,
	# `ZRG_TRACE=1` in the environment, for reading an interleaving back. TRACE=0 turns the
	# whole thing off, and a shipped build never has it: without the define every macro
	# expands to nothing, not even a branch.
	# CORO_STACK=<bytes> overrides the coroutine stack size for the stack-overflow
	# experiment the zergrt.h comment describes; unset, the runtime's own constant stands.
	if ! $CC -std=c11 -g -fno-omit-frame-pointer $([ "${TRACE:-1}" = 0 ] || echo -DZRT_TRACE) ${CORO_STACK:+-DZRT_CORO_STACK=$CORO_STACK} \
		-fsanitize=address,undefined -fno-sanitize-recover=all \
		-I "$RT" -o "$WORK/$name.bin" "$WORK/$name.c" $(rt_sources) 2>"$WORK/$name.cc.log"; then
		printf 'CC     %s\n' "$name"
		head -10 "$WORK/$name.cc.log"
		fail=1
		continue
	fi

	want="$(cat "test-data/codegen/$name.out")"

	# Both worker modes, which is what the runtime's own suite does and for the reason it
	# gives (src/runtime/README.md): a bug in the scheduler's logic survives with one
	# worker while a race needs several, and one worker is the harsher of the two, because
	# nothing else is running to paper over a coroutine that never yields.
	# One worker is run under a SEED, and each repetition uses a different one. That turns
	# the repetitions from N runs of the same schedule — which is what they were, since a
	# cooperative scheduler with one worker is deterministic — into N DIFFERENT schedules,
	# each of which can be run again. A failure now comes with the command that reproduces
	# it, which the last concurrency bug this project found did not: it was fixed by
	# reading the code and never made to happen twice.
	for mode in many one; do
		n=0
		reps=$RUNS
		if [ "$mode" = one ]; then
			reps=$SCHEDULES
		fi
		while [ "$n" -lt "$reps" ]; do
			seed=$((n + 1))
			if [ "$mode" = one ]; then
				got="$(ZRT_WORKERS=1 ZRT_SEED="$seed" "$WORK/$name.bin" 2>"$WORK/$name.err")"
				repro="ZRT_WORKERS=1 ZRT_SEED=$seed"
			else
				# several workers: the OS decides things a seed cannot, so this half stays
				# unseeded and is a search rather than a repro.
				#
				# PARALLEL-1 SIBLINGS run alongside it, oversubscribing the CPU. That is
				# the pressure a loaded CI runner has and an idle desktop does not, and it
				# is what this half was missing: the SEGV that prompted this appeared on a
				# runner in 3 runs and never once in 1560 here. Their output is thrown
				# away — they are load, and the run being measured is the one below.
				mut_extra=0
				while [ "$mut_extra" -lt "$((PARALLEL - 1))" ]; do
					(env -u ZRT_WORKERS -u ZRT_SEED "$WORK/$name.bin" >/dev/null 2>&1) &
					mut_extra=$((mut_extra + 1))
				done
				got="$(env -u ZRT_WORKERS -u ZRT_SEED "$WORK/$name.bin" 2>"$WORK/$name.err")"
				wait
				repro="(several workers under load — not reproducible)"
			fi

			# The exit status is NOT the signal: conc_crash ends in an abort and leaves
			# with 1 on a healthy day. A sanitizer says so in its own words instead, on
			# stderr.
			#
			# The pattern is deliberately narrow. An aborting coroutine makes ASan print
			# `WARNING: ASan is ignoring requested __asan_handle_no_return` — it followed
			# the unwind onto a stack it has no shadow for — and that warning is an
			# artifact of fibers, not a finding. Widening this to match it would gate on
			# the runtime's normal behaviour.
			if grep -Eq 'ERROR: .*Sanitizer|runtime error:' "$WORK/$name.err"; then
				printf 'SAN    %s (%s workers, run %s) — %s\n' "$name" "$mode" "$n" "$repro"
				head -20 "$WORK/$name.err"
				fail=1
				break 2
			fi
			if [ "$got" != "$want" ]; then
				printf 'OUTPUT %s (%s workers, run %s) — %s — wanted %s, got %s\n' \
					"$name" "$mode" "$n" "$repro" "$(echo "$want" | tr '\n' ' ')" "$(echo "$got" | tr '\n' ' ')"
				# and WHAT IT SAID. A program that aborted prints nothing on stdout, so the
				# mismatch above is "got nothing" and the reason is on stderr — which this
				# threw away, because the only thing that ever read it was the narrow
				# sanitizer pattern above. An abort from the runtime's own assertions, a
				# glibc message, a stack trace: all of it landed in a file nobody printed.
				if [ -s "$WORK/$name.err" ]; then
					echo "--- stderr:"
					head -20 "$WORK/$name.err"
				fi
				fail=1
				break 2
			fi
			n=$((n + 1))
		done
	done
done

if [ "$fail" -ne 0 ]; then
	# A sanitizer report is longer than the twenty lines printed above, and the emitted C
	# it points into is worth reading beside it, so the whole working set stays put.
	printf '\nsanitize-conc: the concurrency corpus is not clean under the sanitizers\n' >&2
	printf 'sanitize-conc: the C, the binaries and the full reports are kept in %s\n' "$WORK" >&2
	exit 1
fi
rm -rf "$WORK"
printf '\nsanitize-conc: %s cases x %s seeded schedules + %s multi-worker runs, clean\n' "$cases" "$SCHEDULES" "$RUNS"
