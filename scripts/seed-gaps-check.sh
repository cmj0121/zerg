#!/usr/bin/env bash
#
# seed-gaps-check — the seed's gap list says the same thing in both languages.
#
# `src/bootstrap/README.md` holds the seed's own contract: the rules `zerg` enforces and the
# seed does not, one entry each, and `scripts/reject-check.sh` marks a case `seed-gap` against
# them by name. It is the one list a reader consults to find out why a case is excused, so a
# reader of the other language needs it too.
#
# It had drifted as far as a page can: the English section had 27 entries and `README.zh-TW.md`
# had NO SUCH SECTION AT ALL. That was not a decision anybody made — it happened one entry at a
# time, and each time the person adding one had no reason to look at a file their change did not
# touch. The same shape as the error-code catalogue next door, and found the same way: by a
# reviewer reading both, which is not a mechanism.
#
# WHAT THIS ASSERTS IS THE COUNT, and that is deliberate rather than a first approximation. The
# two texts are translations, so no comparison of their CONTENT survives ordinary editing — a
# reworded entry is not a defect. What cannot be a translation choice is one side having an
# entry the other does not, and the count is exactly that claim with nothing else in it.
#
# It is a gate rather than a note in a review because the next divergence will arrive the same
# way as the last: in a change that has a good reason to touch one file and none to touch the
# other.
set -uo pipefail

EN=${EN:-src/bootstrap/README.md}
ZH=${ZH:-src/bootstrap/README.zh-TW.md}

# The section bounds in each file: the heading that opens the gap list, and the heading that
# ends it. They are parameters because they are the part most likely to be reworded, and a
# rewording should fail LOUDLY here rather than quietly emptying the extraction — which is
# what the floor below is for.
EN_OPEN=${EN_OPEN:-'^## Known gaps$'}
EN_CLOSE=${EN_CLOSE:-'^## Changing the seed$'}
ZH_OPEN=${ZH_OPEN:-'^## 已知落差$'}
ZH_CLOSE=${ZH_CLOSE:-'^## 修改種子時$'}

fail=0

note() {
	printf 'seed-gaps: %s\n' "$1" >&2
	fail=1
}

for f in "$EN" "$ZH"; do
	[ -f "$f" ] || {
		printf 'seed-gaps-check: %s is not there — nothing was compared\n' "$f" >&2
		exit 1
	}
done

# entries <file> <open> <close> — the count of list entries between two headings.
#
# An entry is a top-level `- **`: every one in this section leads with a bolded sentence
# naming the gap, in both languages. A continuation line is indented and a nested list would
# be too, so anchoring on the column is what keeps this counting entries rather than lines.
# `stop` and not `close`, which is not a style choice: `close` is an awk BUILT-IN, and BSD awk
# rejects the whole program for using it as a variable — `syntax error`, both extractions
# empty, and the equality below satisfied by two blanks. Caught here only because the floor
# was already written; without it this gate would have shipped reporting success.
entries() {
	awk -v open="$2" -v stop="$3" '
		$0 ~ open { on = 1; next }
		$0 ~ stop { on = 0 }
		on && /^- \*\*/ { n++ }
		END { print n + 0 }
	' "$1"
}

n_en=$(entries "$EN" "$EN_OPEN" "$EN_CLOSE")
n_zh=$(entries "$ZH" "$ZH_OPEN" "$ZH_CLOSE")

# AND THE COUNTS HAVE TO BE NUMBERS. An awk that bails out prints nothing, and `[ "" -lt 20 ]`
# is a shell ERROR rather than a false — it writes `integer expected` to stderr, leaves `fail`
# untouched, and the script exits 0 having compared nothing. That is this gate failing open in
# the one way its own floor cannot catch, so the guard is separate from the floor.
for n in "$n_en" "$n_zh"; do
	case $n in
	'' | *[!0-9]*)
		printf 'seed-gaps-check: an entry count did not extract as a number (en=%s zh=%s) — nothing was compared\n' \
			"$n_en" "$n_zh" >&2
		exit 1
		;;
	esac
done

# A FLOOR, for the reason every negative assertion in this repository has one: the claim below
# is an EQUALITY, and nothing satisfies an equality more readily than two zeros. A renamed
# heading, a list that stops using `- **`, a file reorganised into subsections — each empties
# both extractions, and `0 = 0` reports success for having compared nothing at all.
#
# 20 against the 32 entries there are today. Far enough below that closing a few seed gaps is
# not a chore — and closing them is the direction this list moves — and far enough above that a
# section which lost most of itself, or its heading, cannot pass.
MIN_ENTRIES=${MIN_ENTRIES:-20}

if [ "$n_en" -lt "$MIN_ENTRIES" ]; then
	note "$EN yielded $n_en entries, below the floor of $MIN_ENTRIES — its gap section no longer reads as a list, so nothing was compared"
elif [ "$n_zh" -lt "$MIN_ENTRIES" ]; then
	note "$ZH yielded $n_zh entries, below the floor of $MIN_ENTRIES — its gap section no longer reads as a list, so nothing was compared"
elif [ "$n_en" -ne "$n_zh" ]; then
	note "the seed's gap list has $n_en entries in $EN and $n_zh in $ZH — a gap recorded in one language and not the other is one half its readers cannot look up"
fi

if [ "$fail" -ne 0 ]; then
	printf 'seed-gaps-check: the two gap lists do not say the same thing\n' >&2
	exit 1
fi

printf 'seed-gaps-check: %s seed gaps, recorded in both languages\n' "$n_en"
