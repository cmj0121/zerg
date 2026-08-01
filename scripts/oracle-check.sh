#!/usr/bin/env bash
#
# oracle-check — the seed and the shipping compiler must AGREE about a valid program.
#
# reject-check already makes `zerg0` the oracle for programs that must be turned away, and
# its header says why: a rule the seed enforces and `zerg` does not is a rule `zerg` LOST on
# the way to self-hosting. That argument does not stop at rejections. A behaviour the seed
# IMPLEMENTS and `zerg` dropped is the same loss, and it is worse, because the program still
# compiles, still runs, and prints a different answer.
#
# That is not hypothetical. `int("42")` parsed a string in the seed and cast the string's
# POINTER to an integer in `zerg` — one compiler printed 42, the other printed an address —
# and every gate in this repo was green. `make build` only needs the compiler to compile
# itself, and the compiler's own source happens not to write `int(s)`; `make examples` gates
# on the EXIT STATUS, and a wrong number exits 0; the corpus is compiled by `zerg` alone, so
# it pins `zerg` against itself. Nothing compared the two.
#
# So: build each program with BOTH, run both, and compare what they print.
#
# The seed is deliberately the NARROWER compiler — it has no `#[dyn]`, no closures capture,
# and several examples say so in their own header. A program it cannot build is SKIPPED, not
# failed; its gaps are its own contract (src/bootstrap/README.md). What is not tolerated is
# two compilers that both accept a program and disagree about it.
#
# The floor at the bottom is the point of the whole script. Every assertion here is of the
# form "these two agree", which is trivially true of an empty set — a renamed directory, a
# seed that stopped building anything, a glob that matches nothing, and this reports success
# for having compared no programs at all.

set -u

ZERG=${ZERG:-./bin/zerg}
ZERG0=${ZERG0:-./bin/zerg0}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

same=0
skip=0
named=0
fail=0

for src in "$@"; do
	name="$(echo "$src" | sed 's|^\./||; s|/|_|g; s|\.zg$||')"

	# the SEED first: it is the narrower compiler, so it decides whether this program is
	# comparable at all, and its failure is a skip rather than a finding
	if ! "$ZERG0" build --emit bin -o "$tmp/$name.0" "$src" >/dev/null 2>&1; then
		skip=$((skip + 1))
		continue
	fi
	# The seed is narrower in most places and WIDER in a few — generics, `derive` — and
	# `zerg` names each of those, which is the implemented-or-named contract rather than a
	# divergence. A refusal that names itself is counted and tolerated; any other way of
	# failing a program the seed compiles is the loss this gate looks for.
	if ! err=$("$ZERG" build --emit bin -o "$tmp/$name.1" "$src" 2>&1 >/dev/null); then
		if [ "${err#*NotImplemented}" != "$err" ]; then
			named=$((named + 1))
			continue
		fi
		echo "BUILD     $src — the seed built it and the shipping compiler did not, without naming a form"
		echo "  $(echo "$err" | head -1)"
		fail=$((fail + 1))
		continue
	fi

	# stdout AND the exit status: a program that aborts prints nothing, and two compilers
	# agreeing on the empty string while one raised and the other returned 0 is the exact
	# shape of an overflow check that went missing
	out0=$("$tmp/$name.0" 2>/dev/null)
	rc0=$?
	out1=$("$tmp/$name.1" 2>/dev/null)
	rc1=$?

	if [ "$out0" = "$out1" ] && [ "$rc0" -eq "$rc1" ]; then
		same=$((same + 1))
		continue
	fi
	echo "DIFFER    $src — the two compilers do not agree"
	echo "  zerg0 (rc $rc0): $(echo "$out0" | head -3 | tr '\n' '|')"
	echo "  zerg  (rc $rc1): $(echo "$out1" | head -3 | tr '\n' '|')"
	fail=$((fail + 1))
done

if [ $fail -ne 0 ]; then
	echo "oracle-check: $fail program(s) mean different things to the two compilers"
	exit 1
fi

if [ "$same" -lt "${MIN_COMPARED:-8}" ]; then
	echo "oracle-check: only $same programs were comparable — the seed is not building, or the list is empty"
	exit 1
fi
echo "oracle-check: $same programs agree between the seed and the shipping compiler"
echo "oracle-check: $skip the seed cannot build, $named the shipping compiler names as not built"
