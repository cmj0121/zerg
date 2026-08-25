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
RULES=${RULES:-src/compiler/zerg/rule.zg}
fail=0

[ -f "$CAT" ] || { echo "chapter-codes-check: no catalogue at $CAT" >&2; exit 1; }
[ -f "$RULES" ] || { echo "chapter-codes-check: no rule registry at $RULES" >&2; exit 1; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# EVERY E9xxx THE CATALOGUE LISTS AS LIVE, which is every one above `### Retired codes`. The
# retired table has the same row shape — `E9088`, then `E3125`, then the reason — so reading
# the whole file asks a chapter to carry a marker for a number that no longer names anything,
# and the only way to satisfy that is to write a marker that is false. #74 retired seventeen
# of these and that is what said so; `error-codes-check` already splits on the same heading.
sed -n '1,/^### Retired codes/p' "$CAT" |
	grep -oE '^\| `E9[0-9]{3}` \|' | grep -oE 'E9[0-9]{3}' | sort -u >"$tmp/all"

# THE CHAPTERS ARE EVERY `.md` UNDER docs/ EXCEPT THE CATALOGUE ITSELF and its translation:
# a code quoted only where every code is quoted is a code no chapter names.
find docs -name '*.md' ! -name 'diagnostics*.md' -print0 >"$tmp/chapters"

# AND THE CHAPTER LIST HAS A GUARD OF ITS OWN, which is the half the derived count at the
# bottom cannot reach. Every comparison below is against `$tmp/all`; the chapters are read by
# `xargs -0 grep`, and BSD xargs handed NO input exits 0 WITHOUT RUNNING grep — so an empty
# list makes the `if` below true for every code, `n` counts on unchanged, the derived count
# still matches, and this gate prints its success line having read no chapter at all.
#
# Measured on macOS: pointed at an empty directory the old code printed `90 unbuilt-form
# codes, each named in a chapter` and exited 0. GNU xargs runs grep once on empty input and
# would have gone red, so the defect was green here and red in CI — the worse of the two ways
# to be wrong, because the machine that reports it is not the machine that can debug it. The
# `2>/dev/null || true` that used to close this line is gone with it: a `find` that fails is a
# gate that cannot run, not a gate with nothing to say.
[ -s "$tmp/chapters" ] || { echo "chapter-codes-check: no chapter was read from docs/ — the extraction has gone stale" >&2; exit 1; }

n=0
while read -r code; do
	n=$((n + 1))
	if xargs -0 grep -qlF "$code" <"$tmp/chapters" >/dev/null 2>&1; then continue; fi
	echo "UNNAMED   $code is an unbuilt form and no chapter names it — quote it in the marker beside the form" >&2
	fail=1
done <"$tmp/all"

# THE EXTRACTION'S OWN GUARD, and it is not a number. Every comparison above is against
# `$tmp/all`, so an extraction that stops matching empties it and turns this gate silently
# green — which is what the guard is for. It was a hand-set floor, 90 and then 80, and both
# times the count moved underneath it: #74 took seventeen codes out of the range in one span,
# and this repo has merged a span that BUILT sixteen forms in one go. A floor set against
# today's count is a number that blames its own extraction the day somebody does correct work
# at scale, which is the same defect the two corrections above were for.
#
# So it is derived. `rule.zg` declares every code exactly once and its discriminant IS the
# number, so the live `E9xxx` rows the catalogue carries are the `= 9xxx` variants the compiler
# has — not approximately, exactly. Comparing the two says the extraction read the whole
# catalogue AND that the catalogue is the registry, and it tracks whatever either becomes.
#
# IT IS A BRANCH INVARIANT, NOT A PER-COMMIT ONE, and that is deliberate. Under this repo's
# commit order — feat, then test, then build, then doc — the feat commit adds a code to
# `rule.zg` and the doc commit catalogues it, so between them the two counts differ by exactly
# the codes in flight and this gate reads STALE for correct work. Measured on this branch:
# `24a3bd91` reads 89 against 90. Nothing is wrong at that commit and nothing here should be
# loosened to hide it — a per-commit CI run or a bisect will meet it, and what is owed is this
# paragraph rather than a weaker assertion. The invariant holds where it is checked: at the
# head of a branch, and at every merge.
declared=$(grep -cE '^	[A-Za-z_][A-Za-z_0-9]* = 9[0-9]{3}$' "$RULES")
[ "$n" -eq "$declared" ] || { echo "STALE     $n codes were read from $CAT and $RULES declares $declared — one of the two has gone stale" >&2; fail=1; }

[ "$fail" -eq 0 ] || { echo "chapter-codes-check: an unbuilt form is not named where its readers are" >&2; exit 1; }
echo "chapter-codes-check: $n unbuilt-form codes, each named in a chapter"
