#!/bin/sh
# deviation-check — what the 0.2.0 criterion is, as a gate rather than a promise.
#
# A `[deviation]` is the specification saying *the compiler does something else, and we
# know*. Each one is a place where a reader who believed the document is wrong about the
# program they are writing, and a document that is wrong in a known way is worse than one
# that is silent, because it was trusted first (#52).
#
# TWO CLAUSES, and the inherited list is what tells them apart:
#
#   1. Every marker NOT on the list must NAME AN ISSUE. A disagreement nobody is
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
# ONLY THE ENGLISH DOCUMENTS ARE SCANNED. `docs-mirror` already holds each zh-TW page to
# its English original, so counting both would make an inventory of 24 read as 48.
set -eu

cd "$(dirname "$0")/.."
LIST=docs/.deviations-inherited
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

# --- every marker in the tree, as `path<TAB>slug` -------------------------------------
found=$(
	for f in $(find docs -name '*.md' ! -name '*.zh-TW.md' | sort); do
		grep -n '^> \*\*\[deviation\]' "$f" | while IFS=: read -r n rest; do
			printf '%s\t%s\n' "$f" "$(slug "$rest")"
		done
	done
)

# --- clause 1: a marker that is not inherited must name an issue -----------------------
for f in $(find docs -name '*.md' ! -name '*.zh-TW.md' | sort); do
	grep -n '^> \*\*\[deviation\]' "$f" | while IFS=: read -r n rest; do
		s=$(slug "$rest")
		if grep -qF "$(printf '%s\t%s' "$f" "$s")" "$LIST" 2>/dev/null; then
			continue
		fi
		# the block is this line and every `>` line under it
		block=$(sed -n "${n},\$p" "$f" | sed -n '/^>/p' | sed '/^>/!q')
		if ! printf '%s' "$block" | grep -q '#[0-9][0-9]*'; then
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
