# shellcheck shell=bash
# grammar.sh — what a production IS, for the two gates that ask about GRAMMAR.
#
# Sourced by grammar-cites (does every citation name a real production?) and by
# grammar-mirror (does the prose companion still say what GRAMMAR says?). It is shared for
# the reason diag.sh is: this is ONE fact about a file's layout, not a list of cases, and a
# second copy of it fails OPEN. An extractor that quietly stops matching reports zero
# unknown citations and zero drifted productions — two green gates measuring nothing.
#
# The rules it encodes, and each is a decision:
#
#   * A DEFINITION starts at column 1: `name ::=`. GRAMMAR aligns the `::=` into a column,
#     so the padding is before it, never after.
#   * A CONTINUATION is an indented line beginning with `|`. That is how an alternation
#     spans lines in both files, and it cannot be confused with a definition because a
#     definition is never indented.
#   * A TRAILING COMMENT needs TWO spaces before its `#`. One space would eat the RHS of
#     `COMMENT ::= '#' [^#[\n] [^\n]*`, where the `#` is the language, not a remark. Every
#     trailing comment in both files is separated by at least two.
#   * WHITESPACE is collapsed to single spaces, because the two files align their columns
#     differently and a column is not a grammatical fact.

# grammar_productions <file> — one line per production, `name<TAB>right-hand-side`.
grammar_productions() {
	awk '
	function strip(s) {
		sub(/[ \t][ \t]+#.*$/, "", s)
		gsub(/[ \t]+/, " ", s)
		sub(/^ /, "", s); sub(/ $/, "", s)
		return s
	}
	/^[A-Za-z][A-Za-z0-9-]*[ \t]*::=/ {
		if (cur != "") print cur "\t" acc
		name = $0; sub(/[ \t]*::=.*$/, "", name)
		rhs  = $0; sub(/^[A-Za-z][A-Za-z0-9-]*[ \t]*::=[ \t]*/, "", rhs)
		cur = name; acc = strip(rhs); next
	}
	/^[ \t]+\|/ { if (cur != "") acc = acc " " strip($0); next }
	{ if (cur != "") { print cur "\t" acc; cur = "" } }
	END { if (cur != "") print cur "\t" acc }
	' "$1"
}

# grammar_self_test — the extractor still behaves as documented. Both gates run this before
# they measure anything, so a rewrite that breaks it fails loudly instead of passing wide.
grammar_self_test() {
	local got bad=0
	got=$(printf '%s\n' \
		"program   ::= stmt-list" \
		"assign-target ::= lvalue" \
		"               | '(' assign-target ')'" \
		"COMMENT   ::= '#' [^#[\\n]  # a line comment" \
		"             | '#' NEWLINE" \
		"# a comment line, not a production" \
		"  indented ::= not-a-definition" \
		| grammar_productions /dev/stdin)

	want() {
		case $got in
		*"$1"*) ;;
		*)
			echo "SELFTEST  the production extractor did not produce: $1"
			bad=1
			;;
		esac
	}
	want "program	stmt-list"
	want "assign-target	lvalue | '(' assign-target ')'"
	want "COMMENT	'#' [^#[\\n] | '#' NEWLINE"

	case $got in
	*"indented"*)
		echo "SELFTEST  an indented line was read as a production definition"
		bad=1
		;;
	esac

	[ $bad -eq 0 ] && return 0
	echo "the production extractor no longer behaves as scripts/lib/grammar.sh documents,"
	echo "so every comparison below would be meaningless and none of them were made"
	return 1
}
