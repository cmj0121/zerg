#!/usr/bin/env bash
#
# test-runner-check — the runner can see a test that fails.
#
# A TEST RUNNER THAT CANNOT DETECT A FAILING TEST IS THE ULTIMATE GATE THAT MEASURES
# NOTHING. Every other gate in this repository asks the compiler a question; this one asks
# the thing that will be asking the questions from now on, and it has to be asked in the one
# direction that is hard to fake: a run must go RED for a test that does not hold, and it
# must go red for each of the four ways a test can fail to hold, not just the tidy one.
#
# So the fixtures are chosen by FAILURE MODE rather than by feature:
#
#   pass    a test that holds                  reported `ok`, and the run exits 0
#   fail    an assertion that does not hold    reported FAIL with the message, exit non-zero
#   crash   a test that overflows its stack    reported CRASH — AND THE TESTS AFTER IT RUN,
#                                              which is the whole reason for one process each
#   exit    a test that calls `os.exit(0)`     NOT counted as passing: it did not finish, and
#                                              its exit status is the one a passing test has
#   none    a directory with no test files     exit 0, and it SAYS so rather than printing
#                                              nothing — a silent run that found nothing is
#                                              indistinguishable from one where all passed
#
# The fixtures live here rather than in the tree for the reason refuse-check's cases do: a
# package checked into the repository is a package every other gate then has to know about —
# `fmt-self` would format it, `treesitter` would parse it, `examples` would try to run it —
# and a fixture whose whole point is that it FAILS has no business on any of those lists.
#
# A FLOOR under the assertion count, for the reason every negative assertion here has one: a
# script that stops making assertions makes none of them fail.
set -u

ZERG=${ZERG:-./bin/zerg}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

pass=0
fail=0

# say <what> <status> — record one assertion. The status is the shell's own, so a claim is
# written as the command that decides it followed by `say "..." $?`.
say() {
	if [ "$2" -eq 0 ]; then
		pass=$((pass + 1))
	else
		printf 'test-runner: %s\n' "$1" >&2
		fail=$((fail + 1))
	fi
}

# --- the fixture package ---------------------------------------------------------------

mkdir -p "$tmp/pkg/lib" "$tmp/pkg/edge" "$tmp/none/quiet"

# lib — the ordinary pair. `twice` is module-private and the test calls it with no import and
# no `pub`, which is the white-box position docs/runtime/package.md describes: a test file is
# a file OF the module, so there is nothing to grant it.
cat >"$tmp/pkg/lib/lib.zg" <<'EOF'
fn twice(n: int) -> int {
	return n * 2
}

pub fn thrice(n: int) -> int {
	return twice(n) + n
}
EOF

cat >"$tmp/pkg/lib/lib_test.zg" <<'EOF'
import (
	"testing"
)

#[test]
fn test_twice_holds() {
	testing.assert_eq(twice(21), 42)
}

#[test]
fn test_thrice_does_not() {
	testing.assert_eq(thrice(2), 7)
}
EOF

# edge — the three that are not a clean pass or a clean failure. The ORDER matters and is the
# assertion: the crash is FIRST, so `test_c_after_the_crash` reporting `ok` is the proof that
# one process per test is real rather than a paragraph.
cat >"$tmp/pkg/edge/edge.zg" <<'EOF'
fn deep(n: int) -> int {
	return n if n == 0

	return deep(n + 1) + 1
}

pub fn plunge() -> int {
	return deep(1)
}
EOF

cat >"$tmp/pkg/edge/edge_test.zg" <<'EOF'
import (
	"os"
	"testing"
)

#[test]
fn test_a_overflows_the_stack() {
	print plunge()
}

#[test]
fn test_b_exits_early() {
	os.exit(0)
	print 1
}

#[test]
fn test_c_after_the_crash() {
	testing.assert(true)
}
EOF

# none — a module with sources and no test file at all.
cat >"$tmp/none/quiet/quiet.zg" <<'EOF'
pub fn one() -> int {
	return 1
}
EOF

# --- the run ------------------------------------------------------------------------------

before=$(ls "$tmp/pkg/lib" "$tmp/pkg/edge")

out=$("$ZERG" test "$tmp/pkg" 2>&1)
status=$?
printf '%s\n' "$out" >"$tmp/out"

[ "$status" -ne 0 ]
say "a run holding a failing test exited 0" $?

# 1. the passing test
grep -qE '^  ok    test_twice_holds$' "$tmp/out"
say "the passing test is not reported \`ok\`" $?

# 2. the failing assertion — the verdict AND the message, because a FAIL that does not say
#    what went wrong sends the reader back to the source to guess.
grep -qE '^  FAIL  test_thrice_does_not$' "$tmp/out"
say "the failing assertion is not reported FAIL" $?

grep -qF 'assert_eq failed' "$tmp/out"
say "the failure does not carry the assertion's message" $?

# 3. the crash, and the tests after it
grep -qE '^  CRASH test_a_overflows_the_stack$' "$tmp/out"
say "a test that overflows its stack is not reported as a crash" $?

grep -qE '^  ok    test_c_after_the_crash$' "$tmp/out"
say "a test declared after the crashing one did not run — one process per test is not real" $?

# 4. `os.exit(0)` is not a pass. Asserted as the ABSENCE of an ok line as well as the
#    presence of a FAIL one: the failure this fixture exists for is the runner reading the
#    child's status, and a status of 0 is what it would have read.
grep -qE '^  ok    test_b_exits_early$' "$tmp/out"
[ $? -ne 0 ]
say "a test that called \`os.exit(0)\` was counted as passing" $?

grep -qE '^  FAIL  test_b_exits_early$' "$tmp/out"
say "a test that exited early is not reported as a failure" $?

# 5. the counts, and the grouping. The summary is the line a person reads, and it is the one
#    place the whole run is stated in numbers: two of the five hold, three do not.
grep -qE '^2 passed, 3 failed$' "$tmp/out"
say "the summary does not count 2 passed and 3 failed" $?

grep -qF 'lib/lib_test.zg' "$tmp/out"
say "the report does not name the file its tests came from" $?

# 6. nothing left behind. The driver is written INTO the package — that is what puts it in
#    the module's scope — so a run that does not take it away leaves a `fn main` in somebody's
#    source directory.
after=$(ls "$tmp/pkg/lib" "$tmp/pkg/edge")
[ "$before" = "$after" ]
say "the run left a generated file behind in the package" $?

# 7. a directory with no test files: exit 0, and NOT silence.
none=$("$ZERG" test "$tmp/none" 2>&1)
status=$?

[ "$status" -eq 0 ]
say "a tree with no test files did not exit 0" $?

printf '%s\n' "$none" | grep -qF 'no tests'
say "a tree with no test files said nothing — a silent run is indistinguishable from one where everything passed" $?

# --- the floor -----------------------------------------------------------------------------
#
# 13 assertions today. The floor is what keeps this from reporting success after a rewrite
# that stops asserting — the failure every gate here is written against, one level up.
MIN_ASSERTS=${MIN_ASSERTS:-13}
total=$((pass + fail))
if [ "$total" -lt "$MIN_ASSERTS" ]; then
	printf 'test-runner-check: %s assertions were made, below the floor of %s — the gate did not run itself\n' \
		"$total" "$MIN_ASSERTS" >&2
	exit 1
fi

if [ "$fail" -ne 0 ]; then
	printf 'test-runner-check: %s of %s assertions failed\n' "$fail" "$total" >&2
	printf '%s\n' "$out" >&2
	exit 1
fi

printf 'test-runner-check: %s assertions — a failing test, a crash, an early exit and an empty tree are each seen\n' "$total"
