#!/usr/bin/env bash
#
# grammar-cites — every GRAMMAR citation the repo makes must resolve.
#
# The compiler, the gate scripts and the corpus all cite the grammar to say why a rule is
# the way it is. Those citations used to carry a LINE — `GRAMMAR:314` — and a line number
# is true only until somebody adds a paragraph above it. When this gate was written, 5 of
# the 6 citations sampled pointed at the wrong production; the header edit that prompted
# the sample had not moved anything yet. Nothing broke, nothing failed, and every one of
# them had quietly been lying for however long ago the shift happened.
#
# So the citation names the PRODUCTION instead — `GRAMMAR#param` — and this gate holds it
# to it. A production's name does not move when the file above it grows, which is why this
# gate can be a resolution check rather than a drift check: the class of bug is gone, and
# what is left is the ordinary typo of naming a production that does not exist.
#
# The convention a citation carries: `GRAMMAR#<name>` means the production `<name> ::=`
# AND the comment block that introduces it, since that is where a grammar states its rules
# in prose. A rule with no production of its own is cited on the production it is about
# (`this` is not a parameter → `GRAMMAR#param`).
#
# Scope. It reads whatever is in the tree, so a checkout without the private test-data
# submodule checks the files it has and says so, rather than failing over an absence.

set -u

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

# shellcheck source=scripts/lib/grammar.sh
. "$(dirname "$0")/lib/grammar.sh"

GRAMMAR_FILE=${GRAMMAR_FILE:-GRAMMAR}

if [ ! -f "$GRAMMAR_FILE" ]; then
	echo "grammar-cites: $GRAMMAR_FILE does not exist — it is what every citation resolves against"
	exit 1
fi

# --- what the grammar defines -----------------------------------------------------------
#
# Through the same extractor grammar-mirror reads it with, and for the reason set out in
# scripts/lib/grammar.sh: what counts as a production is one fact, and a second copy of it
# would fail open here — an extractor that matched nothing would report every citation as
# unknown, and one that matched everything would pass every citation while checking none.
productions() {
	grammar_productions "$GRAMMAR_FILE" | cut -f1
}

known=" $(productions | sort -u | tr '\n' ' ') "
count=$(productions | sort -u | wc -l | tr -d ' ')

if [ "$count" -lt 2 ]; then
	echo "grammar-cites: found $count productions in $GRAMMAR_FILE — the extractor is broken, so"
	echo "               every citation below would be reported as unknown and none were checked"
	exit 1
fi

# The extractor is held to its own cases in the library that owns it; what is left for this
# gate to check is the LOOKUP built from it. `known` is a space-delimited string and every
# verdict below is a substring test against it, so a membership test that has quietly become
# "always true" would pass every citation while checking none.
self_test() {
	local bad=0
	case $known in
	*" program "*) ;;
	*)
		echo "SELFTEST  'program' is a production in $GRAMMAR_FILE and the lookup does not hold it"
		bad=1
		;;
	esac
	case $known in
	*" no-such-production "*)
		echo "SELFTEST  'no-such-production' is not a production and the lookup answers that it is"
		bad=1
		;;
	esac
	# A name that is a SUBSTRING of a real one must not be found: `arg` is a production and
	# `ar` is not, and an unspaced `case` would answer that both are.
	case $known in
	*" ar "*)
		echo "SELFTEST  'ar' is not a production and the lookup matched it inside another name"
		bad=1
		;;
	esac
	[ $bad -eq 0 ] && return 0
	echo "grammar-cites: the production lookup no longer behaves as documented, so nothing was checked"
	return 1
}

grammar_self_test || { echo "grammar-cites: nothing was checked"; exit 1; }
self_test || exit 1

# --- every citation in the tree ----------------------------------------------------------

fail=0
cited=0

# The scan is `git grep`, which reads exactly the TRACKED tree. That is the gate's question
# stated directly — a citation this repository MAKES, rather than one in a file that happens
# to sit beside the checkout — and it is what a walk of the working tree could only
# approximate: `bin/` and `.zerg-cache/` had to be excluded by name because they hold build
# output, and a working note beside the checkout turned this red for citations that are
# going to be deleted rather than fixed. Untracked is untracked; the moment a file is added
# it is read like any other, and a fresh clone has nothing untracked in it at all.
#
# `.zerg-cache` is the reason the old form needed a list, and one this gate learned the hard
# way: a compiled object embeds the compiler's own string literals, so the first refusal
# message to cite a production put `GRAMMAR#import-path` inside four `.o` files, and grep
# answered "Binary file … matches" — which the loop below then read as a file name, a line
# number and a production name all at once. Nothing a build wrote is tracked, so the
# question answers itself now.
#
# `--recurse-submodules` is not optional: the corpus is a submodule, and a walk of the
# working tree read it while a plain `git grep` does not. GRAMMAR itself is excluded because
# a grammar does not cite itself, and this script because the notation section names the
# form so a reader knows what one looks like.

# scan <regex> — every citation of a shape, as `file:line:text`. It is one function because
# the two scans have to agree about what is in scope: a file the by-name check reads and the
# by-line check does not is a file whose findings depend on which question was asked.
scan() {
	git grep --recurse-submodules -nEo "$1" -- ':!GRAMMAR' ':!scripts/grammar-cites.sh' 2>/dev/null
}

while IFS= read -r hit; do
	file=${hit%%:*}
	rest=${hit#*:}
	line=${rest%%:*}
	name=$(printf '%s' "$hit" | sed -E 's/.*GRAMMAR#([A-Za-z][A-Za-z0-9-]*).*/\1/')
	cited=$((cited + 1))
	case $known in
	*" $name "*) ;;
	*)
		echo "UNKNOWN   $file:$line cites GRAMMAR#$name, and $GRAMMAR_FILE derives no such production"
		fail=$((fail + 1))
		;;
	esac
done <<EOF
$(scan "GRAMMAR#[A-Za-z][A-Za-z0-9-]*")
EOF

# The old form, wherever it survives. It is not a broken citation yet — the line may well be
# right today — but it is the one that goes wrong without anyone touching it.
while IFS= read -r hit; do
	[ -n "$hit" ] || continue
	file=${hit%%:*}
	rest=${hit#*:}
	line=${rest%%:*}
	echo "BY-LINE   $file:$line cites $GRAMMAR_FILE by line — cite the production (GRAMMAR#name) instead"
	fail=$((fail + 1))
done <<EOF
$(scan "GRAMMAR:[0-9]+")
EOF

if [ $fail -ne 0 ]; then
	echo "grammar-cites: $fail finding(s) across $cited citation(s) of $count productions"
	exit 1
fi
echo "grammar-cites: $cited citations resolve, against $count productions in $GRAMMAR_FILE"
