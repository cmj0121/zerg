#!/bin/sh
# method-gaps-check — a method name the compiler calls unbuilt is a name a chapter promises,
# and the two lists are the same list (#74).
#
# `c_map_method` and `c_free_method` each end in a pair of raises, and the pair is a SPLIT: a
# name on a hard-coded list answers an `E9xxx` — *the language has this form and this compiler
# has not lowered it* — and every other name answers an `E3xxx`, which says *this program is
# permanently wrong*. The list is what tells the two apart, and it is a second copy of a fact
# that already lives in a chapter's `[not yet]` marker.
#
# NOTHING HELD THE TWO TOGETHER. `chapter-codes` cannot: it asks whether a live `E9xxx` is
# quoted in some chapter, and a method NAME is invisible to that. So somebody marks `m.keys()`
# `[not yet]` in docs/code/collections.md, does not touch emit.zg, and `m.keys()` answers the
# permanent rejection for a form the chapter promises — a green board over a marker that lies.
#
# WHAT IS COMPARED, AND WHY THERE IS NO THIRD COPY. The chapters' convention is to quote the
# compiler's sentence verbatim — _E9100 NotImplemented: the map method `insert`_ — so the
# names a chapter promises can be read out of that quotation, using the message's own literal
# prefix as the needle. The prefix is not written here: it is cut out of the f-string at the
# raise site, up to the `{meth}` hole. Rename the method or reword the sentence and this gate
# follows; write a third spelling of it and there is nowhere for one to go.
#
# BOTH DIRECTIONS FAIL. A name the compiler calls unbuilt and no chapter quotes is a promise
# the compiler is making alone (PROMISED). A name a chapter quotes and the compiler does not
# list is the bite above (REJECTED).
set -eu

SRC=${SRC:-src/compiler/zerg/emit.zg}
RULES=${RULES:-src/compiler/zerg/rule.zg}
fail=0

[ -f "$SRC" ] || { echo "method-gaps-check: no emitter at $SRC" >&2; exit 1; }
[ -f "$RULES" ] || { echo "method-gaps-check: no rule registry at $RULES" >&2; exit 1; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# THE CHAPTERS, JOINED. A marker is a blockquote and it wraps, so the sentence being quoted is
# split across lines at whatever column the wrap fell on — collections.md breaks one of these
# between the code and `NotImplemented:`. Stripping the `> ` and joining puts every quotation
# back on one line before anything looks for it. The catalogue is excluded for the reason
# `chapter-codes` excludes it: a code quoted only where every code is quoted is a code no
# chapter names, and its rows carry a `…` where the method goes.
find docs -name '*.md' ! -name 'diagnostics*.md' -exec cat {} + |
	sed -e 's/^[[:space:]]*>[[:space:]]*//' | tr '\n' ' ' >"$tmp/chapters"

# THE LISTS. The anchor is the `unbuilt` binding: a boolean built out of `meth ==` tests, which
# is the shape both splits are written in and the only shape this gate can read. Its raise is
# the next line, and carries both the rule and the sentence.
grep -n 'unbuilt := meth == "' "$SRC" | cut -d: -f1 >"$tmp/lists"

lists=0
while read -r ln; do
	lists=$((lists + 1))
	sed -n "${ln}p" "$SRC" | grep -oE 'meth == "[A-Za-z_][A-Za-z_0-9]*"' |
		sed -e 's/.*"\(.*\)"/\1/' | sort -u >"$tmp/code-names"

	raise=$(sed -n "$((ln + 1))p" "$SRC")
	variant=$(printf '%s\n' "$raise" | grep -oE 'rule\.Rule\.[A-Za-z_][A-Za-z_0-9]*' | head -1 | sed -e 's/.*\.//')
	if [ -z "$variant" ]; then
		echo "STALE     $SRC:$ln is a method-name list whose next line raises nothing — the anchor has moved" >&2
		fail=1
		continue
	fi

	num=$(grep -oE "^	$variant = [0-9]{4}\$" "$RULES" | grep -oE '[0-9]{4}$')
	if [ -z "$num" ]; then
		echo "STALE     $variant is raised at $SRC:$((ln + 1)) and $RULES declares no such rule" >&2
		fail=1
		continue
	fi
	code="E$num"

	# The needle: the message's literal text up to the `{meth}` hole, which ends in the
	# opening backtick the chapters quote the name inside.
	needle=$(printf '%s\n' "$raise" | sed -n 's/.*f"\(.*\){meth}.*/\1/p')
	if [ -z "$needle" ]; then
		echo "STALE     $code's message at $SRC:$((ln + 1)) has no {meth} hole — nothing to read a name out of" >&2
		fail=1
		continue
	fi

	# grep -o prints every match on the joined line, so the whole chapter set is read in one
	# pass. The needle is spliced into the pattern with its ERE metacharacters escaped, and
	# only the name after it is a pattern; the needle ends in the opening backtick, which is
	# not a name character, so cutting back to the last one leaves the name alone.
	esc=$(printf '%s' "$needle" | sed -e 's/[][\\.^$*+?(){}|]/\\&/g')
	grep -oE "$esc[A-Za-z_][A-Za-z_0-9]*" "$tmp/chapters" |
		sed -e 's/.*[^A-Za-z_0-9]//' | sort -u >"$tmp/doc-names" || true

	while read -r nm; do
		[ -n "$nm" ] || continue
		grep -qx "$nm" "$tmp/doc-names" && continue
		echo "PROMISED  $code calls \`$nm\` unbuilt and no chapter quotes it — a compiler promising a form alone" >&2
		fail=1
	done <"$tmp/code-names"

	while read -r nm; do
		[ -n "$nm" ] || continue
		grep -qx "$nm" "$tmp/code-names" && continue
		echo "REJECTED  a chapter quotes $code for \`$nm\` and $SRC:$ln does not list it — the form is refused permanently instead" >&2
		fail=1
	done <"$tmp/doc-names"

	echo "method-gaps-check: $code — $(tr '\n' ' ' <"$tmp/code-names")"
done <"$tmp/lists"

# The extraction's own guard, and it is a floor of ONE rather than a count: how many splits
# exist is a fact that lives in emit.zg and nowhere else, so there is nothing honest to compare
# a number against. Zero is the failure that matters — every comparison above is inside this
# loop, and an anchor that stops matching runs none of them and goes silently green.
[ "$lists" -ge 1 ] || { echo "EMPTY     no \`unbuilt := meth == …\` list was read from $SRC — the extraction has gone stale" >&2; fail=1; }

[ "$fail" -eq 0 ] || { echo "method-gaps-check: a method name and its chapter disagree about whether the form is coming" >&2; exit 1; }
echo "method-gaps-check: $lists method-name lists, each the same list its chapter promises"
