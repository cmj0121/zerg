#!/usr/bin/env bash
#
# editor-align — an editor file may not know a language fact the compiler contradicts.
#
# Everything else in this tree is held to the compiler by calling it. `zerg fmt` IS the
# formatter, `zerg lsp` asks `emit_files_diag` rather than checking anything itself — so
# there is no second copy to drift. The editor files are the exception and cannot be
# anything else: vim highlights from a keyword list written in vimscript, and nvim has to be
# told how to indent before any Zerg tool has run.
#
# So those two facts get a gate, one each:
#
#   * every reserved word `lookup_keyword` returns is a word the syntax file colours, and
#     every word it colours as a keyword is one the lexer reserves;
#   * the indent CHARACTER the ftplugin and .editorconfig configure is the one `zerg fmt`
#     actually writes;
#   * the indent WIDTH they configure is the one `F403` measures a tab as;
#   * the RULER the ftplugin draws is one past the column `F403` wraps at.
#
# Neither is hypothetical. `zerg.vim`'s own comment records `close` having been missing from
# the list "entirely — the statement that ends a stream has never been coloured", found by
# reading rather than by a gate. And the ftplugin set `expandtab` with a four-space shift
# while F101 indents with a tab and `make fmt-self` holds every source in the tree to it, so
# a person typing in nvim produced spaces that the next save turned into tabs: a whole-file
# diff per write, from the editor and the formatter disagreeing about one rule.

set -u

ZERG=${ZERG:-./bin/zerg}
TOKEN=${TOKEN:-src/compiler/zerg/token.zg}
SYNTAX=${SYNTAX:-editors/nvim/syntax/zerg.vim}
FTPLUGIN=${FTPLUGIN:-editors/nvim/ftplugin/zerg.vim}
EDITORCONFIG=${EDITORCONFIG:-.editorconfig}
FMT=${FMT:-src/compiler/zerg/fmt.zg}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fail=0

# --- 1. the keyword list --------------------------------------------------------------
#
# The lexer's side is `lookup_keyword`, which is the one function that decides what a
# reserved word IS — every other list in the compiler is downstream of it.
awk '/^fn lookup_keyword/,/^}/' "$TOKEN" | grep -oE '"[a-z]+"' | tr -d '"' | sort -u >"$tmp/lexer"

# The syntax file's side is every word it names as a keyword, in any group. `syntax match`
# counts too: `for` is written as one so that the `impl X for Y` override can win, which is
# a highlighting decision and not a claim that `for` is unreserved.
# The vim ARGUMENTS that may follow the group name — `contained`, `skipwhite`, a
# `nextgroup=` — are not words of this language, and dropping them by name is the only way
# to tell them from one: they are lowercase identifiers on the same line as the keywords.
{
	grep -oE '^syntax keyword zerg[A-Za-z]+ .*$' "$SYNTAX" |
		sed -E 's/^syntax keyword zerg[A-Za-z]+ //' | tr ' ' '\n'
	grep -oE '^syntax match zerg[A-Za-z]+ "\\<[a-z]+\\>"' "$SYNTAX" |
		sed -E 's/.*\\<//; s/\\>.*//'
} | grep -E '^[a-z]+$' |
	grep -vxE 'contained|containedin|nextgroup|skipwhite|skipnl|skipempty|transparent|display|conceal|fold|extend|keepend|oneline' |
	sort -u >"$tmp/syntax.all"

# THESE PATTERNS READ SOURCE, so they go stale when the source is rewritten and they go
# stale QUIETLY: an extraction that matches nothing makes the exclusion list empty, and every
# type name the syntax file colours is then reported as invented. That is what happened when
# variants became qualified — `return TInt if …` became `return Ty.TInt if …` — and it is
# why the `EMPTY` check below exists for the other side of this gate.
#
# The TYPE names are named by the syntax file and are not reserved words — `int` is an
# ordinary identifier the parser resolves, so a program may shadow it and the lexer has
# never heard of it. They are held to their own list, in the parser, for the same reason.
{
	# the scalars, which the parser resolves by name
	grep -oE 'return Ty\.T[A-Za-z]+ if name == "[a-z]+"' src/compiler/zerg/parser.zg |
		sed -E 's/.*"([a-z]+)"/\1/'

	# and the containers, which it resolves by name AND a following `[`
	grep -oE 'if name == "[a-z]+" and p_at\(p, Kind\.LBrack\)' src/compiler/zerg/parser.zg |
		sed -E 's/.*"([a-z]+)".*/\1/'

	# `set[T]` is named by the syntax file and is not built: the specification has it and
	# this compiler refuses it, which is the implemented-or-named contract rather than a
	# drift. Highlighting a form the language HAS is what a syntax file is for.
	echo set
} | sort -u >"$tmp/types.all"

# A word that is BOTH a reserved word and a type name — `nil` is the one — belongs to the
# lexer's list, not the exclusion. Subtracting it as a type is what made this gate report
# `nil` uncoloured while the syntax file colours it.
comm -23 "$tmp/types.all" "$tmp/lexer" >"$tmp/types"
comm -23 "$tmp/syntax.all" "$tmp/types" >"$tmp/syntax"

missing=$(comm -23 "$tmp/lexer" "$tmp/syntax")
extra=$(comm -13 "$tmp/lexer" "$tmp/syntax")

if [ -n "$missing" ]; then
	echo "UNCOLOURED  the lexer reserves these and $SYNTAX does not name them:"
	echo "$missing" | sed 's/^/  /'
	fail=$((fail + 1))
fi
if [ -n "$extra" ]; then
	echo "INVENTED    $SYNTAX colours these as keywords and the lexer does not reserve them:"
	echo "$extra" | sed 's/^/  /'
	echo "  (a built-in TYPE name belongs in the zergType group, which this gate excludes)"
	fail=$((fail + 1))
fi

n=$(wc -l <"$tmp/lexer" | tr -d ' ')
if [ "$n" -lt "${MIN_KEYWORDS:-30}" ]; then
	echo "EMPTY       only $n keywords were read from $TOKEN — this gate is measuring nothing"
	fail=$((fail + 1))
fi

# AND THE OTHER SIDE, for the same reason and a defect this gate actually had: the type
# names are read out of the parser by pattern, and when variants became qualified the
# patterns stopped matching. An empty exclusion list does not fail quietly — it reports every
# type name the syntax file colours as invented — but it reports the wrong thing, which is
# worse than reporting nothing.
t=$(wc -l <"$tmp/types.all" | tr -d ' ')
if [ "$t" -lt "${MIN_TYPES:-10}" ]; then
	echo "EMPTY       only $t type names were read from the parser — the extraction has gone stale"
	fail=$((fail + 1))
fi

# --- 2. the indent character ------------------------------------------------------------
#
# Asked of `zerg fmt` rather than of F101's documentation, because what a person's editor
# has to agree with is what the tool WRITES.
#
# The pattern carries a LITERAL tab, built by printf, and is matched as a basic expression.
# `grep -E '^\t\t'` is not the same question on two machines: BSD grep reads `\t` as a tab and
# GNU grep reads it as an undefined escape, i.e. the letter `t` — so the probe answered "tab"
# on macOS and "space" on Linux for the same formatter, and this gate failed in CI alone.
# fmt_return_int reads the number an int-returning function in fmt.zg answers with. Two checks
# below ask that question — the tab width and the wrap column — and a pipeline copied for the
# second is a pipeline that goes stale on one of them.
fmt_return_int() {
	awk -v fn="^fn $1" '$0 ~ fn, /^}/' "$FMT" | grep -oE 'return [0-9]+' | grep -oE '[0-9]+' | head -1
}

tab=$(printf '\t')
printf 'fn main() {\n\tif true {\n\t\tprint 1\n\t}\n}\n' >"$tmp/indent.zg"
"$ZERG" fmt "$tmp/indent.zg" >/dev/null 2>&1
if grep -q "^$tab${tab}print" "$tmp/indent.zg"; then
	want_style=tab
else
	want_style=space
fi

if [ "$want_style" = tab ] && ! grep -qE '^setlocal noexpandtab' "$FTPLUGIN"; then
	echo "INDENT      zerg fmt indents with a TAB and $FTPLUGIN does not set noexpandtab"
	fail=$((fail + 1))
fi
if [ "$want_style" = space ] && grep -qE '^setlocal noexpandtab' "$FTPLUGIN"; then
	echo "INDENT      zerg fmt does not indent with a tab and $FTPLUGIN sets noexpandtab"
	fail=$((fail + 1))
fi

# --- 3. .editorconfig, for the editors this repository does not ship a plugin for --------
#
# `editors/nvim/` configures one editor; this is the file every other one reads. It is held
# to the SAME probe rather than to the ftplugin, so the two cannot agree with each other and
# both be wrong.
#
# `awk` and not an editorconfig library: the question is what one section of one file says,
# and a dependency to read four lines is a dependency in CI.
# The section is compared as TEXT and not as a pattern. `[*.zg]` is a glob, so every
# character in it that would have to be escaped for a regex is one this had to escape
# correctly — and awk's `-v` eats a backslash before it ever reaches the match, which is
# how the first version of this silently found nothing and reported both values unset.
section_value() {
	awk -v sect="$1" -v key="$2" '
		/^[[:space:]]*\[/ {
			line = $0
			gsub(/^[[:space:]]+|[[:space:]]+$/, "", line)
			in_sect = (line == sect)
			next
		}
		in_sect && $0 ~ "^[[:space:]]*" key "[[:space:]]*=" {
			sub(/^[^=]*=[[:space:]]*/, ""); gsub(/[[:space:]]+$/, ""); print
		}
	' "$EDITORCONFIG" | tail -1
}

if [ ! -f "$EDITORCONFIG" ]; then
	echo "MISSING     $EDITORCONFIG — the rule that reaches every editor without a plugin here"
	fail=$((fail + 1))
else
	got_style=$(section_value '[*.zg]' indent_style)
	if [ "$got_style" != "$want_style" ]; then
		echo "INDENT      zerg fmt indents with a $want_style and $EDITORCONFIG says [*.zg] indent_style = ${got_style:-<unset>}"
		fail=$((fail + 1))
	fi

	# The WIDTH is a second claim and a real one. F403 decides whether a line has run past
	# column 80 by counting a tab as `fmt_wrap_tab()`, so an editor displaying it as anything
	# else is applying a different 80-column rule than the formatter did. One number, and the
	# three places that hold it have to hold the same one.
	want_width=$(fmt_return_int fmt_wrap_tab)
	if [ -z "$want_width" ]; then
		echo "EMPTY       fmt_wrap_tab() could not be read from $FMT — this check is measuring nothing"
		fail=$((fail + 1))
	else
		got_width=$(section_value '[*.zg]' indent_size)
		if [ "$got_width" != "$want_width" ]; then
			echo "WIDTH       F403 counts a tab as $want_width and $EDITORCONFIG says [*.zg] indent_size = ${got_width:-<unset>}"
			fail=$((fail + 1))
		fi
		# BOTH options, because they are load-bearing for different things. 'tabstop' is what a
		# reader sees a tab as; 'shiftwidth' is what `ZergIndent` DIVIDES BY to turn a column
		# count into a level and multiplies by to turn it back. The gate read only the first, so
		# the number the indenter is built on was the one option of the three nothing held.
		for opt in tabstop shiftwidth; do
			if ! grep -qE "^setlocal $opt=$want_width\$" "$FTPLUGIN"; then
				echo "WIDTH       F403 counts a tab as $want_width and $FTPLUGIN does not set $opt=$want_width"
				fail=$((fail + 1))
			fi
		done
	fi
fi

# --- 4. the tree-sitter grammar's keyword list -------------------------------------------
#
# `editors/tree-sitter-zerg/grammar.js` is a SECOND implementation of GRAMMAR and mostly
# cannot be held by a diff — `scripts/treesitter-check.sh` holds it to a corpus instead, and
# says why. One part of it can be, and it is the same part the vim file's is: the reserved
# words. A grammar that names a keyword the lexer does not reserve is claiming a word this
# language does not have, and one that misses a keyword parses it as an identifier.
#
# Only what the grammar spells as a bare quoted word in a `choice` or a `seq` is read here.
# That misses nothing that matters: every keyword in it is written that way.
GRAMMARJS=${GRAMMARJS:-editors/tree-sitter-zerg/grammar.js}
TSQUERY=${TSQUERY:-editors/tree-sitter-zerg/queries/highlights.scm}

# QUOTE-AGNOSTIC, and that is not a detail: this read `'[a-z]+'` when it was written, prettier
# then rewrote grammar.js with double quotes on the next commit, and the extraction went to
# ZERO — the floor below is the only reason it was noticed rather than passing green forever.
# A gate over a file that a formatter owns may not depend on how that formatter quotes.
ts_words() {
	{
		grep -oE "[\"'][a-z]+[\"']" "$1" | tr -d "\"'"

		# A rule whose whole body is one word is INLINED by tree-sitter, so no anonymous token
		# survives for a query to name and the NAMED node is what both files use instead —
		# `(nop_statement)`, `(boolean_literal)`, `(nil_literal)`, `(this_literal)`. They are
		# listed for the reason `set` is listed above: a word this gate would otherwise call
		# missing, whose absence is correct and has a name.
		printf '%s\n' nop true false nil this
	} | sort -u
}

# The check runs TWICE, because the tree-sitter side has two flat word lists and they fail
# differently. `grammar.js` must SPELL every reserved word or it parses one as an identifier;
# `highlights.scm` must NAME every reserved word or the word parses correctly and is drawn as
# ordinary text — which is exactly the defect `zerg.vim`'s own comment records about `close`.
for f in "$GRAMMARJS" "$TSQUERY"; do
	[ -f "$f" ] || continue
	ts_words "$f" >"$tmp/ts.all"

	# The floor every extraction here carries, for the reason the others do: a pattern that
	# stops matching leaves an EMPTY list, and an empty list reports every keyword as missing —
	# which is loud — or reports nothing, which is worse.
	# The floor is on what MATCHED, not on how many words were read: an extraction that goes
	# stale still reads plenty of words, and a raw count cannot tell that from a file which
	# simply has other words in it.
	tsn=$(comm -12 "$tmp/ts.all" "$tmp/lexer" | wc -l | tr -d ' ')
	if [ "$tsn" -lt "${MIN_TS_WORDS:-40}" ]; then
		echo "EMPTY       only $tsn reserved words were found in $f — the extraction has gone stale"
		fail=$((fail + 1))
		continue
	fi

	# `lexer - (lexer ∩ ts.all)` is `lexer - ts.all`; the intersection was a temp file and a
	# process spent saying nothing.
	ts_missing=$(comm -23 "$tmp/lexer" "$tmp/ts.all")
	if [ -n "$ts_missing" ]; then
		echo "UNPARSED    the lexer reserves these and $f does not spell them:"
		echo "$ts_missing" | sed 's/^/  /'
		fail=$((fail + 1))
	fi
done

# --- 5. the ruler -------------------------------------------------------------------------
#
# The ftplugin draws a 'colorcolumn' so a person can see where F403's budget ends, and that
# budget is `fmt_wrap_max()` — the column a flat group must END BEFORE. So the ruler is drawn
# one past it: at 80 the group still fits, and 81 is the first column it does not.
#
# The same argument as the width above. A number the formatter owns, written a second time in
# a file the formatter never reads, is a number that drifts — and this one drifts silently,
# because a ruler in the wrong place looks exactly like a ruler.
want_max=$(fmt_return_int fmt_wrap_max)
if [ -z "$want_max" ]; then
	echo "EMPTY       fmt_wrap_max() could not be read from $FMT — this check is measuring nothing"
	fail=$((fail + 1))
else
	want_ruler=$((want_max + 1))
	if ! grep -qE "^setlocal colorcolumn=$want_ruler\$" "$FTPLUGIN"; then
		echo "RULER       F403 wraps a group that reaches column $want_max and $FTPLUGIN does not set colorcolumn=$want_ruler"
		fail=$((fail + 1))
	fi
fi

if [ $fail -ne 0 ]; then
	echo "editor-align: $fail fact(s) an editor file states that the compiler does not"
	exit 1
fi
echo "editor-align: $n reserved words are coloured; the ftplugin and .editorconfig indent the way fmt writes, ${want_width:-?} wide, ruled at ${want_ruler:-?}"
