#!/bin/sh
# cname-space-check — the `zg_` C space belongs to the program's own names, and a generated
# name grows linearly with the type it is keyed by.
#
# A declaration's C name is `zg_<name>`, and that spelling is the `--emit lib` ABI
# (docs/runtime/ffi.md). Every name the emitter GENERATES for itself — a carrier, a drop, a
# renderer, a vtable — is `zgh_<what>…`, spelled by one family of functions. When a generated
# name was spelled under `zg_` by hand, a `fn render_Foo` met the emitter's own `zg_render_Foo`
# and cc reported a redeclaration against generated C (#489). Nothing in a test run reaches
# that until a program happens to pick the name, so this reads the sources.
#
# IT READS THEM THROUGH THE COMPILER'S OWN LEXER (`zerg build --emit tokens`), so what a
# literal is — and what a comment, a rune or a byte is — is the language's answer, not this
# script's. The text examined is every string-literal token: plain, raw, triple-quoted, an
# f-string's and a command's text. In that text, a `zg_` or `zgh_` STARTS A C IDENTIFIER when
# no identifier byte is before it; `.zg_` and `->zg_` are field access, in a struct's
# namespace, and are the two exclusions. An f-string's or command's text is its source
# spelling, so a `\` escape in it separates like the byte it stands for.
#
# A hit belongs to the function whose body holds it: a `fn <name>` at brace depth 0 opens that
# function, from its signature to its body's closing brace. Anything else at depth 0 — a
# `const`, a `struct`, an `impl` and the methods inside it — is outside every function, and a
# hit there is never allowed.
#
# THE RULE, over every non-test `src/compiler/zerg/*.zg`:
#
#   a `zg_` start sits in a function that spells a USER name:
#     emit.zg c_fn_cname        a declared function's or method's symbol, `zg_[<Type>_]<name>`
#     emit.zg c_ty_cname        a declared type's name
#     emit.zg c_const_cname     a module or associated constant's symbol
#     emit.zg c_fresh_cname     a new binding takes its source name, numbered when taken
#     emit.zg c_var_cname       a source name resolved to the binding it took
#     emit.zg c_struct_typedef  a declared field, which lives in its struct's namespace and
#                               never in the file scope a generated symbol takes
#     check.zg chk_defaults     a declared parameter, bound under its own name to type a
#                               default; check.zg sits above emit.zg and c_fresh_cname is
#                               emit's private binder, so the spelling is written here
#
#   a `zgh_` start sits in the function that spells a GENERATED name:
#     emit.zg c_helper_name     the generated space itself, `zgh_<what>[_<key>]`
#     emit.zg c_helper_ofs      the same space keyed by parts, each spelled by c_key
#
# THE LENGTH. A key is often a name the emitter already spelled — a tuple in a tuple, the drop
# of a carrier — so an escape that doubled a byte per level grew a name exponentially: a
# left-nested tuple fifteen deep was a 262 KB identifier. The gate writes a fixture at depth 1, 2,
# and then doubling up to $DEPTH: a left-nested tuple, list, optional and generic struct, each
# printed, so carriers, drops, renderers and specializations are generated. Per family of names
# (`zgh_<what>`, `zgt_`, `zga_`, `zgg_`), the longest name's step from depth d/2 to d is at most
# twice its step from d/4 to d/2 — a level costs what the level before it cost — plus one byte
# for each run of digits in the name, since a length or a position written in decimal gains at
# most one digit when the depth doubles. A key that grows exponentially or quadratically fails
# at a small depth, before cc is handed a huge identifier.
#
# EVERY ASSERTION HERE IS NEGATIVE, and an empty set satisfies a negative assertion. So each
# allowlisted function must be declared in its file and must still spell what it is listed
# for — a rename, or a site that moved, fails here instead of leaving an entry that guards
# nothing — at least one hit of each kind must be found at all, and each fixture must emit at
# least one generated name that grows.
set -eu

DIR=${DIR:-src/compiler/zerg}
ZERG=${ZERG:-bin/zerg}
DEPTH=${DEPTH:-32}
LC_ALL=C
export LC_ALL

USER_SITES='emit.zg:c_fn_cname
emit.zg:c_ty_cname
emit.zg:c_const_cname
emit.zg:c_fresh_cname
emit.zg:c_var_cname
emit.zg:c_struct_typedef
check.zg:chk_defaults'

HELPER_SITES='emit.zg:c_helper_name
emit.zg:c_helper_ofs'

[ -d "$DIR" ] || { echo "cname-space-check: no $DIR" >&2; exit 1; }
[ -x "$ZERG" ] || { echo "cname-space-check: no $ZERG — run make build" >&2; exit 1; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# One line per hit: `<kind> <file>:<function> <file>:<line>: <token>`, `-` for no function.
for f in "$DIR"/*.zg; do
	case $f in *_test.zg) continue ;; esac
	"$ZERG" build --emit tokens "$f" >"$tmp/tokens" || {
		echo "cname-space-check: $ZERG could not lex $f" >&2
		exit 1
	}
	awk -v file="${f##*/}" '
		# the value as --emit tokens quotes it (lexer.zg quote): an escape that stands for a
		# non-identifier byte becomes a space
		function unq(s,    out, i, n, ch, nx) {
			out = ""
			n = length(s)
			for (i = 1; i <= n; i++) {
				ch = substr(s, i, 1)
				if (ch != "\\") { out = out ch; continue }
				nx = substr(s, i + 1, 1)
				if (nx == "\"" || nx == "\\") out = out nx
				else out = out " "
				i += (nx == "x") ? 3 : 1
			}
			return out
		}
		# source text, where a `\` begins an escape
		function unesc(s,    out, i, n, ch) {
			out = ""
			n = length(s)
			for (i = 1; i <= n; i++) {
				ch = substr(s, i, 1)
				if (ch == "\\") { out = out "  "; i++ } else out = out ch
			}
			return out
		}
		function starts(s, pre,    rest, i, off, p, pp) {
			rest = s
			off = 0
			while ((i = index(rest, pre)) > 0) {
				p = (off + i > 1) ? substr(s, off + i - 1, 1) : ""
				pp = (off + i > 2) ? substr(s, off + i - 2, 1) : ""
				if (p !~ /[A-Za-z0-9_]/ && p != "." && !(p == ">" && pp == "-")) return 1
				off += i
				rest = substr(s, off + 1)
			}
			return 0
		}
		{
			tab = index($0, "\t")
			pos = substr($0, 1, tab - 1)
			tok = substr($0, tab + 1)
			if (tok == "fn" && depth == 0 && pend == "") { sawfn = 1; next }
			if (sawfn) {
				sawfn = 0
				if (tok ~ /^IDENT\(/) { pend = substr(tok, 7, length(tok) - 7); next }
			}
			# `#[` opens an attribute and the lexer gives it no separate `[`
			if (tok == "(" || tok == "[" || tok == "#[") { pd++; next }
			if (tok == ")" || tok == "]") { pd--; next }
			if (tok == "{") {
				if (depth == 0 && pd == 0 && pend != "") body = 1
				depth++
				next
			}
			if (tok == "}") {
				depth--
				if (depth == 0 && body) { pend = ""; body = 0 }
				next
			}
			if (tok == ";" && depth == 0 && pd == 0 && !body) { pend = ""; next }
			if (tok !~ /^(STR|RAWSTR|CMD|FSTR|FSTR_BEGIN|FSTR_TEXT|FCMD_BEGIN|FCMD_TEXT)\("/) next
			kind = substr(tok, 1, index(tok, "(") - 1)
			text = unq(substr(tok, length(kind) + 3, length(tok) - length(kind) - 4))
			if (kind != "STR" && kind != "RAWSTR") text = unesc(text)
			at = file ":" (pend == "" ? "-" : pend) " " file ":" substr(pos, 1, index(pos, ":") - 1) ": " tok
			if (starts(text, "zg_")) print "zg " at
			if (starts(text, "zgh_")) print "zgh " at
		}
		END { if (depth != 0 || pd != 0) print "bal " file " ends at brace depth " depth ", bracket depth " pd }
	' "$tmp/tokens" >>"$tmp/hits"
done
touch "$tmp/hits"
if grep "^bal " "$tmp/hits" >&2; then
	echo "cname-space-check: the token walk lost its place, so no hit above can be placed in a function" >&2
	exit 1
fi

fail=0

# check <kind> <allowlist> <what the allowlist is>
check() {
	kind=$1
	sites=$2
	what=$3
	grep "^$kind " "$tmp/hits" >"$tmp/$kind" || true
	n=$(grep -c . <"$tmp/$kind" || true)
	if [ "$n" -lt 1 ]; then
		echo "cname-space-check: no \`${kind}_\` literal found in $DIR — the extraction measured nothing" >&2
		fail=1
	fi
	for s in $sites; do
		file=${s%%:*}
		fn=${s#*:}
		if ! grep -qE "^(pub )?fn ${fn}[([]" "$DIR/$file"; then
			echo "cname-space-check: $fn is listed as $what and $DIR/$file declares no such function — a rename leaves this entry guarding nothing" >&2
			fail=1
		elif ! grep -q "^$kind $s " "$tmp/$kind"; then
			echo "cname-space-check: $file $fn is listed as $what and spells no \`${kind}_\` literal — the entry is stale" >&2
			fail=1
		fi
	done
	while read -r _ site rest; do
		case "
$sites
" in
		*"
$site
"*) ;;
		*)
			where=${site#*:}
			[ "$where" != - ] || where="no function"
			echo "cname-space-check: $rest" >&2
			echo "          \`${kind}_\` is spelled in $where, which is not $what" >&2
			fail=1
			;;
		esac
	done <"$tmp/$kind"
}

check zg "$USER_SITES" "a user-name site"
check zgh "$HELPER_SITES" "the helper-name function"

# fixture <depth> — a left-nested tuple, list, optional and generic struct, each printed
fixture() {
	ty=int
	val=1
	lt=int
	lv=1
	wt=int
	wv=1
	i=0
	while [ "$i" -lt "$1" ]; do
		ty="($ty, int)"
		val="($val, 2)"
		lt="list[$lt]"
		lv="[$lv]"
		wt="Wrap[$wt]"
		wv="Wrap(v: $wv)"
		i=$((i + 1))
	done
	printf 'struct Wrap[T] {\n\tpub v: T\n}\n\nfn main() {\n'
	printf '\tt: %s = %s\n\tprint(t)\n\tl: %s = %s\n\tprint(l)\n' "$ty" "$val" "$lt" "$lv"
	printf '\to: %s? = t\n\tprint(o!)\n\tw: %s = %s\n\tprint(w)\n}\n' "$ty" "$wt" "$wv"
}

# names <depth> — per family, `<family> <length> <digit runs>` of its longest name; a family is
# the `zgh_<what>`, or the bare `zgt_` / `zga_` / `zgg_` prefix
names() {
	fixture "$1" >"$tmp/n$1.zg"
	"$ZERG" build --emit c "$tmp/n$1.zg" >"$tmp/n$1.c" 2>"$tmp/n$1.err" || {
		echo "cname-space-check: the depth-$1 fixture did not compile:" >&2
		sed 's/^/          /' "$tmp/n$1.err" >&2
		return 1
	}
	{ grep -oE '(^|[^A-Za-z0-9_])zg[hgta]_[A-Za-z0-9_]*' "$tmp/n$1.c" || true; } |
		sed -E 's/^[^z]*//' | awk '
			{
				f = $0
				if (f ~ /^zgh_/) { sub(/^zgh_/, "", f); sub(/_.*/, "", f); f = "zgh_" f } else f = substr(f, 1, 4)
				if (length($0) > m[f]) { m[f] = length($0); t = $0; r[f] = gsub(/[0-9]+/, "", t) }
			}
			END { for (f in m) print f, m[f], r[f] }
		' | sort >"$tmp/L$1"
}

# step <d> — each family at depths d/4, d/2 and d: its step from d/2 to d is at most twice the
# step from d/4 to d/2, plus one byte per digit run of its name at d
step() {
	join "$tmp/L$(($1 / 4))" "$tmp/L$(($1 / 2))" | join - "$tmp/L$1" | awk -v d="$1" '
		{
			s = $6 - $4
			p = $4 - $2
			if (s > 2 * p + $7) {
				printf "cname-space-check: %s grows %d bytes from depth %d to %d, over 2 × %d (the step before) + %d (a digit per count) — a level costs more the deeper it is\n", $1, s, d / 2, d, p, $7
				bad = 1
			}
			if (p > 0) grew = 1
		}
		END { if (bad) exit 1; if (!grew) { printf "cname-space-check: no family grew from depth %d to %d — the length was measured on nothing\n", d / 4, d / 2; exit 1 } }
	' >&2
}

for d in 1 2; do
	names "$d" || exit 1
done
if [ ! -s "$tmp/L1" ]; then
	echo "cname-space-check: the depth-1 fixture emitted no zgh_/zgt_/zga_/zgg_ name — the length was measured on nothing" >&2
	fail=1
fi
d=2
while [ "$fail" -eq 0 ] && [ "$d" -lt "$DEPTH" ]; do
	d=$((d * 2))
	names "$d" || exit 1
	step "$d" || fail=1
done

if [ "$fail" -ne 0 ]; then
	echo "cname-space-check: a generated name is spelled \`zgh_\` through c_helper_name, linear in its key; \`zg_\` is the program's names alone (#489)" >&2
	exit 1
fi

deep=$(awk '$2 > m { m = $2 } END { print m + 0 }' "$tmp/L$d")
echo "cname-space-check: $(grep -c . <"$tmp/zg") \`zg_\` and $(grep -c . <"$tmp/zgh") \`zgh_\` literals, each in the function that owns its space; generated names grow by a level's step to depth $d, the longest $deep bytes"
