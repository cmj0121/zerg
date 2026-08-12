#!/usr/bin/env bash
#
# layering-check — what each stage is ALLOWED TO KNOW.
#
# Two claims are made about this language and neither is testable by running it:
#
#   the PARSER builds the AST from TOKENS ALONE — no symbol table, no types, no
#   scope. It is why `zerg fmt` and `zerg lsp` can read a file that does not compile,
#   why an editor can colour a buffer mid-edit, and why GRAMMAR is the whole story
#   about syntax rather than most of it.
#
#   INFERENCE is BOTTOM-UP — a subexpression's type is computed from the
#   subexpression, never from what surrounds it, except at four carve-outs the
#   specification names (docs/core/types.md).
#
# A test cannot see either one. Both are about what the code MAY REACH, and a
# compiler that consults a symbol table in its parser passes every test it has —
# right up to the day someone asks it to format a file with an undefined name in it.
# So this reads the SOURCES, and asserts the reach.
#
# It holds BOTH compilers to it. The seed is the oracle for behaviour and would be a
# poor one for structure — it is the older design, and the shipping compiler is the
# one the specification describes — so each side is asserted in the shape its own
# language makes checkable: the seed by its package imports and its bidirectional
# checker's switch, `zerg` by which of its sibling files it calls into.
#
# Every assertion here is NEGATIVE — "reaches nothing in that layer" — so every one of
# them fails open. That is what the floors are for: an extraction that stops matching
# reports an empty set, and an empty set satisfies every claim below.
set -uo pipefail

ZG=${ZG:-src/compiler/zerg}
GO=${GO:-src/bootstrap/internal}
GRAMMAR=${GRAMMAR:-GRAMMAR}

fail=0

note() {
	printf 'layering: %s\n' "$1" >&2
	fail=1
}

# Every file this reads is named here, because a file that is GONE extracts to nothing
# and nothing satisfies a negative claim. The per-layer floors below cannot see it: a
# layer is several files, and one of the three disappearing still leaves the other two
# over the floor — measured, with `check.zg` and `generic.zg` removed and the gate
# still reporting success on the strength of `emit.zg` alone.
for f in "$GO/parser/parser.go" "$GO/sema/check.go" "$GRAMMAR" \
	"$ZG/parser.zg" "$ZG/fmt.zg" "$ZG/desugar.zg" "$ZG/check.zg" "$ZG/emit.zg" "$ZG/generic.zg"; do
	[ -f "$f" ] || note "$f is not there — this gate reads sources, and an absent one asserts nothing"
done
[ "$fail" -eq 0 ] || {
	printf 'layering-check: the sources it reads are not where it reads them\n' >&2
	exit 1
}

# --- extraction -------------------------------------------------------------------
#
# A DEFINITION is a top-level `fn name(` — the module's namespace, since the `zerg`
# compiler is one module whose files share it. A CALL is a name followed by `(` that
# is NOT preceded by a dot: a method call `x.parse(` names a method on a type, and
# methods live with their types rather than in the file namespace, so counting them
# would report a collision with any sibling function of the same name.
zg_defs() {
	grep -hoE '^(pub )?fn [a-zA-Z_][A-Za-z0-9_]*' "$@" | awk '{print $NF}' | sort -u
}

zg_calls() {
	grep -hoE '(^|[^.A-Za-z0-9_])[a-z_][A-Za-z0-9_]*\(' "$1" |
		grep -oE '[a-z_][A-Za-z0-9_]*' | sort -u
}

# THE LAYER, extracted ONCE. Three callers below ask the same question of the same three
# files — 640 KB of source — and each re-grepped them, so `emit.zg` alone was read six times
# for one answer that cannot change between the calls.
LAYER_FILES="$ZG/check.zg $ZG/emit.zg $ZG/generic.zg"
LAYER_NAME="the checker and the emitter"
# shellcheck disable=SC2086
LAYER_DEFS=$(zg_defs $LAYER_FILES)

if [ "$(printf '%s\n' "$LAYER_DEFS" | wc -l)" -lt 50 ]; then
	note "$LAYER_NAME definitions did not extract — $(printf '%s\n' "$LAYER_DEFS" | wc -l) names found"
	printf 'layering-check: the layer it measures against could not be read\n' >&2
	exit 1
fi

# reaches_into <caller.zg> — the caller must call nothing defined in the layer. A name the
# caller defines itself is its own, not a reach.
reaches_into() {
	local caller=$1
	local hits
	hits=$(comm -12 <(zg_calls "$caller") <(printf '%s\n' "$LAYER_DEFS") |
		comm -23 - <(zg_defs "$caller"))
	if [ -n "$hits" ]; then
		note "$(basename "$caller") reaches into $LAYER_NAME: $(printf '%s' "$hits" | tr '\n' ' ')"
	fi
}

# fields_of <file> <struct> — the field names of a struct declaration, in either
# language: `name: ty` in Zerg, `name ty` in Go.
#
# The indent is a LITERAL tab built by printf, never `\t` inside the pattern, for the
# reason editor-align.sh carries in full: BSD grep reads `\t` as a tab and GNU grep reads
# it as an undefined escape, i.e. the letter `t`. The pattern therefore matched a field on
# macOS and nothing at all on Linux — so both extractions came back empty in CI, and every
# claim below them is NEGATIVE ("reaches nothing in that layer"), which an empty set
# satisfies. The floors are what stood between that and a gate reporting success on two
# structs it never read.
TAB=$(printf '\t')

# A field may be declared `pub`, and every field in this compiler now is: GRAMMAR requires a
# non-`pub` field to carry a default, and none of these has one. The marker is read past
# rather than matched on, because what this asks about is the field's NAME — and a pattern
# that quietly stopped matching would empty the set, which is the failure the paragraph above
# describes and the floors below exist to catch.
zg_fields() {
	awk -v s="$2" '$0 ~ "^(pub )?struct " s " \\{" {on=1; next} on && /^}/ {exit} on' "$1" |
		grep -oE "^$TAB(pub )?[a-z_][A-Za-z0-9_]*:" | sed "s/^$TAB//; s/^pub //; s/:\$//" | sort -u
}

go_fields() {
	awk -v s="$2" '$0 ~ "^type " s " struct \\{" {on=1; next} on && /^}/ {exit} on' "$1" |
		grep -oE "^$TAB[a-zA-Z_][A-Za-z0-9_]*[ $TAB]" | tr -d "$TAB " | sort -u
}

# extra <declared> <actual> — what the actual set has that the declared one does not.
extra() {
	comm -13 <(printf '%s\n' "$1" | tr ' ' '\n' | sort -u) <(printf '%s\n' "$2")
}

# --- 1. the parser answers with tokens alone ---------------------------------------

# The seed's import graph is the strongest form this check takes: a symbol table
# cannot be reached without importing the package that holds one, and Go says so.
PARSER_IMPORTS="ast diag lexer token"
imports=$(grep -hoE '"github.com/cmj0121/zerg/src/bootstrap/internal/[a-z]+"' "$GO"/parser/*.go |
	grep -oE '[a-z]+"$' | tr -d '"' | sort -u)
if [ "$(printf '%s\n' "$imports" | wc -l)" -lt 3 ]; then
	note "the seed parser's imports did not extract"
else
	got=$(extra "$PARSER_IMPORTS" "$imports")
	[ -z "$got" ] || note "the seed's parser imports $(printf '%s' "$got" | tr '\n' ' ')— beyond ast/diag/lexer/token"
fi

# Its STATE says the same thing a second way, and catches what an import cannot: a
# table built out of nothing but the standard library.
PARSER_FIELDS="toks pos diags noBrace"
gf=$(go_fields "$GO/parser/parser.go" parser)
if [ -z "$gf" ]; then
	note "the seed parser's fields did not extract"
else
	got=$(extra "$PARSER_FIELDS" "$gf")
	[ -z "$got" ] || note "the seed's parser carries $(printf '%s' "$got" | tr '\n' ' ')— state beyond the token cursor"
fi

# `zerg` is one module, so its layering is which SIBLING FILES the parser calls into.
# `c_is_ctor` lived in the emitter and the parser called it — a lexical predicate in
# the wrong file, which read as the parser reaching into codegen. It is `name_is_type`
# in token.zg now, and this is what would have said so.
reaches_into "$ZG/parser.zg"

ZPARSER_FIELDS="toks pos impl_ty path depth edepth"
zf=$(zg_fields "$ZG/parser.zg" Parser)
if [ -z "$zf" ]; then
	note "the zerg parser's fields did not extract"
else
	got=$(extra "$ZPARSER_FIELDS" "$zf")
	[ -z "$got" ] || note "zerg's Parser carries $(printf '%s' "$got" | tr '\n' ' ')— state beyond the token cursor"
fi

# --- 2. formatting and desugaring need no name resolution --------------------------
#
# The same claim one layer out, and the one a user meets: `zerg fmt` on a file with an
# undefined name in it must still be `zerg fmt`. `lint.zg` is deliberately NOT here —
# a linter that asks whether a method mutates its receiver is asking a checked
# question, and says so by calling into the checker.
reaches_into "$ZG/fmt.zg"
reaches_into "$ZG/desugar.zg"

# --- 3. inference is bottom-up -----------------------------------------------------
#
# The four CARVE-OUTS are the whole exception list (docs/core/types.md): an untyped
# literal adopts its position's type, a composite literal with no element to speak for
# it takes one, a closure's omitted parameter types come from the function type being
# checked, and a value entering a carrier is wrapped. Everything else synthesizes.
#
# In the seed the exception list is LITERAL — `checkExpr` switches on the node kinds
# that read their context and falls through to `synthExpr` for the rest — so the
# assertion is that the switch is exactly that set. A new case there is a new
# carve-out, and this is the line that makes it say so.
CARVE_OUTS="ast.IntLit ast.FloatLit ast.Unary ast.ListLit ast.ListFill ast.MapLit ast.TupleLit ast.FnExpr"
cases=$(awk '/^func \(c \*checker\) checkExpr\(/ {on=1} on && /^}/ {exit} on' "$GO/sema/check.go" |
	grep -oE 'case \*ast\.[A-Za-z]+' | sed 's/case \*//' | sort -u)
if [ "$(printf '%s\n' "$cases" | wc -l)" -lt 5 ]; then
	note "the seed's context-sensitive switch did not extract"
else
	got=$(extra "$CARVE_OUTS" "$cases")
	[ -z "$got" ] || note "the seed pushes context into $(printf '%s' "$got" | tr '\n' ' ')— a carve-out the specification does not name"
fi

# `zerg` splits it the other way: nothing named `c_infer…` takes an expected type at
# all, and the four carve-outs are answered where the wanted type is already in hand —
# `chk_lit_fits`, `c_carrier_fit`, `c_into`, `c_either_wrap` — which take a `want` and
# return C or a verdict, never a TYPE. So the assertion is on the inference family's
# signatures.
infers=$(grep -cE '^fn c_infer[a-z_]*\(' "$ZG/emit.zg")
if [ "$infers" -lt 8 ]; then
	note "the inference family did not extract — $infers functions found"
else
	leak=$(grep -nE '^fn c_infer[a-z_]*\([^)]*\b(want|wanted|expected|hint)\b' "$ZG/emit.zg")
	[ -z "$leak" ] || note "an inference function takes an expected type: $leak"
fi

# And neither compiler may carry one as STATE, which is the way an expected type gets
# in without appearing in a signature.
for f in $(zg_fields "$ZG/emit.zg" Emitter) $(go_fields "$GO/sema/check.go" checker); do
	case $f in
	want | wanted | expected | hint | want_ty | expect_ty)
		note "an expected type is carried as state: $f"
		;;
	esac
done

# --- 4. GRAMMAR describes syntax alone ---------------------------------------------
#
# The two phrases GRAMMAR used to carry — a production "resolved with scope in hand",
# a form where "NAME RESOLUTION disambiguates" — were the syntax admitting it needed
# the checker. They are gone, and their absence is the same claim as section 1 stated
# where a reader meets it.
res=$(grep -niE 'scope in hand|name resolution|symbol table' "$GRAMMAR")
[ -z "$res" ] || note "GRAMMAR defers to name resolution: $res"

# -----------------------------------------------------------------------------------
if [ "$fail" -ne 0 ]; then
	printf 'layering-check: a stage reaches past what it is allowed to know\n' >&2
	exit 1
fi

printf 'layering-check: both parsers see tokens only, both inferences bottom-up over four carve-outs (%s node kinds)\n' \
	"$(printf '%s\n' "$CARVE_OUTS" | tr ' ' '\n' | wc -l | tr -d ' ')"
