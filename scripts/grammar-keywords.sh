#!/usr/bin/env bash
#
# grammar-keywords — a reserved word that no production uses is a word nobody can write and
# nothing can read.
#
# This is `grammar-cites` pointed the other way. That gate asks whether every citation names
# a production GRAMMAR derives; this one asks whether every word GRAMMAR RESERVES is reached
# by one. Both failures are silent in the same way: nothing stops compiling, and the surface
# quietly carries a word that is only a restriction.
#
# It exists because of `package`. GRAMMAR reserved it, no production mentioned it, and both
# lexers turned it away from every program for years — a keyword whose entire effect was to
# forbid a name. The keyword table was written as "a lexical fact", which is exactly the kind
# of claim that stops being checked against anything.
#
# The check is textual on purpose. A word is USED when it appears quoted somewhere in GRAMMAR
# outside the keyword table itself — in a production's right-hand side, or in the prose that
# defines a form. That is deliberately generous: the question is whether the language has
# somewhere for the word to go, not whether the EBNF alone spells it.
set -u

GRAMMAR_FILE=${GRAMMAR_FILE:-GRAMMAR}

# The table runs from the `keyword ::=` line to the first line that is not a continuation.
table=$(awk '/^keyword +::=/ {inside = 1} inside {print} inside && !/^keyword/ && !/^ *\|/ {exit}' "$GRAMMAR_FILE")
if [ -z "$table" ]; then
	echo "grammar-keywords: no keyword table found in $GRAMMAR_FILE — nothing was checked" >&2
	exit 1
fi

words=$(printf '%s\n' "$table" | grep -oE "'[a-z]+'" | tr -d "'" | sort -u)
n=$(printf '%s\n' "$words" | grep -c .)

# Every line of GRAMMAR that is NOT part of the table, so a word cannot vouch for itself.
rest=$(grep -vFx -f <(printf '%s\n' "$table") "$GRAMMAR_FILE")

fail=0
for w in $words; do
	if ! printf '%s\n' "$rest" | grep -qF "'$w'"; then
		echo "UNREACHED $w — reserved by the keyword table, and no production or note uses it"
		fail=$((fail + 1))
	fi
done

if [ "$fail" -ne 0 ]; then
	echo "grammar-keywords: $fail reserved word(s) the grammar never reaches"
	exit 1
fi
echo "grammar-keywords: $n reserved words, each reached by the grammar that reserves them"
