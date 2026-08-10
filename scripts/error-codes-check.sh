#!/usr/bin/env bash
#
# error-codes-check — a code is an identity, and an identity nothing verifies is a number.
#
# The compiler reports a rule by a code so a gate can pin the RULE where pinning the
# sentence would turn red every time the prose improved. That only works while three things
# agree, and each of them drifts on its own:
#
#   the SOURCE   — the code a diagnostic actually opens with
#   the GATES    — a case that asserts it, which is what makes the code mean anything
#   the CATALOGUE — the row a reader looks up, in docs/tooling/fmt.md
#
# So this asserts the three are the same set. The failure it exists for is the quiet one: a
# code added to the compiler with no case is an identity nobody checks, and a code in the
# catalogue with no source is a rule a reader looks up and cannot reach. Neither breaks a
# build, and neither would be noticed by any other gate here.
#
# It reads the SOURCES rather than running the compiler, because a code that no program can
# reach still owes a row and a case — being unreachable is a separate finding, and one this
# gate would hide if it only looked at what it could make fire.
#
# The catalogue lives in fmt.md beside the formatter's F and the linter's L codes: three
# schemes, one table per scheme, one page. That is deliberate — a reader who meets `E204`
# and a reader who meets `L105` are asking the same question.
set -uo pipefail

SRC=${SRC:-src/compiler/zerg}
DOC=${DOC:-docs/tooling/fmt.md}
GATES=${GATES:-"scripts/refuse-check.sh scripts/reject-check.sh"}

fail=0

# A code in the source is one that a diagnostic string OPENS with — `raise "E204 …"`,
# `f"E413 …"`, `bad(l, "E109", …)`. A mention inside prose (a comment naming a code it
# explains) is not a report and is not counted, which is why the patterns anchor on the
# quote or on the argument position rather than matching the bare word.
codes_in_source() {
	{
		grep -rhoE '(raise |return )f?"E[0-9]{3} ' "$SRC" | grep -oE 'E[0-9]{3}'
		grep -rhoE ', f?"E[0-9]{3} ' "$SRC" | grep -oE 'E[0-9]{3}'
		grep -rhoE '"E[0-9]{3}",' "$SRC" | grep -oE 'E[0-9]{3}'
	} | sort -u
}

# A code a gate asserts. Both scripts spell the assertion as a bare argument — `expect …
# E204` in one, `reject … 'E103 …'` in the other — so the word is looked for anywhere on a
# line that is a case, and comments are dropped first.
codes_in_gates() {
	# shellcheck disable=SC2086
	grep -hoE '^(expect|reject) [^#]*' $GATES | grep -oE '\bE[0-9]{3}\b' | sort -u
}

# A code the catalogue lists: one row per code, in a table whose first cell is the code.
codes_in_doc() {
	grep -oE '^\| `E[0-9]{3}`' "$DOC" | grep -oE 'E[0-9]{3}' | sort -u
}

src=$(codes_in_source)
gates=$(codes_in_gates)
doc=$(codes_in_doc)

report() {
	local what=$1 list=$2
	[ -z "$list" ] && return 0
	local n
	n=$(printf '%s\n' "$list" | wc -l | tr -d ' ')
	printf 'error-codes-check: %s (%s)\n' "$what" "$n" >&2
	printf '%s\n' "$list" | sed 's/^/    /' >&2
	fail=1
}

report "reported by the compiler, asserted by no gate" "$(comm -23 <(printf '%s\n' "$src") <(printf '%s\n' "$gates"))"
report "asserted by a gate, reported by no source" "$(comm -13 <(printf '%s\n' "$src") <(printf '%s\n' "$gates"))"
report "reported by the compiler, missing from the catalogue" "$(comm -23 <(printf '%s\n' "$src") <(printf '%s\n' "$doc"))"
report "in the catalogue, reported by no source" "$(comm -13 <(printf '%s\n' "$src") <(printf '%s\n' "$doc"))"

# A code used twice is two rules under one identity, which is the thing a code exists to
# prevent — and it cannot be seen by comparing the three sets, because a duplicate is
# present in all of them.
dup=$(grep -rhoE '(raise |return |, )f?"E[0-9]{3} ' "$SRC" | grep -oE 'E[0-9]{3}' | sort | uniq -d)
report "reported from more than one place — two rules under one identity" "$dup"

if [ "$fail" -ne 0 ]; then
	printf 'error-codes-check: the source, the gates and the catalogue disagree\n' >&2
	exit 1
fi

printf 'error-codes-check: %s codes — each reported once, asserted by a gate, and listed\n' \
	"$(printf '%s\n' "$src" | wc -l | tr -d ' ')"
