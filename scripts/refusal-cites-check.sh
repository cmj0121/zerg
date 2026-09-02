#!/bin/sh
# refusal-cites-check — every `NotImplemented:` says WHERE the form it refuses is specified.
#
# The contract this board already keeps is *implemented or refused by name*. A refusal that is
# named is half an answer: the reader knows the compiler will not build the form, and not where
# the form is written down — so they cannot tell a hole in the GRAMMAR from a name the standard
# library has not got, and they cannot look up what they were promised.
#
# WHAT THIS MEASURES THAT NOTHING ELSE DID. `chapter-codes` asks the other direction: every
# unbuilt-form code is NAMED in a chapter. It has been green while a refusal's own sentence
# pointed at nothing, and it is per-CHAPTER — a chapter covers many forms, so it cannot say
# which one is narrowed. The production is the granularity that matters, and it was recorded
# nowhere: the counterexample inventory is one row per production, so `bind-target runs` stayed
# true and said nothing while two of that production's three alternatives were refused by name.
# Finding that took a sweep of every live refusal by hand. This is what makes it a query.
#
# THE RULE IS ONE SENTENCE. A `NotImplemented:` message contains either
#
#   GRAMMAR#<production>   the form is a derivation, and this is the production it narrows
#   docs/<path>.md         the form is not a production — a built-in, a stdlib name, a method —
#                          and this is the chapter that specifies it
#
# and nothing new is invented to say it: both shapes were already in use, both are what a reader
# needs, and neither is a second copy of a fact kept somewhere else.
#
# IT IS A FLOOR, and says so. It reads the citation, not the claim around it: a message may name
# the wrong production and this gate will not know. What it can see is a refusal that names none.
set -eu

SRC=${SRC:-src/compiler/zerg}
GRAMMAR=${GRAMMAR:-GRAMMAR}

. "$(dirname "$0")/lib/grammar.sh"

[ -f "$GRAMMAR" ] || { echo "refusal-cites-check: no grammar at $GRAMMAR" >&2; exit 1; }
[ -d "$SRC" ] || { echo "refusal-cites-check: no compiler source at $SRC" >&2; exit 1; }

grammar_self_test || exit 1

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fail=0

grammar_productions "$GRAMMAR" | cut -f1 | sort -u >"$tmp/productions"

# THE POPULATION IS DERIVED FROM THE SOURCE. Every `NotYet*` rule that has a raise site is a
# refusal a program can meet; one that is declared and never raised is a retired number, which
# `error-codes-check` is what holds. A list here would be a list that goes stale silently.
grep -ho 'rule\.Rule\.NotYet[A-Za-z]*' "$SRC"/*.zg | sed 's/rule\.Rule\.//' | sort -u >"$tmp/raised"
[ -s "$tmp/raised" ] || { echo "refusal-cites-check: no refusal was found in $SRC — the extraction has gone stale" >&2; exit 1; }

n=0
cited=0
chaptered=0
uncited=0
while read -r name; do
	n=$((n + 1))

	# THE MESSAGE AND NOTHING AROUND IT. A comment near the raise is not what a reader is shown,
	# and a window wide enough to catch one is wide enough to borrow the neighbour's — which is
	# how a gate two chapters over came to read a ticket 91 lines away (#86).
	msg=$(grep -ho "rule\.Rule\.$name, [^\"]*\"[^\"]*\"" "$SRC"/*.zg | head -1)
	if [ -z "$msg" ]; then
		echo "NOMESSAGE $name — a raise site with no sentence this gate can read" >&2
		fail=1
		continue
	fi

	prod=$(printf '%s' "$msg" | grep -oE 'GRAMMAR#[A-Za-z][A-Za-z0-9-]*' | sed 's/^GRAMMAR#//' | head -1)
	if [ -n "$prod" ]; then
		if ! grep -qxF "$prod" "$tmp/productions"; then
			echo "NOSUCHPROD $name — its sentence names GRAMMAR#$prod, and the grammar has no such production" >&2
			fail=1
			continue
		fi
		cited=$((cited + 1))
		printf '%s\t%s\n' "$prod" "$name" >>"$tmp/narrowed"
		continue
	fi

	if printf '%s' "$msg" | grep -qE 'docs/[A-Za-z0-9/_.-]+\.md'; then
		chaptered=$((chaptered + 1))
		continue
	fi

	echo "UNCITED   $name — its sentence says the form is not built and not where the form is specified" >&2
	uncited=$((uncited + 1))
	fail=1
done <"$tmp/raised"

if [ "$fail" -ne 0 ]; then
	echo "refusal-cites-check: $uncited of $n refusals name neither a production nor a chapter" >&2
	exit 1
fi

# The derived table, which is the thing that could not be asked before: a production that RUNS
# and still has a form inside it refused by name.
if [ -f "$tmp/narrowed" ]; then
	echo "refusal-cites-check: $(cut -f1 "$tmp/narrowed" | sort -u | grep -c .) productions have a form inside them refused by name"
fi
echo "refusal-cites-check: $n refusals — $cited name the production they narrow, $chaptered name the chapter that specifies the name"
