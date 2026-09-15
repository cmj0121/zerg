#!/usr/bin/env bash
#
# examples-check — every example builds, is checked, runs, and prints what its file says.
#
#   usage: examples-check.sh <example source>...
#          REFUSED="<path>..."  the examples that must be turned away instead
#
# An example is a claim about the language, and this is what holds it to the claim. The list of
# sources is a GLOB the makefile expands and hands over, because `EXAMPLE_SRCS` is also what the
# linter and the tree-sitter gate walk, and a scope written twice goes stale on whichever copy
# the next directory is not added to.
#
# CHECKED AS WELL AS BUILT, because they are not the same walk. `--emit bin` loads the program
# unit by unit (cmd/unit.zg) and every other stage loads it whole (cmd/source.zg), so a rule
# that reads what an import RESOLVED to can be right in one loader and wrong in the other —
# `examples/1g/siblings` built and ran and printed the right three lines while `--emit check`
# refused it, because only one of the two loaders knew a sibling import loads nothing (#57).
# The check costs no `cc`, and it is the stage an editor runs on every save.
#
# THE EXPECTED OUTPUT LIVES IN THE PRIVATE SUBMODULE, at `test-data/examples/<the example's
# path>.out`. `examples/` is what a reader opens, and a reader opens it for PROGRAMS: a `.out`
# beside a `.zg` is this project's test fixture sitting in the middle of somebody else's
# tutorial, and it is corpus content like every other expected output in this tree.
#
# IT IS OPT-IN twice over. A file that is not there is not compared, which is what the
# concurrent examples need — `11_coroutines` interleaves, and pinning one interleaving would be
# a gate that fails on a correct program — and it is also what a checkout WITHOUT the submodule
# gets: every example still builds, is checked and is run, and only the comparison is missing.
# So this gate is not one of the fourteen behind the corpus fetch, and "every example owes an
# expectation" is asked only where the expectations can be. Requiring the file unconditionally
# is what broke CI on every job that does not fetch the corpus.
#
# THE RUN IS COMPARED THROUGH scripts/lib/runcmp.sh, which is where this repository decides what
# comparing a run means — all three answers, against files rather than through `$(...)`. This
# loop used to hold the three apart by hand: a non-zero exit was `RUN`, the output went through
# `diff -q`, and stderr was asserted empty in a line of its own.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

# shellcheck source=scripts/lib/runcmp.sh
. "$ROOT/scripts/lib/runcmp.sh"

ZERG=${ZERG:-./bin/zerg}
OUTDIR=${OUTDIR:-bin/examples}
CORPUS=${CORPUS:-test-data/examples}
REFUSED=${REFUSED:-}

# THE FLOOR a shrinking glob runs into. The guard against a mistyped pattern is not that the
# loop fails, it is that the loop has nothing to iterate and says so happily.
MIN_EXAMPLES=${MIN_EXAMPLES:-40}

# AND THE SECOND FLOOR, for the comparison rather than the run. The comparison is silent when a
# file is absent, so a renamed directory or a moved corpus turns the whole assertion off and
# leaves a target that reports "39 examples built and run" having compared none of them. This is
# what tells that apart from the concurrent examples legitimately having no file.
MIN_COMPARED=${MIN_COMPARED:-36}

[ -x "$ZERG" ] || {
	printf 'examples: %s is not built — run `make build` first\n' "$ZERG" >&2
	exit 2
}

runcmp_self_test || exit 1

mkdir -p "$OUTDIR"
have_out=1
[ -d "$CORPUS" ] || have_out=0

fail=0
n=0
compared=0

slug() { printf '%s' "$1" | sed 's|^examples/||; s|/|_|g; s|\.zg$||'; }

for src in "$@"; do
	case " $REFUSED " in *" $src "*) continue ;; esac

	out="$OUTDIR/$(slug "$src")"
	if ! "$ZERG" build "$src" --emit check >/dev/null 2>&1; then
		printf 'CHECK  %s\n' "$src"
		fail=1
		continue
	fi
	if ! "$ZERG" build "$src" --emit bin -o "$out" >/dev/null 2>&1; then
		printf 'BUILD  %s\n' "$src"
		fail=1
		continue
	fi

	run_capture "$OUTDIR/got" "$out"
	rc=$?

	want="$CORPUS/$(printf '%s' "$src" | sed 's|^examples/||; s|\.zg$|.out|')"
	if [ ! -f "$want" ]; then
		if [ "$have_out" -eq 1 ]; then
			printf 'NO-OUT %s — an example a reader copies owes what it prints\n' "$src"
			fail=1
			continue
		fi
		# no expectation to compare against, and still held to leaving cleanly and quietly:
		# an example prints to stdout.
		want=-
	fi

	if ! verdict=$(run_compare "$OUTDIR/got" "$rc" "$want" - 0); then
		printf '%s %s — %s\n' "$(printf '%s' "$verdict" | cut -f1)" "$src" \
			"$(printf '%s' "$verdict" | cut -f2-)"
		fail=1
		continue
	fi

	[ "$want" = - ] || compared=$((compared + 1))
	n=$((n + 1))
done

# A NEGATIVE example is a claim a build-and-run loop cannot check: it can only report that the
# build failed, which is what a typo does too. So the refusal is held to what it SAYS and to
# carrying a place, which is the same standard `make reject` holds its own cases to.
#
# WHAT IT SAYS BELONGS TO THE EXAMPLE, not to a shared substring. That held while all of them
# were refused for one reason and would stop the day a second reason was written, so it lives
# beside the expected output as `test-data/examples/<path>.refused`.
for src in $REFUSED; do
	if say=$("$ZERG" build "$src" --emit bin -o "$OUTDIR/refused" 2>&1); then
		printf 'BUILT  %s (it must be refused)\n' "$src"
		fail=1
		continue
	fi
	want="$CORPUS/$(printf '%s' "$src" | sed 's|^examples/||; s|\.zg$|.refused|')"
	if [ -f "$want" ]; then
		printf '%s\n' "$say" | grep -qF "$(cat "$want")" || {
			printf 'SAID   %s: %s\n' "$src" "$say"
			fail=1
			continue
		}
	fi
	printf '%s\n' "$say" | grep -q "$(basename "$src"):" || {
		printf 'PLACE  %s said no file:line:col\n' "$src"
		fail=1
		continue
	}
	n=$((n + 1))
done

[ "$fail" -eq 0 ] || {
	printf 'examples: an example no longer builds, or no longer runs\n' >&2
	exit 1
}
[ "$n" -ge "$MIN_EXAMPLES" ] || {
	printf 'examples: only %s were built, and the floor is %s\n' "$n" "$MIN_EXAMPLES" >&2
	exit 1
}

if [ "$have_out" -eq 1 ]; then
	[ "$compared" -ge "$MIN_COMPARED" ] || {
		printf 'examples: the corpus is there and only %s outputs were compared, floor %s — this gate is measuring nothing\n' \
			"$compared" "$MIN_COMPARED" >&2
		exit 1
	}
	printf 'examples: %s examples built and run, %s held to what they print\n' "$n" "$compared"
else
	printf 'examples: %s examples built and run (test-data not initialized — no output was compared)\n' "$n"
fi
