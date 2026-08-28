#!/usr/bin/env bash
#
# behaviour-check — every GRAMMAR production the compiler BUILDS has a program that runs it.
#
# `productions-check` samples every production at `--emit ast` and asserts it is read, or
# refused by name. It says out loud what that is not: *`parse` is the AST stage only — a form
# may parse and still be unbuilt past it*. So the refusal side of the language had a ledger
# end to end — marker, code, refusal case — and the BUILT side had 362 codegen cases with
# nothing mapping them to the surface. "Does every form this compiler builds actually run?"
# was not answered no; it was unanswerable.
#
# THE UNIT IS THE PRODUCTION, one file each, `test-data/behaviour/<name>.zg` with the output
# it prints beside it. That is the same unit `productions-check` uses and for the same reason:
# a case per FORM is the only shape in which coverage can be asserted in both directions.
#
# A CASE IS A WHOLE PROGRAM AND IT PRINTS. The samples this corpus grew out of are script-mode
# statements, which compile mode treats as a nop — so `print 1 + 2 - 3` built an executable
# that ran and printed nothing at all. Twenty-seven of them did. A case that prints nothing
# asserts nothing, and the floor below is not what catches that; the `.out` being non-empty is.
#
# WHAT A `refused` ROW IS. Twenty-five forms are turned away — twenty-four at the parse, one
# at the check — and a runnable case for them is a contradiction: what would run is the
# refusal, which `refuse-check` owns. The row records the code instead, and the gate puts the
# form to the compiler to confirm that is still the code it gets. So a form that starts being
# BUILT fails here rather than quietly leaving the ledger.
#
# THE EXPECTED OUTPUT IS RECORDED, NOT DERIVED, exactly as the codegen corpus does. It pins
# behaviour; it does not prove the value is the right one. Every one of these was read before
# it was recorded — integer division, `~1` as -2, a rune printing as its code point, the
# `%g`-style float — and a diff here means the language changed, which is the question a
# golden corpus answers.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

# shellcheck source=scripts/lib/grammar.sh
. "$ROOT/scripts/lib/grammar.sh"

ZERG=${ZERG:-./bin/zerg}
DIR=${DIR:-test-data/behaviour}
SAMPLES=${SAMPLES:-test-data/productions}
GRAMMAR=${GRAMMAR:-GRAMMAR}
INVENTORY=${INVENTORY:-$DIR/INVENTORY}

# THE FLOOR. Every assertion here compares two sets, and two empty sets are the same set — a
# renamed GRAMMAR, a corpus directory that moved, an extractor that stopped matching, and this
# reports full coverage for having compared nothing. GRAMMAR held 176 productions when this
# was written and only ever grows; 150 is that number with room to collapse a chapter.
MIN_PRODUCTIONS=${MIN_PRODUCTIONS:-150}

[ -x "$ZERG" ] || {
	printf 'behaviour-check: %s is not built — run `make build` first\n' "$ZERG" >&2
	exit 2
}

# The corpus is a private submodule, so an absent one is a checkout that did not ask for it.
if [ ! -d "$DIR" ]; then
	echo "behaviour-check: $DIR is not there (git submodule update --init) — nothing checked"
	exit 0
fi

tmp=$(mktemp -d) || exit 2
trap 'rm -rf "$tmp"' EXIT

fail=0
ran=0
refused=0

grammar_self_test || exit 1
grammar_productions "$GRAMMAR" | cut -f1 | sort -u >"$tmp/productions"
n_prod=$(grep -c . "$tmp/productions")

if [ "$n_prod" -lt "$MIN_PRODUCTIONS" ]; then
	printf 'behaviour-check: %s yielded %s productions, below the floor of %s\n' \
		"$GRAMMAR" "$n_prod" "$MIN_PRODUCTIONS" >&2
	exit 1
fi

[ -f "$INVENTORY" ] || {
	printf 'behaviour-check: %s is not there — a corpus with no inventory explains no absence\n' "$INVENTORY" >&2
	exit 1
}

grep -v '^#' "$INVENTORY" | grep . | cut -f1 | sort -u >"$tmp/rows"

# --- coverage, both directions --------------------------------------------------------
missing=$(comm -23 "$tmp/productions" "$tmp/rows")
extra=$(comm -13 "$tmp/productions" "$tmp/rows")
if [ -n "$missing" ]; then
	printf 'behaviour-check: a production with no row — it is neither run nor recorded as refused:\n' >&2
	printf '%s\n' "$missing" | sed 's/^/          /' >&2
	fail=$((fail + 1))
fi
if [ -n "$extra" ]; then
	printf 'behaviour-check: a row naming no production — rename it, or add the production:\n' >&2
	printf '%s\n' "$extra" | sed 's/^/          /' >&2
	fail=$((fail + 1))
fi

# --- every row is what it says it is --------------------------------------------------
while IFS=$'\t' read -r name verdict code; do
	case "$name" in '' | '#'*) continue ;; esac

	if [ "$verdict" = runs ]; then
		src="$DIR/$name.zg"
		want="$DIR/$name.out"
		[ -f "$src" ] || {
			printf 'behaviour-check: %s says `runs` and %s is not there\n' "$name" "$src" >&2
			fail=$((fail + 1))
			continue
		}
		[ -s "$want" ] || {
			printf 'behaviour-check: %s has no expected output — a case that prints nothing asserts nothing\n' "$name" >&2
			fail=$((fail + 1))
			continue
		}
		if ! "$ZERG" build --emit bin "$src" -o "$tmp/case" >"$tmp/build.log" 2>&1; then
			printf 'behaviour-check: %s says `runs` and does not build\n' "$name" >&2
			sed 's/^/          /' "$tmp/build.log" >&2
			fail=$((fail + 1))
			continue
		fi
		"$tmp/case" >"$tmp/got" 2>&1
		if ! diff -u "$want" "$tmp/got" >"$tmp/diff"; then
			printf 'behaviour-check: %s prints something else now\n' "$name" >&2
			sed '1,2d;s/^/          /' "$tmp/diff" >&2
			fail=$((fail + 1))
			continue
		fi
		ran=$((ran + 1))
		continue
	fi

	if [ "$verdict" != refused ]; then
		printf 'behaviour-check: %s has the verdict `%s`, which is neither `runs` nor `refused`\n' "$name" "$verdict" >&2
		fail=$((fail + 1))
		continue
	fi

	# A REFUSED ROW IS PUT TO THE COMPILER, so a form that starts being built fails here
	# rather than sitting on a list nobody re-reads. The sample is the productions corpus's,
	# because that is where one sample per production already lives.
	sample="$SAMPLES/$name.zg"
	[ -f "$sample" ] || {
		printf 'behaviour-check: %s says `refused` and %s is not there to be refused\n' "$name" "$sample" >&2
		fail=$((fail + 1))
		continue
	}
	got=$("$ZERG" build --emit ast "$sample" 2>&1 >/dev/null | grep -oE 'E[0-9]{4}' | head -1)
	[ -n "$got" ] || got=$("$ZERG" build --emit check "$sample" 2>&1 | grep -oE 'E[0-9]{4}' | head -1)
	if [ -z "$got" ]; then
		printf 'behaviour-check: %s says `refused` (%s) and the compiler accepts it — it is built now, so give it a case\n' \
			"$name" "$code" >&2
		fail=$((fail + 1))
		continue
	fi
	if [ "$got" != "$code" ]; then
		printf 'behaviour-check: %s says `refused` with %s and the compiler answers %s\n' "$name" "$code" "$got" >&2
		fail=$((fail + 1))
		continue
	fi
	refused=$((refused + 1))
done < <(grep -v '^#' "$INVENTORY" | grep .)

[ "$fail" -eq 0 ] || {
	printf 'behaviour-check: the language surface and what runs are not the same set\n' >&2
	exit 1
}

printf 'behaviour-check: %s/%s GRAMMAR productions — %s run a program that prints, %s refused by name\n' \
	"$((ran + refused))" "$n_prod" "$ran" "$refused"
