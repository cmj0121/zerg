#!/usr/bin/env bash
#
# reject-fuzz — take the programs that WORK and break them on purpose.
#
# scripts/reject-check.sh holds 54 ill-formed programs, every one of them written by hand.
# That is the shape of its blind spot: it contains the mistakes somebody thought of. This
# takes the corpus's WELL-FORMED programs — the ones that must compile and run — applies a
# mutation that makes each one ill-formed in a way the language has a rule about, and holds
# the result to the same contract every rejection here owes:
#
#   1. a non-zero exit
#   2. NOT a cc diagnostic (cc opens a line `path:line:col: error:`; this compiler opens
#      with `error:` and puts the place on an indented `-->` beneath)
#   3. a `--> file:line:col` of its own — REPORTED, not enforced, until the parser's and
#      the emitter's refusals carry one
#
# What it does NOT assert is WHICH rule fires. A mutation can break a program in more ways
# than the one it aimed at, and pinning the sentence would make this a test of the mutator
# rather than of the contract. The sentence is reject-check's job; this is about the
# contract holding for inputs nobody chose.
#
# The mutations are textual and deliberately dumb. A generator that understood the language
# would produce programs a person never writes; a mutated real program is one somebody
# nearly wrote.

set -u

# shellcheck source=scripts/lib/diag.sh
. "$(dirname "$0")/lib/diag.sh"

ZERG=${ZERG:-./bin/zerg}
CORPUS=${CORPUS:-test-data/codegen}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

pass=0
fail=0
skip=0
noplace=0

# how many refusals may still carry no place: every one of them is from the parser or the
# emitter, which do not report through chk_at yet. Lower it, never raise it.
NOPLACE_MAX=${NOPLACE_MAX:-37}

# mutate writes the mutated program to stdout, or exits non-zero when the mutation does not
# apply to this source. It is awk rather than sed because the patterns need the indentation
# and the optional `mut` in the same expression, and every attempt to write that as nested
# shell quoting produced a script that matched almost nothing and reported success.
mutate() {
	awk -v KIND="$2" -f scripts/reject-fuzz.awk "$1"
}

for src in "$CORPUS"/*.zg; do
	name="$(basename "$src" .zg)"

	# only programs this compiler already accepts: a case it cannot build says nothing
	# about what it does with a broken one
	"$ZERG" build --emit c "$src" >/dev/null 2>&1 || continue

	for kind in extra-arg wrong-type write-immutable int-condition mixed-operands; do
		out_zg="$tmp/$name.$kind.zg"
		if ! mutate "$src" "$kind" >"$out_zg"; then
			skip=$((skip + 1))
			continue
		fi

		out=$("$ZERG" build --emit bin -o "$tmp/$name.$kind.bin" "$out_zg" 2>&1 >/dev/null)
		if [ $? -eq 0 ]; then
			# a mutation that happens to leave a valid program is not a finding
			skip=$((skip + 1))
			continue
		fi

		if is_cc_diag "$out"; then
			echo "VIA CC    $name/$kind — cc reported it, not the compiler"
			echo "$out" | head -3
			fail=$((fail + 1))
			continue
		fi
		# REPORTED, NOT ENFORCED — the same shape as CORPUS_PASS. A place is owed by every
		# diagnostic and the parser's and the emitter's refusals do not have one yet; a
		# gate that fails on a known gap is not a gate, and a gate that says nothing about
		# it lets the gap grow. The count is the thing to watch: when it reaches zero this
		# becomes an assertion.
		if ! has_place "$out"; then
			noplace=$((noplace + 1))
			continue
		fi
		pass=$((pass + 1))
	done
done

if [ $fail -ne 0 ]; then
	echo "reject-fuzz: $fail mutation(s) reached cc instead of being refused by the compiler"
	exit 1
fi
echo "reject-fuzz: $pass+$noplace mutated programs refused by the compiler, none left to cc"
echo "reject-fuzz: $noplace of them said no place ($skip mutations did not apply)"

# A RATCHET, in the shape CORPUS_PASS uses. A count that is only printed drifts: 37 becomes
# 60 and the gate still says OK. This can only go down, and the day it reaches zero the
# report above becomes an assertion.
if [ "$noplace" -gt "$NOPLACE_MAX" ]; then
	echo "reject-fuzz: $noplace refusals say no place, and the ceiling is $NOPLACE_MAX"
	exit 1
fi
