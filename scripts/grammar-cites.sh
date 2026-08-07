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

GRAMMAR_FILE=${GRAMMAR_FILE:-GRAMMAR}

if [ ! -f "$GRAMMAR_FILE" ]; then
	echo "grammar-cites: $GRAMMAR_FILE does not exist — it is what every citation resolves against"
	exit 1
fi

# --- what the grammar defines -----------------------------------------------------------
#
# A production is a name at the start of a line followed by '::='. Continuation lines of an
# alternation begin with whitespace, so they cannot be mistaken for a definition, and a
# comment begins with '#'.
productions() {
	sed -nE 's/^([A-Za-z][A-Za-z0-9-]*)[[:space:]]*::=.*/\1/p' "$GRAMMAR_FILE"
}

known=" $(productions | sort -u | tr '\n' ' ') "
count=$(productions | sort -u | wc -l | tr -d ' ')

if [ "$count" -lt 2 ]; then
	echo "grammar-cites: found $count productions in $GRAMMAR_FILE — the extractor is broken, so"
	echo "               every citation below would be reported as unknown and none were checked"
	exit 1
fi

# self_test holds the extractor to what this gate assumes about it. A `productions` that has
# quietly become "match nothing" fails loudly above; one that has become "match everything"
# would pass every citation silently, which is the failure this catches.
self_test() {
	local bad=0
	case $known in
	*" program "*) ;;
	*)
		echo "SELFTEST  'program' is a production in $GRAMMAR_FILE and the extractor missed it"
		bad=1
		;;
	esac
	case $known in
	*" no-such-production "*)
		echo "SELFTEST  'no-such-production' is not a production and the extractor found one"
		bad=1
		;;
	esac
	# A continuation line ('             | other') must not read as a definition.
	if printf '%s\n' "             | 'x' ::= y" | sed -nE 's/^([A-Za-z][A-Za-z0-9-]*)[[:space:]]*::=.*/\1/p' | grep -q .; then
		echo "SELFTEST  an indented continuation line was read as a production definition"
		bad=1
	fi
	[ $bad -eq 0 ] && return 0
	echo "grammar-cites: the extractor no longer behaves as documented, so nothing was checked"
	return 1
}

self_test || exit 1

# --- every citation in the tree ----------------------------------------------------------

fail=0
cited=0

# The scan excludes .git and bin (build output), and GRAMMAR itself: a grammar does not cite
# itself, and the notation section names the form so a reader knows what one looks like.
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
$(grep -rEno "GRAMMAR#[A-Za-z][A-Za-z0-9-]*" \
	--exclude-dir=.git --exclude-dir=bin --exclude=GRAMMAR --exclude=grammar-cites.sh . 2>/dev/null)
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
$(grep -rEno "GRAMMAR:[0-9]+" \
	--exclude-dir=.git --exclude-dir=bin --exclude=GRAMMAR --exclude=grammar-cites.sh . 2>/dev/null)
EOF

if [ $fail -ne 0 ]; then
	echo "grammar-cites: $fail finding(s) across $cited citation(s) of $count productions"
	exit 1
fi
echo "grammar-cites: $cited citations resolve, against $count productions in $GRAMMAR_FILE"
