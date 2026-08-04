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
# The skip inventory, one file per corpus. Each lives WITH the programs it accounts for:
# examples/ is public and test-data is a private submodule, and a case name is corpus content
# — a public file listing thirty of them would be the corpus leaking out one line at a time.
# A checkout without the submodule is not passed those programs either, so the two halves stay
# in step.
SKIPS=${SKIPS:-"scripts/oracle-skips.txt test-data/oracle-skips.txt"}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

same=0
skip=0
named=0
fail=0

# seed_reason is WHY the seed turned a program away, with the file and the line stripped off.
# The position moves every time the program above it is edited; the sentence is what says
# whether this is the same gap as yesterday.
seed_reason() {
	"$ZERG0" build --emit bin -o "$tmp/probe" "$1" 2>&1 |
		head -1 |
		sed -E 's|^.*\.zg:[0-9]+:[0-9]+: ||'
}

: >"$tmp/skipped"
: >"$tmp/seen"

for src in "$@"; do
	name="$(echo "$src" | sed 's|^\./||; s|/|_|g; s|\.zg$||')"
	echo "$src" >>"$tmp/seen"

	# the SEED first: it is the narrower compiler, so it decides whether this program is
	# comparable at all, and its failure is a skip rather than a finding
	if ! "$ZERG0" build --emit bin -o "$tmp/$name.0" "$src" >/dev/null 2>&1; then
		skip=$((skip + 1))
		printf '%s\t%s\n' "$src" "$(seed_reason "$src")" >>"$tmp/skipped"
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

# --- the skipped set is a CONTRACT, not a bucket -----------------------------------
#
# Everything above compares the programs BOTH compilers build. What the seed turns away is
# skipped, and until this section existed that was where a whole class of finding went to be
# invisible: the seed is the narrower compiler on most of the language and the CORRECT one on
# some of it, so "the seed refuses this and `zerg` does not" is either a gap the seed is
# allowed to have or a rule `zerg` LOST — and one number at the bottom of the run cannot tell
# them apart. Thirty-four programs sat in it, unread, while every gate was green.
#
# So the set is written down, with the seed's own sentence beside each one, and this asks
# three things of it:
#
#   a program skipped that is NOT in the file — something changed and nobody looked
#   a program in the file that now BUILDS — the entry has rotted
#   a program whose REASON moved — the seed turned it away for a different rule
#
# An entry is only asked about if THIS RUN looked at the program: the two inventories cover
# two corpora, and a developer running the gate over examples/ alone would otherwise be told
# that thirty test-data cases had started building. `make oracle` passes both.
: >"$tmp/listed"
for f in $SKIPS; do
	[ -f "$f" ] && grep -v '^#' "$f" | grep -v '^[[:space:]]*$' >>"$tmp/listed"
done

while IFS="$(printf '\t')" read -r path reason; do
	grep -Fqx "$path" "$tmp/seen" || continue

	grep -Fqx "$(printf '%s\t%s' "$path" "$reason")" "$tmp/skipped" && continue

	if grep -Fq "$(printf '%s\t' "$path")" "$tmp/skipped"; then
		echo "REASON    $path — the seed turns it away for a different rule now"
		echo "  was: $reason"
		echo "  now: $(grep -F "$(printf '%s\t' "$path")" "$tmp/skipped" | head -1 | cut -f2-)"
	else
		echo "STALE     $path — the seed builds it now; drop its line from the skip inventory"
	fi
	fail=$((fail + 1))
done <"$tmp/listed"

while IFS="$(printf '\t')" read -r path reason; do
	grep -Fq "$(printf '%s\t' "$path")" "$tmp/listed" && continue

	echo "UNLISTED  $path — the seed refuses it and nothing says why"
	echo "  $reason"
	echo "  if that is a gap the seed is allowed to have, add the line to the skip inventory;"
	echo "  if it is a rule the shipping compiler lost, it belongs in reject-check.sh"
	fail=$((fail + 1))
done <"$tmp/skipped"

if [ $fail -ne 0 ]; then
	echo "oracle-check: $fail program(s) mean different things to the two compilers"
	exit 1
fi

if [ "$same" -lt "${MIN_COMPARED:-8}" ]; then
	echo "oracle-check: only $same programs were comparable — the seed is not building, or the list is empty"
	exit 1
fi
echo "oracle-check: $same programs agree between the seed and the shipping compiler"
echo "oracle-check: $skip the seed cannot build (each one accounted for in $SKIPS), $named the shipping compiler names as not built"
