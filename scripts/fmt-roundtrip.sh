#!/usr/bin/env bash
#
# fmt-roundtrip — WHAT THE FORMATTER WRITES, THE PARSER READS.
#
# The property, in one line: for every source the formatter accepts, the formatted result
# must PARSE, and formatting it a second time must change nothing.
#
# It is the one thing a formatter cannot be allowed to get wrong, and nothing else here was
# asking. `fmt-corpus` asks whether a case is ALREADY canonical, which a rule that never
# fires on it passes; `fmt-self` asks the same of the compiler's own sources; `fmt-tokens`
# asks that spacing rules do not move a token, and turns the F4xx rewrites OFF to ask it.
# So the rules that CHANGE THE SHAPE of the code — the ones that can produce a form the
# grammar does not have — were measured by no gate at all, and F401 went on rewriting
#
#     if s := v { return s }   ->   return s if s := v
#
# which stops at the `:=` with E205, and whose `s` names nothing now that its binding is
# gone. `zerg fmt` destroyed the file, said nothing, exited 0, and `zerg fmt --check` then
# called the wreckage canonical. Three files were lost to it before it was found by hand.
#
# THE PRECONDITION IS THE POINT. It is not "the output parses" flatly — fmt has to work on
# source this compiler cannot read, because that is exactly when a person reaches for it,
# and the parser still refuses a long list of forms the language has and it has not built.
# What fmt owes is that it never SUBTRACTS: a file that came in a program goes out a program.
# So a source that does not parse to begin with is skipped, and the count of what WAS
# measured is checked against a floor, because every skip here is silent.
#
# Two halves, because a Zerg source is parsed two different ways and both need covering:
#
#   1. THE STANDALONE SOURCES — the fmt corpus, the codegen corpus and the numbered
#      examples. Each is a whole program in one file, so each can be parsed on its own, and
#      the probe is `zerg build --emit ast` on a COPY: an independent answer, from the
#      parser itself, not from the formatter's opinion of its own work. This is where the
#      teeth are. The codegen corpus is 171 programs held to canonical form by nothing, so
#      fmt genuinely rewrites them, and a rewrite is the only thing that can be wrong.
#
#   2. THE COMPILER ITSELF — which cannot be done file by file, because a module's file is
#      not an entry point and `--emit ast` on one alone answers E502 about its imports. So
#      the whole of `src/` is copied, every source in the copy is formatted, and the COPY is
#      parsed through ENTRIES that reach it. Nothing in the real tree is written.

set -u

ZERG=${ZERG:-./bin/zerg}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fail=0
n=0

# A FLOOR under how many standalone sources were actually round-tripped, of the kind
# fmt-tokens and reject-fuzz carry. Every `continue` below is silent by design — a source
# that does not parse says nothing about formatting — so a `zerg` that started refusing a
# whole class of source would shrink this number to nothing and still exit 0.
#
# 180 against the 214 measured today: far enough below that moving a case out of the corpus
# is not a chore here, far enough above that a partial or wrong-commit submodule checkout
# cannot pass. A `test-data/` with a handful of cases left in it is the failure that looks
# most like success.
MIN_SOURCES=${MIN_SOURCES:-180}

# --- 1. the standalone sources -------------------------------------------------------------
for src in test-data/fmt/*.zg test-data/codegen/*.zg examples/[0-9][0-9]_*.zg; do
	[ -f "$src" ] || continue

	cp "$src" "$tmp/case.zg"

	# THE PRECONDITION: fmt is only answerable for what was a program on the way in.
	"$ZERG" build --emit ast "$tmp/case.zg" >/dev/null 2>&1 || continue

	# Every rule ON, unlike fmt-tokens. The F4xx rewrites are the whole point here: they are
	# the group allowed to change the token stream, which is what makes them the group able
	# to write a form the grammar does not have.
	if ! "$ZERG" fmt "$tmp/case.zg" >/dev/null 2>&1; then
		printf 'REFUSE %s — fmt would not format a source that parses\n' "$(basename "$src")"
		fail=1
		continue
	fi

	if ! "$ZERG" build --emit ast "$tmp/case.zg" >/dev/null 2>&1; then
		printf 'PARSE  %s — it parsed, and the formatted result does not\n' "$(basename "$src")"
		"$ZERG" build --emit ast "$tmp/case.zg" 2>&1 | head -2 | sed 's/^/       /'
		fail=1
		continue
	fi

	# THE FIXPOINT: formatting twice is formatting once. A rule that alternates between two
	# renderings parses at every step and still makes the tool's output depend on how many
	# times it was run, which no CI can hold anything to.
	cp "$tmp/case.zg" "$tmp/once.zg"
	"$ZERG" fmt "$tmp/case.zg" >/dev/null 2>&1
	if ! cmp -s "$tmp/once.zg" "$tmp/case.zg"; then
		printf 'FIXPT  %s — formatting it twice is not formatting it once\n' "$(basename "$src")"
		diff "$tmp/once.zg" "$tmp/case.zg" | head -6 | sed 's/^/       /'
		fail=1
		continue
	fi

	n=$((n + 1))
done

if [ "$n" -lt "$MIN_SOURCES" ]; then
	echo "fmt-roundtrip: only $n standalone sources were measured, and the floor is $MIN_SOURCES"
	exit 1
fi

# --- 2. the compiler itself ----------------------------------------------------------------
#
# The same three questions, asked once of ~10k lines that are a single program. `cp -R src`
# and not a formatted-in-place tree: this gate runs on a working checkout, alongside other
# people's edits, and a gate that writes to `src/` to measure something is a gate that loses
# somebody's afternoon the one time it is interrupted.
mkdir -p "$tmp/tree"
cp -R src "$tmp/tree/src"

self=$(ls "$tmp"/tree/src/compiler/*.zg "$tmp"/tree/src/compiler/cmd/*.zg \
	"$tmp"/tree/src/compiler/zerg/*.zg "$tmp"/tree/src/compiler/lsp/*.zg \
	"$tmp"/tree/src/stdlib/*.zg 2>/dev/null)

# THE ENTRIES THIS TREE IS PARSED THROUGH. `--emit ast` reads an entry and everything it
# imports, so `zergc.zg` covers the compiler and the five stdlib modules it happens to use —
# and NOTHING ELSE in `src/stdlib`. That gap is not theoretical: the same probe planted in
# `os.zg` went undetected here and in `io.zg` was caught, because one is imported and the
# other is not. So a one-line entry is written for each module and the standard library is
# reached by name rather than by luck.
#
# Each is probed BEFORE anything is formatted, and dropped if it does not parse then — the
# precondition again, one module at a time. `atomic` is the reason: it declares a generic
# struct and refuses to be imported at all (E511), which is a fact about this compiler and
# not about the formatter.
entries=""
for m in "$tmp"/tree/src/stdlib/*.zg; do
	e="$tmp/tree/src/compiler/zz-rt-$(basename "$m" .zg).zg"
	printf 'import "%s"\n\nfn main() {\n\tnop\n}\n' "$(basename "$m" .zg)" >"$e"
	ZERG_ROOT="$tmp/tree" "$ZERG" build --emit ast "$e" >/dev/null 2>&1 || rm -f "$e"
done

for e in "$tmp/tree/src/compiler/zergc.zg" "$tmp"/tree/src/compiler/zz-rt-*.zg; do
	[ -f "$e" ] || continue
	ZERG_ROOT="$tmp/tree" "$ZERG" build --emit ast "$e" >/dev/null 2>&1 && entries="$entries $e"
done

if [ -z "$entries" ]; then
	echo "fmt-roundtrip: nothing in src/ parsed before formatting, so nothing here was measured"
	exit 1
fi

if ! "$ZERG" fmt $self >/dev/null 2>&1; then
	printf 'REFUSE the compiler — fmt would not format its own sources\n'
	"$ZERG" fmt $self 2>&1 | head -3 | sed 's/^/       /'
	fail=1
else
	for entry in $entries; do
		ZERG_ROOT="$tmp/tree" "$ZERG" build --emit ast "$entry" >/dev/null 2>&1 && continue
		printf 'PARSE  %s — it parsed, and the formatted tree does not\n' "$(basename "$entry")"
		ZERG_ROOT="$tmp/tree" "$ZERG" build --emit ast "$entry" 2>&1 | head -3 | sed 's/^/       /'
		fail=1
	done

	# The fixpoint, whole-tree: `zerg fmt` names every file it rewrote, so a second run
	# naming anything at all is a second run that changed something.
	again=$("$ZERG" fmt $self 2>&1)
	if [ -n "$again" ]; then
		printf 'FIXPT  the compiler — formatting it twice is not formatting it once\n'
		echo "$again" | head -3 | sed 's/^/       /'
		fail=1
	fi
fi

if [ $fail -ne 0 ]; then
	echo "fmt-roundtrip: the formatter wrote something the parser cannot read"
	exit 1
fi
echo "fmt-roundtrip: $n standalone sources and the compiler itself survive being formatted"
