#!/bin/sh
# generic-walks-check — the solver and the substituter answer the same set of shapes.
#
# `gen_parts` takes a type APART — its sub-types, in written order, which is how a type
# parameter is found inside one — and `subst_ty` puts it BACK with the parameter replaced. They
# are one question asked in two directions, so a shape either of them does not name is a
# specialization that keeps a type parameter in the signature it has just decided.
#
# BOTH FAILURES HAVE HAPPENED, days apart, and neither was visible. `gen_parts` was missing the
# array, the `mut &`, the raw pointer and the `unsafe` marker — four shapes added to the type
# language after it was written — and what fell out was a refusal whose sentence was false: _the
# type parameter `T` is not decided by this call_, about an argument whose type says `T` in
# plain sight. Fixing that side left `subst_ty` without `TFn`, so `apply(twice, 21)` was told
# _argument 1 is `fn(T) -> T`, and this gives `fn(int) -> int`_ — a sentence naming the
# parameter it had already solved.
#
# A CATCH-ALL IS WHAT LETS A SHAPE ARRIVE SILENTLY, and it cannot simply be banned: most matches
# over a type are predicates — *is this a `str`?* — where a catch-all is the right answer and an
# arm per variant would be absurd. What can be held is these two AGAINST EACH OTHER, which needs
# no list of shapes to be maintained anywhere: the grammar of the check is the two functions.
#
# THE FLOOR IS WHY THIS IS NOT A GREP THAT CAN GO QUIET. Four gates in this tree already went
# stale on a rename and passed by matching nothing; an empty set satisfies every negative
# assertion. So the count is asserted before the comparison is.
set -eu

SRC=${SRC:-src/compiler/zerg/generic.zg}
MIN=${MIN:-8}

[ -f "$SRC" ] || { echo "generic-walks-check: no $SRC" >&2; exit 1; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# the arms of one function, by name: from its `fn <name>(` to the first column-0 `}`
arms() {
	awk -v fn="$1" '
		$0 ~ "^(pub )?fn " fn "\\(" { on = 1 }
		on { print }
		on && /^}/ { exit }
	' "$SRC" | grep -oE 'Ty\.T[A-Za-z]+' | sort -u
}

arms gen_parts >"$tmp/parts"
arms subst_ty >"$tmp/subst"

n_parts=$(grep -c . <"$tmp/parts" || true)
n_subst=$(grep -c . <"$tmp/subst" || true)

if [ "$n_parts" -lt "$MIN" ] || [ "$n_subst" -lt "$MIN" ]; then
	echo "generic-walks-check: gen_parts names $n_parts shapes and subst_ty names $n_subst — under the floor of $MIN, so the comparison below measured nothing (a rename empties this)" >&2
	exit 1
fi

if ! diff -u "$tmp/parts" "$tmp/subst" >"$tmp/diff"; then
	echo "generic-walks-check: gen_parts and subst_ty do not name the same shapes (- gen_parts, + subst_ty)" >&2
	sed '1,2d;s/^/          /' "$tmp/diff" >&2
	echo "generic-walks-check: a shape one of them names and the other does not is a specialization that keeps a type parameter" >&2
	exit 1
fi

echo "generic-walks-check: gen_parts and subst_ty name the same $n_parts shapes — what the solver takes apart, the substituter puts back"
