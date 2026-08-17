#!/usr/bin/env bash
#
# docs-links — every path this repo cites resolves, and every `#fragment` on a link names
# a heading that is still on the page it points at.
#
# THE PATH HALF was all there was, and it stops one question short. It asks whether the
# file EXISTS; a page can outlive the thing that was cited from it. When `fmt.md` was split
# into chapters, four citations kept pointing at a page that still existed and no longer
# held what they named — green the whole time.
#
# THE FRAGMENT HALF is the part of that which is decidable. A `#fragment` names a HEADING,
# a heading is a fact about the file, and the two can be compared. It found the defect it
# was written for on its first run: `docs/code/collections.zh-TW.md` linked
# `../core/types.zh-TW.md#typed-positions` — its English counterpart's anchor, on a page
# whose heading reads `### 有型別的位置（Typed positions）` and therefore slugs to
# `有型別的位置typed-positions`. A zh-TW reader had been landing at the top of the page for
# an unknown length of time.
#
# WHAT STAYS UNDECIDABLE, said plainly rather than papered over: a citation in prose — "see
# `docs/tooling/fmt.md`" in a source comment, which is most of what the path half reads —
# names a page and nothing on it. There is no claim to check. Only two kinds of citation in
# this tree name something inside a page: a `#fragment`, which is this script's second half,
# and an error code, which `error-codes-check` already owns. A gate over the rest would have
# to guess, and a gate that guesses is worse than a gate that says what it does not cover.
#
# TWO TRAPS, both of which produced a false positive in a first draft of this:
#
#   GitHub replaces each space with a hyphen and does NOT collapse the runs. `Into` — an
#   ordinary conversion spec` slugs to `into--an-ordinary-conversion-spec`, with TWO
#   hyphens, because the backtick and the em-dash are deleted and the two spaces that were
#   around them both survive as hyphens. Collapsing them reports every such anchor broken.
#
#   The fragment must not be split on whitespace. A same-page link `](#log)` has an EMPTY
#   target, and a `read` with the default IFS drops the empty field and slides the fragment
#   into `target` — after which every same-page anchor in the tree looks like a link to a
#   file named `log`.
#
# A THIRD, which is why the locale is chosen rather than inherited: `[:alnum:]` is what
# separates a letter from punctuation, and under `LC_ALL=C` a CJK heading is neither — every
# byte of it is dropped, `### 有型別的位置（Typed positions）` slugs to `typed-positions`,
# and the broken link above PASSES. The self-test below picks a UTF-8 locale by checking
# that it does not, and fails loudly if no candidate works, because the alternative is a
# gate that is green on the CI runner for the reason it should be red.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

# A FLOOR under the fragment half. The extraction is a pair of regexes over markdown, and a
# regex that stops matching finds no broken anchors for the same reason a clean tree does.
MIN_ANCHORS=${MIN_ANCHORS:-80}

tmp=$(mktemp -d) || exit 2
trap 'rm -rf "$tmp"' EXIT
seen="$tmp/seen"

fail=0

# --- the path half ------------------------------------------------------------------------
#
# Every `docs/…md` path the repo mentions ANYWHERE — most of them plain-text citations in
# source comments, which no link checker reads — and every relative link between pages.
for p in $(git grep -hoE 'docs/[A-Za-z0-9_./-]+\.md' -- . ':!docs' | sort -u); do
	[ -f "$p" ] || {
		echo "CITED     $p"
		fail=1
	}
done

for f in $(git ls-files '*.md'); do
	d=$(dirname "$f")
	for l in $(sed -E 's/\]\(/\n](/g' "$f" |
		sed -nE 's/^\]\((\.\.\/[^)#:]*|[^)#:]*\.md)(#[^)]*)?\).*/\1/p'); do
		[ -e "$d/$l" ] || {
			echo "LINK      $f -> $l"
			fail=1
		}
	done
done

# --- the fragment half ---------------------------------------------------------------------

# slug_line <heading-line> — GitHub's anchor for that heading: drop the `#`s, lowercase,
# delete everything that is not a letter, a digit, a space, `_` or `-`, then each space
# becomes one hyphen.
slug_line() {
	printf '%s\n' "$1" |
		sed -E 's/^#+[[:space:]]+//' |
		tr '[:upper:]' '[:lower:]' |
		sed -E 's/[^[:alnum:][:space:]_-]//g' |
		tr ' ' '-'
}

# Four cases, one per rule the slug has, and the last two are the traps above: the em-dash
# case pins that a run of spaces is NOT collapsed, the CJK case pins that a letter outside
# ASCII survives. Run under each candidate locale until one passes them all.
slug_self_test() {
	[ "$(slug_line '## The claim')" = "the-claim" ] || return 1
	[ "$(slug_line '### `L4xx` — resolution')" = "l4xx--resolution" ] || return 1
	[ "$(slug_line '### 有型別的位置（Typed positions）')" = "有型別的位置typed-positions" ] || return 1
	[ "$(slug_line '## What is _normative_')" = "what-is-_normative_" ] || return 1
	return 0
}

picked=""
for cand in "${LC_ALL:-}" "${LANG:-}" C.UTF-8 en_US.UTF-8 zh_TW.UTF-8; do
	case $cand in
	*UTF-8 | *UTF8 | *utf8) ;;
	*) continue ;;
	esac
	if LC_ALL=$cand slug_self_test 2>/dev/null; then
		picked=$cand
		break
	fi
done

if [ -z "$picked" ]; then
	echo "docs-links: no UTF-8 locale on this machine slugs a CJK heading the way GitHub does,"
	echo "            so every zh-TW anchor would be compared against the wrong string — nothing"
	echo "            was checked. Install one of C.UTF-8 / en_US.UTF-8 / zh_TW.UTF-8."
	exit 1
fi
export LC_ALL=$picked

# slugs_of <page> — every anchor that page answers to, in order. A repeated slug takes
# GitHub's disambiguating suffix (`-1`, `-2`, …), which is how a second `## Status` is
# reached. Lines inside a fenced block are skipped: a `#` there is a comment in a Zerg
# program, and `docs/tooling/lsp.md` has one that reads like a six-level heading.
slugs_of() {
	awk '
		/^[ \t]*```/ { fence = !fence; next }
		fence        { next }
		/^#{1,6}[ \t]/ { print }
	' "$1" | while IFS= read -r h; do
		s=$(slug_line "$h")
		n=$(grep -cxF -- "$s" "$seen" 2>/dev/null || true)
		printf '%s\n' "$s" >>"$seen"
		if [ "${n:-0}" -eq 0 ]; then printf '%s\n' "$s"; else printf '%s-%s\n' "$s" "$n"; fi
	done
}

anchors=0
for f in $(git ls-files '*.md'); do
	d=$(dirname "$f")
	sed -E 's/\]\(/\n](/g' "$f" |
		sed -nE 's/^\]\(([^)#]*)#([^)]+)\).*/\1|\2/p' >"$tmp/links"

	while IFS='|' read -r target frag; do
		[ -n "$frag" ] || continue
		case $target in
		"") page=$f ;;                    # a same-page anchor
		http* | mailto:*) continue ;;     # not ours to check
		*) page="$d/$target" ;;
		esac
		case $page in *.md) ;; *) continue ;; esac
		[ -f "$page" ] || continue # the path half above owns a missing page

		# The slugs go to a FILE before they are searched, and the reason is a defect this
		# gate shipped with: `slugs_of | grep -qx` under `pipefail` reports a BROKEN anchor
		# for a heading that is there. `grep -q` exits at the first match, `slugs_of` dies of
		# SIGPIPE with 141, and `pipefail` hands that up as the pipeline's status — so an
		# anchor passed or failed by WHERE ITS HEADING SITS in the page, late headings
		# passing and early ones failing. Writing the list out first also computes it once
		# per page instead of once per link into it.
		cache="$tmp/slugs.$(printf '%s' "$page" | tr '/.' '__')"
		if [ ! -f "$cache" ]; then
			: >"$seen"
			slugs_of "$page" >"$cache"
		fi
		if ! grep -qx -- "$frag" "$cache"; then
			echo "ANCHOR    $f -> ${target:-(this page)}#$frag names no heading there"
			fail=1
		fi
		anchors=$((anchors + 1))
	done <"$tmp/links"
done

if [ "$anchors" -lt "$MIN_ANCHORS" ]; then
	echo "docs-links: $anchors fragments were checked, below the floor of $MIN_ANCHORS — the link"
	echo "            extraction stopped matching, and no anchors found reads exactly like none broken"
	exit 1
fi

[ "$fail" -eq 0 ] || {
	echo "docs-links: a citation does not resolve"
	exit 1
}

echo "docs-links: every cited docs path resolves, and $anchors link fragments name a heading that is there"
