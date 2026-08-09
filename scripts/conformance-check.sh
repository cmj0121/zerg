#!/usr/bin/env bash
#
# conformance-check — GRAMMAR's twelve chapters, one file each, held to the standing rule.
#
# test-data/conformance/g01..g12 is a surface sample per chapter of GRAMMAR: every form the
# chapter derives, written once. NOTHING RAN IT — `make corpus` reads test-data/codegen
# only, and grep finds the twelve names in no runner, no Makefile target and no test — so a
# corpus written to say "this is the language" said it to nobody, and eleven of the twelve
# did not build on the day this gate was written.
#
# WHAT IS ASSERTED is docs/conformance.md's standing rule, and only that: a form is either
# handled or refused BY NAME. So each file must either
#
#   PARSE — every form in it is one this compiler reads, or
#   be refused with a SENTENCE that names the form it is turning away.
#
# and may never be refused with the message a typo gets, nor crash, nor be turned away in
# silence.
#
# NOT run-and-compare, which is what `corpus` does and what these files cannot do: they are
# a list of FORMS, so they name helpers that do not exist (`spawn work(1)`) and import
# modules that are not there. Asking them to run would mean rewriting them into programs,
# and a program that has to be a program is a narrower sample than a chapter needs.
#
# The stage is `--emit ast`: it reads, lexes and parses, and stops before anything is
# lowered. That is the widest stage these files can reach — and until this gate was
# written, that stage answered 0 and printed nothing for every one of them, because the
# driver wrapped it in a `guard`.
set -u

# shellcheck source=scripts/lib/diag.sh
. "$(dirname "$0")/lib/diag.sh"

ZERG=${ZERG:-./bin/zerg}
DIR=${DIR:-test-data/conformance}

# The corpus is a private submodule, so an absent one is not a failure — it is a checkout
# that did not ask for it. What that must not also cover is a directory that IS there and
# holds nothing, which is what the floor at the bottom is for.
if [ ! -d "$DIR" ]; then
	echo "conformance-check: $DIR is not there (git submodule update --init) — nothing checked"
	exit 0
fi

# A TYPO'S MESSAGE. These are the shapes a diagnostic takes when the compiler did not
# recognise the form and reported the token it was standing on instead. Every one of them
# was the real answer to a real GRAMMAR form at some point in this corpus's life:
# `expected `=>`, found `|`` was the or-pattern, and `no type named `ptr`` was ptr-type.
#
# It is a NEGATIVE test, so it fails open, which is the risk the list is worth taking: the
# alternative is a per-file expected-message inventory, and a case name in this corpus is
# private content that may not be written down in this repo.
is_typo_msg() {
	printf '%s\n' "$1" | grep -qE "^(error: )?(expected |undefined |no type named |no field |unexpected )"
}

fail=0
parsed=0
named=0

for src in "$DIR"/g*.zg; do
	[ -e "$src" ] || continue
	name=$(basename "$src" .zg)

	out=$("$ZERG" build --emit ast "$src" 2>&1 >/dev/null)
	status=$?

	if [ $status -eq 0 ]; then
		parsed=$((parsed + 1))
		continue
	fi

	# A crash is the one outcome the rule never allows, and it says nothing — so without
	# this it would read as a refusal with an empty message.
	if is_crash "$status"; then
		echo "CRASHED   $name — the compiler died of signal $((status - 128))"
		fail=$((fail + 1))
		continue
	fi

	if [ -z "$out" ]; then
		echo "SILENT    $name — non-zero exit and nothing said"
		fail=$((fail + 1))
		continue
	fi

	if is_typo_msg "$out"; then
		echo "UNNAMED   $name — refused with the message a typo gets, for a form GRAMMAR derives"
		echo "  $(printf '%s\n' "$out" | head -1)"
		fail=$((fail + 1))
		continue
	fi

	named=$((named + 1))
done

n=$((parsed + named + fail))
if [ "$n" -eq 0 ]; then
	echo "conformance-check: no g*.zg found in $DIR — the corpus is one file per GRAMMAR chapter"
	exit 1
fi

if [ $fail -ne 0 ]; then
	echo "conformance-check: $fail of $n chapters are neither read nor refused by name"
	exit 1
fi

echo "conformance-check: $n GRAMMAR chapters — $parsed parse, $named are refused by name"
