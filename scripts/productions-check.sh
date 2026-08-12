#!/usr/bin/env bash
#
# productions-check — EVERY production GRAMMAR derives is read, or refused by name.
#
# `make conformance` already claims that sentence, and cannot deliver it. Its corpus is
# twelve files, one per GRAMMAR CHAPTER, and a file stops at its FIRST refusal — so every
# form written below that line is never reached, while the gate reports green. Measured on
# the day this was written: `g05_functions.zg` and `g07_types.zg` both contain `mut &` in a
# function type and a standalone `unsafe fn`, and both were masked by an earlier `E223` /
# `E216`. Neither form had EVER been put to the compiler by that gate. Twelve files against
# 171 productions is at most twelve forms measured, and in practice fewer.
#
# So the unit here is the PRODUCTION, not the chapter: one sample per production, one
# production per file, `test-data/productions/<name>.zg`. A file that stops at its first
# refusal now costs exactly the one form it is about.
#
# WHAT IS ASSERTED, per sample, is docs/conformance.md's standing rule and only that:
#
#   PARSE — every form in it is one this compiler reads, or
#   be refused with a SENTENCE that names the form it is turning away
#
# and never with the message a typo gets, never in silence, never by dying. `is_typo_msg`
# is the same idea conformance-check.sh carries and for the same reason: a negative test
# that fails open, taken over a per-file expected-message inventory, because a corpus case's
# content is private and an inventory of its messages would be that content in this repo.
#
# AND COVERAGE IS ASSERTED TOO, which is the half that makes the rest mean anything. The
# sample set must equal `grammar_productions GRAMMAR` exactly, in BOTH directions — a
# production with no sample is named, and a sample naming no production is named. A gate
# that silently measures a subset is precisely what this replaces, so it may not be able to
# become one.
#
# WHAT EXISTS AND WHAT IS EXEMPT is the INVENTORY's business, not this script's. Some
# productions are below the token (`WS`, `NEWLINE`, `hex-digit`, an f-string's `interp`) and
# no file can BE one; they are still sampled, by the smallest program whose tokens spell
# them, and the inventory says so in a `lexical` class with a note. Nothing is exempt: every
# production has a row and a file, and a form that cannot stand alone declares that rather
# than quietly having no sample. An unexplained exemption is how the chapter corpus reached
# twelve forms while claiming 171.
#
# The stage is `--emit ast` — lex, parse, and stop before anything is lowered — for the
# reason conformance-check.sh gives: these are a list of FORMS, so they name helpers that do
# not exist and import modules that are not there, and asking them to RUN would mean
# rewriting each into a program, which is a narrower sample than a production needs.
set -u

# shellcheck source=scripts/lib/diag.sh
. "$(dirname "$0")/lib/diag.sh"
# shellcheck source=scripts/lib/grammar.sh
. "$(dirname "$0")/lib/grammar.sh"

ZERG=${ZERG:-./bin/zerg}
GRAMMAR=${GRAMMAR:-GRAMMAR}
DIR=${DIR:-test-data/productions}
INVENTORY=${INVENTORY:-$DIR/INVENTORY}

# THE FLOOR. Every assertion below is of the form "the samples and the productions are the
# same set", and two empty sets are the same set — a renamed GRAMMAR, an extractor that
# stopped matching, a corpus directory that moved, and this reports full coverage for having
# compared nothing at all. GRAMMAR held 171 productions when this was written and only ever
# grows; 150 is that number with room to shrink a chapter and without room to collapse.
MIN_PRODUCTIONS=${MIN_PRODUCTIONS:-150}

# The corpus is a private submodule, so an absent one is not a failure — it is a checkout
# that did not ask for it. A directory that IS there and holds nothing is a different thing,
# and the floor above is what catches it.
if [ ! -d "$DIR" ]; then
	echo "productions-check: $DIR is not there (git submodule update --init) — nothing checked"
	exit 0
fi

if [ ! -f "$GRAMMAR" ]; then
	echo "productions-check: $GRAMMAR is not there — nothing was compared"
	exit 1
fi

# The extractor answers what a production IS, and it is the SAME one grammar-cites and
# grammar-mirror read. A third answer to that question is the drift this repo keeps paying
# for: a hand-rolled `grep` for this gate said 167 where the shared reader says 171, and the
# four it missed are the upper-case lexical tokens — exactly the ones most likely to be
# quietly dropped from a coverage claim.
grammar_self_test || exit 1

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fail=0
parsed=0
named=0

grammar_productions "$GRAMMAR" | cut -f1 | sort -u >"$tmp/productions"
n_prod=$(wc -l <"$tmp/productions" | tr -d ' ')

if [ "$n_prod" -lt "$MIN_PRODUCTIONS" ]; then
	echo "productions-check: $GRAMMAR yielded $n_prod productions, below the floor of $MIN_PRODUCTIONS"
	echo "  the extractor or the file changed shape, and comparing against it would measure nothing"
	exit 1
fi

# The inventory is read before any sample is run, because a missing ROW is a coverage hole
# whether or not the file beside it happens to parse.
if [ ! -f "$INVENTORY" ]; then
	echo "productions-check: $INVENTORY is not there — a sample set with no inventory explains no exemption"
	exit 1
fi

grep -v '^[[:space:]]*#' "$INVENTORY" | grep -v '^[[:space:]]*$' >"$tmp/rows"
cut -f1 "$tmp/rows" | sort -u >"$tmp/listed"

report() {
	what=$1
	list=$2
	[ -z "$list" ] && return 0
	echo "productions-check: $what"
	printf '%s\n' "$list" | sed 's/^/    /'
	fail=$((fail + 1))
}

report "derived by GRAMMAR, named by no inventory row" \
	"$(comm -23 "$tmp/productions" "$tmp/listed")"
report "named by an inventory row, derived by no production" \
	"$(comm -13 "$tmp/productions" "$tmp/listed")"

# A row's CLASS is a closed set, and a `lexical` one owes the note that says which token
# spells it — that note IS the explanation an exemption would otherwise skip.
while IFS=$'\t' read -r name class note; do
	case $class in
	program | phrase) ;;
	lexical)
		[ -n "$note" ] && [ "$note" != "-" ] ||
			report "a lexical row with no note — it must name the token that spells it" "$name"
		;;
	*)
		report "an inventory class that is not program, phrase or lexical" "$name	$class"
		;;
	esac
done <"$tmp/rows"

# Now the samples. One per production, and the production's own name is the file's, so a
# reader who meets `GRAMMAR#tuple-lit` knows where to look without an index.
while read -r name; do
	src="$DIR/$name.zg"
	if [ ! -f "$src" ]; then
		echo "NOSAMPLE  $name — no $src"
		fail=$((fail + 1))
		continue
	fi

	out=$("$ZERG" build --emit ast "$src" 2>&1 >/dev/null)
	status=$?

	if [ $status -eq 0 ]; then
		parsed=$((parsed + 1))
		continue
	fi

	# A crash is the one outcome the rule never allows, and it says nothing — so without
	# this it would read as a refusal with an empty message.
	if is_crash "$status"; then
		echo "CRASHED   $name — the compiler died of signal $((status - 128))"
		fail=$((fail + 1))
		continue
	fi

	if [ -z "$out" ]; then
		echo "SILENT    $name — non-zero exit and nothing said"
		fail=$((fail + 1))
		continue
	fi

	if is_typo_msg "$out"; then
		echo "UNNAMED   $name — refused with the message a typo gets, for a form GRAMMAR derives"
		echo "  $(printf '%s\n' "$out" | head -1)"
		fail=$((fail + 1))
		continue
	fi

	named=$((named + 1))
done <"$tmp/productions"

# A sample file naming no production is the other direction of the same drift: it is a form
# nothing asks about, and it makes the coverage arithmetic below a coincidence.
ls "$DIR"/*.zg 2>/dev/null | sed 's|.*/||;s|\.zg$||' | sort -u >"$tmp/files"
report "a sample file naming no production — rename it, or add the production" \
	"$(comm -13 "$tmp/productions" "$tmp/files")"

if [ "$fail" -ne 0 ]; then
	echo "productions-check: coverage is not what it claims, or a form is neither read nor refused by name"
	exit 1
fi

echo "productions-check: $n_prod/$n_prod GRAMMAR productions sampled — $parsed parse, $named refused by name"
