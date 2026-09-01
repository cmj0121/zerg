#!/bin/sh
# grammar-cited-check — every production a reader writes is named from a chapter (#116).
#
# `grammar-cites` asks the other direction: every citation in the docs resolves to a real
# production. It has been green throughout, and it can be green over a handful of citations —
# nothing has ever asked whether a production is cited AT ALL. That is the same shape as
# `chapter-codes` for an error code: a code has to be named where its readers are, and a form
# does too. When this was written, 21 of 149 were.
#
# THE POPULATION IS DERIVED AND NOT LISTED, and it reuses a classification the board already
# checks. Two of the counterexample inventory's verdicts are exactly the machinery a chapter
# should not be asked to explain:
#
#   no-boundary   a pure alternation or a rename, deriving what its alternatives derive
#                 (`expr`, `statement`, `postfix-expr`, `literal`)
#   spelled-by    a lexical atom below a token (`hex-digit`, `letter`, `str-char`)
#
# What is left is what a reader writes. A hand-written population is how this repository has
# repeatedly measured the wrong set, so the rule is mechanical and the inventory is where it
# already lives.
#
# THE NUMBER IS A FLOOR AND SAYS SO. This gate can tell that a chapter NAMES a form. It cannot
# tell that the chapter EXPLAINS one, and a pass that scattered anchors into the nearest
# paragraph would turn it green while saying nothing — a failure this board has found in itself
# more than once. So the rule the pass follows is written down rather than checked here: the
# anchor goes where the prose already describes the form, and where there is no such prose what
# is owed is the paragraph and not the anchor.
set -eu

GRAMMAR=${GRAMMAR:-GRAMMAR}
INVENTORY=${INVENTORY:-test-data/counterexamples/INVENTORY}
DOCS=${DOCS:-docs}

. "$(dirname "$0")/lib/grammar.sh"

[ -f "$GRAMMAR" ] || { echo "grammar-cited-check: no grammar at $GRAMMAR" >&2; exit 1; }
if [ ! -f "$INVENTORY" ]; then
	echo "grammar-cited-check: skipped — $INVENTORY is not here (git submodule update --init)"
	exit 0
fi

grammar_self_test || exit 1

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fail=0

grammar_productions "$GRAMMAR" | cut -f1 | sort -u >"$tmp/all"

# THE INVENTORY IS HELD TO THE GRAMMAR FIRST. `counterexample-check` asserts the same thing and
# this gate cannot assume it ran: a row naming a production that no longer exists, or a
# production with no row, would silently shrink the population — which is the failure mode this
# whole file is written against.
awk -F'\t' '$1 !~ /^#/ && NF >= 2 { print $1 }' "$INVENTORY" | sort -u >"$tmp/rows"
if ! diff -u "$tmp/all" "$tmp/rows" >"$tmp/diff"; then
	echo "grammar-cited-check: the inventory and the grammar name different productions (- grammar, + inventory)" >&2
	sed '1,2d;s/^/          /' "$tmp/diff" >&2
	exit 1
fi

awk -F'\t' '$1 !~ /^#/ && NF >= 2 && $2 != "no-boundary" && $2 != "spelled-by" { print $1 }' "$INVENTORY" |
	sort -u >"$tmp/want"
[ -s "$tmp/want" ] || { echo "grammar-cited-check: the population is empty — the inventory's verdicts stopped matching" >&2; exit 1; }

# THE CHAPTERS ARE THE ENGLISH ONES. A citation in a translation is held to the original by
# `docs-mirror`, which compares the two documents block for block — so asking both here would
# be asking the same question twice and calling a missing pair a missing citation.
find "$DOCS" -name '*.md' ! -name '*.zh-TW.md' -print0 >"$tmp/chapters"
[ -s "$tmp/chapters" ] || { echo "grammar-cited-check: no chapter was read from $DOCS — the extraction has gone stale" >&2; exit 1; }

# THE ANCHOR AND NOTHING ELSE. A production's name is an ordinary English word often enough
# — `type`, `block`, `field` — that a bare match would call every chapter a citation of half the
# grammar. The anchor is what a reader can follow, and it is what this counts.
xargs -0 grep -ohE 'GRAMMAR#[A-Za-z][A-Za-z0-9-]*' <"$tmp/chapters" |
	sed 's/^GRAMMAR#//' | sort -u >"$tmp/cited"

n=0
missing=0
while read -r p; do
	n=$((n + 1))
	if grep -qxF "$p" "$tmp/cited"; then continue; fi
	echo "UNCITED   $p — a reader writes this form and no English chapter names it" >&2
	missing=$((missing + 1))
	fail=1
done <"$tmp/want"

if [ "$fail" -ne 0 ]; then
	echo "grammar-cited-check: $missing of $n productions a reader writes are named by no chapter" >&2
	exit 1
fi

echo "grammar-cited-check: $n productions a reader writes, each named by a chapter — a floor: it reads the anchor, not the paragraph"
