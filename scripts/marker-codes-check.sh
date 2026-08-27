#!/bin/sh
# marker-codes-check — a `[not yet]` that quotes no code is a claim nothing holds (#86).
#
# `docs/conformance.md` makes one promise about every unbuilt form: *using it is a clean
# compile error naming the form*. A marker that quotes the code — _E9017 NotImplemented:
# an array type `[T; N]`_ — is a claim `make error-codes-check` and the refusal corpus can
# both reach. A marker that quotes nothing is a sentence, and a sentence is checked by
# whoever last read it. Measured on the day this gate was written: of 157 markers in the
# English documents, 123 quoted no code at all — so the standing rule was verifiable for
# about a fifth of the forms that claim it.
#
# THE RULE. Every `**[not yet]**` marker's own block must quote an error code THAT EXISTS —
# a number `src/compiler/zerg/rule.zg` still assigns. Quoting a retired one is the staleness
# #86 names from the other side: the form got built, the rule left the catalogue, and the
# marker beside it kept saying the feature is missing.
#
# The block is the marker's PARAGRAPH plus the fenced sample immediately under it, because
# that is where a refusal is usually shown; it is never the rest of the page, which is the
# reader `deviation-check` used to have and the reason a marker there could borrow a ticket
# from ninety-one lines away.
#
# THE NUMBER HERE IS NOT #86's. That ticket measured 133 markers with no code by reading the
# marker's own SENTENCE; scoped to the block a reader actually meets, it is 53. Neither
# reading is wrong — a code three lines below the marker in the same block does connect the
# marker to its rule, and demanding it be restated in the sentence is a formatting rule
# rather than a claim about the compiler. The block is what this gate holds, and 53 is what
# is owed.
#
# THE INHERITED LIST is `docs/.markers-uncoded`, and it works the way the deviation list
# does — with one clause more, which is the half that makes it a ratchet rather than a
# filing cabinet:
#
#   1. A marker NOT on the list must quote a code. New prose cannot add to the debt.
#   2. An entry ON the list must still be FINDABLE. A marker that was deleted or reworded
#      takes its line with it.
#   3. An entry on the list that NOW QUOTES A CODE must come off. Without this the list
#      only ever grows stale: the work would get done and the countdown would not move.
#
# There is no clause saying the list must be empty. It is a release criterion, and a gate
# that fails until the last day is a gate somebody turns off on the second.
#
# ONLY THE BOLD FORM IS A MARKER. `[not yet]` also appears backticked, in prose that
# describes the marker system, and inside sample programs — none of those is a claim about
# a form. Fenced blocks are skipped whole for that reason.
#
# ONLY THE ENGLISH DOCUMENTS ARE SCANNED, for the reason `deviation-check` gives: the
# zh-TW twin is held to its original by `docs-mirror`, so counting both would double every
# number here.
set -eu

cd "$(dirname "$0")/.."
LIST=docs/.markers-uncoded
fail=0

# THE FLOOR. Every assertion below compares the documents against a list, and an extractor
# that stopped matching compares an empty set to an empty set and reports success. The
# English documents held 157 markers when this was written and the language has a long way
# to go before they thin out; 100 is that number with room to close a chapter's worth.
MIN_MARKERS=${MIN_MARKERS:-100}

pages() {
	find docs -name '*.md' ! -name '*.zh-TW.md' | sort
}

# THE CATALOGUE is `rule.zg`'s own assignments, which is the same file `error-codes-check`
# reads and the one #86 names. A code no longer in it is retired, and a marker quoting one
# is stale by definition.
RULES=${RULES:-src/compiler/zerg/rule.zg}
CODES=$(mktemp)
trap 'rm -f "$CODES"' EXIT
grep -oE '^	[A-Za-z][A-Za-z0-9]* = [0-9]{4}$' "$RULES" | grep -oE '[0-9]{4}$' | sort -u >"$CODES"
[ -s "$CODES" ] || {
	echo "marker-codes-check: no codes read from $RULES — nothing could be compared"
	exit 1
}

# `path<TAB>slug<TAB>CODED|UNCODED`, one row per LINE that carries a marker.
#
# The block is found by walking out from the marker's line to the paragraph's edges — a
# blank line, or inside a blockquote a `>` with nothing after it — and then forward over a
# fenced sample that is attached to it, with at most one blank line between. A marker in a
# table row therefore reads the whole table, which is right: that is where those markers
# put their codes.
scan() {
	awk -v codes="$CODES" '
		BEGIN { while ((getline c < codes) > 0) live[c] = 1 }
		{ line[NR] = $0 }
		END {
			fence = 0
			for (i = 1; i <= NR; i++) {
				if (line[i] ~ /^[[:space:]]*(> )?[[:space:]]*```/) { fence = !fence; fenced[i] = 1; continue }
				fenced[i] = fence
			}
			for (i = 1; i <= NR; i++) {
				if (fenced[i] || line[i] !~ /\*\*\[not yet\]\*\*/) continue
				# THE BLOCK STOPS AT A SIBLING LIST ITEM. A Markdown list carries no blank
				# line between its items, so a walk that ends only at a blank one swallows the
				# whole list — `docs/core/decorators.md` has one thirty-three lines long — and
				# a marker in one bullet is then satisfied by a code in another. That is the
				# reader `deviation-check` had, in a smaller room, and it was doing the same
				# thing here: the `#[repr]` marker quotes no code at all and passed on the
				# `E9079` of the bullet above it.
				#
				# NO APOSTROPHE BELONGS ANYWHERE IN THIS AWK PROGRAM, comments included. It is
				# one single-quoted shell word, so an apostrophe closes the quote and every
				# line after it is read as shell. This paragraph cost two attempts to write:
				# the first put one in the prose, and the second put one in the warning.
				for (s = i; s > 1; s--) {
					if (isitem(line[s])) break
					if (isbreak(line[s - 1])) break
				}
				for (e = i; e < NR; e++)
					if (isbreak(line[e + 1]) || isitem(line[e + 1])) break
				j = e + 1
				if (j <= NR && isblank(line[j])) j++
				if (j <= NR && line[j] ~ /^[[:space:]]*(> )?[[:space:]]*```/) {
					for (k = j + 1; k <= NR; k++)
						if (line[k] ~ /^[[:space:]]*(> )?[[:space:]]*```/) break
					e = k
				}
				coded = "UNCODED"
				for (k = s; k <= e; k++) {
					t = line[k]
					while (match(t, /E[0-9][0-9][0-9][0-9]/)) {
						n = substr(t, RSTART + 1, 4)
						t = substr(t, RSTART + RLENGTH)
						if (n in live) { coded = "CODED"; break }
						coded = "RETIRED " n
					}
					if (coded == "CODED") break
				}
				# A MARKER MAY BE THE WHOLE LINE. `docs/core/specs.md` carries one — a list item
				# wrapped so that `**[not yet]**` sits alone on its continuation — and slugging
				# that line leaves nothing at all, which is not a key: the empty field collapsed
				# against its neighbour and the row read as its own verdict. The key is then the
				# nearest line of the block that has words, looking up first, because that is the
				# sentence the marker is about.
				sl = slug(line[i])
				for (k = i - 1; k >= s && sl == ""; k--) sl = slug(line[k])
				for (k = i + 1; k <= e && sl == ""; k++) sl = slug(line[k])
				if (sl == "") sl = "line " i
				printf "%s\t%s\t%s\n", FILENAME, sl, coded
			}
		}
		function isblank(t) { return t ~ /^[[:space:]]*$/ }
		function isbreak(t) { return isblank(t) || t ~ /^[[:space:]]*>[[:space:]]*$/ }
		function isitem(t) { return t ~ /^[[:space:]]*(>[[:space:]]*)?([-*][[:space:]]|[0-9]+\.[[:space:]])/ }
		function slug(t,   n, w, out, i) {
			gsub(/\[not yet\]/, "", t)
			t = tolower(t)
			gsub(/[^a-z0-9]/, " ", t)
			n = split(t, w, " ")
			out = ""
			for (i = 1; i <= n && i <= 7; i++) out = out (out == "" ? "" : " ") w[i]
			return out
		}
	' "$1"
}

rows=$(for f in $(pages); do scan "$f"; done)
total=$(printf '%s\n' "$rows" | grep -c . || true)
uncoded=$(printf '%s\n' "$rows" | grep -cv '	CODED$' || true)

[ "$total" -ge "$MIN_MARKERS" ] || {
	echo "marker-codes-check: $total markers found, floor is $MIN_MARKERS — the extractor is reading nothing"
	exit 1
}

# --- clause 1: a marker that is not inherited must quote a live code -------------------
# A RETIRED code is never inheritable. The list is for work not done yet; a marker naming a
# number the catalogue no longer assigns is a marker that is already WRONG, and carrying it
# on a countdown would be filing the bug instead of fixing it.
printf '%s\n' "$rows" | while IFS='	' read -r f s c; do
	case "$c" in
	CODED) continue ;;
	RETIRED*) echo "STALE CODE $f: $s — quotes ${c#RETIRED }, which $RULES no longer assigns" ;;
	*)
		grep -qF "$(printf '%s\t%s' "$f" "$s")" "$LIST" 2>/dev/null && continue
		echo "NO CODE    $f: $s"
		;;
	esac
	echo "$f" >>"$LIST.fail"
done
[ -f "$LIST.fail" ] && { fail=$((fail + $(wc -l <"$LIST.fail"))); rm -f "$LIST.fail"; }

# --- clauses 2 and 3: an inherited entry is still there, and still uncoded -------------
while IFS='	' read -r f s; do
	case "$f" in '' | '#'*) continue ;; esac
	row=$(printf '%s\n' "$rows" | grep -F "$(printf '%s\t%s\t' "$f" "$s")" || true)
	if [ -z "$row" ]; then
		echo "GONE       $f: $s — take it off $LIST"
		fail=$((fail + 1))
	elif ! printf '%s\n' "$row" | grep -q '	UNCODED$'; then
		echo "CODED      $f: $s — take it off $LIST"
		fail=$((fail + 1))
	fi
done <"$LIST"

left=$(grep -cv '^#' "$LIST" || true)
[ "$fail" -eq 0 ] || {
	echo "marker-codes-check: the inventory and the documents disagree"
	exit 1
}
echo "marker-codes-check: $total markers — $((total - uncoded)) quote a code, $left inherited and still to close"
