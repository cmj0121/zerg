#!/usr/bin/env bash
#
# examples-index-check — every example the gate builds is named in the index a reader opens.
#
# WHY THIS EXISTS. Thirty-three example programs sat in this tree and NO DOCUMENT LINKED TO ANY OF
# THEM — `grep -rn 'examples/' README.md docs/README.md` was empty, and so was a grep for a markdown
# link to one. They were reached by `ls` and by nothing else. `examples/README.md` is the door, and
# a door is only as good as the day somebody last updated it.
#
# AN INDEX OF THIRTY-THREE NAMES IS A LIST WRITTEN TWICE, which is the hazard `mk/gates.mk` already
# names about `SELF_SRCS` and `EXAMPLE_SRCS`: *a scope written twice goes stale on whichever copy the
# next directory is not added to*. `make examples` globs the programs; this asks whether the index
# names what that glob found. The check is ONE DIRECTION and cheap — an example the index never
# mentions — because that is the direction the drift goes: somebody adds an example and the gate
# starts building it, which is exactly when nothing reminds them to write a line about it.
#
# `docs-links` cannot see this. It asks whether a cited path exists; an example nobody cites is
# invisible to it, and being uncited is the whole defect.
#
# ONLY THE ENGLISH PAGE IS READ, and that is not half a check: `make docs-mirror` holds the pair to
# the same table ROWS, so a name missing from the translation is that gate's finding. Reading both
# here would be the second copy this file exists to complain about.
#
# A FLOOR, like every gate here: the extraction is a glob, and a glob that stops matching finds no
# missing names for the same reason a complete index does.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

INDEX=${INDEX:-examples/README.md}

# 33 today. Far enough below that adding one is not a chore here, far enough above that a glob
# which stopped matching cannot pass.
MIN_EXAMPLES=${MIN_EXAMPLES:-25}

[ -f "$INDEX" ] || {
	echo "examples-index: $INDEX is not there — the examples have no door" >&2
	exit 1
}

fail=0
seen=0

# The SAME shapes `EXAMPLE_SRCS` globs, reduced to the name a reader looks up: a numbered program by
# its filename, a directory program by the directory.
for f in examples/[0-9][0-9]_*.zg; do
	[ -f "$f" ] || continue
	seen=$((seen + 1))
	name=$(basename "$f")
	grep -qF "($name)" "$INDEX" || {
		echo "examples-index: $f is built by \`make examples\` and named nowhere in $INDEX" >&2
		fail=1
	}
done

for f in examples/*/main.zg examples/1g/*/main.zg; do
	[ -f "$f" ] || continue
	dir=$(dirname "$f")
	# `examples/1g` itself holds no main.zg, and the outer glob would otherwise reach the
	# directory that holds the inner ones.
	name=${dir#examples/}
	seen=$((seen + 1))
	grep -qF "($name)" "$INDEX" || {
		echo "examples-index: $dir is built by \`make examples\` and named nowhere in $INDEX" >&2
		fail=1
	}
done

if [ "$seen" -lt "$MIN_EXAMPLES" ]; then
	echo "examples-index: found $seen examples and the floor is $MIN_EXAMPLES — the glob stopped matching, so an index of nothing would pass" >&2
	exit 1
fi

[ "$fail" -eq 0 ] || exit 1
echo "examples-index: $seen examples, each named in $INDEX"
