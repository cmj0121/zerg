#!/bin/sh
# docs-refused — a ` ```refused E1234 ` fence claims its contents are a program this compiler
# turns away with exactly that code, and this is the gate that asks the compiler.
#
# WHY A THIRD TAG. Two already carry contracts and neither is this one. ` ```zerg ` is
# docs-zerg's: a whole program that `--emit check` ACCEPTS. ` ```text ` is a picture — the
# same gate's own words, "a picture is ` ```text `" — and it asserts nothing at all. A marker
# beside an unbuilt form wants the third thing: a program, and the refusal it earns.
#
# It does not begin with `zerg`, and that is deliberate. docs-zerg refuses any fence whose
# language starts with `zerg` and is not exactly `zerg`, so that ` ```zerg ignore ` cannot be
# invented later; its comment gives the way out in the same breath — "the only way to say
# 'this cannot be built' is to stop calling it Zerg". This stops calling it Zerg and says
# something stronger than `text` in its place.
#
# WHAT IT WAS WRITTEN FOR. `docs/core/specs.md` carried, for two releases, a `[not yet]` whose
# four sentences were each false: it said `impl Ix[int] for C` beside `impl Ix[str] for C` is
# _E4025 `C` declares `ix` twice_. Both impls declare; the failure is at the CALL and it is
# E3154. Every gate passed it — `marker-codes` because E4025 still exists, `chapter-codes`
# because the inventory agreed with the catalogue, `docs-mirror` because the translation said
# the same wrong thing. Nothing ran the program, because the program was prose. A sample under
# that marker would have failed the moment somebody wrote it.
#
# THE CODE IS ON THE FENCE, not read out of the marker's paragraph. A block that carries its
# own expectation can be moved, quoted or copied without the claim coming apart from it, and
# the gate needs no notion of which marker a block belongs to.
#
# NO OPT-OUT, for docs-zerg's reason and in its shape: a fence whose language begins with
# `refused` and is not exactly `refused <CODE>` is a failure (clause 2), so a decorated one
# cannot be invented without editing this file on purpose.
#
# A FLOOR, like every other gate here: extraction is an `awk` over a fence marker, and a
# pattern that stops matching would report zero blocks and pass. The floor is what makes an
# empty walk a failure rather than a clean board.
#
#   usage: docs-refused.sh [<file.md> …]        (default: every tracked .md)

set -eu

ZERG=${ZERG:-./bin/zerg}
MIN_BLOCKS=${MIN_BLOCKS:-1}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

if [ "$#" -gt 0 ]; then
	pages="$*"
else
	pages=$(git ls-files '*.md')
fi

blocks=0
fail=0

for page in $pages; do
	[ -f "$page" ] || continue

	# 1. every ` ```refused <CODE> ` block, with the line its fence opened on and the code it
	#    claims. Only a fence at the START of a line opens one, for docs-zerg's reason: an
	#    indented fence is inside a list item and belongs to whatever encloses it.
	rm -rf "$tmp/b"
	mkdir -p "$tmp/b"
	awk -v out="$tmp/b" '
		/^```refused E[0-9][0-9][0-9][0-9]$/ {
			open = NR
			code = substr($0, 12)
			f = out "/" NR ".zg"
			printf "" > f
			print NR "\t" code >> (out "/INDEX")
			next
		}
		open && /^```$/ { close(f); open = 0; next }
		open { print >> f }
		END { if (open) printf "UNCLOSED %d\n", open > "/dev/stderr" }
	' "$page" 2>"$tmp/awkerr"

	if [ -s "$tmp/awkerr" ]; then
		printf 'docs-refused: %s — a ```refused fence is never closed (%s)\n' "$page" "$(cat "$tmp/awkerr")" >&2
		fail=$((fail + 1))
	fi

	# 2. NO OPT-OUT. A fence whose language begins with `refused` and is not exactly
	#    `refused <CODE>` would sail past clause 1 by never matching it.
	if grep -nE '^```refused' "$page" | grep -vE '^[0-9]+:```refused E[0-9]{4}$' >"$tmp/bad"; then
		while IFS= read -r hit; do
			printf 'docs-refused: %s:%s — a ```refused fence carries one error code and nothing else\n' \
				"$page" "${hit%%:*}" >&2
			fail=$((fail + 1))
		done <"$tmp/bad"
	fi

	[ -f "$tmp/b/INDEX" ] || continue

	while IFS=$(printf '\t') read -r line code; do
		blocks=$((blocks + 1))
		src="$tmp/b/$line.zg"

		# `--emit check` is the walk that reports without writing C, which is what docs-zerg
		# uses for the other half of this pair. A refusal is what we want, so a SUCCESS is
		# the failure here.
		if "$ZERG" build --emit check "$src" >"$tmp/out" 2>&1; then
			printf 'docs-refused: %s:%s — claims %s and the compiler accepts the program\n' \
				"$page" "$line" "$code" >&2
			fail=$((fail + 1))
			continue
		fi

		got=$(grep -oE 'E[0-9]{4}' "$tmp/out" | head -1)
		if [ "$got" != "$code" ]; then
			printf 'docs-refused: %s:%s — claims %s, the compiler says %s\n' \
				"$page" "$line" "$code" "${got:-nothing}" >&2
			printf '          %s\n' "$(head -1 "$tmp/out" | cut -c1-140)" >&2
			fail=$((fail + 1))
		fi
	done <"$tmp/b/INDEX"
done

if [ "$blocks" -lt "$MIN_BLOCKS" ]; then
	printf 'docs-refused: %d blocks is under the floor of %d — the fence pattern matched almost nothing\n' \
		"$blocks" "$MIN_BLOCKS" >&2
	exit 1
fi

if [ "$fail" -gt 0 ]; then
	printf 'docs-refused: %d claim(s) the compiler does not make\n' "$fail" >&2
	exit 1
fi

printf 'docs-refused: %d sample(s) under a marker, each refused with the code its fence claims\n' "$blocks"
