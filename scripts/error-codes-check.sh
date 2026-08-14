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
# THAT IS ABOUT A DIAGNOSTIC, AND AN ICE IS NOT ONE. A refusal whose message opens with
# `internal:` reports that the COMPILER is in a state its own structure rules out — an
# unreachable match arm kept loud (parser.zg's `p_impossible`) — and it is outside this
# scheme entirely, deliberately: a code is an identity a gate pins to a PROGRAM, and there
# is no program to write a case from. Giving one a number would turn this gate red with
# "asserted by no gate", correctly, and the only way to satisfy it would be to delete a
# check that is doing its job. Unreachable-but-about-a-program is the separate finding above;
# unreachable-because-the-compiler-is-broken is not a diagnostic at all.
#
# The catalogue lives in fmt.md beside the formatter's F and the linter's L codes: three
# schemes, one table per scheme, one page. That is deliberate — a reader who meets `E204`
# and a reader who meets `L105` are asking the same question.
#
# AND IT ANSWERS ONE MORE QUESTION, which is the reason for the second half of this script:
# what is the NEXT FREE CODE. Three collisions happened in one week — `E387`, `E477` and
# `E288`/`E289` — each because parallel work could only grep the catalogue and could not see
# a sibling's choice. This gate catches a collision once both halves are in one tree, which
# is too late to be cheap; reporting the next free code per range is what lets it be asked
# BEFORE, by a person or by an agent, with one command.
#
# That answer is only worth having while every number below a range's high-water mark is
# accounted for. A code that is neither in the live table nor in the RETIRED one is a gap,
# and a gap is a number somebody may reissue without knowing it once meant something else —
# so a gap fails here. Retiring is the deliberate way to leave one behind: the number is
# never reused, an old build's message keeps its meaning, and the range counts on from its
# mark.
set -uo pipefail

SRC=${SRC:-src/compiler}
DOC=${DOC:-docs/tooling/fmt.md}

# The TRANSLATED catalogue, held to the same code set. It is asked separately from the three
# lists above because it answers a different question: those ask whether a code is real, and
# this asks whether a reader of the other language can look it up. Nothing gated it, so the
# two drifted the first time somebody added a code — four rows landed in the English table and
# none in this one, inside a commit that updated two other zh-TW pages, which means the
# omission was not a decision anybody made.
DOC_ZH=${DOC_ZH:-docs/tooling/fmt.zh-TW.md}
GATES=${GATES:-"scripts/refuse-check.sh scripts/reject-check.sh"}

fail=0

# A code in the source is one that a diagnostic string OPENS with — `raise "E204 …"`,
# `f"E413 …"`, `bad(l, "E109", …)`. A mention inside prose (a comment naming a code it
# explains) is not a report and is not counted, which is why the patterns anchor on the
# quote or on the argument position rather than matching the bare word.
#
# `--include='*.zg'` because `grep -r` reads whatever is in the tree, and what is in the tree
# is not only the compiler: an editor's `parser.zg.bak`, a patch left by a rebase, a copy
# made while comparing two versions — each of them a SECOND source for every code it holds,
# so this reports 99 duplicates and the gate is red for a file nobody meant to keep. A gate
# that can be broken by a stray backup is a gate people learn to disbelieve.
code_reports() {
	grep -rhoE --include='*.zg' '((raise |return |, )f?"E[0-9]{3} |"E[0-9]{3}",)' "$SRC" | grep -oE 'E[0-9]{3}'
}

codes_in_source() {
	code_reports | sort -u
}

# A code a gate asserts. Both scripts spell the assertion as a bare argument — `expect …
# E204` in one, `reject … E307 …` in the other — so the word is looked for anywhere on a
# line that is a case, and comments are dropped first.
#
# A case may be INDENTED, because a family of them is written as a loop over the shapes it
# covers. Anchoring on the column rather than on the word would have hidden every code only
# such a family asserts, and reported it as a code no gate pins.
codes_in_gates() {
	# shellcheck disable=SC2086
	grep -hoE '^[[:space:]]*(expect|reject) [^#]*' $GATES | grep -oE '\bE[0-9]{3}\b' | sort -u
}

# A code the catalogue lists: one row per code, in a table whose first cell is the code.
#
# The RETIRED table has the same shape, so the two are told apart by which side of the
# `### Retired codes` heading a row is on rather than by the row itself. A heading is what
# a reader already uses to tell them apart, and giving the retired rows a different column
# layout would be a second spelling of a code for the sake of a grep.
codes_in_doc() {
	sed -n "1,/^### Retired codes/p" "$DOC" |
		grep -oE '^\| `E[0-9]{3}`' | grep -oE 'E[0-9]{3}' | sort -u
}

codes_retired() {
	sed -n "/^### Retired codes/,\$p" "$DOC" |
		grep -oE '^\| `E[0-9]{3}`' | grep -oE 'E[0-9]{3}' | sort -u
}

# The translated table, live and retired rows together: what the zh-TW page owes is the same
# SET of codes, and which side of its own retired heading each one sits on is that page's
# business rather than this gate's.
codes_in_doc_zh() {
	grep -oE '^\| `E[0-9]{3}`' "$DOC_ZH" | grep -oE 'E[0-9]{3}' | sort -u
}

# WHICH STAGE A RANGE BELONGS TO, read from the catalogue's range table — `| \`E2xx\` | parser
# | … |` — as `2 parser`, one line per range.
#
# It is read rather than listed here for the reason the walk below reads its ranges: the
# catalogue is the authority on what the scheme IS, and a copy in this script is a second
# authority that only has to be right until somebody edits one of them. It matters more now
# than it did, because a stage may own more than one range: `E2xx` filled and the parser's
# numbers continue in `E6xx`, so "the next free code" has to be answered per STAGE or it
# answers twice and names neither — which is the collision this half of the script exists to
# prevent, arriving by a different road.
stages_in_doc() {
	grep -oE '^\| `E[0-9]xx` \| [a-z]+' "$DOC" | sed -E 's/^\| `E([0-9])xx` \| /\1 /' | sort -u
}

src=$(codes_in_source)
gates=$(codes_in_gates)
doc=$(codes_in_doc)
retired=$(codes_retired)
doc_zh=$(codes_in_doc_zh)

# A FLOOR, and it is on the catalogue rather than on the compiler. Every comparison below is
# a set difference, and a difference against an empty set is empty: a renamed heading, a
# reformatted table, a `sed` range that stops matching, and all four `report` calls go quiet
# while the range walk finds no marks to walk to. 150 against the 258 rows there are today.
MIN_CODES=${MIN_CODES:-150}
n_doc=$(printf '%s\n' "$doc" | grep -c .)
if [ "$n_doc" -lt "$MIN_CODES" ]; then
	printf 'error-codes-check: the catalogue yielded %s codes, below the floor of %s\n' "$n_doc" "$MIN_CODES" >&2
	printf 'error-codes-check: %s no longer reads as one table per scheme, so nothing was compared\n' "$DOC" >&2
	exit 1
fi

# AND THE SAME FLOOR UNDER THE TRANSLATION, which the paragraph above is exactly as true of:
# the two comparisons against `doc_zh` are set differences, and one of them is
# `English minus zh-TW`, so an extraction that stops matching empties the zh-TW side and that
# difference becomes the whole catalogue — loud. The other direction, `zh-TW minus English`,
# goes SILENT instead, and silence is what a floor is for.
#
# ONE FLOOR FOR BOTH TABLES, and the same number, because the assertion right below is that
# they list the same SET: two independently tunable water lines for one set is two knobs that
# can contradict each other. It is compared against a count that includes the retired rows
# (codes_in_doc_zh does not split them, deliberately), so it is measured against a larger
# base than the English one — 150 against the 310 rows there are today, where the English
# floor is 150 against 292 live ones. Both are far enough below to survive ordinary growth
# and far enough above that a table which lost most of itself cannot pass.
n_doc_zh=$(printf '%s\n' "$doc_zh" | grep -c .)
if [ "$n_doc_zh" -lt "$MIN_CODES" ]; then
	printf 'error-codes-check: the zh-TW catalogue yielded %s codes, below the floor of %s\n' "$n_doc_zh" "$MIN_CODES" >&2
	printf 'error-codes-check: %s no longer reads as a table of codes, so the mirror was not compared\n' "$DOC_ZH" >&2
	exit 1
fi

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

# AND THE SAME SET IN THE OTHER LANGUAGE. Both directions, because both have happened: a code
# added to one table and not the other leaves a reader who cannot look it up, and a row left
# behind in the translation names a rule that no longer exists.
report "listed in English, missing from the zh-TW catalogue" \
	"$(comm -23 <(printf '%s\n%s\n' "$doc" "$retired" | sort -u) <(printf '%s\n' "$doc_zh"))"
report "listed in the zh-TW catalogue, missing from English" \
	"$(comm -13 <(printf '%s\n%s\n' "$doc" "$retired" | sort -u) <(printf '%s\n' "$doc_zh"))"

# EVERY RULE HAS ONE, which is the half the three sets cannot see: they compare codes that
# already exist, so a rule reported with no code at all is absent from all three and nothing
# fires. The reporting functions take the code as an ARGUMENT, so the compiler's own arity
# rule already refuses a call that omits it — what is left to check is that the argument is a
# code rather than a string that happens to be there, and the only calls exempt are the
# forwarding ones that pass a `code` they were handed.
uncoded=$(grep -rnE 'chk_at\(|chk_at_place\(|chk_note\(|chk_note_at\(|diag_at\(|p_diag\(|Diag\(' "$SRC" --include='*.zg' |
	grep -vE '"[ELF][0-9]{3}"|, code,|fstr_slice\(|fn (chk_(at|note)|p?_?diag_at|p_diag)|:[[:space:]]*#|list\[(zerg\.)?Diag\]')
report "reported without a code — a rule with no identity is one no gate can pin" "$uncoded"

# THE PARSER REPORTS THROUGH ITS CHANNEL, and this is what keeps that true one site at a
# time. The rule above holds a channel CALL to a literal code; it cannot see a refusal that
# never calls one, and the parser's refusals are raises — which is precisely how they drifted
# in the first place, each site free to spell a code and a place or to spell neither.
#
# So one shape is asserted instead: no raise there takes a string LITERAL. That is exactly
# what the pattern below sees and it is worth saying what it does NOT — `raise m` after
# `m := "…"` passes, and so does `raise anything(…)`. The second one MUST pass: every site
# here raises a call, `p_diag` and `p_impossible` alike, so a rule against calls would be a
# rule against the channel. What is left is a hole one deliberate variable wide, and the
# alternative — a list of the channel's entry points — is worse: it needs adding to every
# time a rule earns a helper of its own, and the day it is not, the site it names is the one
# going unchecked. This catches the way the drift actually happened, which is somebody
# writing the sentence where they stood.
bypass=$(grep -nE '(^|[^A-Za-z_])raise f?"' "$SRC/zerg/parser.zg" | grep -vE ':[[:space:]]*#')
report "a parser refusal raising a string literal — it owes a code and a place, and a hand-written message carries whatever its author remembered" "$bypass"

# A code used twice is two rules under one identity, which is the thing a code exists to
# prevent — and it cannot be seen by comparing the three sets, because a duplicate is
# present in all of them.
#
# It reads `code_reports`, the SAME extraction the source set is built from, because it once
# had its own copy of the pattern: when the checked rules moved their code out of the message
# and into an argument, `codes_in_source` learned the new shape and this line did not, so 93
# of 203 codes could be given to two rules with nothing saying so. A gate that scans for a
# thing twice is a gate that scans for two things eventually.
dup=$(code_reports | sort | uniq -d)
report "reported from more than one place — two rules under one identity" "$dup"

# A code that is BOTH live and retired is the one state the two tables must never be in
# together: a reader looking it up gets two answers, and the range walk below would accept
# the number twice over.
report "listed as live and as retired at once" \
	"$(comm -12 <(printf '%s\n' "$doc") <(printf '%s\n' "$retired"))"

# THE RANGE WALK, and it is what makes "the next free code" a fact rather than a guess. Each
# range counts from its own `x01` up to its high-water mark, and every number in between is
# either listed above or retired below. A number that is neither is a GAP: nothing says
# whether it once meant something, so nothing stops the next rule taking it and quietly
# reassigning every message an old build ever printed with it.
#
# The mark is per range, and it is the LIVE-or-retired maximum rather than the live one —
# retiring the highest code in a range must not hand the number straight back.
#
# THE RANGES ARE READ FROM THE CATALOGUE rather than listed here, because a hardcoded
# `1 2 3 4 5` is a second statement of which ranges exist: the day the parser's numbers ran
# past `E298` and continued in `E6xx`, the walk would have skipped the new range entirely —
# no gap check, no next-free answer — and said nothing, which is the one failure mode this
# whole script is against.
#
# AND A FULL RANGE SAYS SO. `mark + 1` is the next free code only while there is one: at
# `x99` it names the first number of the NEXT range, which belongs to a different stage. The
# advice was the thing this script exists to make trustworthy, and E2xx reached `E298` — one
# number from handing out `E300` to whoever asked for a parser code.
#
# THE ANSWER IS PER STAGE, not per range, and that is the difference a continuation range
# makes. A person does not ask "what is free in E2xx", they ask "what number does the next
# PARSER rule take" — and once one stage owns two ranges, a per-range answer names two
# numbers and cannot say which. So the ranges are grouped by the stage the catalogue gives
# them and the answer is the highest range's, because that is what a continuation MEANS: the
# parent range is closed, and a number left below it is retired rather than held open, or
# there are two places to allocate from and the collision is back.
all_codes=$(printf '%s\n%s\n' "$doc" "$retired" | grep -E '^E[0-9]{3}$' | sort -u)
stages=$(stages_in_doc)

# `<digit> <answer>` per range, accumulated in a string rather than an associative array:
# `declare -A` is bash 4, and the bash a macOS box has without homebrew is 3.2.
range_answer=""
for r in $(printf '%s\n' "$all_codes" | sed -E 's/^E([0-9]).*/\1/' | sort -u); do
	in_range=$(printf '%s\n' "$all_codes" | grep -E "^E$r" | sed 's/^E//')
	[ -z "$in_range" ] && continue
	mark=$(printf '%s\n' "$in_range" | sort -n | tail -1)

	missing=""
	n=$((r * 100 + 1))
	while [ "$n" -le "$mark" ]; do
		printf '%s\n' "$in_range" | grep -qx "$n" || missing="$missing E$n"
		n=$((n + 1))
	done
	if [ -n "$missing" ]; then
		printf 'error-codes-check: E%sxx has a number that is neither listed nor retired (%s)\n' \
			"$r" "$(printf '%s' "$missing" | wc -w | tr -d ' ')" >&2
		printf '   %s\n' "$missing" >&2
		printf '    a gap is a code the next rule may reissue, silently reassigning every message an old build printed with it\n' >&2
		printf '    retire it in the catalogue instead, with the reason\n' >&2
		fail=1
	fi
	if [ "$mark" -ge $((r * 100 + 99)) ]; then
		range_answer="$range_answer$r E${r}xx-is-full
"
	else
		range_answer="$range_answer$r E$((mark + 1))
"
	fi

	# A RANGE IN USE THAT THE CATALOGUE DOES NOT DESCRIBE. The stage a code belongs to is the
	# one thing about it a reader cannot work out from the number, and the answer below is
	# grouped by it — so a range with no row is a range whose codes are advice nobody can act
	# on, and it would drop out of the report silently rather than say why.
	if ! printf '%s\n' "$stages" | grep -q "^$r "; then
		printf 'error-codes-check: E%sxx holds codes and the range table in %s does not describe it\n' "$r" "$DOC" >&2
		printf '    add a row naming the stage it reports for; the next-free answer is grouped by that name\n' >&2
		fail=1
	fi
done

# ONE ANSWER PER STAGE, from the highest range that stage owns. See the paragraph above the
# walk: a stage may own two ranges and only one of them is open.
next_free=""
for stage in $(printf '%s\n' "$stages" | awk '{print $2}' | sort -u); do
	top=$(printf '%s\n' "$stages" | awk -v s="$stage" '$2 == s {print $1}' | sort -n | tail -1)
	answer=$(printf '%s\n' "$range_answer" | awk -v r="$top" '$1 == r {print $2}')
	[ -z "$answer" ] && answer="E${top}01"
	next_free="$next_free $stage $answer,"
done
next_free=${next_free%,}

if [ "$fail" -ne 0 ]; then
	printf 'error-codes-check: the source, the gates and the catalogue disagree\n' >&2
	exit 1
fi

# THE ANSWER, printed on the way out whether or not anybody asked, because the cost of
# printing it is a line and the cost of not having it was three collisions in a week.
printf 'error-codes-check: %s codes — each reported once, asserted by a gate, and listed (%s retired)\n' \
	"$(printf '%s\n' "$src" | wc -l | tr -d ' ')" \
	"$(printf '%s\n' "$retired" | grep -c .)"
printf 'error-codes-check: next free code per stage —%s\n' "$next_free"
