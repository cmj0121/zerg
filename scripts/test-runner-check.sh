#!/usr/bin/env bash
#
# test-runner-check — the runner can see a test that fails, on both of the paths it has.
#
# A TEST RUNNER THAT CANNOT DETECT A FAILING TEST IS THE ULTIMATE GATE THAT MEASURES
# NOTHING. Every other gate in this repository asks the compiler a question; this one asks
# the thing that will be asking the questions from now on, and it has to be asked in the one
# direction that is hard to fake: a run must go RED for a test that does not hold, and it
# must go red for each of the ways a test can fail to hold, not just the tidy one.
#
# THERE ARE TWO PATHS AND BOTH ARE MEASURED. `zerg test` runs a package's tests as
# coroutines in one process, and re-runs whatever that process did not get back from one
# process each. The two are chosen by the runner and not by the caller, so a fixture picks
# which one it exercises by picking what its tests DO:
#
#   the spawn path — a `guard` around a coroutine contains it
#     pass      a test that holds                 reported `ok`
#     fail      an assertion that does not hold   reported FAIL with the message
#     skip      `ctx.skip(reason)`                reported SKIP with the reason, counted apart
#     abort     an uncaught `IndexError`          reported FAIL — and the tests after it run,
#                                                 in the SAME process, which is the whole
#                                                 claim: no NOTE line for that file
#
#   the fallback path — the process itself went, so a process each is the only way back
#     crash     a test that overflows its stack   reported CRASH, attributed to that test
#     exit      a test that calls `os.exit(0)`    NOT counted as passing: it did not finish,
#                                                 and its exit status is a passing test's
#     after     a test declared after both        still runs, and reports `ok`
#     said      the report SAYS the strategy changed — a run that quietly ran half of itself
#               another way is a run whose result nobody can interpret
#
#   none    a directory with no test files     exit 0, and it SAYS so rather than printing
#                                              nothing — a silent run that found nothing is
#                                              indistinguishable from one where all passed
#   skips   a package that only passes and skips   exit 0: a skip is not a failure
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

mkdir -p "$tmp/pkg/lib" "$tmp/pkg/edge" "$tmp/none/quiet" "$tmp/skips/plat"

# lib — everything the in-process path is supposed to contain. `twice` is module-private and
# the test calls it with no import and no `pub`, which is the white-box position
# docs/runtime/package.md describes: a test file is a file OF the module, so there is nothing
# to grant it.
cat >"$tmp/pkg/lib/lib.zg" <<'EOF'
fn twice(n: int) -> int {
	return n * 2
}

pub fn thrice(n: int) -> int {
	return twice(n) + n
}
EOF

# THE PARAMETER LIST IS PART OF THE FIXTURE. `test_twice_holds` takes no parameter at all and
# `test_knows_its_name` takes a context, in one file, because the runner writes the call from
# the SIGNATURE — a runner that got that wrong would fail to compile one of the two.
#
# `test_reads_off_the_end` is second-to-last on purpose: an uncaught abort inside a coroutine
# is caught by the `guard` around it, so the test after it runs in the SAME process, and the
# absence of a NOTE line for this file is what says so.
cat >"$tmp/pkg/lib/lib_test.zg" <<'EOF'
import (
	"testing"
)

#[test]
fn test_twice_holds(ctx: testing.Context) {
	ctx.log("a note from a test that passes")
	testing.assert_eq(twice(21), 42)
}

#[test]
fn test_knows_its_name(ctx: testing.Context) {
	testing.assert_eq(ctx.name(), "test_knows_its_name")
}

#[test]
fn test_thrice_does_not(ctx: testing.Context) {
	ctx.log("thrice(2) is twice(2) + 2")
	testing.assert_eq(thrice(2), 7)
}

#[test]
fn test_not_on_this_platform(ctx: testing.Context) {
	ctx.skip("not on this platform")
	testing.assert(false)
}

#[test]
fn test_reads_off_the_end() {
	xs: list[int] = []
	print xs[9]
}

#[test]
fn test_after_the_abort() {
	testing.assert(true)
}
EOF

# edge — the two that no `guard` and no coroutine can contain, because they end the PROCESS.
# The ORDER matters and is the assertion: the stack overflow is FIRST, so `test_c_after_the_end`
# reporting `ok` is the proof that the fallback re-ran the remainder rather than giving up at
# the first death.
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
fn test_c_after_the_end() {
	testing.assert(true)
}
EOF

# none — a module with sources and no test file at all.
cat >"$tmp/none/quiet/quiet.zg" <<'EOF'
pub fn one() -> int {
	return 1
}
EOF

# skips — a package where nothing fails and something did not apply. A run of it must exit 0:
# a skip is not a failure, and a runner that treated it as one would make "this does not run
# here" the same as "this is broken".
cat >"$tmp/skips/plat/plat_test.zg" <<'EOF'
import (
	"testing"
)

#[test]
fn test_holds() {
	testing.assert(true)
}

#[test]
fn test_elsewhere(ctx: testing.Context) {
	ctx.skip("only on the other platform")
}
EOF

# --- the run ------------------------------------------------------------------------------

before=$(ls "$tmp/pkg/lib" "$tmp/pkg/edge")

out=$("$ZERG" test "$tmp/pkg" 2>&1)
status=$?
printf '%s\n' "$out" >"$tmp/out"

[ "$status" -ne 0 ]
say "a run holding a failing test exited 0" $?

# --- the spawn path ------------------------------------------------------------------------

# 1. the passing test, and the two signatures. `test_twice_holds` takes a context and
#    `test_after_the_abort` takes nothing; both must be called, so both must be `ok`.
grep -qE '^  ok    test_twice_holds$' "$tmp/out"
say "the passing test is not reported \`ok\`" $?

grep -qE '^  ok    test_after_the_abort$' "$tmp/out"
say "a test declared with no parameter was not called" $?

# 2. the context knows which test it is. Asserted from INSIDE the test — `ctx.name()`
#    compared against the literal name — so a runner that handed every test the same context
#    goes red here and nowhere else.
grep -qE '^  ok    test_knows_its_name$' "$tmp/out"
say "\`ctx.name()\` did not answer the test's own name" $?

# 3. the failing assertion — the verdict AND the message, because a FAIL that does not say
#    what went wrong sends the reader back to the source to guess.
grep -qE '^  FAIL  test_thrice_does_not$' "$tmp/out"
say "the failing assertion is not reported FAIL" $?

grep -qF 'assert_eq failed' "$tmp/out"
say "the failure does not carry the assertion's message" $?

# 4. a note is shown for the test that FAILED and for no other. Both halves are the rule —
#    `ctx.log` that always prints is a debug print with a longer name.
grep -qF 'thrice(2) is twice(2) + 2' "$tmp/out"
say "a failing test's \`ctx.log\` note was not shown" $?

grep -qF 'a note from a test that passes' "$tmp/out"
[ $? -ne 0 ]
say "a passing test's \`ctx.log\` note was shown — a note is for explaining a failure" $?

# 5. the skip: its own verdict, its reason on the line, and NOT a failure.
grep -qE '^  SKIP  test_not_on_this_platform +not on this platform$' "$tmp/out"
say "\`ctx.skip\` is not reported SKIP with its reason" $?

grep -qE '^  (FAIL |CRASH) test_not_on_this_platform$' "$tmp/out"
[ $? -ne 0 ]
say "a skipped test was reported as a failure" $?

# 6. an uncaught abort IN THE COROUTINE: contained, reported, and the tests after it run in
#    the same process. The NOTE line is what the fallback prints when it has to take over, so
#    its ABSENCE for this file is the claim that no process died here.
grep -qE '^  FAIL  test_reads_off_the_end$' "$tmp/out"
say "an uncaught abort inside a test is not reported as that test failing" $?

# the message must be a REPORT line — indented under the verdict — and not merely present
# in the captured output: the runtime prints an uncaught abort on stderr on its way past, so
# a plain search for the text passes even when the report itself said nothing
grep -qE '^        index out of range$' "$tmp/out"
say "the abort's own message did not reach the report" $?

awk '/lib_test.zg/{f=1} f&&/NOTE/{found=1} END{exit found?1:0}' "$tmp/out"
say "the file whose worst test merely aborted still fell back to one process per test" $?

# --- the fallback path ---------------------------------------------------------------------

# 7. the stack overflow, attributed to the test that caused it rather than to the run.
grep -qE '^  CRASH test_a_overflows_the_stack$' "$tmp/out"
say "a test that overflows its stack is not reported as a crash" $?

# 8. `os.exit(0)` is not a pass. Asserted as the ABSENCE of an ok line as well as the
#    presence of a FAIL one: the failure this fixture exists for is the runner reading the
#    process's status, and a status of 0 is what it would have read.
grep -qE '^  ok    test_b_exits_early$' "$tmp/out"
[ $? -ne 0 ]
say "a test that called \`os.exit(0)\` was counted as passing" $?

grep -qE '^  FAIL  test_b_exits_early$' "$tmp/out"
say "a test that exited early is not reported as a failure" $?

# 9. the tests after them still run — the whole reason the isolated path was kept.
grep -qE '^  ok    test_c_after_the_end$' "$tmp/out"
say "a test declared after the two that end the process did not run" $?

# 10. and the report SAYS the strategy changed, exactly once — for the file that needed it.
grep -qE '^  NOTE  the in-process run ended here' "$tmp/out"
say "the report did not say that the run fell back to one process per test" $?

[ "$(grep -cE '^  NOTE  ' "$tmp/out")" -eq 1 ]
say "the fallback note appears for a file that did not need it" $?

# --- the summary ---------------------------------------------------------------------------

# 11. the counts, and the grouping. The summary is the line a person reads, and it is the one
#     place the whole run is stated in numbers — with the skip counted APART from both, since
#     a suite whose skips are passes goes green on a platform where none of it ran.
grep -qE '^4 passed, 4 failed, 1 skipped$' "$tmp/out"
say "the summary does not count 4 passed, 4 failed and 1 skipped" $?

grep -qF 'lib/lib_test.zg' "$tmp/out"
say "the report does not name the file its tests came from" $?

# 12. nothing left behind. The driver is written INTO the package — that is what puts it in
#     the module's scope — so a run that does not take it away leaves a `fn main` in somebody's
#     source directory.
after=$(ls "$tmp/pkg/lib" "$tmp/pkg/edge")
[ "$before" = "$after" ]
say "the run left a generated file behind in the package" $?

# 13. a directory with no test files: exit 0, and NOT silence.
none=$("$ZERG" test "$tmp/none" 2>&1)
status=$?

[ "$status" -eq 0 ]
say "a tree with no test files did not exit 0" $?

printf '%s\n' "$none" | grep -qF 'no tests'
say "a tree with no test files said nothing — a silent run is indistinguishable from one where everything passed" $?

# 14. a skip is not a failure, said in the one place it decides something: the exit status.
skips=$("$ZERG" test "$tmp/skips" 2>&1)
status=$?

[ "$status" -eq 0 ]
say "a run whose only non-pass was a skip exited non-zero" $?

printf '%s\n' "$skips" | grep -qE '^1 passed, 0 failed, 1 skipped$'
say "a run of one pass and one skip did not count them apart" $?

# --- the floor -----------------------------------------------------------------------------
#
# 25 assertions today. The floor is what keeps this from reporting success after a rewrite
# that stops asserting — the failure every gate here is written against, one level up.
MIN_ASSERTS=${MIN_ASSERTS:-25}
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

printf 'test-runner-check: %s assertions — both paths, and a failure, a skip, a crash, an early exit and an empty tree are each seen\n' "$total"
