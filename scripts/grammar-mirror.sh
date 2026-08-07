#!/usr/bin/env bash
#
# grammar-mirror — the prose companion still says what GRAMMAR says.
#
# docs/surface/grammar.md is a SECOND COPY of 119 of GRAMMAR's productions, and its
# zh-TW twin is a third. Two copies of a language fact drift, and these already had: while
# this gate was being written, `select-arm`, `recv-arm` and `send-arm` still derived
# `'=>' expr` on both pages where GRAMMAR derives `'=>' stmt` — a rule the shipping
# compiler had been following for long enough that a `for select` with `break` in an arm
# builds and runs. Nothing read the page against the file it copies, so nothing said so.
#
# The copy is not a mistake to delete: a reader wants the productions beside the prose that
# explains them, and a page of pure cross-references explains nothing. What it needs is to
# be held to the original, which is what this does.
#
# ELISION is legal, because the page abbreviates deliberately and says so in its own
# notation table (`name ::= …`). Three forms:
#
#     name ::= …              the whole right-hand side stands in; anything matches
#     name ::= prefix …       GRAMMAR's right-hand side must START with prefix
#     name ::= <exact>        no ellipsis is a claim, and it must match GRAMMAR exactly
#
# So a page may say less than GRAMMAR. It may not say something ELSE — which is the one
# thing an unmarked simplification does: `unsafe-group ::= 'unsafe' '{' unsafe-item* '}'`
# read as the rule, and dropped the separators that `stmt-list` two hundred lines above it
# spells out in full.

set -u

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

# shellcheck source=scripts/lib/grammar.sh
. "$(dirname "$0")/lib/grammar.sh"

GRAMMAR_FILE=${GRAMMAR_FILE:-GRAMMAR}
PAGES=${PAGES:-"docs/surface/grammar.md docs/surface/grammar.zh-TW.md"}

# A FLOOR under how many productions a page has to have mirrored for its report to mean
# anything, of the kind the corpus and fmt-tokens carry. A page that stops matching — a
# fence renamed, a section rewritten — would otherwise pass with `0 productions checked`,
# which is what "no drift" and "nothing measured" look like when they are the same number.
MIRROR_MIN=${MIRROR_MIN:-100}

if [ ! -f "$GRAMMAR_FILE" ]; then
	echo "grammar-mirror: $GRAMMAR_FILE does not exist — it is what the pages are measured against"
	exit 1
fi

grammar_self_test || { echo "grammar-mirror: nothing was checked"; exit 1; }

# elides <page-rhs> <grammar-rhs> — is the page's right-hand side a legal abbreviation of
# GRAMMAR's? Held to its own cases below before it is used on anything.
elides() {
	local page=$1 want=$2 prefix
	[ "$page" = "…" ] && return 0
	case $page in
	*" …")
		prefix=${page% …}
		case $want in
		"$prefix"*) return 0 ;;
		esac
		return 1
		;;
	esac
	[ "$page" = "$want" ]
}

elides_self_test() {
	local bad=0
	elides "…" "anything at all" || { echo "SELFTEST  a bare … does not stand in for a right-hand side"; bad=1; }
	elides "nop | …" "nop | binding | reassign" || { echo "SELFTEST  a prefix … does not match its prefix"; bad=1; }
	elides "a b" "a b" || { echo "SELFTEST  an exact right-hand side does not match itself"; bad=1; }
	elides "nop | …" "binding | nop" && { echo "SELFTEST  a prefix … matched a right-hand side it does not prefix"; bad=1; }
	elides "a b" "a b c" && { echo "SELFTEST  an exact right-hand side matched a longer one"; bad=1; }
	elides "…x" "y" && { echo "SELFTEST  an … not at the end was treated as an elision"; bad=1; }
	[ $bad -eq 0 ] && return 0
	echo "grammar-mirror: the elision rule no longer behaves as documented, so nothing was checked"
	return 1
}

elides_self_test || exit 1

# --- what GRAMMAR derives ----------------------------------------------------------------
#
# Through files and `join` rather than an associative array: `declare -A` is bash 4, macOS
# still ships 3.2 as /bin/bash, and no other script in this repo needs anything newer. The
# sorts are LC_ALL=C so that join's idea of order is the same as theirs whatever locale a
# developer has. A page may state a production TWICE (three do, in two sections each) and
# join pairs both occurrences, which is what should happen: both are claims.
tmp=$(mktemp -d) || exit 2
trap 'rm -rf "$tmp"' EXIT

grammar_productions "$GRAMMAR_FILE" | LC_ALL=C sort -t"$(printf '\t')" -k1,1 >"$tmp/want"
count=$(wc -l <"$tmp/want" | tr -d ' ')

if [ "$count" -lt "$MIRROR_MIN" ]; then
	echo "grammar-mirror: only $count productions found in $GRAMMAR_FILE — below the floor of"
	echo "                $MIRROR_MIN, so every page below would be reported as drifted and none were checked"
	exit 1
fi

# --- and what each page claims it derives -------------------------------------------------

fail=0
for page in $PAGES; do
	if [ ! -f "$page" ]; then
		echo "MISSING   $page — it is listed as a copy of $GRAMMAR_FILE and is not there"
		fail=$((fail + 1))
		continue
	fi

	grammar_productions "$page" | LC_ALL=C sort -t"$(printf '\t')" -k1,1 >"$tmp/have"

	LC_ALL=C join -t"$(printf '\t')" -v2 -j1 -o 2.1 "$tmp/want" "$tmp/have" >"$tmp/unknown"
	while IFS= read -r name; do
		[ -n "$name" ] || continue
		echo "UNKNOWN   $page derives '$name', and $GRAMMAR_FILE derives no such production"
		fail=$((fail + 1))
	done <"$tmp/unknown"

	checked=0
	elided=0
	LC_ALL=C join -t"$(printf '\t')" -j1 -o 0,1.2,2.2 "$tmp/want" "$tmp/have" >"$tmp/pairs"
	while IFS="$(printf '\t')" read -r name want have; do
		[ -n "$name" ] || continue
		checked=$((checked + 1))
		case $have in
		*…*) elided=$((elided + 1)) ;;
		esac
		if ! elides "$have" "$want"; then
			echo "DRIFTED   $page derives '$name' differently from $GRAMMAR_FILE"
			echo "          $GRAMMAR_FILE: $name ::= $want"
			echo "          $page: $name ::= $have"
			fail=$((fail + 1))
		fi
	done <"$tmp/pairs"

	if [ "$checked" -lt "$MIRROR_MIN" ]; then
		echo "THIN      $page mirrors $checked productions, below the floor of $MIRROR_MIN — a page that"
		echo "          stopped matching reports no drift for the same reason a page with none does"
		fail=$((fail + 1))
		continue
	fi
	printf '  %-34s %s mirrored, %s of them abbreviated\n' "$page" "$checked" "$elided"
done

if [ $fail -ne 0 ]; then
	echo "grammar-mirror: $fail finding(s) — a page states a production $GRAMMAR_FILE does not"
	exit 1
fi
echo "grammar-mirror: every mirrored production matches $GRAMMAR_FILE, or abbreviates it with …"
