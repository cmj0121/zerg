#!/usr/bin/env bash
#
# counterexample-check — a program GRAMMAR does not derive must be REFUSED.
#
# Every other gate on this board asks the compiler about programs that ARE Zerg. `corpus`
# runs them, `oracle` compares two compilers on them, `conformance` and `productions` ask
# whether each form is read. None of them can see an OVER-ACCEPTANCE: a program outside the
# language that the compiler takes anyway, silently, and gives a meaning GRAMMAR never gave
# it. `reject-check.sh` covers that ground for SEMANTIC rules — a program that is Zerg and
# does not hold together — and syntax had nothing.
#
# The blind spot is not theoretical. Every one of these was accepted, in silence, by a
# compiler with a full board of green gates: the 1-tuple `(1, )` typed as `(int)`; a
# trailing comma in four bracket kinds; `0x_1F`; an unparenthesised `{`-opening head read
# as a map literal; `?? raise … if`; and `[1 2]` and `f(1 2)`, where the comma was simply
# optional in eight readers at once.
#
# ORGANISED BY PRODUCTION, one directory each, because the question a reader has is
# "what does GRAMMAR#tuple-lit refuse?" and a flat list of programs cannot answer it. The
# directory name IS the production, and the gate holds it to `grammar_productions GRAMMAR`
# — a directory naming no production is a citation that no longer resolves, the same
# failure `make grammar-cites` exists for.
#
# WHAT IS ASSERTED, per case:
#
#   1. a non-zero exit — the program is turned away
#   2. it said something — a refusal with no sentence is indistinguishable from a crash
#   3. it did not die of a signal
#   4. nothing shaped like a cc diagnostic, and no cache path: the standing rule is that
#      the COMPILER refuses, never the C compiler against generated code nobody wrote
#
# The stage is `--emit ast`, because the default claim is the strong one: a program GRAMMAR
# does not derive should not survive the PARSE. Two markers, each on a case's FIRST line,
# say when it is something else — and both are the opposite of an exemption, because both
# keep the case in its production's directory and both turn this gate red the day the
# situation changes:
#
#   # LATE: <code> <why>   the parse takes it and a later stage refuses it, with that code.
#                          Then `--emit ast` must ACCEPT and `--emit c` must refuse naming
#                          the code — `1 = 2` parses as an assignment and E3002 is the
#                          checker saying the target is not a place. `--emit c` and not
#                          `--emit bin` on purpose: it reaches the checker and the emitter
#                          and never runs cc, so nothing here can be answered by a C
#                          compiler against code nobody wrote.
#   # KNOWN-OPEN: <why>    nothing refuses it at all. The assertion INVERTS: the program
#                          must still be ACCEPTED, so a measured over-acceptance stays
#                          visible, and whoever fixes it is told to drop the marker.
#
# Omitting either kind of case is what would make it invisible, which is how every
# over-acceptance in the list above survived a full board of green gates.
set -u

# shellcheck source=scripts/lib/diag.sh
. "$(dirname "$0")/lib/diag.sh"
# shellcheck source=scripts/lib/grammar.sh
. "$(dirname "$0")/lib/grammar.sh"

ZERG=${ZERG:-./bin/zerg}
GRAMMAR=${GRAMMAR:-GRAMMAR}
DIR=${DIR:-test-data/counterexamples}

# THE FLOOR, and it is two numbers because this gate can go quiet in two ways. Every
# assertion is per-case, so an empty corpus passes them all — that is what MIN_CASES is for.
# And a corpus that kept its case count while collapsing onto one production would still
# answer "what does GRAMMAR#tuple-lit refuse" for exactly one production, which is what
# MIN_PRODUCTIONS is for. 40 cases over 30 productions, against the 57 over 44 written the
# day this gate landed — the gap is room to retire a case whose rule moved, and nowhere near
# the handful a wrong-commit or shallow checkout leaves behind.
MIN_CASES=${MIN_CASES:-40}
MIN_PRODUCTIONS=${MIN_PRODUCTIONS:-30}

# The corpus is a private submodule, so an absent one is not a failure — it is a checkout
# that did not ask for it. The floors above are what catch a directory that IS there and
# holds almost nothing.
if [ ! -d "$DIR" ]; then
	echo "counterexample-check: $DIR is not there (git submodule update --init) — nothing checked"
	exit 0
fi

if [ ! -f "$GRAMMAR" ]; then
	echo "counterexample-check: $GRAMMAR is not there — no directory name could be checked"
	exit 1
fi

grammar_self_test || exit 1

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

grammar_productions "$GRAMMAR" | cut -f1 | sort -u >"$tmp/productions"

fail=0
refused=0
late=0
open=0
prods=0

for d in "$DIR"/*/; do
	[ -d "$d" ] || continue
	prod=$(basename "$d")

	if ! grep -qx "$prod" "$tmp/productions"; then
		echo "NOPROD    $prod/ — a directory naming no production GRAMMAR derives"
		fail=$((fail + 1))
		continue
	fi
	prods=$((prods + 1))

	for src in "$d"*.zg; do
		[ -e "$src" ] || continue
		name="$prod/$(basename "$src" .zg)"

		# A marker is on the FIRST line, so a case cannot acquire one by having the word
		# appear in prose three paragraphs down.
		marker=$(head -1 "$src")
		known_open=0
		late_code=""
		case $marker in
		'# KNOWN-OPEN:'*) known_open=1 ;;
		'# LATE: '*)
			late_code=$(printf '%s\n' "$marker" | sed -E 's/^# LATE: ([A-Z][0-9]{3}).*/\1/')
			if [ "$late_code" = "$marker" ]; then
				echo "MARKER    $name — a LATE marker must name the code the later stage reports"
				fail=$((fail + 1))
				continue
			fi
			;;
		esac

		out=$("$ZERG" build --emit ast "$src" 2>&1 >/dev/null)
		status=$?

		if is_crash "$status"; then
			echo "CRASHED   $name — the compiler died of signal $((status - 128))"
			fail=$((fail + 1))
			continue
		fi

		if [ $status -eq 0 ]; then
			if [ $known_open -eq 1 ]; then
				open=$((open + 1))
				continue
			fi
			if [ -z "$late_code" ]; then
				echo "ACCEPTED  $name — GRAMMAR does not derive this program and the parse took it"
				fail=$((fail + 1))
				continue
			fi

			# A LATE case: the parse was meant to take it, so the refusal is asked for from
			# the stage the marker names. `--emit c` reaches the checker and the emitter and
			# stops before cc.
			lout=$("$ZERG" build --emit c "$src" 2>&1 >/dev/null)
			lstatus=$?
			if is_crash "$lstatus"; then
				echo "CRASHED   $name — the compiler died of signal $((lstatus - 128)) at --emit c"
				fail=$((fail + 1))
			elif [ $lstatus -eq 0 ]; then
				echo "ACCEPTED  $name — marked LATE $late_code and nothing refused it at all"
				fail=$((fail + 1))
			elif cc_answered "$lout"; then
				echo "CC        $name — refused by the C compiler, not by zerg"
				echo "  $(printf '%s\n' "$lout" | head -1)"
				fail=$((fail + 1))
			elif ! printf '%s\n' "$lout" | grep -q "$late_code"; then
				echo "CODE      $name — marked LATE $late_code and refused by another rule"
				echo "  $(printf '%s\n' "$lout" | head -1)"
				fail=$((fail + 1))
			else
				late=$((late + 1))
			fi
			continue
		fi

		if [ $known_open -eq 1 ]; then
			echo "FIXED     $name — marked KNOWN-OPEN and now refused; drop the marker"
			echo "  $(printf '%s\n' "$out" | head -1)"
			fail=$((fail + 1))
			continue
		fi

		if [ -n "$late_code" ]; then
			echo "EARLY     $name — marked LATE $late_code and the PARSE refuses it; drop the marker"
			echo "  $(printf '%s\n' "$out" | head -1)"
			fail=$((fail + 1))
			continue
		fi

		if [ -z "$out" ]; then
			echo "SILENT    $name — non-zero exit and nothing said"
			fail=$((fail + 1))
			continue
		fi

		if cc_answered "$out"; then
			echo "CC        $name — refused by the C compiler, not by zerg"
			echo "  $(printf '%s\n' "$out" | head -1)"
			fail=$((fail + 1))
			continue
		fi

		refused=$((refused + 1))
	done
done

n=$((refused + late + open))

if [ "$n" -lt "$MIN_CASES" ]; then
	echo "counterexample-check: $n cases, below the floor of $MIN_CASES — the corpus did not arrive"
	fail=$((fail + 1))
fi
if [ "$prods" -lt "$MIN_PRODUCTIONS" ]; then
	echo "counterexample-check: $prods productions covered, below the floor of $MIN_PRODUCTIONS"
	fail=$((fail + 1))
fi

if [ "$fail" -ne 0 ]; then
	echo "counterexample-check: a program GRAMMAR does not derive was accepted, or refused by the wrong tool"
	exit 1
fi

tail=""
[ "$late" -ne 0 ] && tail="$tail, $late refused after the parse"
[ "$open" -ne 0 ] && tail="$tail, $open still accepted and marked KNOWN-OPEN"

echo "counterexample-check: $n programs GRAMMAR does not derive over $prods productions — $refused refused at the parse$tail"
