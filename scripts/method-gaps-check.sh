#!/bin/sh
# method-gaps-check — a method name the compiler calls unbuilt is a name a chapter promises,
# and the two lists are the same list (#74).
#
# A method-gap code says *the language has this method and this compiler has not lowered it*.
# Where such a code is SPLIT — a name on a hard-coded list answers the `E9xxx`, and every other
# name answers an `E3xxx`, which says *this program is permanently wrong* — the list is what
# tells the two apart, and it is a second copy of a fact that already lives in a chapter's
# `[not yet]` marker.
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
# list is the bite above (REJECTED). A name quoted under ANOTHER code is a third (MISCODED):
# the sentence is the compiler's and the number beside it is not, so a reader looking the code
# up meets a different rule — the defect `b1bf789f` fixed by hand.
#
# WHAT THIS GATE CANNOT SEE, and where that half is held instead. It reads NAMES and never the
# RECEIVER a name is given on: two of `E9107`'s five are given on a receiver set rather than on
# every value (`next` on a channel, `iter` on what `for … in` walks), and a chapter quotes a
# sentence, not a set — there is nothing on the doc side to compare a predicate against. Delete
# the receiver test and this gate stays green. What notices is a CASE: `iter-on-a-value-no-loop-
# walks` and `next-on-a-value-that-is-not-an-iterator` in scripts/reject-check.sh pin an `int`
# to the permanent verdict, and widening either clause makes both red.
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
# chapter names, and its rows carry a `…` where the method goes. The TRANSLATIONS are excluded
# because a name quoted only in `zh-TW` would satisfy the English chapter, and the pair is
# already held together by `docs-mirror`; this gate asks the normative half.
find docs -name '*.md' ! -name '*.zh-TW.md' ! -name 'diagnostics*.md' -exec cat {} + |
	sed -e 's/^[[:space:]]*>[[:space:]]*//' | tr '\n' ' ' >"$tmp/chapters"

# THE METHOD-GAP RAISES, read out of the messages rather than listed here. One is a raise
# whose SUBJECT is the name — `NotImplemented: the method `{meth}`` or `the map method
# `{meth}`` — which is what tells it from the other `NotImplemented:` raises that happen to
# carry a `{meth}` hole (an rvalue mutation, an associated function).
#
# THE ANCHOR USED TO REQUIRE A CONDITION, and that is how #74 shipped its own defect. It read
# `.*\) if `, so only a raise that was ALREADY split was visible, and the one that was not —
# `E9056`, calling every name it was reached with unbuilt, `xs.wobble()` included — could not
# be seen by the gate written to find exactly that. A gate that only inspects the work already
# done cannot report the work not done. So the condition is no longer part of the anchor: it
# is read below, and its absence is a finding of its own.
grep -nE 'rule\.Rule\.[A-Za-z_]+, f"NotImplemented: the ([a-z]+ )?method .\{meth\}' "$SRC" |
	cut -d: -f1 >"$tmp/raises"

# THE NAMES, read from the bindings the raise's condition stands on: walk up from the raise
# while the lines are bindings, take every `meth == "…"` in them, and if there are none, follow
# the functions those bindings CALL and read their bodies. The two splits are spelled
# differently on purpose — a map's list is the binding itself, and `E9107`'s names live in the
# function that answers each name's substitute sentence, because there the name, the receiver
# it is given on and what to write instead are one clause — and which spelling a split uses is
# not a fact this gate should hold an opinion about. Where the decision is made is written in
# the code; this follows it, and says so when it cannot.
read_names() {
	: >"$tmp/code-names"
	: >"$tmp/callees"
	i=$(($1 - 1))
	while [ "$i" -gt 0 ]; do
		line=$(sed -n "${i}p" "$SRC")
		printf '%s\n' "$line" | grep -qE '^	[a-z_][a-z_0-9]* :?= ' || break
		printf '%s\n' "$line" | grep -oE 'meth == "[A-Za-z_][A-Za-z_0-9]*"' |
			sed -e 's/.*"\(.*\)"/\1/' >>"$tmp/code-names"
		printf '%s\n' "$line" | grep -oE '[a-z_][a-z_0-9]*\(' | tr -d '(' >>"$tmp/callees"
		i=$((i - 1))
	done

	if [ ! -s "$tmp/code-names" ]; then
		while read -r callee; do
			[ -n "$callee" ] || continue
			sed -n "/^fn ${callee}(/,/^}/p" "$SRC" |
				grep -oE 'meth == "[A-Za-z_][A-Za-z_0-9]*"' |
				sed -e 's/.*"\(.*\)"/\1/' >>"$tmp/code-names"
		done <"$tmp/callees"
	fi

	sort -u "$tmp/code-names" -o "$tmp/code-names"
}

lists=0
while read -r ln; do
	lists=$((lists + 1))
	raise=$(sed -n "${ln}p" "$SRC")
	variant=$(printf '%s\n' "$raise" | grep -oE 'rule\.Rule\.[A-Za-z_][A-Za-z_0-9]*' | head -1 | sed -e 's/.*\.//')

	num=$(grep -oE "^	$variant = [0-9]{4}\$" "$RULES" | grep -oE '[0-9]{4}$')
	if [ -z "$num" ]; then
		echo "STALE     $variant is raised at $SRC:$ln and $RULES declares no such rule" >&2
		fail=1
		continue
	fi
	code="E$num"

	# AND IT MUST BE CONDITIONAL. An unconditional method-gap raise calls EVERY name it is
	# reached with unbuilt, so a name the language does not give the receiver — and no build
	# will ever grow — is refused as a form somebody is waiting for. That is the whole of #74,
	# and `E9056` was it: the list's fallback, one arm from the map's fallback the same change
	# had split, wearing `NotImplemented:` for `xs.wobble()`. There is nothing to compare a
	# chapter against here, because there is no list; what is owed is the split.
	case $raise in
	*") if "*) ;;
	*)
		echo "UNSPLIT   $code names a method and calls every one of them unbuilt with no condition — a name the language does not give is refused as a form that is coming" >&2
		fail=1
		continue
		;;
	esac

	# The needle: the message's literal text up to the FIRST `{meth}` hole, which ends in the
	# opening backtick the chapters quote the name inside. Both cuts take the first match —
	# `.*f"` and `\(.*\){meth}` are greedy, and a message with a second hole yielded a needle
	# no chapter contains and a false PROMISED on correct code.
	needle=$(printf '%s\n' "$raise" | sed -e 's/^[^"]*f"//' -e 's/{meth}.*//')
	case "$needle" in
	"$raise" | "") echo "STALE     $code's message at $SRC:$ln has no {meth} hole — nothing to read a name out of" >&2; fail=1; continue ;;
	esac

	read_names "$ln"
	if [ ! -s "$tmp/code-names" ]; then
		echo "STALE     $code is split at $SRC:$ln and no method-name list decides it — the extraction has gone stale, or the list has" >&2
		fail=1
		continue
	fi

	# grep -o prints every match on the joined line, so the whole chapter set is read in one
	# pass. The needle is spliced into the pattern with its ERE metacharacters escaped, and
	# only the name after it is a pattern; the needle ends in the opening backtick, which is
	# not a name character, so cutting back to the last one leaves the name alone. The space
	# between the two is optional-and-any: a marker that wraps right after the backtick puts
	# one there, and the join above cannot tell that space from the one it did not write.
	esc=$(printf '%s' "$needle" | sed -e 's/[][\\.^$*+?(){}|]/\\&/g')
	grep -oE "$esc[[:space:]]*[A-Za-z_][A-Za-z_0-9]*" "$tmp/chapters" |
		sed -e 's/.*[^A-Za-z_0-9]//' | sort -u >"$tmp/doc-names" || true

	# AND WHICH CODE THE CHAPTER PUT BESIDE IT. The needle is the sentence alone, so a marker
	# quoting the right sentence under the wrong number satisfied it — the reader follows the
	# number and lands on another rule. A quotation that names NO code is left alone: chapters
	# quote a second name by saying "the same sentence for …", and the sentence is the claim.
	grep -oE "E[0-9]{4} $esc[[:space:]]*[A-Za-z_][A-Za-z_0-9]*" "$tmp/chapters" |
		sort -u >"$tmp/doc-coded" || true
	while read -r hit; do
		[ -n "$hit" ] || continue
		case "$hit" in
		"$code "*) continue ;;
		esac
		echo "MISCODED  a chapter quotes $code's sentence under $(printf '%s' "$hit" | cut -d' ' -f1) — \"$hit\"" >&2
		fail=1
	done <"$tmp/doc-coded"

	while read -r nm; do
		[ -n "$nm" ] || continue
		grep -qx "$nm" "$tmp/doc-names" && continue
		echo "PROMISED  $code calls \`$nm\` unbuilt and no chapter quotes it — a compiler promising a form alone" >&2
		fail=1
	done <"$tmp/code-names"

	while read -r nm; do
		[ -n "$nm" ] || continue
		grep -qx "$nm" "$tmp/code-names" && continue
		echo "REJECTED  a chapter quotes $code for \`$nm\` and $SRC does not list it — the form is refused permanently instead" >&2
		fail=1
	done <"$tmp/doc-names"

	echo "method-gaps-check: $code — $(tr '\n' ' ' <"$tmp/code-names")"
done <"$tmp/raises"

# The extraction's own guard. How many splits exist is a fact that lives in emit.zg and nowhere
# else, so there is nothing honest to compare a number against — and a number is what the floor
# used to be: at one, it caught ZERO lists and not the loss of one of two, which is the failure
# that actually happens. What replaces it is per-split and derived: every split this file names
# must yield a list, or it is STALE above. Zero splits is the last thing left to say out loud,
# because every comparison is inside the loop and an anchor that stops matching runs none.
[ "$lists" -ge 1 ] || { echo "EMPTY     no split method-gap raise was read from $SRC — the extraction has gone stale" >&2; fail=1; }

[ "$fail" -eq 0 ] || { echo "method-gaps-check: a method name and its chapter disagree about whether the form is coming" >&2; exit 1; }
echo "method-gaps-check: $lists method-name lists, each the same list its chapter promises"
