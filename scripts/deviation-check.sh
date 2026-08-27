#!/bin/sh
# deviation-check — what the 0.2.0 criterion is, as a gate rather than a promise.
#
# A `[deviation]` is the specification saying *the compiler does something else, and we
# know*. Each one is a place where a reader who believed the document is wrong about the
# program they are writing, and a document that is wrong in a known way is worse than one
# that is silent, because it was trusted first (#52).
#
# THREE CLAUSES, and the inherited list is what tells the middle two apart:
#
#   0. Every `**[deviation]**` in the documents is EITHER a marker in the canonical form
#      — a `> **[deviation]**` leading its own blockquote — or a line of the allowlist,
#      which is the handful of places that talk ABOUT the marker rather than raising one.
#      Without this clause the form is optional, and an optional form is one the two
#      clauses below cannot see: `docs/language.md` carried a deviation about the
#      scheduler written inline, in the middle of a sentence, and this gate reported one
#      marker for as long as it has existed while the documents held two.
#
#   1. Every marker NOT on the inherited list must NAME AN ISSUE. A disagreement nobody is
#      accountable for is how "no deviations" stops being met by fixing compilers and
#      starts being met by deleting markers. A marker this release's sweep writes MAY
#      ship unfixed — that is deliberate — but it owes a ticket.
#
#   2. Every marker ON the list must still be FINDABLE. The list is the set the release
#      inherited, and the release closes when it is empty; an entry whose marker is gone
#      is an entry that must come off, so the countdown cannot drift from the documents.
#
# There is no clause saying "the list must be empty", and that is not an omission: it is
# the release criterion, not a build criterion, and a gate that fails until the last day
# is a gate somebody turns off on the second day.
#
# THE KEY IS A SLUG, not a line number: a paragraph added above a marker moves it, and
# file:line would then report work that nobody did. The slug is the marker's first six
# significant words, which survives everything except rewriting the marker — and a marker
# that is about to be deleted does not get rewritten.
#
# A MARKER MAY BE INDENTED. One is: a `> **[deviation]**` nested inside a list item carries two
# spaces before the `>`, and an anchored `^>` counted it as no marker at all — which is the
# shape of hole this gate exists to close, found by `docs-mirror` disagreeing about a count
# this one said was zero.
#
# ONLY THE ENGLISH DOCUMENTS ARE SCANNED. `docs-mirror` already holds each zh-TW page to
# its English original, so counting both would make an inventory of 24 read as 48.
set -eu

cd "$(dirname "$0")/.."
LIST=docs/.deviations-inherited
PROSE=docs/.marker-prose
fail=0

slug() {
	printf '%s' "$1" |
		tr 'A-Z' 'a-z' |
		sed 's/\[deviation\]//g; s/\[implementation-defined\]//g' |
		tr -c 'a-z0-9' ' ' |
		tr -s ' ' |
		sed 's/^ //; s/ $//' |
		cut -d' ' -f1-7
}

pages() {
	find docs -name '*.md' ! -name '*.zh-TW.md' | sort
}

# THE BLOCK A MARKER OWNS is its own blockquote PARAGRAPH: the marker's line, then the
# quote lines under it, stopping where the quote ends (`>` gone) or where the paragraph
# does (a `>` with nothing after it — Markdown's paragraph break inside a quote).
#
# The reader this replaces was `sed -n "${n},\$p" | sed -n '/^>/p' | sed '/^>/!q'`, which
# is every quote line from the marker TO THE END OF THE FILE: the trailing `q` never fires
# because every line it is handed already starts with `>`. So clause 1 was satisfied by a
# ticket ANYWHERE below the marker on the page. Measured on the day this was rewritten:
# the one marker `docs/runtime/package.md` carries names no ticket of its own and passed
# on a `#69` ninety-one lines further down, inside an unrelated `[not yet]` — and #69 is
# a closed bug about a refusal's wording, which has nothing to do with the flat namespace
# the marker is about. The `^>` was wrong twice over, too: the gate's own header says a
# marker may be indented, and for an indented one that anchor matched every line except
# the marker's own block.
block_of() {
	sed -n "$2,\$p" "$1" | awk '
		NR == 1 { print; next }
		/^[[:space:]]*>[[:space:]]*$/ { exit }
		/^[[:space:]]*>/ { print; next }
		{ exit }
	'
}

# --- clause 0: every mention is a canonical marker, or is allowlisted prose ------------
# The allowlist is keyed the same way the inherited list is, by `path<TAB>slug`, so that
# exempting one SENTENCE never exempts a chapter: `docs/conformance.md` defines what the
# marker means and may still one day raise one.
for f in $(pages); do
	grep -n '\*\*\[deviation\]\*\*' "$f" | while IFS=: read -r n rest; do
		case "$rest" in
		*'> **[deviation]**'*) continue ;;
		esac
		s=$(slug "$rest")
		if grep -qF "$(printf '%s\t%s' "$f" "$s")" "$PROSE" 2>/dev/null; then
			continue
		fi
		echo "NOT A MARKER  $f:$n: $s"
		echo "$f" >>"$LIST.fail"
	done
done

# --- every marker in the tree, as `path<TAB>slug` -------------------------------------
found=$(
	for f in $(pages); do
		grep -n '^[[:space:]]*> \*\*\[deviation\]' "$f" | while IFS=: read -r n rest; do
			printf '%s\t%s\n' "$f" "$(slug "$rest")"
		done
	done
)

# --- clause 1: a marker that is not inherited must name an issue -----------------------
for f in $(pages); do
	grep -n '^[[:space:]]*> \*\*\[deviation\]' "$f" | while IFS=: read -r n rest; do
		s=$(slug "$rest")
		if grep -qF "$(printf '%s\t%s' "$f" "$s")" "$LIST" 2>/dev/null; then
			continue
		fi
		if ! block_of "$f" "$n" | grep -q '#[0-9][0-9]*'; then
			echo "NO TICKET  $f: $s"
			echo "$f" >>"$LIST.fail"
		fi
	done
done
[ -f "$LIST.fail" ] && { fail=$((fail + $(wc -l <"$LIST.fail"))); rm -f "$LIST.fail"; }

# --- clause 2: every inherited entry must still be findable ---------------------------
while IFS='	' read -r f s; do
	case "$f" in '' | '#'*) continue ;; esac
	if ! printf '%s\n' "$found" | grep -qF "$(printf '%s\t%s' "$f" "$s")"; then
		echo "CLOSED     $f: $s — take it off $LIST"
		fail=$((fail + 1))
	fi
done <"$LIST"

n=$(printf '%s\n' "$found" | grep -c . || true)
left=$(grep -cv '^#' "$LIST" || true)
[ "$fail" -eq 0 ] || {
	echo "deviation-check: the inventory and the documents disagree"
	exit 1
}
echo "deviation-check: $n markers, $left of them inherited and still to dispose of"
