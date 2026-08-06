#!/usr/bin/env bash
#
# desugar-golden — each case in test-data/desugar desugars to exactly the source beside it.
#
# desugar-check.sh asks whether the two programs BEHAVE the same, which is the claim that
# matters and is also the claim that cannot see a rule quietly doing nothing: a rule that
# declines every input passes it perfectly. This asks the other question — what the text
# actually comes out as — and it asks it against a file a reviewer reads, so a change to
# what a rule emits arrives in a diff rather than in a number.
#
# `<case>.zg` is the sugared source; `<case>.core.zg` is what `zerg desugar` must produce
# from it, byte for byte. The core file is also desugared, and must not move: a rule that
# emitted something another rule rewrites would make the pass depend on how many times it
# was run.
#
# The last section is `lint-check`'s discipline: every rule must have a case that makes it
# FIRE. It is asked rather than declared — switch the rule off, and if the output is the
# same, nothing here exercises it.

set -u

ZERG=${ZERG:-./bin/zerg}
DIR=${DIR:-test-data/desugar}
RULES=${RULES:-"D101 D102 D103"}
MIN=${MIN_CASES:-6}

if [ ! -d "$DIR" ]; then
	echo "desugar-golden: $DIR is missing (git submodule update --init)"
	exit 1
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fail=0
n=0

for src in "$DIR"/*.zg; do
	case "$src" in
	*.core.zg) continue ;;
	esac
	[ -e "$src" ] || continue

	name=$(basename "$src" .zg)
	want="$DIR/$name.core.zg"
	n=$((n + 1))

	if [ ! -f "$want" ]; then
		echo "MISSING   $src — a case is the pair, and its .core.zg is not there"
		fail=$((fail + 1))
		continue
	fi

	cp "$src" "$tmp/$name.zg"
	if ! err=$("$ZERG" desugar "$tmp/$name.zg" 2>&1 >/dev/null); then
		echo "REFUSED   $src — the transform would not read it"
		echo "  $(echo "$err" | head -1)"
		fail=$((fail + 1))
		continue
	fi

	if ! cmp -s "$tmp/$name.zg" "$want"; then
		echo "GOLDEN    $src — what it desugars to is not what is written beside it"
		diff "$want" "$tmp/$name.zg" | head -12
		fail=$((fail + 1))
		continue
	fi

	cp "$want" "$tmp/$name.again.zg"
	"$ZERG" desugar "$tmp/$name.again.zg" >/dev/null 2>&1
	if ! cmp -s "$tmp/$name.again.zg" "$want"; then
		echo "FIXPOINT  $want — desugaring it again changes it"
		diff "$want" "$tmp/$name.again.zg" | head -12
		fail=$((fail + 1))
	fi
done

# --- every rule has a case that makes it fire ------------------------------------------
for rule in $RULES; do
	fired=0
	for src in "$DIR"/*.zg; do
		case "$src" in
		*.core.zg) continue ;;
		esac
		[ -e "$src" ] || continue

		name=$(basename "$src" .zg)
		cp "$src" "$tmp/$rule.$name.on.zg"
		cp "$src" "$tmp/$rule.$name.off.zg"
		"$ZERG" desugar "$tmp/$rule.$name.on.zg" >/dev/null 2>&1
		"$ZERG" desugar --off "$rule" "$tmp/$rule.$name.off.zg" >/dev/null 2>&1
		if ! cmp -s "$tmp/$rule.$name.on.zg" "$tmp/$rule.$name.off.zg"; then
			fired=1
			break
		fi
	done
	if [ "$fired" -eq 0 ]; then
		echo "UNTESTED  $rule — no case in $DIR changes when this rule is switched off"
		fail=$((fail + 1))
	fi
done

if [ $fail -ne 0 ]; then
	echo "desugar-golden: $fail case(s) do not desugar to what is written beside them"
	exit 1
fi

if [ "$n" -lt "$MIN" ]; then
	echo "desugar-golden: only $n cases were checked, and the floor is $MIN"
	exit 1
fi
echo "desugar-golden: $n cases desugar to exactly the source beside them, and every rule fires in one"
