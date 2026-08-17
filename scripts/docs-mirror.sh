#!/usr/bin/env bash
#
# docs-mirror — every en/zh-TW page pair has the same SHAPE.
#
# This tree keeps 32 documents twice, and until now only four of those pairs were held to
# each other by anything at all: `grammar-mirror` (the productions), `error-codes-check` (the F/L
# catalogue), `seed-gaps-check` (the seed's gap list) and `version-check` (the two READMEs'
# version banner). Each of those compares one KIND of claim inside one named page. The other
# twenty-eight pairs were kept in step by hand, and the way hand-kept things fail is not that
# a sentence is mistranslated — it is that a whole section, a row, or a marker never crossed.
#
# It had already happened, and this gate's first run said where:
#
#   src/runtime/README.zh-TW.md      two whole sections missing (the TSan and ASan chapters)
#   src/compiler/README.zh-TW.md     one section and its table missing
#   docs/core/derive.zh-TW.md        a section in a different place in the document
#   docs/code/coroutine.zh-TW.md     a sentence carrying a [not yet] dropped
#   docs/tooling/fmt.zh-TW.md        a ```text diagram fenced as ```zerg
#
# WHAT IT CANNOT COMPARE is the prose, and that is the whole point of the shape: a
# translation says something different by design, so nothing about the WORDS is checkable.
# What survives translation is the structure the words hang on — how many sections there are
# and how they nest, how many code blocks and in what language, how many table rows, and how
# many of each status marker. A section that never crossed changes all four; a sentence that
# was rewritten changes none.
#
# HEADING TEXT is deliberately not compared, for that reason. A zh-TW heading is a
# translation ("## The claim" / "## 主張"), so its text is not a fact about the pair. The
# LEVEL SEQUENCE is: `# ## ## ### ##` is the document's outline, and dropping, adding or
# moving a section is exactly what changes it.
#
# MARKERS ARE COUNTED PER MARKER, NOT PER LINE. `grep -c` counts LINES, and a zh-TW page
# wraps at a different column than its English twin — two markers that share a line in
# English may land on two lines in Chinese, or the other way round, and either way `grep -c`
# reports a difference that is not one. Worse, it MISSES one: the real defect above is a
# paragraph with two `[not yet]` on one line in English and one in the translation, which
# `grep -c` scores 1 against 1. The count here is over occurrences.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

# A FLOOR, like every gate here. Pair discovery is a glob, and a glob that stops matching
# reports no drift for the same reason a tree with none does.
MIN_PAIRS=${MIN_PAIRS:-30}

tmp=$(mktemp -d) || exit 2
trap 'rm -rf "$tmp"' EXIT

# shape <file> — the document's structure, one claim per line, in source order, each
# prefixed by the line it came from. Everything inside a fence is skipped: a `#` there is a
# Zerg comment, not a heading, and a `|` there is a diagram.
shape() {
	awk '
		BEGIN { fence = 0 }
		/^[ \t]*```/ {
			if (fence) { fence = 0; next }
			fence = 1
			lang = $0
			sub(/^[ \t]*```/, "", lang)
			sub(/[ \t]+$/, "", lang)
			if (lang == "") lang = "(none)"
			printf "%d\tFENCE %s\n", NR, lang
			next
		}
		fence { next }
		/^#{1,6} / { printf "%d\tH%d\n", NR, index($0, " ") - 1; next }
		/^\|/     { printf "%d\tROW\n", NR; next }
	' "$1"
}

# markers <file> — how many of each status marker the page carries, counted by OCCURRENCE.
# The three kinds are docs/conformance.md's; a fourth would have to be added here, and a
# marker that is not one of the three is not a marker.
markers() {
	awk '
		{
			s = $0
			ny += gsub(/\[not yet\]/, "", s)
			id += gsub(/\[implementation-defined\]/, "", s)
			dv += gsub(/\[deviation\]/, "", s)
		}
		END {
			printf "not-yet %d\n", ny + 0
			printf "implementation-defined %d\n", id + 0
			printf "deviation %d\n", dv + 0
		}
	' "$1"
}

fail=0
pairs=0

for zh in $(git ls-files '*.zh-TW.md'); do
	en="${zh%.zh-TW.md}.md"
	if [ ! -f "$en" ]; then
		echo "ORPHAN    $zh translates a page that is not there"
		fail=$((fail + 1))
		continue
	fi
	pairs=$((pairs + 1))

	shape "$en" >"$tmp/en"
	shape "$zh" >"$tmp/zh"

	# Walk both in lockstep and report the FIRST divergence. Everything after it is a
	# consequence of it — one dropped section shifts the rest of the stream — so a full
	# diff would name a hundred lines for one edit.
	if ! cmp -s <(cut -f2 "$tmp/en") <(cut -f2 "$tmp/zh"); then
		awk -v en="$en" -v zh="$zh" '
			NR == FNR { el[FNR] = $0; sub(/^[0-9]+\t/, "", el[FNR]); eln[FNR] = $1; en_n = FNR; next }
			{ zl[FNR] = $0; sub(/^[0-9]+\t/, "", zl[FNR]); zln[FNR] = $1; zh_n = FNR }
			END {
				n = en_n > zh_n ? en_n : zh_n
				for (i = 1; i <= n; i++) {
					if (el[i] == zl[i]) continue
					printf "MIRROR    %s and %s part company\n", en, zh
					printf "            %s:%s  %s\n", en, (i <= en_n ? eln[i] : "eof"), (i <= en_n ? el[i] : "(nothing left)")
					printf "            %s:%s  %s\n", zh, (i <= zh_n ? zln[i] : "eof"), (i <= zh_n ? zl[i] : "(nothing left)")
					exit
				}
			}
		' "$tmp/en" "$tmp/zh"
		fail=$((fail + 1))
	fi

	markers "$en" >"$tmp/enm"
	markers "$zh" >"$tmp/zhm"
	paste "$tmp/enm" "$tmp/zhm" | while IFS="$(printf '\t')" read -r a b; do
		[ "$a" = "$b" ] || echo "MARKERS   $en has ${a% *} ×${a##* }, $zh has ×${b##* }"
	done >"$tmp/mdiff"
	if [ -s "$tmp/mdiff" ]; then
		cat "$tmp/mdiff"
		fail=$((fail + 1))
	fi
done

if [ "$pairs" -lt "$MIN_PAIRS" ]; then
	echo "docs-mirror: $pairs pairs found, below the floor of $MIN_PAIRS — the discovery glob"
	echo "             stopped matching, and a tree with no pairs has no drift to report"
	exit 1
fi

if [ "$fail" -ne 0 ]; then
	echo "docs-mirror: $fail finding(s) — a page and its translation are not the same document"
	exit 1
fi

echo "docs-mirror: $pairs en/zh-TW pairs — same outline, same blocks, same rows, same markers"
