#!/usr/bin/env bash
#
# corpus-check — run `zerg` against the test-data corpus it owns, and hold the skip list back.
#
# The test-data corpus belongs to the self-hosting compiler: it describes the LANGUAGE, which
# is what `zerg` is growing toward, while the seed is covered by its own unit tests.
#
# EVERY CASE IS GATED except the ones scripts/corpus-skips.txt names, and that file carries the
# argument for why the list is a denylist rather than an allowlist. What this script adds is
# that the list is now held to all three of a ledger's clauses rather than to one, and that the
# one it had asked about an exit status rather than about a rule: see the ledger file's header.
#
# A case's THREE ANSWERS are compared — what it prints, what it writes to stderr, and how it
# leaves — through scripts/lib/runcmp.sh, which is the one place in this repository that decides
# what comparing a run means. This loop is where two of those three were learned the hard way:
# the abort contract's exit status was invisible until a case that stopped aborting passed, and
# seven cases were writing a message on stderr that nothing had recorded, so the whole of what a
# reader sees when a Zerg program dies could have changed and every gate stayed green.
#
# It was a recipe in mk/gates.mk. It is a script because a recipe cannot source a library, and
# both of the libraries above exist so that six loops stop each having their own answer.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

# shellcheck source=scripts/lib/ledger.sh
. "$ROOT/scripts/lib/ledger.sh"
# shellcheck source=scripts/lib/runcmp.sh
. "$ROOT/scripts/lib/runcmp.sh"

ZERG=${ZERG:-./bin/zerg}
DIR=${DIR:-test-data/codegen}
SKIPS=${SKIPS:-"scripts/corpus-skips.txt test-data/corpus-skips.txt"}

# A `conc_` case is run more than once. Every other case is a function of its source, so one
# run answers the question; a concurrent one is a function of its source AND of an interleaving
# the scheduler picks fresh each time, and a race that shows up one run in twenty would sail
# through a single attempt. Repetition is what makes it a gate rather than a coin toss. They
# are milliseconds each, so the whole corpus stays quick.
CONC_REPS=${CONC_REPS:-10}

# A FLOOR under how many cases the gate has to have run, of the kind fmt-tokens and reject-fuzz
# carry. The submodule guard below catches an ABSENT corpus and nothing else; a shallow, partial
# or wrong-commit checkout leaves the directory THERE with a handful of cases in it, the glob
# shrinks to match, and the gate reports `1/1 cases pass` and exits 0 — success for having
# measured almost nothing, which is the one failure that still looks like a corpus.
#
# 60 against the 210 that pass today. The gap is room for cases to move into the skip list while
# they wait for a feature, so that adding a case for something `zerg` cannot build yet is not
# also a chore here; it is nowhere near the two or three a broken checkout leaves behind.
MIN_CASES=${MIN_CASES:-60}

[ -x "$ZERG" ] || {
	printf 'corpus: %s is not built — run `make build` first\n' "$ZERG" >&2
	exit 2
}
[ -d "$DIR" ] || {
	printf 'corpus: test-data submodule not initialized (git submodule update --init)\n' >&2
	exit 2
}

ledger_self_test || exit 1
runcmp_self_test || exit 1

tmp=$(mktemp -d) || exit 2
trap 'rm -rf "$tmp"' EXIT

fail=0
ran=0
total=0

# THE LEDGER, NORMALISED TO THE CODE. A line reads `<case>TAB<code> <sentence>`: the code is
# what the clauses compare, because it is what the compiler answers with, and the sentence
# beside it is for a person reading the file. Comparing the whole reason would make a reworded
# sentence look like a rule that moved.
# shellcheck disable=SC2086 # $SKIPS is a list of ledger paths and is meant to split
ledger_read $SKIPS | awk -F'\t' '{ n = split($2, w, " "); print $1 "\t" w[1] }' >"$tmp/ledger"
cut -f1 "$tmp/ledger" | sort -u >"$tmp/skipped"

for src in "$DIR"/*.zg; do
	name="$(basename "$src" .zg)"
	total=$((total + 1))
	grep -Fqx "$name" "$tmp/skipped" && continue

	if ! "$ZERG" build --emit bin -o "$tmp/case" "$src" >/dev/null 2>&1; then
		printf 'BUILD  %s\n' "$name"
		fail=1
		continue
	fi

	want_rc=0
	[ -f "$DIR/$name.rc" ] && want_rc=$(cat "$DIR/$name.rc")
	reps=1
	case $name in conc_*) reps=$CONC_REPS ;; esac

	n=0
	while [ "$n" -lt "$reps" ]; do
		run_capture "$tmp/got" "$tmp/case"
		rc=$?
		if ! verdict=$(run_compare "$tmp/got" "$rc" "$DIR/$name.out" "$DIR/$name.err" "$want_rc"); then
			printf '%s %s (run %s) — %s\n' "$(printf '%s' "$verdict" | cut -f1)" \
				"$name" "$n" "$(printf '%s' "$verdict" | cut -f2-)"
			fail=1
			break
		fi
		n=$((n + 1))
	done
	[ "$n" -eq "$reps" ] && ran=$((ran + 1))
done

[ "$fail" -eq 0 ] || {
	printf 'corpus: a case that used to pass regressed\n' >&2
	exit 1
}
[ "$ran" -ge "$MIN_CASES" ] || {
	printf 'corpus: only %s cases were run, and the floor is %s\n' "$ran" "$MIN_CASES" >&2
	exit 1
}

# --- the skip list, held in all three directions ---------------------------------------
#
# What is OBSERVED is each listed case put to the compiler again, and the code it is refused
# with. A case that builds contributes no observation, which is what makes it STALE below.
: >"$tmp/observed"
while read -r name; do
	[ -n "$name" ] || continue
	[ -f "$DIR/$name.zg" ] || {
		printf 'corpus: the skip list names %s and %s/%s.zg is not there\n' "$name" "$DIR" "$name" >&2
		fail=1
		continue
	}
	# THREE OUTCOMES, NOT TWO. It BUILDS — no observation, which is what makes the line STALE
	# below. It is refused WITH A CODE — that is the observation, and the MOVED clause compares
	# it. Or it fails with NO code at all, which is neither: `cc` absent, the case unparseable,
	# the corpus at a commit this compiler cannot read. That third outcome used to be indexed
	# as the second and read as "still waiting for its feature", which is the whole reason the
	# line names a code rather than an exit status.
	if say=$("$ZERG" build --emit bin -o "$tmp/skip" "$DIR/$name.zg" 2>&1 >/dev/null); then
		continue
	fi
	code=$(printf '%s\n' "$say" | grep -oE 'E[0-9]{4}' | head -1)
	if [ -z "$code" ]; then
		printf 'SKIPPED-NO-CODE %s — it fails and names no rule: %s\n' \
			"$name" "$(printf '%s' "$say" | head -1)"
		fail=1
		continue
	fi
	printf '%s\t%s\n' "$name" "$code" >>"$tmp/observed"
done <"$tmp/skipped"

while IFS="$(printf '\t')" read -r name code; do
	# shellcheck disable=SC2086 # $SKIPS is a list of ledger paths and is meant to split
	printf 'SKIPPED-BUT-BUILDS %s — it builds now, so delete its line; that deletion IS the gate for %s\n' \
		"$name" "$(ledger_lookup "$name" $SKIPS)"
	fail=1
done < <(ledger_stale "$tmp/observed" "$tmp/skipped" "$tmp/ledger")

while IFS="$(printf '\t')" read -r name was now; do
	printf 'SKIPPED-OTHER-RULE %s — its line says %s and the compiler answers %s\n' "$name" "$was" "$now"
	fail=1
done < <(ledger_moved "$tmp/observed" "$tmp/skipped" "$tmp/ledger")

[ "$fail" -eq 0 ] || {
	printf 'corpus: the skip list no longer says what the compiler does\n' >&2
	exit 1
}

printf 'corpus: %s/%s cases pass, and %s are still refused by the code their line names\n' \
	"$ran" "$total" "$(grep -c . "$tmp/skipped")"
