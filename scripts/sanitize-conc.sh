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

# WHO IS SPEAKING, and about WHAT. This script serves two gates now — `sanitize-conc` over the
# twenty `conc_*` cases and `sanitize-corpus` over the other 179 — and every message hardcoded
# the first one's name. A reader of a failing `make sanitize-corpus` was told "the concurrency
# corpus is not clean under the sanitizers", about 179 cases that are not it.
GATE="${GATE:-sanitize-conc}"
CORPUS="${CORPUS:-the concurrency corpus}"

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
SCHEDULES="${SCHEDULES:-${REPS:-60}}"
RUNS="${RUNS:-${REPS:-30}}"
PARALLEL="${PARALLEL:-4}"

[ -x "$ZERG" ] || {
	printf "$GATE: %s is not built — run `make build` first\n" "$ZERG" >&2
	exit 2
}
[ -d test-data/codegen ] || {
	printf "$GATE: test-data submodule not initialized (git submodule update --init)\n" >&2
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

# detect_stack_use_after_return is ASKED FOR rather than inherited, and it is the harshest
# option this gate sets. With it on, an instrumented frame does not live on the stack at
# all: ASan hands it out of a per-thread arena elsewhere in the address space. That is the
# configuration under which a coroutine's locals — `zrt_waiter w`, which chan.c parks on a
# wait queue precisely because a suspended stack does not move — stop being on the
# coroutine's stack, and it is the configuration the runtime's fiber annotations
# (ZRT_ASAN_SWITCH_TO/DONE) exist to survive.
#
# It is written down here because the DEFAULT is not the same everywhere: the CI compiler
# had it on and this developer's did not, so the gate checked a weaker program locally than
# it did on the runner — which is how a SEGV came to be reproducible only on CI, and stayed
# unexplained for two days. A gate whose strength depends on the host is not one gate.
#
# LEAK DETECTION IS ON, and until now it never had been. That is worth saying plainly,
# because the line above it used to claim otherwise: until the fiber annotations landed,
# ASan's idea of the running stack was the worker thread's, so LeakSanitizer scanned the
# wrong range for roots, found stale pointers to everything, and reported nothing. A gate
# measuring nothing looks exactly like a gate finding nothing.
#
# With the annotations it measures, and the first honest run found EIGHT cases leaking and
# 39 reports between them. They were not scheduler leaks: the shipping emitter cut memory
# management on purpose (src/compiler/README.md), so nothing it emitted ever gave anything
# back. Bindings, by-value parameters, list elements, struct fields, every string join,
# every value nobody binds, and finally the abort path — which moved every release onto the
# runtime's cleanup stack — have closed all 39.
#
# So a leak report here is now a REGRESSION rather than a known debt, and this gate is what
# says so. LeakSanitizer does not exist on macOS — asking for it there aborts the process
# before main — so that half of this gate is address + UB only, and Linux CI is where the
# leak half actually runs.
case "$(uname -s)" in
Linux)
	export ASAN_OPTIONS="detect_leaks=1:detect_stack_use_after_return=1"
	LEAKS="on"
	;;
*)
	export ASAN_OPTIONS="detect_leaks=0:detect_stack_use_after_return=1"
	LEAKS="off — LeakSanitizer is not available on $(uname -s)"
	;;
esac

WORK="$(mktemp -d "${TMPDIR:-/tmp}/zerg-sanitize.XXXXXX")" || exit 2

# THE RUNTIME IS COMPILED ONCE. It was inside the case loop — `$(rt_sources)` on every link — so
# the same ~14 sanitized translation units were rebuilt per case: fine at 20 concurrency cases,
# and 2,800 of them once `sanitize-corpus` brought the other 179. The flags never vary and the
# sources do not either, so the only thing a case needs is its own object linked against these.
#
# The `ls`/`uname` in rt_sources() is also once now rather than once per case.
mkdir -p "$WORK/rt"
RT_OBJS=""
for rsrc in $(rt_sources); do
	robj="$WORK/rt/$(basename "$rsrc").o"
	if ! $CC -std=c11 -g -fno-omit-frame-pointer \
		-fsanitize=address,undefined -fno-sanitize-recover=all \
		-I "$RT" -c "$rsrc" -o "$robj" 2>"$WORK/rt.cc.log"; then
		printf 'CC     the runtime does not build under the sanitizers\n' >&2
		head -10 "$WORK/rt.cc.log" >&2
		exit 1
	fi
	RT_OBJS="$RT_OBJS $robj"
done

printf "$GATE: address + undefined, leak detection %s, %s seeded single-worker schedules + %s multi-worker runs per case\n\n" "$LEAKS" "$SCHEDULES" "$RUNS"

fail=0
cases=0
seen_known=""

# KNOWN is a list of files naming the cases that report today, one `<case>TAB<allocator>` per
# line. Unset — which is how `sanitize-conc` runs — every report is a failure and nothing below
# changes. Set, the reports are held to the list instead, and the list is held back: see
# scripts/sanitize-leaks.txt for why it is a named set with a reason each rather than a count.
KNOWN=${KNOWN:-}

known_reason() {
	for kf in $KNOWN; do
		[ -f "$kf" ] || continue
		awk -F'\t' -v n="$1" '$1 == n { print $2; found = 1 } END { exit !found }' "$kf" && return 0
	done
	return 0
}

# How many cases must actually be measured, checked at the bottom. The submodule guard above
# catches an ABSENT test-data and nothing else: a shallow, partial or wrong-commit checkout
# has the directory, the `conc_*.zg` glob below matches one case or none, and every assertion
# in this script is of the form "this run was clean" — which is trivially true of a case that
# was never built. The gate then prints `0 cases x 60 seeded schedules ... clean` and exits 0.
#
# 12 against the 17 conc_ cases there are today: room for one to be renamed or retired
# without this needing a line, and well above what a broken checkout leaves behind. It is
# overridable because a CASES-narrowed run is deliberately measuring one case — hammering a
# rare multi-worker race is `CASES=... MIN_CASES=1`.
MIN_CASES=${MIN_CASES:-12}

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

	if ! $CC -std=c11 -g -fno-omit-frame-pointer \
		-fsanitize=address,undefined -fno-sanitize-recover=all \
		-I "$RT" -o "$WORK/$name.bin" "$WORK/$name.c" $RT_OBJS 2>"$WORK/$name.cc.log"; then
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
			# `WARNING: ASan is ignoring requested __asan_handle_no_return` IS a finding,
			# and this gate spent two days believing the opposite. It means ASan measured
			# the running stack against bounds that are not this coroutine's, gave up on
			# cleaning the shadow, and — the part no warning says out loud — is keeping
			# that coroutine's frames in a fake stack belonging to a WORKER THREAD, which
			# is unmapped the moment that worker stands down. A waiter parked on a channel
			# then sits in a hole in the address space, and the next walk of the queue
			# takes a SEGV with nothing for ASan to say about it.
			#
			# So it is matched, and a run that prints it fails. The annotations in sched.c
			# are what keep it quiet; if it comes back, they have been lost.
			if grep -Eq 'ERROR: .*Sanitizer|runtime error:|ASan is ignoring' "$WORK/$name.err"; then
				# THE KNOWN SET, when one is given. Without `KNOWN` this is what it always
				# was: a report is a failure. With it, the report is held to a LIST — the
				# contract scripts/sanitize-leaks.txt argues for, and the same three clauses
				# `oracle-check` holds its skips to.
				#
				# The reason is the first runtime frame that allocated, because a case that
				# starts leaking somewhere else is a new defect wearing an old name.
				listed=$(known_reason "$name")

				# the `?` that marks a host-dependent line is not part of the allocator
				listed=${listed#\?}
				if [ -n "$listed" ]; then
					why=$(grep -oE "in (zrt_[a-z_]+|buf_alloc|str_alloc) [^ ]*csrc/(fmt|str|map|list|ref|unwind)\.c:" "$WORK/$name.err" | head -1 | awk '{ print $2 }')
					if [ -n "$why" ] && [ "$why" != "$listed" ]; then
						printf 'REASON %s — listed as %s, and it now allocates in %s\n' "$name" "$listed" "$why"
						fail=1
					else
						seen_known="$seen_known $name"
					fi
					break 2
				fi
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
	printf "\n$GATE: $CORPUS is not clean under the sanitizers\n" >&2
	printf "$GATE: the C, the binaries and the full reports are kept in %s\n" "$WORK" >&2
	exit 1
fi
rm -rf "$WORK"
if [ "$cases" -lt "$MIN_CASES" ]; then
	printf "\n$GATE: only %s cases were measured, and the floor is %s\n" "$cases" "$MIN_CASES" >&2
	exit 1
fi

# THE OTHER DIRECTION, and the half that makes the list shrink. A line whose case no longer
# reports is a leak somebody fixed, and leaving it there means the next one to arrive under
# that name passes. It is checked only where LEAK DETECTION RAN: on macOS every line would
# read as fixed, which is a report about the platform and not about the code.
if [ -n "$KNOWN" ]; then
	stale=""
	for kf in $KNOWN; do
		[ -f "$kf" ] || continue
		while IFS="$(printf '\t')" read -r kname kwhy; do
			case $kname in '' | '#'*) continue ;; esac
			case " $seen_known " in *" $kname "*) continue ;; esac

			# A REASON THAT OPENS WITH `?` IS NOT HELD BY THIS HALF. The list is a set of
			# cases and it cannot say "on some hosts": `assert_claim` reports on linux/arm64
			# and not on linux/amd64, so a strict STALE turns a true line into a red board on
			# whichever architecture is not the one it was measured on. UNLISTED still covers
			# it — nothing new may leak anywhere — and what is given up is only the claim that
			# this particular line is still earning its place.
			case $kwhy in '?'*) continue ;; esac

			stale="$stale $kname"
		done <"$kf"
	done
	if [ "$LEAKS" = "on" ]; then
		if [ -n "$stale" ]; then
			printf "\n$GATE: these no longer report and their lines are still there —%s\n" "$stale" >&2
			printf "$GATE: delete each from the list; that deletion IS the gate for the fix\n" >&2
			exit 1
		fi
	else
		printf "$GATE: the known-report list was NOT re-checked — leak detection is off here\n"
	fi
fi
# The leak state is named HERE and not only in the header. `clean` on its own is the word a
# reader takes away, and on macOS it means clean of what address and undefined behaviour see,
# with leak detection off — the header that said so has scrolled past by then, and on the
# board it was never shown at all. Its neighbour `mem-check` ends with "no per-round leak",
# which names what was measured rather than declaring an absence.
known_note=""
[ -n "$KNOWN" ] && known_note=", $(printf '%s' "$seen_known" | wc -w | tr -d ' ') of them reporting as listed"
printf "\n$GATE: %s cases x %s seeded schedules + %s multi-worker runs%s, clean under address + undefined, leak detection %s\n" "$cases" "$SCHEDULES" "$RUNS" "$known_note" "$LEAKS"
