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
# per-entry rather than a pattern — which is why they are listed and not matched:
#
#   all clean run help upgrade   — the ordinary verbs of a Makefile
#   install uninstall            — they CHANGE the machine; `install-check` is the gate
#   fmt                          — it rewrites sources; `fmt-self` is the gate
#   test linux-ci                — they ARE the board, and a board on the board recurses
#   release release-tarball      — they BUILD an artifact, and the board builds nothing to keep
#   release-smoke                — the board asks about this repository; `release-smoke` asks
#                                  about a tarball on a machine that has no repository, which
#                                  is a question only a release can put. It is the gate on the
#                                  artifact and `make release` runs it, so a tarball cannot be
#                                  produced without it — but it has no place on a board that
#                                  runs on every commit, because there is no tarball then.
#   build-deps                 — DATA, not a question. It prints the prerequisite list `bin/zerg`
#                                  is rebuilt for, one file per line, so `build-deps-check` can
#                                  hold the rule to it without keeping a second copy. It asserts
#                                  nothing itself; the gate that reads it is on the board.
#   install-editors, uninstall-editors
#                                — the half of an install that belongs to a PERSON rather than
#                                  to a prefix: nvim's syntax and LSP client, and the cloc
#                                  definition registered where cloc reads it. Both write under
#                                  the user's home, which is why they are not `install` — and
#                                  a board that ran them would rewrite the editor configuration
#                                  of whoever is running the board.
NOT_A_GATE="all clean run help upgrade build-deps install install-editors uninstall uninstall-editors fmt test linux-ci release release-tarball release-smoke"

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
	case " $NOT_A_GATE " in *" $t "*) continue ;; esac
	printf '%s\n' "$board" | grep -qx "$t" ||
		note "\`make $t\` is a gate the board does not name — add it to LINUX_GATES, or to the not-a-gate list with its reason"
done

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
# THE RULE IS NOT "reads it ⇒ must be conditional". `treesitter`, `oracle` and `examples` all
# read the corpus and all three work without it, by design. So the ones that tolerate the
# absence are NAMED here, with the reason, and anything else that reads it must be behind the
# fetch. A gate added tomorrow is in one of the two sets or it is a finding.
TOLERATES="entry-path examples install-check oracle treesitter"

for t in $board; do
	body=$(awk -v pat="^$t:" '$0 ~ pat { on = 1; next } on && /^[a-z-]+:/ { exit } on' "$GATES_MK" "$MAKEFILE" 2>/dev/null)
	printf '%s' "$body" | grep -q 'test-data' || continue

	case " $(printf '%s ' $conditional) " in *" $t "*) continue ;; esac
	case " $TOLERATES " in *" $t "*) continue ;; esac

	note "\`make $t\` reads the private corpus, is not behind the fetch, and is not named as tolerating its absence"
done

# CLAUSE 5 — a gate that SWEEPS a discovered set declares a floor.
#
# "A gate that measures nothing looks exactly like a gate that found nothing" is written out in
# prose at seven sites and checked at none. The doctrine belongs here, where the board is
# already read.
#
# NOT EVERY GATE OWES ONE, and forcing a number onto the two that do not is how a floor becomes
# decoration: `cache-key-check` asserts that two keys DIFFER and `install-check` that named
# files are where `make install` put them — neither sweeps a set that could come back empty.
# They are named, with that as the reason, the way the tolerating gates above are.
# `build` is here for a different reason: the script its recipe reaches is `gen-version.sh`,
# a GENERATOR rather than a check, and a generator has nothing to measure.
NO_FLOOR="cache-key-check install-check build"

for t in $board; do
	case " $NO_FLOOR " in *" $t "*) continue ;; esac

	body=$(awk -v pat="^$t:" '$0 ~ pat { on = 1; next } on && /^[a-z-]+:/ { exit } on' "$GATES_MK" "$MAKEFILE" 2>/dev/null)
	gscript=$(printf '%s' "$body" | grep -oE '\./scripts/[a-z0-9-]+\.sh' | head -1)
	[ -n "$gscript" ] || continue
	[ -f "${gscript#./}" ] || continue

	grep -qE 'MIN|floor|-lt [0-9]+|-ge [0-9]+|self_test' "${gscript#./}" ||
		note "\`make $t\` sweeps through ${gscript#./} and that script declares no floor — a walk that stops matching would report success"
done

printf 'gates-check: %s of them run only when the private corpus was fetched — %s\n' \
	"$n_cond" "$(printf '%s\n' "$conditional" | tr '\n' ' ')"
