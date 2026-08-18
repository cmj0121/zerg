#!/usr/bin/env bash
#
# doc-examples-check — the examples in a module's comments are RUN, and their stated output
# is what they actually print.
#
# AN EXAMPLE NOBODY EXECUTES IS AN UNVERIFIED CLAIM, and this project has spent a whole span
# removing exactly that shape — spec markers describing a compiler that no longer existed.
# Twenty-one hand-written examples in `strings.zg` would have been twenty-one new ones. So
# they are held to the module the same way a test is: the ` ```zerg ` block is compiled and
# run, and the ` ```output ` block beside it has to be what came out.
#
# THE FENCES ARE THE FORM `zerg doc` AND `zerg test` WILL TAKE (docs/runtime/package.md lists
# "a doc example run as a test" among what is not built). This script is not that extractor —
# it is one module's worth of it, written so the examples are true TODAY and so the day the
# real one lands it inherits examples that already pass rather than a backlog to audit. They
# sit in plain `#` comments because `##` is not yet a token in this compiler.
#
#   usage: doc-examples-check.sh <module.zg> [<module.zg> …]
#
# A FLOOR, like every other gate here: a parser that stops recognising the fences finds no
# examples and would otherwise report success for having compared nothing.
set -uo pipefail

ZERG=${ZERG:-./bin/zerg}
MIN_EXAMPLES=${MIN_EXAMPLES:-20}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fail=0
total=0

for src in "$@"; do
	[ -f "$src" ] || {
		printf 'doc-examples: %s is not there\n' "$src" >&2
		exit 1
	}

	module=$(basename "$src" .zg)

	# Pull the pairs out. A code line and an output line are written to their own file, in
	# source order; the `# ` prefix every comment carries is stripped, and nothing else is.
	# THE FENCES MAY BE INDENTED. A doc comment inside an `impl` block carries that block's
	# indentation, and an anchored `^# ` missed every one of them — silently, which is the
	# exact failure this gate exists to remove: `log.zg` had three examples inside
	# `impl Logger` and the gate ran none of them while reporting success.
	awk '
		/^[ \t]*# ```zerg$/   { mode = "code"; next }
		/^[ \t]*# ```output$/ { mode = "out";  next }
		/^[ \t]*# ```$/       { mode = "";     next }
		mode == "code" { sub(/^[ \t]*# ?/, ""); print > (T "/code") }
		mode == "out"  { sub(/^[ \t]*# ?/, ""); print > (T "/want") }
	' T="$tmp" "$src"

	[ -s "$tmp/code" ] || {
		printf 'doc-examples: %s declares no ```zerg example\n' "$src" >&2
		fail=$((fail + 1))
		continue
	}

	# One program that prints every example expression in order. `print` is what renders the
	# value, which is why an example of a function answering a `list[T]` has to reduce it —
	# E9059, a composite has no structural `Display` in this compiler.
	{
		printf 'import "%s"\n\nfn main() {\n' "$module"
		sed 's/^/\tprint /' "$tmp/code"
		printf '}\n'
	} >"$tmp/main.zg"

	n=$(wc -l <"$tmp/code" | tr -d ' ')
	total=$((total + n))

	rm -f "$tmp/probe"
	if ! "$ZERG" build "$tmp/main.zg" -o "$tmp/probe" >"$tmp/build.log" 2>&1; then
		printf 'doc-examples: %s — an example does not compile\n' "$src" >&2
		cat "$tmp/build.log" >&2
		fail=$((fail + 1))
		continue
	fi

	"$tmp/probe" >"$tmp/got" 2>&1

	if ! diff -u "$tmp/want" "$tmp/got" >"$tmp/diff"; then
		printf 'doc-examples: %s — an ```output block is not what the example prints\n' "$src" >&2
		cat "$tmp/diff" >&2
		fail=$((fail + 1))
	fi

	rm -f "$tmp/code" "$tmp/want" "$tmp/got"
done

if [ "$total" -lt "$MIN_EXAMPLES" ]; then
	printf 'doc-examples: %s examples were run, below the floor of %s — the fences stopped being recognised\n' \
		"$total" "$MIN_EXAMPLES" >&2
	exit 1
fi

[ "$fail" -eq 0 ] || exit 1

printf 'doc-examples: %s examples run, and each prints what its ```output block says\n' "$total"
