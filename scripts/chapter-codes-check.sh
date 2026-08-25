#!/bin/sh
# chapter-codes-check — every unbuilt form is named where its readers are (#74).
#
# `E9xxx` is the NotImplemented range: a form the LANGUAGE has and this compiler does not
# build. A reader meets that fact in one of two places — the chapter that describes the form,
# or the compiler's own refusal — and only the first is somewhere they look BEFORE writing the
# program. `docs/tooling/diagnostics.md` lists every code and is a catalogue, not a chapter: a
# reader consults it after being refused, which is too late to have chosen differently.
#
# So each code must be QUOTED in a chapter. Quoted, not merely covered: the marker beside a
# form is what a reader acts on, and `specs.md` already shows the shape —
#
#   > **[not yet]** An `impl` whose target carries type arguments is _E9038 NotImplemented: …_
#
# — which is a marker a reader can carry to the compiler's output and back.
#
# THERE ARE NO EXCEPTIONS. There were twenty, in `docs/.codes-not-a-form`: codes in this range
# that described a program the language REJECTS rather than a form it is waiting for, held on a
# list because moving them out of `E9xxx` spends numbers this project retires rather than
# reuses. #74 spent them. Every code left in the range is a form, so every code left in the
# range owes a chapter marker, and the escape hatch is gone rather than emptied — an exception
# list with nothing legitimate on it is where the next code goes to stop being counted.
set -eu

CAT=${CAT:-docs/tooling/diagnostics.md}
fail=0

[ -f "$CAT" ] || { echo "chapter-codes-check: no catalogue at $CAT" >&2; exit 1; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# every E9xxx the catalogue lists
grep -oE '^\| `E9[0-9]{3}` \|' "$CAT" | grep -oE 'E9[0-9]{3}' | sort -u >"$tmp/all"

# THE CHAPTERS ARE EVERY `.md` UNDER docs/ EXCEPT THE CATALOGUE ITSELF and its translation:
# a code quoted only where every code is quoted is a code no chapter names.
find docs -name '*.md' ! -name 'diagnostics*.md' -print0 >"$tmp/chapters" 2>/dev/null || true

n=0
while read -r code; do
	n=$((n + 1))
	if xargs -0 grep -qlF "$code" <"$tmp/chapters" >/dev/null 2>&1; then continue; fi
	echo "UNNAMED   $code is an unbuilt form and no chapter names it — quote it in the marker beside the form" >&2
	fail=1
done <"$tmp/all"

# THE FLOOR IS 80, and it moved: it was 90 against the 106 codes in the range before #74 took
# seventeen of them out of it, which is a number the change itself would have tripped. It is
# here for one job — every comparison above is against `$tmp/all`, and an extraction that stops
# matching empties it and turns this gate silently green.
[ "$n" -ge 80 ] || { echo "EMPTY     only $n codes were read from $CAT — the extraction has gone stale" >&2; fail=1; }

[ "$fail" -eq 0 ] || { echo "chapter-codes-check: an unbuilt form is not named where its readers are" >&2; exit 1; }
echo "chapter-codes-check: $n unbuilt-form codes, each named in a chapter"
