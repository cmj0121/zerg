#!/usr/bin/env bash
#
# gates-check — a gate nobody runs is a gate that finds nothing.
#
# This repository's gates are make targets, and there are three places a target has
# to appear before it protects anything: the makefile that defines it, `LINUX_GATES`
# that names the board, and the workflow that runs the board on every push. Adding a
# gate touches the first one and it is easy to stop there — the target works, `make x`
# is green, and nothing ever runs it again.
#
# That is not hypothetical. When this was written, SEVEN targets on `LINUX_GATES` were
# absent from the workflow — `lint`, `lint-check`, `fmt-corpus`, `grammar-keywords`,
# `install-check`, `test`, `layering` — and two more, `cache-key-check` and
# `error-codes-check`, were on no list at all. Nine gates, each green whenever somebody
# thought to type it.
#
# So: every gate the makefiles define is on the board, and everything on the board is
# run by CI. What a gate ASSERTS is its own business; this is only about whether anyone
# asks it.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/ledger.sh
. "$ROOT/scripts/lib/ledger.sh"

tmp=$(mktemp -d) || exit 2
trap 'rm -rf "$tmp"' EXIT

MAKEFILE=${MAKEFILE:-Makefile}
GATES_MK=${GATES_MK:-mk/gates.mk}
WORKFLOW=${WORKFLOW:-.github/workflows/ci.yml}

fail=0
MAKE_BIN=${MAKE_BIN:-make}

note() {
	printf 'gates: %s\n' "$1" >&2
	fail=1
}

for f in "$MAKEFILE" "$WORKFLOW"; do
	[ -f "$f" ] || {
		printf 'gates-check: %s is not there — nothing was checked\n' "$f" >&2
		exit 1
	}
done

# WHICH FILES THE GATES ARE WRITTEN IN. Not necessarily one: the root Makefile may keep
# the verbs and `include` the file the gates live in, and a check that went on reading
# `Makefile` alone would then find a dozen targets, no LINUX_GATES at all, and either
# report a complete board while looking at the verbs or fall over on the extraction
# below. A gate that guards the board's completeness must not be blinded by the board
# being reorganised.
#
# It follows the root's own `include` lines rather than being handed a path, so whatever
# make parses is what this reads. An include that names a file which is not there is an
# error and not a shorter list — the same standard the loop above holds $MAKEFILE to.
MAKEFILES=$MAKEFILE
for inc in $(sed -n 's/^include  *//p' "$MAKEFILE"); do
	# `include $(GATES_MK)` is a path make expands and this script does not, because it
	# reads the makefile as text. One simple reference is resolved against the `NAME :=
	# value` in the same file — enough for a path named once and used twice, and anything
	# more elaborate is caught below as a file that is not there rather than skipped.
	case $inc in
	'$('*')' | '${'*'}')
		name=${inc#??}
		name=${name%?}
		inc=$(sed -n "s/^$name *:\{0,1\}= *//p" "$MAKEFILE" | head -1)
		;;
	esac
	[ -f "$inc" ] || {
		printf 'gates-check: %s includes %s and it is not there — nothing was checked\n' \
			"$MAKEFILE" "$inc" >&2
		exit 1
	}
	MAKEFILES="$MAKEFILES $inc"
done

# NOT A GATE. Each of these is a command rather than an assertion, and the reason is
# per-entry rather than a pattern — which is why it is a LEDGER and not a pattern: a name
# with the sentence that earned it, held in both directions by scripts/lib/ledger.sh.
#
# THE SECOND DIRECTION IS THE NEW HALF. This was a space-separated list with its reasons in
# the prose above it, so a name could outlive the target it excused: delete `release-smoke`
# and its exemption sits here forever, silently excusing the next target somebody gives that
# name. Three lists in this file had that shape and none of them had a stale clause.
ledger_self_test || exit 1

cat >"$tmp/not-a-gate" <<'LEDGER'
all	an ordinary verb of a Makefile
clean	an ordinary verb of a Makefile
run	an ordinary verb of a Makefile
help	an ordinary verb of a Makefile
upgrade	an ordinary verb of a Makefile
install	it CHANGES the machine; `install-check` is the gate
uninstall	it CHANGES the machine; `install-check` is the gate
fmt	it rewrites sources; `fmt-self` is the gate
test	it IS the board, and a board on the board recurses
linux-ci	it IS the board, and a board on the board recurses
release	it BUILDS an artifact, and the board builds nothing to keep
release-tarball	it BUILDS an artifact, and the board builds nothing to keep
release-smoke	it asks about a tarball on a machine that has no repository, which is a question only a release can put; `make release` runs it, so a tarball cannot be produced without it, and there is no tarball on every commit
build-deps	DATA, not a question: it prints the prerequisite list `bin/zerg` is rebuilt for, one file per line, so `build-deps-check` can hold the rule to it without a second copy
install-editors	the half of an install that belongs to a PERSON rather than to a prefix — nvim's syntax and LSP client, and the cloc definition — and a board that ran it would rewrite the editor configuration of whoever is running the board
uninstall-editors	the other half of that, and the same reason
LEDGER

# shellcheck disable=SC2086 # $MAKEFILES is a list of paths and is meant to split
targets=$(grep -hoE '^[a-z][a-z0-9-]*:' $MAKEFILES | tr -d ':' | sort -u)
# shellcheck disable=SC2086 # ditto
board=$(grep -hoE '^LINUX_GATES \?= .*' $MAKEFILES | sed 's/^LINUX_GATES ?= //' | tr ' ' '\n' | grep -v '^$' | sort -u)

if [ "$(printf '%s\n' "$board" | wc -l)" -lt 20 ]; then
	note "LINUX_GATES did not extract — $(printf '%s\n' "$board" | wc -l) entries found"
	printf 'gates-check: the board could not be read\n' >&2
	exit 1
fi

# 1. every gate the Makefile defines is on the board.
for t in $targets; do
	ledger_lookup "$t" "$tmp/not-a-gate" >/dev/null && continue
	printf '%s\n' "$board" | grep -qx "$t" ||
		note "\`make $t\` is a gate the board does not name — add it to LINUX_GATES, or to the not-a-gate list with its reason"
done

# 1b. AND THE OTHER DIRECTION: a name excused from the board still names a target. Without
#     this the exemption outlives the thing it excused, and the next target given that name
#     inherits an excuse nobody wrote for it.
printf '%s\n' "$targets" | sed 's/$/\tdefined/' >"$tmp/targets"
while IFS="$(printf '\t')" read -r name reason; do
	note "\`$name\` is excused from the board — \"$reason\" — and no makefile defines it any more"
done < <(ledger_stale "$tmp/targets" - "$tmp/not-a-gate")

# 2. everything on the board is run by CI. The workflow spells a gate as its own step,
#    `run: make <target>`, so that a failure names the gate rather than the board.
#
#    WHAT THIS CANNOT SEE: it asserts the step is PRESENT, and the property wanted is that
#    the step RUNS. Board gates sit behind `if: steps.corpus_fetch.outputs.available ==
#    'true'` because they need the private submodule, and a skipped step is green — which is
#    the exact failure the header above recounts, one level up. Closing it means the board
#    being single-sourced (CI running `make test`, or its steps generated from a matrix) rather
#    than a third copy of the list this script compares against; until then the conditional
#    ones are trusted, and that is the declared limit of clause 2.
#
#    HOW MANY THEY ARE IS COUNTED, not written down. This paragraph said six and the workflow
#    header said nine about the same set, which was thirteen: a prose count of a list drifts
#    the moment a step joins it, and a limit whose SIZE is wrong reads as a smaller limit than
#    it is. The tagline names them, so the trusted set is in front of whoever reads the run.
#    A leading `VAR=value` is allowed on the run line. A gate may need one — `treesitter`
#    takes `REQUIRE=1` so that a runner without node FAILS rather than skipping — and the
#    alternative was a target that reads the variable itself, which would hide from a reader
#    of the workflow the fact that CI asks a stricter question than a developer does.
for t in $board; do
	grep -qE "run: ([A-Z_]+=[^ ]+ )*make (-j[0-9]+ )?$t\$" "$WORKFLOW" ||
		note "\`make $t\` is on the board and the workflow never runs it"
done

# CLAUSE 3 — a name on the board has a RECIPE behind it.
#
# Clauses 1 and 2 compare three lists of names against each other, and three lists can agree
# perfectly about a gate that does not exist. Delete a target while its name stays on
# LINUX_GATES and in the workflow: this script counted it, `make <gate>` answered `Nothing to
# be done` and exited 0, and the board printed it OK. A gate that measures nothing looks
# exactly like a gate that finds nothing, which is the failure this whole script is against.
#
# It only reads that way because the name is `.PHONY` — make has no file to look for, so it
# has nothing to complain about. Without `.PHONY` the same deletion is a loud `No rule to
# make target`, so for a while the three gates that had fallen off the `.PHONY` line were
# the only ones protected here, by an omission.
#
# `make -n` prints the commands a target WOULD run; a target with no recipe prints none.
for t in $board; do
	# `make -n` prints the commands, but it also prints its OWN lines to stdout — `Nothing to
	# be done for X` is the very case being caught here, so both `make: ` and `make[n]: ` go.
	[ -n "$($MAKE_BIN -n "$t" 2>/dev/null | grep -vE '^make(\[[0-9]+\])?: ')" ] ||
		note "\`make $t\` is on the board and has no recipe — the board would report it OK"
done

if [ "$fail" -ne 0 ]; then
	printf 'gates-check: a gate is defined, or listed, but not run\n' >&2
	exit 1
fi

# The conditional set, derived from the workflow rather than remembered. `grep -A2` is the
# same window clause 2 reads the step through: the `if:` line, then the `run:` line under it.
conditional=$(grep -A 2 "if: steps.corpus_fetch.outputs.available == 'true'" "$WORKFLOW" |
	grep -oE 'run: ([A-Z_]+=[^ ]+ )*make [a-z-]+' | sed 's/.*make //' | sort -u)
n_cond=$(printf '%s\n' "$conditional" | grep -c . || true)

printf 'gates-check: %s gates — each on the board, each run by CI\n' \
	"$(printf '%s\n' "$board" | wc -l | tr -d ' ')"
# CLAUSE 4 — a gate that READS the private corpus is either guarded or says it tolerates the
# absence.
#
# The derived set above was printed and compared to nothing, and this file's own doctrine is
# that a printed number asserts nothing. What it costs was measured the hard way: `examples`
# reads `test-data/examples` and is NOT behind the fetch, so every CI job that does not fetch
# the corpus answered `NO-OUT` for a file that was there, one repository over — and the fix
# went into the recipe rather than into the rule.
#
# THE RULE IS NOT "reads it ⇒ must be conditional". Several gates read the corpus and work
# without it, by design. So the ones that tolerate the absence are a LEDGER, with the reason
# per entry, and anything else that reads it must be behind the fetch. A gate added tomorrow
# is in one of the two sets or it is a finding — and a name here that no longer reads the
# corpus, or that has since moved behind the fetch, is a finding too, which is the direction
# a bare list could not be asked.
cat >"$tmp/tolerates" <<'LEDGER'
entry-path	it reads the corpus and skips when the file is not there
examples	every example builds, is checked and is run without the corpus; only the comparison is missing
gates	the only `test-data/` in gates-check.sh is the pattern this clause matches WITH, and a rule must not find itself
grammar-cited	it reads `test-data/counterexamples/INVENTORY` and SKIPS when the file is not there
lsp	the examples alone clear every floor it sets and its one-walk case reads only src/compiler; the corpus only widens the buffers
oracle	it compares the two compilers over whatever programs it is handed
treesitter	it parses the sources it is given, and the corpus only widens the set
LEDGER

# READS IT THROUGH ITS SCRIPT, TOO. The recipe was the whole window, and a gate whose recipe
# is one `./scripts/x.sh` line reads the corpus INSIDE that script — ten of them do, and this
# clause saw none. A rule that only looks where the reading happens to be spelled is the shape
# this file exists to catch, one level down.
reads_corpus() {
	body=$(awk -v pat="^$1:" '$0 ~ pat { on = 1; next } on && /^[a-z-]+:/ { exit } on' "$GATES_MK" "$MAKEFILE" 2>/dev/null)
	printf '%s' "$body" | grep -q 'test-data' && return 0

	# A MENTION IS NOT A READ. Three scripts name `test-data/...` in a COMMENT — where a case
	# lives, why one is written here instead — and counting those made the clause report gates
	# that touch nothing. Comment lines are dropped, and what is left has to look like access:
	# a path under `test-data/` that is not preceded by a word character.
	for sc in $(printf '%s' "$body" | grep -oE '\./scripts/[a-z0-9-]+\.sh'); do
		[ -f "${sc#./}" ] || continue
		grep -v '^[[:space:]]*#' "${sc#./}" | grep -qE '(^|[^[:alnum:]_/])test-data/' && return 0
	done
	return 1
}

: >"$tmp/unguarded"
for t in $board; do
	reads_corpus "$t" || continue

	case " $(printf '%s ' $conditional) " in *" $t "*) continue ;; esac
	printf '%s\tunguarded\n' "$t" >>"$tmp/unguarded"

	ledger_lookup "$t" "$tmp/tolerates" >/dev/null && continue

	note "\`make $t\` reads the private corpus, is not behind the fetch, and is not named as tolerating its absence"
done

while IFS="$(printf '\t')" read -r name reason; do
	note "\`make $name\` is named as tolerating an absent corpus — \"$reason\" — and it no longer reads the corpus outside the fetch"
done < <(ledger_stale "$tmp/unguarded" - "$tmp/tolerates")

# AND THE OTHER DIRECTION, which nothing asked. A gate behind the fetch that reads nothing from
# the corpus is held back by a guard it does not need — the reverse assertion `CORPUS_SKIP`
# added for itself, here for the set beside it.
for t in $conditional; do
	reads_corpus "$t" && continue

	note "\`make $t\` is behind the corpus fetch and reads nothing from the corpus — the guard costs it every job that does not fetch"
done

# CLAUSE 5 — a gate that SWEEPS a discovered set declares a floor.
#
# "A gate that measures nothing looks exactly like a gate that found nothing" is written out in
# prose at seven sites and checked at none. The doctrine belongs here, where the board is
# already read.
#
# NOT EVERY GATE OWES ONE, and forcing a number onto the ones that do not is how a floor
# becomes decoration. They are a LEDGER with the reason each, like the two above, and held the
# same way: a name that has left the board is excusing nothing and says so.
cat >"$tmp/no-floor" <<'LEDGER'
cache-key-check	it asserts that two keys DIFFER, and sweeps no set that could come back empty
install-check	it asserts that named files are where `make install` put them, and sweeps no set either
build	the script its recipe reaches is `gen-version.sh`, a GENERATOR rather than a check, and a generator has nothing to measure
LEDGER

printf '%s\n' "$board" | sed 's/$/\ton the board/' >"$tmp/board"
while IFS="$(printf '\t')" read -r name reason; do
	note "\`make $name\` is excused from declaring a floor — \"$reason\" — and it is not on the board any more"
done < <(ledger_stale "$tmp/board" - "$tmp/no-floor")

for t in $board; do
	ledger_lookup "$t" "$tmp/no-floor" >/dev/null && continue

	body=$(awk -v pat="^$t:" '$0 ~ pat { on = 1; next } on && /^[a-z-]+:/ { exit } on' "$GATES_MK" "$MAKEFILE" 2>/dev/null)
	gscript=$(printf '%s' "$body" | grep -oE '\./scripts/[a-z0-9-]+\.sh' | head -1)
	[ -n "$gscript" ] || continue
	[ -f "${gscript#./}" ] || continue

	grep -qE 'MIN|floor|-lt [0-9]+|-ge [0-9]+|self_test' "${gscript#./}" ||
		note "\`make $t\` sweeps through ${gscript#./} and that script declares no floor — a walk that stops matching would report success"
done

# --- a gate that runs an external tool shows that tool's output --------------------------
#
# A GATE IS A DIAGNOSTIC ABOUT THIS REPOSITORY, and the standing rule for a diagnostic is that
# it is either true or absent. Two of them were neither: `treesitter-check` swallowed the
# `tree-sitter` output and reported "grammar.js does not generate" — a CAUSE — when what had
# happened was that the runner could not fetch the tool, and `ci-fetch-testdata.sh` reported
# that the fetch failed WITH credentials when the runner's DNS was down. Each sent a reader
# after the wrong thing, on `main`, inside one day (#181).
#
# WHAT IS CHECKED IS THE SHAPE, because the sentence is prose and the shape is not: the command
# runs with its output CAPTURED rather than discarded, and the failure branch PRINTS what it
# captured. A gate that does both cannot assert a cause it did not establish, whatever its
# wording says, and one that stops doing either is a finding here.
#
# IT IS A NAMED PAIR AND NOT A SWEEP. "Which gates run an external tool" is not a question this
# script can answer — a tool is any command — so the honest scope is the two that were wrong,
# and a third joins them when somebody decides it should.
cat >"$tmp/shows-output" <<'LEDGER'
scripts/treesitter-check.sh	runs `tree-sitter generate`, whose failure and whose absence look nothing alike
scripts/ci-fetch-testdata.sh	runs `git submodule update`, where a bad token, an unpushed commit and no network all land
LEDGER

: >"$tmp/shows-not"
while IFS="$(printf '\t')" read -r sc why; do
	[ -n "$sc" ] || continue
	if [ ! -f "$sc" ]; then
		printf '%s\tthe file is gone\n' "$sc" >>"$tmp/shows-not"
		continue
	fi

	# captured into a variable, and printed back out on the failing path
	grep -qE '^[[:space:]]*[a-z_]+=\$\(.*2>&1' "$sc" && grep -qE 'printf .*"\$(gen|fetch)"' "$sc" && continue

	printf '%s\tit runs a tool and does not show what the tool said\n' "$sc" >>"$tmp/shows-not"
done < <(ledger_read "$tmp/shows-output")

while IFS="$(printf '\t')" read -r sc why; do
	note "$sc runs an external tool and its failure does not carry the tool's own output — \"$why\""
done <"$tmp/shows-not"

printf 'gates-check: %s of them run only when the private corpus was fetched — %s\n' \
	"$n_cond" "$(printf '%s\n' "$conditional" | tr '\n' ' ')"

# AND THE LAST WORD IS THE EXIT STATUS. There was a `$fail` check in the middle of this script
# and none at the end, so clauses 4 and 5 — and every ledger clause added since — wrote their
# findings to stderr and left with 0. Two clauses that report and cannot fail are two clauses
# nobody would have noticed were wrong, which is the failure this whole script is against, in
# the script itself.
if [ "$fail" -ne 0 ]; then
	printf 'gates-check: a gate is defined, or listed, but not run\n' >&2
	exit 1
fi
