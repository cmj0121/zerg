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
#   * the indent character the ftplugin configures is the one `zerg fmt` actually writes.
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

# The TYPE names are named by the syntax file and are not reserved words — `int` is an
# ordinary identifier the parser resolves, so a program may shadow it and the lexer has
# never heard of it. They are held to their own list, in the parser, for the same reason.
{
	# the scalars, which the parser resolves by name
	grep -oE 'return T[A-Za-z]+ if name == "[a-z]+"' src/compiler/zerg/parser.zg |
		sed -E 's/.*"([a-z]+)"/\1/'

	# and the containers, which it resolves by name AND a following `[`
	grep -oE 'if name == "[a-z]+" and p_at\(p, LBrack\)' src/compiler/zerg/parser.zg |
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

# --- 2. the indent character ------------------------------------------------------------
#
# Asked of `zerg fmt` rather than of F101's documentation, because what a person's editor
# has to agree with is what the tool WRITES.
printf 'fn main() {\n\tif true {\n\t\tprint 1\n\t}\n}\n' >"$tmp/indent.zg"
"$ZERG" fmt "$tmp/indent.zg" >/dev/null 2>&1
if grep -qE '^\t\tprint' "$tmp/indent.zg"; then
	if ! grep -qE '^setlocal noexpandtab' "$FTPLUGIN"; then
		echo "INDENT      zerg fmt indents with a TAB and $FTPLUGIN does not set noexpandtab"
		fail=$((fail + 1))
	fi
else
	if grep -qE '^setlocal noexpandtab' "$FTPLUGIN"; then
		echo "INDENT      zerg fmt does not indent with a tab and $FTPLUGIN sets noexpandtab"
		fail=$((fail + 1))
	fi
fi

if [ $fail -ne 0 ]; then
	echo "editor-align: $fail fact(s) an editor file states that the compiler does not"
	exit 1
fi
echo "editor-align: $n reserved words are coloured, and the ftplugin indents the way fmt writes"
