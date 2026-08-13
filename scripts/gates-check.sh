#!/usr/bin/env bash
#
# gates-check — a gate nobody runs is a gate that finds nothing.
#
# This repository's gates are Makefile targets, and there are three places a target has
# to appear before it protects anything: the Makefile that defines it, `LINUX_GATES`
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
# So: every gate the Makefile defines is on the board, and everything on the board is
# run by CI. What a gate ASSERTS is its own business; this is only about whether anyone
# asks it.
set -uo pipefail

MAKEFILE=${MAKEFILE:-Makefile}
WORKFLOW=${WORKFLOW:-.github/workflows/ci.yml}

fail=0

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

# NOT A GATE. Each of these is a command rather than an assertion, and the reason is
# per-entry rather than a pattern — which is why they are listed and not matched:
#
#   all clean run help upgrade   — the ordinary verbs of a Makefile
#   install uninstall            — they CHANGE the machine; `install-check` is the gate
#   fmt                          — it rewrites sources; `fmt-self` is the gate
#   ci linux-ci                  — they ARE the board, and a board on the board recurses
NOT_A_GATE="all clean run help upgrade install uninstall fmt ci linux-ci"

targets=$(grep -oE '^[a-z][a-z0-9-]*:' "$MAKEFILE" | tr -d ':' | sort -u)
board=$(grep -oE '^LINUX_GATES \?= .*' "$MAKEFILE" | sed 's/^LINUX_GATES ?= //' | tr ' ' '\n' | grep -v '^$' | sort -u)

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
#    the step RUNS. Six board gates sit behind `if: steps.corpus_fetch.outputs.available ==
#    'true'` because they need the private submodule, and a skipped step is green — which is
#    the exact failure the header above recounts, one level up. Closing it means the board
#    being single-sourced (CI running `make ci`, or its steps generated from a matrix) rather
#    than a third copy of the list this script compares against; until then the conditional
#    six are trusted, and that is the declared limit of clause 2.
for t in $board; do
	grep -qE "run: make (-j[0-9]+ )?$t\$" "$WORKFLOW" ||
		note "\`make $t\` is on the board and the workflow never runs it"
done

if [ "$fail" -ne 0 ]; then
	printf 'gates-check: a gate is defined, or listed, but not run\n' >&2
	exit 1
fi

printf 'gates-check: %s gates — each on the board, each run by CI\n' \
	"$(printf '%s\n' "$board" | wc -l | tr -d ' ')"
