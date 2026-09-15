# shellcheck shell=bash
# runcmp.sh — running a program this toolchain produced, and holding it to what it should do.
#
# A run has THREE answers: what it wrote to stdout, what it wrote to stderr, and how it left.
# Six loops in this repository run a program and compare, and each one had decided for itself
# how many of the three to look at — so what a gate could SEE depended on which gate it was.
# The corpus compared stdout alone until an abort contract needed the exit status, and its
# message on stderr until seven cases turned out to be writing one nobody had recorded. Two
# sites threw stderr away outright: `install-check` ran the program the INSTALLED compiler
# built with `2>/dev/null`, and `release-tarball` did the same to the probe it derives the
# platform slug from — so a toolchain that had started writing to stderr shipped.
#
# The comparison itself is the other half of the same fact. `$(...)` strips every trailing
# newline from a command's output AND from the file it is compared with, so a program that
# stopped ending in a newline — or started ending in three — passed unchanged. Everything
# here compares FILES with `cmp`, and a caller that wants the text reads the file.
#
# WHAT IS NOT HERE is the wording. Each gate says what it found in its own voice, about its own
# corpus, and that is the part worth keeping apart; this file decides only WHICH questions are
# asked and holds the answer in a shape a caller can render.

# run_capture <prefix> <cmd>... — run the command with its streams kept apart: stdout in
# `<prefix>.out`, stderr in `<prefix>.err`. The command's own exit status is this function's,
# so a caller reads it the way it would read the program's.
run_capture() {
	local prefix=$1
	shift
	"$@" >"$prefix.out" 2>"$prefix.err"
}

# run_compare <prefix> <rc> <want-out> <want-err> <want-rc> — the three comparisons, over what
# `run_capture` left behind. It prints `<verdict>TAB<detail>` and returns 1 on the first
# disagreement, and prints nothing and returns 0 when all three agree.
#
#   <want-out>  a file to compare stdout against, or `-` to ask nothing of it
#   <want-err>  a file to compare stderr against — and its ABSENCE is not an excuse but a
#               claim: the program writes nothing to stderr, and a program that does gets
#               `UNPINNED-STDERR` naming the file the text belongs in. `-` is the same claim
#               where there is nowhere to record it: an example writes to stdout, full stop
#   <want-rc>   the exit status to expect, or `-` to ask nothing of it
run_compare() {
	local prefix=$1 rc=$2 want_out=$3 want_err=$4 want_rc=$5

	# HOW IT LEFT IS ASKED FIRST, because it is the coarsest of the three: a program that died
	# printed nothing, and reporting that as an OUTPUT that changed sends a reader to look at
	# the text rather than at the exit.
	if [ "$want_rc" != "-" ] && [ "$rc" != "$want_rc" ]; then
		printf 'STATUS\twant %s, got %s%s\n' "$want_rc" "$rc" \
			"$([ -s "$prefix.err" ] && printf ' — %s' "$(head -1 "$prefix.err")")"
		return 1
	fi
	if [ "$want_out" != "-" ] && ! cmp -s "$want_out" "$prefix.out"; then
		printf 'OUTPUT\t%s\n' "$(head -1 "$prefix.out")"
		return 1
	fi
	if [ "$want_err" != "-" ] && [ -f "$want_err" ]; then
		if ! cmp -s "$want_err" "$prefix.err"; then
			printf 'STDERR\t%s\n' "$(head -1 "$prefix.err")"
			return 1
		fi
	elif [ -s "$prefix.err" ]; then
		if [ "$want_err" = "-" ]; then
			printf 'STDERR\t%s\n' "$(head -1 "$prefix.err")"
		else
			printf 'UNPINNED-STDERR\t%s — put it in %s\n' "$(head -1 "$prefix.err")" "$want_err"
		fi
		return 1
	fi
	return 0
}

# run_same <prefix-a> <rc-a> <prefix-b> <rc-b> — the same three questions asked of two RUNS
# rather than of a run and a golden: the two compilers on one program, or the sugar and the
# core spelling of it. There is no expected answer here and none is needed — what is asserted
# is that the two agree, on all three, which is the assertion a recorded output cannot make
# about a program nobody has written the output down for.
run_same() {
	local a=$1 rc_a=$2 b=$3 rc_b=$4

	if ! cmp -s "$a.out" "$b.out"; then
		printf 'OUTPUT\t%s | %s\n' "$(head -1 "$a.out")" "$(head -1 "$b.out")"
		return 1
	fi
	if ! cmp -s "$a.err" "$b.err"; then
		printf 'STDERR\t%s | %s\n' "$(head -1 "$a.err")" "$(head -1 "$b.err")"
		return 1
	fi
	if [ "$rc_a" != "$rc_b" ]; then
		printf 'STATUS\t%s | %s\n' "$rc_a" "$rc_b"
		return 1
	fi
	return 0
}

# runcmp_self_test — the comparisons still answer as this file documents. They are the shape
# of assertion that fails OPEN: every one of them reports success when it matches nothing, so
# a rewrite that broke `run_compare` would leave six loops comparing two empty files and
# calling it agreement. Each gate runs this before it measures anything, exactly as
# scripts/lib/grammar.sh and scripts/lib/ledger.sh are run before theirs.
runcmp_self_test() {
	local dir bad=0 got rc
	dir=$(mktemp -d) || return 1

	want() {
		if [ "$2" != "$3" ]; then
			printf 'SELFTEST  %s: wanted [%s], got [%s]\n' "$1" "$3" "$2"
			bad=1
		fi
	}

	printf 'hello\n' >"$dir/want.out"

	run_capture "$dir/a" printf 'hello\n'
	rc=$?
	got=$(run_compare "$dir/a" "$rc" "$dir/want.out" "$dir/nosuch.err" 0 | cut -f1)
	want "a program that agrees on all three says nothing" "$got" ""

	run_capture "$dir/b" sh -c 'printf "hello\n"; printf "a word\n" >&2'
	rc=$?
	got=$(run_compare "$dir/b" "$rc" "$dir/want.out" "$dir/nosuch.err" 0 | cut -f1)
	want "an unrecorded word on stderr is a finding" "$got" "UNPINNED-STDERR"

	run_capture "$dir/c" sh -c 'printf "hello\n"; exit 3'
	rc=$?
	got=$(run_compare "$dir/c" "$rc" "$dir/want.out" "$dir/nosuch.err" 0 | cut -f1)
	want "an exit status that moved is a finding" "$got" "STATUS"

	run_capture "$dir/d" printf 'hello'
	rc=$?
	got=$(run_compare "$dir/d" "$rc" "$dir/want.out" "$dir/nosuch.err" 0 | cut -f1)
	want "a trailing newline that went missing is a finding" "$got" "OUTPUT"

	run_capture "$dir/e" sh -c 'printf "hello\n"; printf "why\n" >&2; exit 2'
	rc=$?
	got=$(run_compare "$dir/e" "$rc" "$dir/want.out" "$dir/nosuch.err" 0)
	want "how it left is reported before what it printed" "$(printf '%s' "$got" | cut -f1)" "STATUS"
	want "and the first line of stderr comes with it" "$(printf '%s' "$got" | cut -f2-)" "want 0, got 2 — why"

	got=$(run_compare "$dir/b" 0 "$dir/want.out" - 0 | cut -f1)
	want "a program with nowhere to record stderr must still be silent" "$got" "STDERR"

	got=$(run_same "$dir/a" 0 "$dir/b" 0 | cut -f1)
	want "two runs that differ on stderr alone are not the same run" "$got" "STDERR"

	got=$(run_same "$dir/a" 0 "$dir/a" 1 | cut -f1)
	want "two runs that differ on the exit status alone are not the same run" "$got" "STATUS"

	rm -rf "$dir"
	[ $bad -eq 0 ] && return 0
	echo "the comparisons no longer behave as scripts/lib/runcmp.sh documents,"
	echo "so every run compared below would have been compared with less than three questions"
	return 1
}
