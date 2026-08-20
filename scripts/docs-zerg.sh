#!/usr/bin/env bash
#
# docs-zerg — a ` ```zerg ` fence in the specification claims its contents are Zerg, and this
# is what holds it to that claim: every one of them is compiled.
#
# A FENCE LANGUAGE IS A CLAIM NOBODY WAS CHECKING. When this was written, 30 ` ```zerg ` blocks
# lived in the English chapters and 21 of them were not Zerg at all — diagrams with a `→` down
# the middle (`E1004 this character is not part of any Zerg token`), before/after pairs that
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
# UNLESS IT IS ONE FILE OF A PROGRAM THAT HAS MORE THAN ONE. `getting-started.md` is the first
# chapter to teach a second file, and a second file is the one shape the paragraph above cannot
# express: `main.zg` importing `./greet` is a whole program, and it is not a whole FILE. Both of
# its blocks were red here for being honest.
#
# So a block whose FIRST LINE names a path — `# greet/greet.zg` — is a file of a DIRECTORY
# PROGRAM rather than a program on its own, and consecutive such blocks are assembled at the
# paths they name and checked together. That comment is not a marker invented for this gate: it
# is the line a reader needs anyway to know which file they are looking at, which is why it can
# carry the meaning without becoming the opt-out that clause 2 exists to forbid. It cannot say
# "skip me" — it says "I am a file", and a file still has to compile.
#
# A GROUP IS CHECKED THE WAY A READER WOULD CHECK IT, not file by file: `zerg build --emit
# check` on the entry — the file declaring `fn main` — which compiles everything it imports;
# and `zerg test` on each directory holding a `*_test.zg`, because a normal build leaves a test
# file on the floor and it would otherwise be the one block written and never compiled. A group
# with no entry, or with two, is a failure: nothing would say what to build.
#
# THERE IS NO SKIP LIST AND NO OPT-OUT, and that is deliberate rather than unfinished. The
# alternative — a marker on the fence saying "do not compile this one" — was considered and
# declined: after the retagging pass there was nothing left for it to mark, and a mechanism
# with no user is a hole waiting for the first person in a hurry. So the only way to say
# "this cannot be built" is to stop calling it Zerg, which is a statement a reader of the
# source can see. A DECORATED FENCE IS A FAILURE HERE (clause 2 below) precisely so that
# ` ```zerg ignore ` cannot be invented later without changing this file on purpose.
#
# THE BLIND SPOT THIS HAD IS CLOSED. `--emit check` used to resolve nothing in a TOP-LEVEL
# binding's initialiser — a unit declaring no `main` never lowered one — so `a := builder()`
# with no `builder` anywhere passed here, and three of fmt.md's layout examples were green for
# that reason rather than for being complete. That was a gap in the compiler and not in this
# gate, and closing it (#29) did here exactly what this paragraph said it would: the three
# blocks failed, and each is now ` ```text `, because a bracket landing in a column is a
# picture of layout and not a program anybody would write.
#
# WHAT IT STILL CANNOT SEE. It does not DIFF anything: `doc-examples-check.sh` is the gate
# that holds an example to its stated output, and it owns the module comments. This one owns
# the chapters, and it asks only whether they compile — with the one exception above, where a
# group's tests are run because filtering happens before building, so `zerg test -k <a name no
# test has>` reports "no such test" and exits clean over a test file that does not compile.
# That was measured, not assumed.
#
# And within a group, a file the entry never imports and no test reaches is WRITTEN AND NOT
# COMPILED. The entry requirement is what keeps that from being the common case rather than
# the corner one.
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

# A directory program is built from INSIDE its own directory, the way its reader would, so the
# compiler cannot be reached by a path relative to the tree.
ZERG=$(cd "$(dirname "$ZERG")" && pwd)/$(basename "$ZERG")

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

# The temp directory holds ONE .zg at a time and nothing else, because a stale sibling would
# let one block's scaffolding satisfy the next block's import. A directory program is assembled
# under `prog/` and that whole subtree is removed when its group closes, for the same reason.
tmp=$(mktemp -d) || exit 2
trap 'rm -rf "$tmp"' EXIT

fail=0
blocks=0
seen=0

# check_group <page> <the lines its blocks opened on> — check what the path-named blocks
# collected so far amount to, and clear them. A no-op when no group is open.
check_group() {
	cg_page=$1
	cg_lines=$2
	[ -d "$tmp/prog" ] || return 0

	cg_entries=$(cd "$tmp/prog" && grep -lE '^fn main\(' $(find . -name '*.zg' ! -name '*_test.zg') 2>/dev/null | sed 's|^\./||' | sort)
	cg_n=$(printf '%s' "$cg_entries" | grep -c . || true)

	if [ "$cg_n" != 1 ]; then
		printf 'docs-zerg: %s: the blocks opening at%s are one program with %s files declaring `fn main` — nothing says what to build\n' \
			"$cg_page" "$cg_lines" "$cg_n" >&2
		fail=$((fail + 1))
	elif ! (cd "$tmp/prog" && "$ZERG" build --emit check "$cg_entries") >"$tmp/build.log" 2>&1; then
		printf 'docs-zerg: %s: the blocks opening at%s are one program rooted at `%s`, and the compiler does not agree\n' \
			"$cg_page" "$cg_lines" "$cg_entries" >&2
		sed 's/^/            /' "$tmp/build.log" >&2
		fail=$((fail + 1))
	fi

	# A `*_test.zg` is on the floor of that build, so it gets the command that does compile it.
	for cg_d in $(cd "$tmp/prog" && find . -name '*_test.zg' -exec dirname {} \; | sed 's|^\./||;s|^\.$|.|' | sort -u); do
		if ! (cd "$tmp/prog" && "$ZERG" test "$cg_d") >"$tmp/build.log" 2>&1; then
			printf 'docs-zerg: %s: the `*_test.zg` of `%s` does not build and pass\n' "$cg_page" "$cg_d" >&2
			sed 's/^/            /' "$tmp/build.log" >&2
			fail=$((fail + 1))
		fi
	done

	rm -rf "$tmp/prog"
}

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

	rm -rf "$tmp/prog"
	group=

	while IFS= read -r line; do
		blocks=$((blocks + 1))

		# 3. A FIRST LINE NAMING A PATH makes this block a file of a directory program.
		#    The comment is what a reader needs anyway; here it is also what says this
		#    block is not expected to stand alone.
		path=$(sed -n '1{/^# [A-Za-z0-9_][A-Za-z0-9_./-]*\.zg$/s/^# //p;}' "$tmp/$line.zg")
		case $path in
		*..*)
			printf 'docs-zerg: %s:%s — `%s` reaches outside the program it is a file of\n' \
				"$page" "$line" "$path" >&2
			fail=$((fail + 1))
			path=
			;;
		esac
		if [ -n "$path" ]; then
			mkdir -p "$tmp/prog/$(dirname "$path")"
			mv "$tmp/$line.zg" "$tmp/prog/$path"
			group="$group $line"
			continue
		fi

		# A block that stands alone ends whatever group was open before it.
		check_group "$page" "$group"
		group=

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

	check_group "$page" "$group"
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
