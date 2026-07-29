#!/usr/bin/env bash
#
# selfhost-fixpoint.sh — the compiler compiles itself, and gets the same answer twice.
#
# `make build` already builds two stages: the Go seed builds an intermediate compiler, and
# that intermediate builds the one that ships. What it has never done is COMPARE them, so a
# codegen change that makes the compiler emit subtly different C for its own source is
# invisible — the build is green, the shipped binary passes every corpus case, and the
# compiler has quietly stopped being a fixpoint of itself. The corpus cannot see it either:
# every case there is a small program, and the one program large enough to catch a rare
# emitter path is the compiler.
#
# This script is that comparison. The seed builds stage1; stage1 emits the compiler's C and
# also builds stage2 from it; stage2 emits the compiler's C again. Those two emissions must
# be byte-identical. Once they are, the chain has converged and there is nothing left to
# check: a stage3 built from stage2's emission would be the same program as stage2, so it
# would emit the same C in turn. That is why this stops at two generations where the old
# M5 script went to three — the third generation restates the second rather than testing it.
#
# Nothing here is written into bin/. Every stage lands in a temp directory of its own, with
# its own build cache, so a fixpoint run can neither disturb nor be disturbed by a
# `make build` happening beside it.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

# The compiler's own source set is deliberately NOT listed here. Both compilers resolve
# `import` for themselves, so naming the entry file names the whole program — and a list
# written down instead is a thing that goes stale in silence. The previous version of this
# script still named a five-file compiler long after fmt.zg and lint.zg had joined it, and
# still linked a runtime that had neither sched.c nor chan.c in it.
ENTRY="${ZERG_ENTRY:-src/compiler/zergc.zg}"
JOBS="${JOBS:-4}"

WORK="$(mktemp -d "${TMPDIR:-/tmp}/zerg-fixpoint.XXXXXX")" || exit 2
export ZERG_CACHE="$WORK/cache"

# A gate is only worth having if a failure says where to look, so every way out of this
# script that is not success comes through here and leaves the stages behind to read.
die() {
	printf '\nfixpoint: %s\n' "$1" >&2
	printf 'fixpoint: the stages are kept in %s\n' "$WORK" >&2
	exit 1
}

size() { wc -c <"$1" | tr -d ' '; }

# Every stage runs quiet, into a log. A stage that fails on emitted C makes `cc` print
# thousands of lines, and the one worth reading is the FIRST — everything after it is the
# same mistake restated about a file nobody wrote by hand. The log is kept either way.
build_step() {
	local what="$1" log="$WORK/$2"
	shift 2
	if "$@" >"$log" 2>&1; then
		return
	fi
	printf '\n--- the first 15 lines of %s\n' "$log" >&2
	head -15 "$log" >&2
	die "$what"
}

# The emitting stages are the same idea, except their whole point is the C on stdout, so
# only the chatter goes to the log.
emit_step() {
	local what="$1" out="$WORK/$2" log="$WORK/$3"
	shift 3
	if ! "$@" >"$out" 2>"$log"; then
		printf '\n--- the first 15 lines of %s\n' "$log" >&2
		head -15 "$log" >&2
		die "$what"
	fi
	[ -s "$out" ] || die "$what — it emitted nothing at all"
}

# stage0 is the Go seed. It is built here rather than taken from bin/ for two reasons: the
# check should say what THIS tree does, not what someone last left in bin/, and writing to
# bin/ would replace a binary another build may be running at that moment.
printf 'stage0: building the Go seed\n'
build_step "the Go seed does not build" stage0.log \
	go build -C src/bootstrap -o "$WORK/zerg0" ./cmd/zerg

printf 'stage1: the seed builds a compiler from %s\n' "$ENTRY"
build_step "the seed could not build stage1" stage1-build.log \
	"$WORK/zerg0" build "$ENTRY" -o "$WORK/stage1"

printf "stage1: emitting the compiler's own C\n"
emit_step "stage1 could not emit its own source" stage1.c stage1-emit.log \
	"$WORK/stage1" build --emit c "$ENTRY"

printf 'stage2: stage1 builds the compiler again, this time with itself\n'
build_step "stage1 could not build stage2 — its own C did not compile" stage2-build.log \
	"$WORK/stage1" build --emit bin -j "$JOBS" -o "$WORK/stage2" "$ENTRY"

printf "stage2: emitting the compiler's own C\n"
emit_step "stage2 could not emit its own source" stage2.c stage2-emit.log \
	"$WORK/stage2" build --emit c "$ENTRY"

if cmp -s "$WORK/stage1.c" "$WORK/stage2.c"; then
	printf '\nfixpoint: stage1 and stage2 emit the same %s bytes of C — the compiler is a fixpoint\n' "$(size "$WORK/stage1.c")"
	rm -rf "$WORK"
	exit 0
fi

# The whole point of failing here is the next person's afternoon, so say WHERE it diverged.
# `cmp` gives the first differing byte and line, which is the one place worth looking at
# first; the diff after it shows the shape of the divergence — one renamed temporary reads
# very differently from an emitter that has started reordering its output.
printf '\nfixpoint: BROKEN — stage1 and stage2 emit different C\n' >&2
printf '  stage1.c   %s bytes\n' "$(size "$WORK/stage1.c")" >&2
printf '  stage2.c   %s bytes\n' "$(size "$WORK/stage2.c")" >&2
printf '  %s\n' "$(cmp "$WORK/stage1.c" "$WORK/stage2.c" 2>&1 | sed 's/^[^ ]* [^ ]* //')" >&2
printf '  %s lines differ, of %s\n' \
	"$(diff "$WORK/stage1.c" "$WORK/stage2.c" | grep -c '^[<>]')" "$(wc -l <"$WORK/stage1.c" | tr -d ' ')" >&2
printf '\n--- stage1.c\n+++ stage2.c (first 40 lines of the difference)\n' >&2
diff -u "$WORK/stage1.c" "$WORK/stage2.c" | tail -n +3 | head -40 >&2

die "the compiler no longer reproduces its own output"
