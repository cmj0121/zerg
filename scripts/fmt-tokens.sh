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
# This is the property that catches it. The token stream a source produces must survive
# being formatted — same kinds, same lexemes, same order. Only the POSITIONS may move,
# which is what formatting is, so the line:col column is dropped before comparing.
#
# It runs over every corpus this repo has, not only the fmt cases: the codegen programs are
# the wider surface, and a spacing rule that mangles a form no fmt case contains is exactly
# the shape of the two defects this gate exists for.

set -u

ZERG=${ZERG:-./bin/zerg}

# every F4xx rule, off: see the note at the fmt call below
REWRITES="--off F401 --off F402 --off F403 --off F404 --off F405 --off F406 --off F407 --off F408"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fail=0
n=0

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
echo "fmt-tokens: $n sources format without moving a token"
