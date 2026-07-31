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
REPS="${REPS:-10}"

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

printf 'sanitize-conc: address + undefined, leak detection %s, %s seeded single-worker schedules + %s multi-worker runs per case\n\n' "$LEAKS" "$REPS" "$REPS"

fail=0
cases=0
for src in test-data/codegen/conc_*.zg; do
	name="$(basename "$src" .zg)"
	cases=$((cases + 1))

	if ! "$ZERG" build --emit c "$src" >"$WORK/$name.c" 2>"$WORK/$name.emit.log"; then
		printf 'EMIT   %s\n' "$name"
		head -5 "$WORK/$name.emit.log"
		fail=1
		continue
	fi

	# shellcheck disable=SC2046  # the source list is one path per line and has no spaces
	if ! $CC -std=c11 -g -fno-omit-frame-pointer \
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
		while [ "$n" -lt "$REPS" ]; do
			seed=$((n + 1))
			if [ "$mode" = one ]; then
				got="$(ZRT_WORKERS=1 ZRT_SEED="$seed" "$WORK/$name.bin" 2>"$WORK/$name.err")"
				repro="ZRT_WORKERS=1 ZRT_SEED=$seed"
			else
				# several workers: the OS decides things a seed cannot, so this half stays
				# unseeded and is a search rather than a repro
				got="$(env -u ZRT_WORKERS -u ZRT_SEED "$WORK/$name.bin" 2>"$WORK/$name.err")"
				repro="(several workers — not reproducible)"
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
printf '\nsanitize-conc: %s cases x %s seeded schedules + %s multi-worker runs, clean\n' "$cases" "$REPS" "$REPS"
