#!/usr/bin/env bash
#
# dead-code-check — the dead-code questions that are about this REPOSITORY rather than about a
# program, and so have nowhere else to be asked.
#
# `zerg lint`'s L1xx family answers the ones that are about a program: an import nothing reaches
# through (`L101`), a private function nobody calls (`L102`), a binding nobody reads (`L103`),
# and the rest. It is on the board, it runs over every entry this project writes, and none of
# that reaches the two questions below, because neither of them is a question about one program.
#
# 1. A `pub` FUNCTION NOBODY CALLS. `L102` is about a PRIVATE function, and deliberately: a
#    public name is a module's interface, and a library's interface answers to callers outside
#    the tree. But the compiler is not a library. `src/compiler` is a PROGRAM — one entry,
#    `zergc.zg`, and everything else reached from it — so a `pub fn` in it that nothing in it
#    calls is dead in exactly the sense `L102` means, and the only reason it is public is that
#    a module boundary sits between the caller it used to have and itself.
#
#    THE STDLIB IS THE CARVE-OUT and it is not a hedge. `src/stdlib` is a library: `strings.pad`
#    exists for programs this repository does not contain, and a rule that called it dead would
#    be asking the library to justify itself against its own test suite. So the sweep is the
#    compiler's sources, and the stdlib is not swept — #140's "not this task" says the same.
#
# 2. A SCRIPT NOBODY INVOKES. Every gate on the board is a make target and most reach a script
#    under `scripts/`; a script that no makefile, workflow, script or document names is a gate
#    nobody runs, wearing a file. `gates-check` asks whether every TARGET is on the board and
#    cannot see a file that is not a target at all.
#
# WHAT IS NOT HERE is the third question #140 names — an import nobody uses. That one IS about a
# program, it is `L101`, and what was wrong with it was the rule rather than the reach: it asked
# whether ANY file of the merged program reached through the namespace, and an import binds a
# namespace in the FILE that writes it (`E5007`, #57). It is fixed in the linter, which is where
# the language's own rule belongs; a second copy of it here would be the thing this repository
# keeps finding in its own gates.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

# shellcheck source=scripts/lib/ledger.sh
. "$ROOT/scripts/lib/ledger.sh"

SELF=scripts/dead-code-check.sh

COMPILER=${COMPILER:-src/compiler}
SCRIPTDIR=${SCRIPTDIR:-scripts}

# THE FLOORS. Both questions are of the form "nothing here is dead", which is trivially true of
# a population of nothing: a renamed directory, a glob that stops matching, an extraction that
# stops recognising a declaration, and this reports a clean tree for having examined none of it.
#
# 300 against the 366 `pub fn` the compiler declares today, and 40 against the 60-odd scripts.
MIN_PUBS=${MIN_PUBS:-300}
MIN_SCRIPTS=${MIN_SCRIPTS:-40}

ledger_self_test || exit 1

tmp=$(mktemp -d) || exit 2
trap 'rm -rf "$tmp"' EXIT

fail=0

# --- 1. a `pub` function nobody calls ---------------------------------------------------
#
# THE CODE, WITHOUT THE COMMENTS. This tree explains itself at length and names its own
# functions while doing it — `tgt_kids` is described in the paragraph above its declaration —
# so a sweep that counted every mention would find every function used and report nothing,
# forever. Comment lines go; what is left is what runs.
#
# AND THE LINES ARE PADDED. The test is "the name with a non-identifier character on each side",
# and written with `^`/`$` alternations it silently stopped matching some declarations and not
# others while it was being written. A leading and trailing space means the pattern needs no
# anchors at all, which is one fewer way for a negative assertion to go quiet.
find "$COMPILER" -name '*.zg' -print0 |
	xargs -0 grep -hv '^[ \t]*#' |
	sed 's/^/ /; s/$/ /' >"$tmp/code"

find "$COMPILER" -name '*.zg' -print0 |
	xargs -0 grep -hoE '^pub fn [a-zA-Z_][a-zA-Z0-9_]*' |
	sed 's/^pub fn //' | sort -u >"$tmp/pubs"

n_pubs=$(grep -c . "$tmp/pubs")
if [ "$n_pubs" -lt "$MIN_PUBS" ]; then
	printf 'dead-code: %s yielded %s public functions, below the floor of %s — the extraction stopped matching\n' \
		"$COMPILER" "$n_pubs" "$MIN_PUBS" >&2
	exit 1
fi

# A NAME WITH ONE OCCURRENCE IS ITS OWN DECLARATION AND NOTHING ELSE. The declaration line is
# code, so it counts; the question is whether anything else does.
while read -r name; do
	[ -n "$name" ] || continue
	[ "$(grep -cE "[^a-zA-Z0-9_]$name[^a-zA-Z0-9_]" "$tmp/code")" -le 1 ] || continue

	where=$(grep -rln "^pub fn $name(" "$COMPILER" --include='*.zg' | head -1)
	printf 'DEAD-PUB  %s — `pub fn %s` and nothing in the compiler calls it; make it private or delete it\n' \
		"$where" "$name"
	fail=1
done <"$tmp/pubs"

# --- 2. a script nobody invokes ---------------------------------------------------------
#
# A HAND TOOL IS NOT A DEAD SCRIPT, and the difference is a sentence somebody wrote rather than
# a shape this can see: `memprobe.sh` exists to be typed by a person chasing a `mem-check`
# failure, and it says so in its own header. So the exceptions are a ledger — scripts/lib/
# ledger.sh — and its STALE clause means a name here that stops being a script, or starts being
# invoked, is a finding rather than a line nobody re-reads.
cat >"$tmp/hand-tools" <<'LEDGER'
scripts/lib/memprobe.sh	a hand tool for locating which allocation a mem-check failure is about, typed by a person and named by nothing; it says so in its own header
LEDGER

find "$SCRIPTDIR" -type f \( -name '*.sh' -o -perm -u+x \) | sort -u >"$tmp/scripts"
n_scripts=$(grep -c . "$tmp/scripts")
if [ "$n_scripts" -lt "$MIN_SCRIPTS" ]; then
	printf 'dead-code: %s yielded %s scripts, below the floor of %s — the walk stopped matching\n' \
		"$SCRIPTDIR" "$n_scripts" "$MIN_SCRIPTS" >&2
	exit 1
fi

# WHAT IS OBSERVED IS THE UNINVOKED SET, which is what the ledger is a ledger OF. A hand tool
# that something has started invoking is no longer a hand tool, and a line for a file that is
# gone excuses nothing — both are the STALE clause, and both need the observation to be the
# state the ledger describes rather than its opposite.
: >"$tmp/uninvoked"
invoked=0
while read -r path; do
	[ -n "$path" ] || continue
	base=$(basename "$path")
	# ANYWHERE BUT ITSELF, AND NOT IN THIS FILE. A script naming its own filename in its
	# usage line is not an invocation, and several of these carry one — and the ledger below
	# names a path, so without the second exclusion this rule finds itself and reports the one
	# script it is written to excuse as invoked. `gates-check` has the same line for the same
	# reason, about its own pattern.
	if grep -rlF "$base" Makefile mk .github scripts docs editors src 2>/dev/null |
		grep -v "^$SELF\$" | grep -qv "^$path\$"; then
		invoked=$((invoked + 1))
		continue
	fi
	printf '%s\tnothing names it\n' "$path" >>"$tmp/uninvoked"

	ledger_lookup "$path" "$tmp/hand-tools" >/dev/null && continue

	printf 'DEAD-SCRIPT %s — no makefile, workflow, script or document names it\n' "$path"
	fail=1
done <"$tmp/scripts"

while IFS="$(printf '\t')" read -r path reason; do
	printf 'STALE-TOOL %s — listed as a hand tool, "%s", and something invokes it now, or it is gone\n' \
		"$path" "$reason"
	fail=1
done < <(ledger_stale "$tmp/uninvoked" - "$tmp/hand-tools")

[ "$fail" -eq 0 ] || {
	printf 'dead-code: something in this repository is written and reached by nothing\n' >&2
	exit 1
}

printf 'dead-code: %s public functions of the compiler are called, %s scripts are invoked, %s named as a hand tool\n' \
	"$n_pubs" "$invoked" "$(ledger_read "$tmp/hand-tools" | grep -c .)"
