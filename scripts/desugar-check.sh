#!/usr/bin/env bash
#
# desugar-check — a program and the same program with its sugar undone must do the same thing.
#
# GRAMMAR defines several surface forms AS something else: `return x if c` is `if c { return
# x }`, `for c { … }` is `for { if not (c) { break } … }`, `for i in a..b { … }` is that with
# a counter. Every one of those definitions was an assertion nothing checked. The compiler
# lowers the sugar directly — `c_return_if`, `c_forrange` — so the core form it is defined as
# goes down a DIFFERENT path in the emitter, and the two paths were never compared.
#
# They diverge in exactly the way that is hardest to see: both programs compile, both run,
# and one of them is wrong. A `continue` that skips a step, a bound evaluated twice, a
# teardown registered on one path and not the other — none of that fails a build.
#
# So: desugar a copy of each program, build both, run both, compare what they print and what
# they exit with. `zerg desugar` is the transform under test and the corpus is the input to
# it, which means this gate grows every time a case is added to the corpus rather than every
# time somebody remembers to extend it.
#
# THE C IS COMPARED TOO, where the claim holds. D101 is the one rule whose output the emitter
# reaches by the same route as its input — four of the five postfix guards are desugared in
# the PARSER — so a program whose only sugar is a guard must emit byte-identical C. The other
# two rules produce a `for (;;)` where the sugar produced a `while` or a counted `for`, which
# is the same program and not the same text. A file opts into the stronger check by being one
# the weaker rules do not touch, which is asked rather than declared: desugar it with D101
# switched off, and if nothing changed, D101 was the only rule that fired.
#
# The floor at the bottom is the point of the whole script, as it is in oracle-check: every
# assertion here is of the form "these two agree", which is trivially true of an empty set.

set -u

ZERG=${ZERG:-./bin/zerg}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

same=0
skip=0
samec=0
fail=0

# Each source is built from a COPY OF ITS WHOLE DIRECTORY, desugared in place. A program is
# not always one file — `import` resolves against the source's own directory — so desugaring
# the entry alone would build a core-form program against sugared units and compare the two
# halves of nothing. One copy per directory, made once.
copied=""
for src in "$@"; do
	dir=$(dirname "$src")
	slug=$(echo "$dir" | sed 's|^\./||; s|/|_|g; s|^\.$|root|')
	case " $copied " in
	*" $slug "*) continue ;;
	esac
	copied="$copied $slug"

	mkdir -p "$tmp/$slug"
	cp -R "$dir/." "$tmp/$slug/" 2>/dev/null || true

	# a case the transform cannot read is a finding, not a skip: `zerg desugar` refuses a
	# source whose brackets do not close and says so, and the corpus holds no such program
	if ! err=$("$ZERG" desugar "$tmp/$slug"/*.zg 2>&1 >/dev/null); then
		echo "DESUGAR   $dir — the transform refused a source in this directory"
		echo "  $(echo "$err" | head -1)"
		fail=$((fail + 1))
	fi

	# every rule's output must be its own fixpoint. A rule that produced what another rule
	# rewrites would be a pass whose answer depends on how many times it is run, and no
	# program's behaviour would change to say so.
	if ! err=$("$ZERG" desugar --check "$tmp/$slug"/*.zg 2>&1 >/dev/null); then
		echo "FIXPOINT  $dir — a desugared source still holds sugar"
		echo "  $(echo "$err" | head -1)"
		fail=$((fail + 1))
	fi
done

for src in "$@"; do
	dir=$(dirname "$src")
	slug=$(echo "$dir" | sed 's|^\./||; s|/|_|g; s|^\.$|root|')
	core="$tmp/$slug/$(basename "$src")"
	name=$(echo "$src" | sed 's|^\./||; s|/|_|g; s|\.zg$||')

	# a program the compiler cannot build as written is not this gate's finding — the corpus
	# carries cases waiting on features, and `make corpus` is what accounts for them
	if ! "$ZERG" build --emit bin -o "$tmp/$name.sugar" "$src" >/dev/null 2>&1; then
		skip=$((skip + 1))
		continue
	fi

	if ! err=$("$ZERG" build --emit bin -o "$tmp/$name.core" "$core" 2>&1 >/dev/null); then
		echo "BUILD     $src — it builds as written and not with its sugar undone"
		echo "  $(echo "$err" | head -1)"
		fail=$((fail + 1))
		continue
	fi

	out0=$("$tmp/$name.sugar" 2>/dev/null)
	rc0=$?
	out1=$("$tmp/$name.core" 2>/dev/null)
	rc1=$?

	if [ "$out0" != "$out1" ] || [ "$rc0" -ne "$rc1" ]; then
		echo "DIFFER    $src — the sugar and the core form do not do the same thing"
		echo "  as written (rc $rc0): $(echo "$out0" | head -3 | tr '\n' '|')"
		echo "  desugared  (rc $rc1): $(echo "$out1" | head -3 | tr '\n' '|')"
		fail=$((fail + 1))
		continue
	fi
	same=$((same + 1))

	# --- the stronger claim, for the files it holds for ------------------------------
	#
	# Unchanged means there was no sugar to undo and nothing to compare. Changed by D101
	# alone means the emitter must have taken the same route through both, so the C is
	# compared as text — with the `#line` directives dropped, since the two sources say the
	# same thing on different lines and out of different files, which is the one difference
	# that is not a finding.
	cmp -s "$src" "$core" && continue
	cp "$src" "$tmp/$name.probe.zg"
	"$ZERG" desugar --off D101 "$tmp/$name.probe.zg" >/dev/null 2>&1
	cmp -s "$src" "$tmp/$name.probe.zg" || continue

	"$ZERG" build --emit c "$src" 2>/dev/null | grep -v -e '^#line' -e '^$' >"$tmp/$name.sugar.c"
	"$ZERG" build --emit c "$core" 2>/dev/null | grep -v -e '^#line' -e '^$' >"$tmp/$name.core.c"
	if ! cmp -s "$tmp/$name.sugar.c" "$tmp/$name.core.c"; then
		echo "EMIT      $src — only D101 fired, so the emitted C must be identical, and it is not"
		diff "$tmp/$name.sugar.c" "$tmp/$name.core.c" | head -6
		fail=$((fail + 1))
		continue
	fi
	samec=$((samec + 1))
done

if [ $fail -ne 0 ]; then
	echo "desugar-check: $fail program(s) do not survive having their sugar undone"
	exit 1
fi

if [ "$same" -lt "${MIN_COMPARED:-8}" ]; then
	echo "desugar-check: only $same programs were comparable — the list is empty, or nothing builds"
	exit 1
fi
echo "desugar-check: $same programs do the same thing with their sugar undone ($skip not built as written)"
echo "desugar-check: $samec of them emit byte-identical C, D101 being the only rule that fired"
