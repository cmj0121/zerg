#!/usr/bin/env bash
#
# docs-zerg — a ` ```zerg ` fence in the specification claims its contents are Zerg, and this
# is what holds it to that claim: every one of them is compiled.
#
# A FENCE LANGUAGE IS A CLAIM NOBODY WAS CHECKING. When this was written, 30 ` ```zerg ` blocks
# lived in the English chapters and 21 of them were not Zerg at all — diagrams with a `→` down
# the middle (`E104 this character is not part of any Zerg token`), before/after pairs that
# declare the same function twice, notation the lexer has never heard of. The translations
# agreed with them perfectly, which is why `docs-mirror` could not see it: both languages said
# `zerg` and both were wrong, so the pair matched. Only the compiler disagreed, and nothing
# asked it.
#
# The one that WAS caught is the shape of the whole defect. `fmt.md`'s F409 diagram was tagged
# `zerg` in the Chinese and `text` in the English; `docs-mirror` reported it as drift the day
# it first ran. Twenty-one more had the same disease and were invisible for having caught it
# in both languages at once.
#
# WHAT A ` ```zerg ` BLOCK MUST BE: a whole Zerg program, on its own, that
# `zerg build --emit check` accepts. Not a fragment that would compile inside something else,
# and not an excerpt of a real file — those are pictures of code, and a picture is ` ```text `.
#
# THERE IS NO SKIP LIST AND NO OPT-OUT, and that is deliberate rather than unfinished. The
# alternative — a marker on the fence saying "do not compile this one" — was considered and
# declined: after the retagging pass there was nothing left for it to mark, and a mechanism
# with no user is a hole waiting for the first person in a hurry. So the only way to say
# "this cannot be built" is to stop calling it Zerg, which is a statement a reader of the
# source can see. A DECORATED FENCE IS A FAILURE HERE (clause 2 below) precisely so that
# ` ```zerg ignore ` cannot be invented later without changing this file on purpose.
#
# WHAT IT CANNOT SEE: `--emit check` does not resolve the calls in a TOP-LEVEL binding's
# initialiser, so `a := builder()` with no `builder` anywhere passes — three of fmt.md's
# layout examples are green for that reason rather than for being complete. That is a gap in
# the compiler and not in this gate; when it closes, those blocks fail here and get made whole
# or retagged. Nor does it run anything: `doc-examples-check.sh` is the gate that DIFFS an
# example's stated output, and it owns the module comments. This one owns the chapters.
#
#   usage: docs-zerg.sh [<file.md> …]        (default: every .md under docs/)
#
# A FLOOR, like every other gate here: extraction is an `awk` over a fence marker, and a
# marker that stops matching finds no bad blocks for the same reason an honest tree does.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

ZERG=${ZERG:-./bin/zerg}
MIN_BLOCKS=${MIN_BLOCKS:-24}
MIN_PAGES=${MIN_PAGES:-8}

[ -x "$ZERG" ] || {
	printf 'docs-zerg: %s is not there — run `make build` first\n' "$ZERG" >&2
	exit 2
}

# `git ls-files` and not a glob: an untracked scratch .md beside the chapters is not part of
# the specification and must not be able to fail — or to pad — this gate.
#
# The floors guard DISCOVERY, so naming pages on the command line switches them off — that
# invocation is a person checking one chapter, and it knows how many pages it asked for.
pages=$*
whole_tree=0
[ -n "$pages" ] || {
	pages=$(git ls-files 'docs/*.md' 'docs/**/*.md')
	whole_tree=1
}

# The temp directory holds ONE .zg at a time and nothing else, because `import "log"` resolves
# beside the importing file before it reaches the standard library. A directory with leftovers
# in it would let one block's scaffolding satisfy the next block's import.
tmp=$(mktemp -d) || exit 2
trap 'rm -rf "$tmp"' EXIT

fail=0
blocks=0
seen=0

for page in $pages; do
	[ -f "$page" ] || {
		printf 'docs-zerg: %s is not there\n' "$page" >&2
		exit 2
	}
	seen=$((seen + 1))

	# 1. every ` ```zerg ` block, with the line its fence opened on. The line number is what
	#    makes a failure actionable: the extracted file is a temp path nobody can open twice.
	#
	#    Only a fence at the START of a line opens a block. An indented one is inside a list
	#    item and belongs to whatever encloses it, and a ` ``` ` inside a block is not a
	#    thing this grammar has.
	awk -v out="$tmp" '
		/^```zerg$/ { open = NR; f = out "/" NR ".zg"; printf "" > f; next }
		open && /^```$/ { close(f); print open >> (out "/INDEX"); open = 0; next }
		open { print >> f }
		END { if (open) printf "UNCLOSED %d\n", open > "/dev/stderr" }
	' "$page" 2>"$tmp/awkerr"

	if [ -s "$tmp/awkerr" ]; then
		printf 'docs-zerg: %s — a ```zerg fence is never closed (%s)\n' "$page" "$(cat "$tmp/awkerr")" >&2
		fail=$((fail + 1))
	fi

	# 2. NO OPT-OUT. A fence whose language begins with `zerg` and is not exactly `zerg` is
	#    an invented escape hatch — ` ```zerg ignore `, ` ```zergc `, ` ```zerg,no-check ` —
	#    and it would sail past clause 1 by never matching it.
	if grep -nE '^```zerg.+$' "$page" >"$tmp/decorated"; then
		while IFS= read -r hit; do
			printf 'docs-zerg: %s:%s — a ```zerg fence takes no decoration; a block that cannot be built is ```text\n' \
				"$page" "${hit%%:*}" >&2
		done <"$tmp/decorated"
		fail=$((fail + 1))
	fi

	[ -f "$tmp/INDEX" ] || continue

	while IFS= read -r line; do
		blocks=$((blocks + 1))
		src="$tmp/block.zg"
		mv "$tmp/$line.zg" "$src"
		if ! "$ZERG" build --emit check "$src" >"$tmp/build.log" 2>&1; then
			printf 'docs-zerg: %s:%s — the fence says zerg and the compiler does not agree\n' \
				"$page" "$line" >&2
			sed 's/^/            /' "$tmp/build.log" >&2
			fail=$((fail + 1))
		fi
		rm -f "$src"
	done <"$tmp/INDEX"
	rm -f "$tmp/INDEX"
done

if [ "$whole_tree" -eq 1 ]; then
	if [ "$seen" -lt "$MIN_PAGES" ]; then
		printf 'docs-zerg: %s pages read, below the floor of %s — discovery stopped matching\n' \
			"$seen" "$MIN_PAGES" >&2
		exit 1
	fi

	if [ "$blocks" -lt "$MIN_BLOCKS" ]; then
		printf 'docs-zerg: %s blocks compiled, below the floor of %s — the fence marker stopped matching\n' \
			"$blocks" "$MIN_BLOCKS" >&2
		exit 1
	fi
fi

[ "$fail" -eq 0 ] || {
	printf 'docs-zerg: %s finding(s) — a fence says zerg and its contents are not\n' "$fail" >&2
	exit 1
}

printf 'docs-zerg: %s ```zerg blocks over %s pages — each one a program this compiler accepts\n' \
	"$blocks" "$seen"
