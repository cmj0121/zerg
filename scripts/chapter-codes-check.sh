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
# THE EXCEPTIONS ARE A FILE AND NOT A THRESHOLD. `docs/.codes-not-a-form` holds the codes in
# this range that describe a program the language REJECTS rather than a form it is waiting for,
# and its header says why that is a question about the range rather than about the codes. A
# count would have let the set drift; a list makes every member answerable by name.
set -eu

CAT=${CAT:-docs/tooling/diagnostics.md}
SKIP=${SKIP:-docs/.codes-not-a-form}
fail=0

[ -f "$CAT" ] || { echo "chapter-codes-check: no catalogue at $CAT" >&2; exit 1; }
[ -f "$SKIP" ] || { echo "chapter-codes-check: no exception list at $SKIP" >&2; exit 1; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# every E9xxx the catalogue lists, and every one the exception list excuses
grep -oE '^\| `E9[0-9]{3}` \|' "$CAT" | grep -oE 'E9[0-9]{3}' | sort -u >"$tmp/all"
grep -vE '^#' "$SKIP" | grep -oE '^E9[0-9]{3}' | sort -u >"$tmp/skip"

# THE CHAPTERS ARE EVERY `.md` UNDER docs/ EXCEPT THE CATALOGUE ITSELF and its translation:
# a code quoted only where every code is quoted is a code no chapter names.
find docs -name '*.md' ! -name 'diagnostics*.md' -print0 >"$tmp/chapters" 2>/dev/null || true

n=0
while read -r code; do
	n=$((n + 1))
	if xargs -0 grep -qlF "$code" <"$tmp/chapters" >/dev/null 2>&1; then continue; fi
	grep -qE "^$code	" "$SKIP" && continue
	echo "UNNAMED   $code is an unbuilt form and no chapter names it — quote it in the marker beside the form, or list it in $SKIP" >&2
	fail=1
done <"$tmp/all"

# AND THE EXCEPTION LIST MUST STILL BE HONEST: an entry whose code has since been quoted in a
# chapter, or retired from the catalogue, is an entry that must come off — or the list becomes
# a place codes go to stop being counted.
while read -r code; do
	grep -qx "$code" "$tmp/all" ||
		{ echo "STALE     $code is on $SKIP and the catalogue no longer lists it" >&2; fail=1; }
	if xargs -0 grep -qlF "$code" <"$tmp/chapters" >/dev/null 2>&1; then
		echo "COVERED   $code is on $SKIP and a chapter names it after all — take it off" >&2
		fail=1
	fi
done <"$tmp/skip"

skipped=$(grep -c . <"$tmp/skip")
[ "$n" -ge 90 ] || { echo "EMPTY     only $n codes were read from $CAT — the extraction has gone stale" >&2; fail=1; }

[ "$fail" -eq 0 ] || { echo "chapter-codes-check: an unbuilt form is not named where its readers are" >&2; exit 1; }
echo "chapter-codes-check: $n unbuilt-form codes, each named in a chapter or answered for by name in $SKIP ($skipped)"
