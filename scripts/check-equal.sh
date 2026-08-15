#!/usr/bin/env bash
#
# check-equal — `--emit check` and `--emit c` must find the SAME things.
#
# `--emit check` exists because asking "is this a program" used to cost a full lowering to
# C: the rules live inside the emitting walk, so the language server's only way to reach
# them was to build 3.6 MB of C it never read, at 2.8 GB of peak memory, on every check.
# The check stage runs that same walk with the text dropped instead of accumulated.
#
# THE HAZARD IS NOT THE SPEEDUP, it is a path that misses a finding. A check that reports
# less than the build reports paints a clean buffer for a file that will not compile —
# which is worse than a slow one, because the editor is then actively lying. So the gate is
# not "check is fast" and not "check refuses bad programs"; it is that for every program in
# the corpus the two stages print the SAME DIAGNOSTICS, byte for byte, in the same order,
# and exit the same way.
#
# Byte for byte is deliberate. Comparing the set of codes would let a place drift, comparing
# the codes and places would let the order drift, and the order is what a person reads
# first. There is one output to compare and no reason to compare less of it.
#
# The corpus is two halves, because a program with no findings compares two empty strings
# and proves nothing:
#
#   the corpus AS WRITTEN     well-formed programs, which pin that neither stage INVENTS a
#                             finding the other does not have
#   the corpus MUTATED        the same programs broken by scripts/reject-fuzz.awk — the six
#                             mutations that gate reject-fuzz — which is where the findings
#                             come from, at a scale nobody would hand-write
#
# and the floor at the bottom counts the second half specifically. Every assertion here is
# "these two agree", which is trivially true of two empty outputs: a mutator that stopped
# applying, a renamed corpus, a submodule at the wrong commit, and this reports success for
# having compared nothing that could have differed.

set -u

ZERG=${ZERG:-./bin/zerg}
CORPUS=${CORPUS:-test-data/codegen}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

same=0
withdiag=0
fail=0
unbuildable=0
skip=0

# compare <label> <source> — put one program to both stages and hold their reports to each
# other. Returns non-zero when they differ.
#
# `-o` on the emit side, so its C goes to a file rather than onto the stream being compared;
# the check side produces no artifact and takes none. What is compared is stderr, which is
# where every diagnostic and every abort goes, plus the exit status.
compare() {
	label=$1
	src=$2

	"$ZERG" build --emit c "$src" -o "$tmp/emit.c" 2>"$tmp/emit.err" >/dev/null
	rc_emit=$?
	"$ZERG" build --emit check "$src" 2>"$tmp/check.err" >/dev/null
	rc_check=$?

	if [ "$rc_emit" -ne "$rc_check" ]; then
		echo "STATUS    $label — --emit c exited $rc_emit, --emit check exited $rc_check"
		return 1
	fi
	if ! diff -u "$tmp/emit.err" "$tmp/check.err" >"$tmp/delta"; then
		echo "DIFFERS   $label — the two stages did not report the same findings"
		sed -n '1,12p' "$tmp/delta"
		return 1
	fi
	[ -s "$tmp/emit.err" ] && withdiag=$((withdiag + 1))
	same=$((same + 1))
	return 0
}

[ -d "$CORPUS" ] || {
	echo "check-equal: $CORPUS does not exist (git submodule update --init)"
	exit 1
}

for src in "$CORPUS"/*.zg; do
	name="$(basename "$src" .zg)"

	compare "$name" "$src" || fail=$((fail + 1))

	# only programs this compiler already accepts get mutated: a case it cannot build for
	# reasons of its own says nothing about what the two stages do with a broken one.
	if ! "$ZERG" build --emit check "$src" >/dev/null 2>&1; then
		unbuildable=$((unbuildable + 1))
		continue
	fi

	for kind in extra-arg missing-arg wrong-type write-immutable int-condition mixed-operands; do
		out_zg="$tmp/$name.$kind.zg"
		if ! awk -v KIND="$kind" -f scripts/reject-fuzz.awk "$src" >"$out_zg"; then
			skip=$((skip + 1))
			continue
		fi
		compare "$name/$kind" "$out_zg" || fail=$((fail + 1))
	done
done

if [ "$fail" -ne 0 ]; then
	echo "check-equal: $fail program(s) where --emit check and --emit c disagree"
	exit 1
fi

echo "check-equal: $same programs, both stages identical ($skip mutations did not apply, $unbuildable sources skipped)"
echo "check-equal: $withdiag of them reported findings"

# THE FLOOR, and it counts the programs that HAD something to report. A run where every
# comparison was between two empty outputs has proved that neither stage invents a finding
# and nothing at all about whether either one loses one — which is the failure this gate
# exists for.
if [ "$withdiag" -lt "${MIN_WITH_DIAG:-100}" ]; then
	echo "check-equal: only $withdiag programs produced any diagnostic — the mutator is not applying"
	exit 1
fi
