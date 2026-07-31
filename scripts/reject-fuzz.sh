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
unbuildable=0

# How many refusals may still carry no place: every one of them is from the parser or the
# emitter, which do not report through chk_at yet.
#
# PER KIND, and one kind is deliberately not capped. A single scalar over six mutation
# kinds and a growing corpus had to be raised for reasons that were not regressions —
# 37 -> 51 for a new kind, -> 53 for teaching that kind to skip declarations, -> 54, 56, 57
# for corpus cases — and each raise buried whatever else moved. Worse, one kind losing
# places while another gained them keeps the total flat, which is the one thing the total
# cannot see.
#
# `extra-arg` is REPORTED and not capped. It is dominated by the parser's and the emitter's
# `NotImplemented` refusals, which are a known gap tracked as a whole rather than through
# this gate, and it grows by about one per corpus file — so a ceiling on it measures how
# many test programs exist. The other five are the check.zg rules, which DO report a place,
# and there the number is small, meaningful, and may only go down.

NOPLACE_MAX_missing_arg=${NOPLACE_MAX_missing_arg:-7}
NOPLACE_MAX_wrong_type=${NOPLACE_MAX_wrong_type:-0}
NOPLACE_MAX_write_immutable=${NOPLACE_MAX_write_immutable:-3}
NOPLACE_MAX_int_condition=${NOPLACE_MAX_int_condition:-1}
NOPLACE_MAX_mixed_operands=${NOPLACE_MAX_mixed_operands:-0}

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
	"$ZERG" build --emit c "$src" >/dev/null 2>&1 || { unbuildable=$((unbuildable + 1)); continue; }

	for kind in extra-arg missing-arg wrong-type write-immutable int-condition mixed-operands; do
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
		# a refusal SAYS something. An internal abort, a panic, an empty message — none has
		# a place either, so without this they would land in `noplace` and pass under a
		# ceiling that exists to watch a different thing entirely.
		if [ -z "$out" ]; then
			echo "SILENT    $name/$kind — non-zero exit and nothing said"
			fail=$((fail + 1))
			continue
		fi

		if ! has_place "$out"; then
			noplace=$((noplace + 1))
			eval "noplace_${kind//-/_}=\$(( \${noplace_${kind//-/_}:-0} + 1 ))"
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
echo "reject-fuzz: $noplace of them said no place ($skip mutations did not apply, $unbuildable sources skipped)"

# PER KIND, because a single scalar over six kinds hides an offset: one kind losing places
# while another gains them keeps the total flat, and the total is the only thing the ceiling
# below can see. The breakdown is what a raise has to be argued from.
for kind in extra-arg missing-arg wrong-type write-immutable int-condition mixed-operands; do
	eval "n=\${noplace_${kind//-/_}:-0}"
	echo "reject-fuzz:   $kind $n"
done

# A RATCHET, in the shape CORPUS_PASS uses. A count that is only printed drifts: 37 becomes
# 60 and the gate still says OK. These can only go down, and the day they reach zero the
# report above becomes an assertion.
rc=0
for kind in missing-arg wrong-type write-immutable int-condition mixed-operands; do
	eval "n=\${noplace_${kind//-/_}:-0}"
	eval "cap=\${NOPLACE_MAX_${kind//-/_}}"
	if [ "$n" -gt "$cap" ]; then
		echo "reject-fuzz: $kind has $n refusals with no place, and its ceiling is $cap"
		rc=1
	fi
done
exit $rc
