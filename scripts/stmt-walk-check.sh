#!/usr/bin/env bash
#
# stmt-walk-check — a walk that reaches into a block reaches into EVERY block.
#
# `Stmt.SScope` is a bare `{ … }` used as a statement (GRAMMAR#compound-stmt). It carries a
# whole block, like `SIf` and `SFor` and `SAssert` do, and it is the one a hand-written match
# forgets — because it is the only block form with no keyword in front of it to remind you.
#
# TEN WALKS HAD FORGOTTEN IT, across three files, and every one ended in `_ =>`. What fell out
# was not one bug with one shape; it was every kind this project says it does not ship:
#
#   a legal program REFUSED, with a reason that is not true — `{ x: T = v }` inside a
#   template was _no type named `T`_, and a function whose only `return` sat in a block was
#   _its body falls off the end_;
#
#   a rule that simply DID NOT FIRE — `{ break }` outside any loop, and a `return` leaving a
#   `guard`, both of which this compiler has a code for and neither of which got it;
#
#   a finding about CORRECT CODE — `L102 private function never called` about a function
#   called on the line above, from the linter's usage walk;
#
#   and code reaching CC — the `break` above arrived as _'break' statement not in loop or
#   switch statement_, and a `[fn(a: T) …; 2]` as _unknown type name 'zg_T'_, both against
#   generated C nobody wrote, which is the failure the standing contract names first.
#
# One arm each. The arms were not hard; FINDING them was, and that is what this is for.
#
# THE RULE IS MECHANICAL: a function with a `Stmt.SIf(…) =>` ARM owes a `Stmt.SScope` one.
# `SIf` is the marker because every statement walk in this tree has an arm for it — a walk
# that did not would not be a walk over statements.
#
# AN ARM, NOT A MENTION, and that distinction is what makes the rule usable rather than a
# list of exceptions. The parser CONSTRUCTS `Stmt.SIf(...)` in four places and walks nothing;
# a constructor has no `=>` after its closing paren and a match arm does. Without that test
# this reported eight functions, four of them parser constructors.
#
# THE EXCEPTIONS ARE LISTED WITH THEIR REASON, and a stale entry fails: a function that stops
# matching the rule has either been fixed or renamed, and either way the line here is a claim
# about code that is no longer there. That is the contract `scripts/oracle-skips.txt` and
# `CORPUS_SKIP` both carry, for the same reason — a skip list nobody re-reads is where
# findings go to hide.
#
# A FLOOR, like every gate here. The extraction is an `awk` over a function header and an arm
# pattern; a pattern that stops matching finds no offenders for the same reason a clean tree
# does, and reports success for having examined nothing.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

SRCS=${SRCS:-"src/compiler/zerg src/compiler/cmd src/compiler/lsp"}

# A FLOOR under the walks examined. Ten is what this found and fixed; the tree has more than
# that and will grow more, so the number is far enough below to not be a chore and far enough
# above that a glob matching one file cannot pass.
WALK_MIN=${WALK_MIN:-12}

# <file>:<function>	the sentence saying why it is not a walk.
#
# `p_jump_is_guarded` CLASSIFIES ONE STATEMENT — it answers whether a jump was written under
# a guard, and `SIf` is one of the two shapes that means yes. It descends into nothing, so a
# bare block is not a case it is missing; it is a case whose answer is no.
EXCEPTIONS='src/compiler/zerg/parser.zg:p_jump_is_guarded'

fail=0

note() {
	printf 'stmt-walk: %s\n' "$1" >&2
	fail=1
}

# every function carrying a `Stmt.SIf(…) =>` arm, and whether it also names Stmt.SScope
walks=$(
	for d in $SRCS; do
		for f in "$d"/*.zg; do
			[ -f "$f" ] || continue
			awk -v F="$f" '
			function flush() {
				if (fn != "") printf "%s:%s\t%d\t%d\n", F, fn, sif, ssc
			}
			/^(pub )?fn [a-zA-Z_]/ {
				flush()
				fn = $0; sub(/^(pub )?fn /, "", fn); sub(/\(.*/, "", fn)
				sif = 0; ssc = 0
				next
			}
			/Stmt\.SIf\([^)]*\)[ \t]*=>/ { sif = 1 }
			/Stmt\.SScope/ { ssc = 1 }
			END { flush() }
			' "$f"
		done
	done | awk -F'\t' '$2 == 1'
)

examined=$(printf '%s' "$walks" | grep -c . || true)
if [ "$examined" -lt "$WALK_MIN" ]; then
	note "examined $examined statement walks, and the floor is $WALK_MIN — the extraction stopped matching, so an empty answer is not a clean tree"
	printf 'stmt-walk: %s\n' "FAILED" >&2
	exit 1
fi

while IFS=$'\t' read -r where _ ssc; do
	[ -n "$where" ] || continue
	listed=0
	case ":$EXCEPTIONS:" in
	*":$where:"*) listed=1 ;;
	esac

	if [ "$ssc" -eq 0 ] && [ "$listed" -eq 0 ]; then
		note "$where walks statements and has no \`Stmt.SScope\` arm — a bare block hides everything written inside it from this walk"
	fi
	if [ "$ssc" -eq 1 ] && [ "$listed" -eq 1 ]; then
		note "$where is listed as not a walk and now names \`Stmt.SScope\` — the exception is stale and its sentence describes code that is no longer there"
	fi
done <<EOF
$walks
EOF

# and a listed function that no longer carries the arm at all: renamed, deleted, or rewritten
for ex in $EXCEPTIONS; do
	if ! printf '%s' "$walks" | grep -q "^$ex	"; then
		note "$ex is listed as an exception and carries no \`Stmt.SIf(…) =>\` arm — the rule does not reach it, so the line says nothing"
	fi
done

if [ $fail -ne 0 ]; then
	exit 1
fi
echo "stmt-walk: $examined statement walks, each reaching a bare block"
