#!/usr/bin/env bash
#
# fmt-tokens — formatting changes SPACING, never the token stream.
#
# `zerg fmt --check` proves the corpus is already canonical, and idempotence proves
# formatting twice is formatting once. Neither can see a rule that is stably WRONG: a form
# printed wrongly is printed the same wrong way on the second pass, so the fixpoint holds
# and the output is still not what the person wrote. That is how `fn main( {` came back as
# `fn main({` and `print("hi")` as `print ("hi")` with nothing said.
#
# The property: the token stream a source produces must survive being formatted — same
# kinds, same lexemes, same order. Only the POSITIONS may move, which is what formatting is,
# so the line:col column is dropped before comparing.
#
# WHAT THAT DOES NOT COVER, since the header used to claim otherwise. A change that only
# moves whitespace — `print("hi")` into `print ( "hi" )` — produces the same tokens and is
# invisible here; so is a rule that eats a COMMENT, because a comment is not a token at
# all. The two defects above are of the first kind. What this does catch is a token
# retexted, merged, dropped or reordered, and a formatted source that no longer lexes —
# the class where formatting changes what a program MEANS.
#
# It runs over the fmt cases, the codegen programs and the numbered examples: the wider
# surface matters, because a spacing rule that mangles a form no fmt case contains is
# exactly the shape those two defects had.

set -u

ZERG=${ZERG:-./bin/zerg}

# EVERY F4xx RULE, OFF — see the note at the fmt call below. The list is READ FROM THE TABLE
# that documents them rather than written out here, because a written one goes stale silently:
# it said F401..F408 and `F409` had been added since, so the gate ran the one rewrite it exists
# to hold constant and reported its own blind spot as a source changing meaning. A rule added
# to the table joins this list by being in it.
#
# The floor under that read is the same one every derived list here needs: a table that
# stopped matching, or a renamed file, would empty the set and turn every rewrite back on.
REWRITES=$(grep -oE '^\| `F4[0-9][0-9]`' docs/tooling/fmt.md | grep -oE 'F4[0-9][0-9]' | sort -u | sed 's/^/--off /' | tr '\n' ' ')
n_rewrites=$(printf '%s' "$REWRITES" | tr ' ' '\n' | grep -c -- '--off' || true)
if [ "${n_rewrites:-0}" -lt 8 ]; then
	printf 'fmt-tokens: only %s F4xx rules were read from docs/tooling/fmt.md — the rewrites this gate turns off are not all off\n' "${n_rewrites:-0}" >&2
	exit 2
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fail=0
n=0

# how many sources must actually be measured. Raise it when the corpus grows; a gate that
# measures nothing looks exactly like a gate that found nothing.
MIN_SOURCES=${MIN_SOURCES:-100}

for src in test-data/fmt/*.zg test-data/codegen/*.zg examples/[0-9][0-9]_*.zg; do
	[ -f "$src" ] || continue

	# a source this compiler cannot lex says nothing about what formatting does to it
	"$ZERG" build --emit tokens "$src" >"$tmp/before" 2>/dev/null || continue

	cp "$src" "$tmp/case.zg"
	# The F4xx rules are OFF. They are the group that "changes the code's SHAPE, not its
	# spacing" — F401 rewrites `if c { raise e }` into `raise e if c`, which is fewer
	# tokens on purpose — so they are the one thing allowed to change the stream, and each
	# says which rule did it. What must never change it is LAYOUT and SPACING, which is
	# F1xx-F3xx, and that is what this measures. The first run of this gate reported F401
	# as a defect, which was the gate being wrong rather than the formatter.
	"$ZERG" fmt $REWRITES "$tmp/case.zg" >/dev/null 2>&1 || continue
	"$ZERG" build --emit tokens "$tmp/case.zg" >"$tmp/after" 2>/dev/null || {
		printf 'LEX    %s — the formatted source no longer lexes\n' "$(basename "$src")"
		fail=1
		continue
	}

	# Two things are dropped, and both are what formatting IS.
	#
	# The `line:col` column, because a formatter moves tokens.
	#
	# And the inserted `;`. The lexer turns a line break into one when the last token can
	# end an item, so a `;` is not something a person wrote — it is a record of where the
	# lines were, which is the one thing a layout rule is for. Keeping it would make this
	# gate say "formatting changed the token stream" every time it changed a line break.
	cut -f2- "$tmp/before" | grep -v '^;$' >"$tmp/b.toks"
	cut -f2- "$tmp/after" | grep -v '^;$' >"$tmp/a.toks"
	if ! cmp -s "$tmp/b.toks" "$tmp/a.toks"; then
		printf 'TOKENS %s — formatting changed the token stream\n' "$(basename "$src")"
		diff "$tmp/b.toks" "$tmp/a.toks" | head -6
		fail=1
		continue
	fi
	n=$((n + 1))
done

if [ $fail -ne 0 ]; then
	echo "fmt-tokens: formatting changed what a source MEANS, not only how it looks"
	exit 1
fi
# A FLOOR, for the reason reject-fuzz has a ceiling: a count that is only printed drifts.
# Every `|| continue` above is silent, so a formatter that began refusing a whole class of
# source would shrink this number and still exit 0.
if [ "$n" -lt "$MIN_SOURCES" ]; then
	echo "fmt-tokens: only $n sources were measured, and the floor is $MIN_SOURCES"
	exit 1
fi
echo "fmt-tokens: $n sources format without moving a token"
