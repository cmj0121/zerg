#!/usr/bin/env bash
#
# entry-path-check — the same program is the same program, however its entry was spelled.
#
# `zerg build src/compiler/zergc.zg` and `zerg build /abs/…/src/compiler/zergc.zg` are one
# program. Nothing about it changed between the two commands; only the string a person typed
# did. So the C must be the same C.
#
# IT WAS NOT. Measured before this gate existed: 4.5 MB of C with 450 KB differing, and the
# module tags shifted — the same private function emitted as `zg_m2_has_str` in one build and
# `zg_m4_has_str` in the other. A module's identity here is the DIRECTORY it was read from
# (`c_mod_of`), so one build named the project's modules relatively while the standard
# library's stayed absolute, and `c_mod_tag` — which SORTS those names to number them — sorted
# the two sets into different orders. `emit.zg`'s own comment already records one spelling of
# this defect: *the module identity was a function of how the entry path happened to be TYPED*.
#
# WHAT IS NORMALISED, AND WHY IT IS NOT CHEATING. `#line` carries the source path AS THE USER
# SPELLED IT, and it must: a `cc` error and a debugger have to land on the file the programmer
# named, so an absolute build owes absolute `#line`s. Comparing the two files raw would
# therefore demand that `#line` lose the path, which is the one thing `#line` is for. So the
# repository prefix is stripped from BOTH sides — the same substitution, applied identically —
# and what is left is every byte that is not a path the user chose.
#
# Both sides get it because the standard library resolves through `ZERG_ROOT`, which comes from
# the EXECUTABLE's own location and is absolute in either build. Stripping one side only turned
# the stdlib's `#line`s relative on that side alone, which is a difference the gate would then
# report and the compiler never made.
#
# A FLOOR, like every gate here: two empty files are identical, and a build that emitted
# nothing would pass for having compared nothing.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

ZERG=${ZERG:-./bin/zerg}
ENTRY=${ENTRY:-src/compiler/zergc.zg}

# The compiler's own C is ~4.5 MB. Far enough below that the number is not a chore when the
# compiler grows or shrinks, far enough above that a stage which stopped emitting cannot pass.
MIN_BYTES=${MIN_BYTES:-1000000}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

if ! "$ZERG" build --emit c "$ENTRY" -o "$tmp/rel.c" 2>"$tmp/rel.err"; then
	echo "entry-path: the relative build failed: $(head -1 "$tmp/rel.err")" >&2
	exit 1
fi
if ! "$ZERG" build --emit c "$ROOT/$ENTRY" -o "$tmp/abs.c" 2>"$tmp/abs.err"; then
	echo "entry-path: the absolute build failed: $(head -1 "$tmp/abs.err")" >&2
	exit 1
fi

n=$(wc -c <"$tmp/rel.c" | tr -d ' ')
if [ "$n" -lt "$MIN_BYTES" ]; then
	echo "entry-path: the build emitted $n bytes and the floor is $MIN_BYTES — two files this small are identical for the wrong reason" >&2
	exit 1
fi

sed "s|$ROOT/||g" "$tmp/rel.c" >"$tmp/rel.norm"
sed "s|$ROOT/||g" "$tmp/abs.c" >"$tmp/abs.norm"

if ! cmp -s "$tmp/rel.norm" "$tmp/abs.norm"; then
	echo "entry-path: the same program emitted different C for a relative and an absolute entry path" >&2
	diff "$tmp/rel.norm" "$tmp/abs.norm" | head -8 | sed 's/^/  /' >&2
	echo "  (a module's identity is reaching the output through the spelling of the entry path)" >&2
	exit 1
fi

echo "entry-path: $n bytes of C, byte-identical whether the entry was named relatively or absolutely"
