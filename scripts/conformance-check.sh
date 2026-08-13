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

# `is_typo_msg` — the message a form gets when the compiler did not recognise it — comes
# from diag.sh, where it belongs: every one of its shapes was the real answer to a real
# GRAMMAR form at some point in this corpus's life (`expected `=>`, found `|`` was the
# or-pattern, `no type named `ptr`` was ptr-type), and productions-check.sh asserts against
# the identical list for the identical reason. It was written out in both and "kept in step
# by hand", which is how it came to miss the `E204 ` prefix in both at once.

# TWO PROFILES, and the split is not a convenience. Everything in the core is answerable by
# an implementation targeting anything at all; the SYSTEM profile is inline assembly, raw
# pointers and the `unsafe` groups that hold them — forms whose meaning is a machine's, so an
# implementation that has no machine to speak of (one targeting a VM, or a checker) declines
# the profile rather than failing the language.
#
# Declining is not silence. A system chapter must still be REFUSED BY NAME, which is the
# same rule as everywhere else — what the profile changes is whether refusing it is a
# finding. See docs/conformance.md.
is_system_chapter() {
	case $1 in g12_*) return 0 ;; esac
	return 1
}

fail=0
parsed=0
named=0
sys_parsed=0
sys_named=0

for src in "$DIR"/g*.zg; do
	[ -e "$src" ] || continue
	name=$(basename "$src" .zg)

	out=$("$ZERG" build --emit ast "$src" 2>&1 >/dev/null)
	status=$?

	if [ $status -eq 0 ]; then
		if is_system_chapter "$name"; then
			sys_parsed=$((sys_parsed + 1))
		else
			parsed=$((parsed + 1))
		fi
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

	if is_system_chapter "$name"; then
		sys_named=$((sys_named + 1))
	else
		named=$((named + 1))
	fi
done

n=$((parsed + named + sys_parsed + sys_named + fail))
if [ "$n" -eq 0 ]; then
	echo "conformance-check: no g*.zg found in $DIR — the corpus is one file per GRAMMAR chapter"
	exit 1
fi

# The split matches chapters by NAME, so a corpus that renumbers its files would leave
# `is_system_chapter` matching nothing — and the gate would go on passing while quietly
# holding every chapter to the core profile. An empty side of a partition is the failure a
# partition cannot report, so it is named here rather than inferred from the counts.
if [ $((sys_parsed + sys_named)) -eq 0 ]; then
	echo "conformance-check: no chapter classified system — is_system_chapter matches nothing in $DIR"
	exit 1
fi

if [ $fail -ne 0 ]; then
	echo "conformance-check: $fail of $n chapters are neither read nor refused by name"
	exit 1
fi

echo "conformance-check: $n GRAMMAR chapters — core: $parsed parse, $named refused by name; system: $sys_parsed parse, $sys_named refused by name"
