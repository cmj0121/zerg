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
#   the fixtures — a test declares what it needs and the framework builds it
#     once      two tests sharing one fixture      both see build 1, asserted from inside
#     chain     `schema` is built from `db`        the test names only `schema` and gets it
#     reverse   setup outermost, teardown inner    the whole sequence, not four greps
#     survives  a test fails under a fixture       the level runs on and the teardown still runs
#     broken    a fixture that raises              every test needing it FAILS, by name and reason
#     inherited `fixtures_test.zg` two levels up   available below, and its copy taken away after
#     rebuilt   the fallback re-runs one test      it stands that test's fixtures up again
#     refused   no such fixture / a circle / a type that is not what the fixture produces —
#               each reported with a place BEFORE anything runs, and nothing runs
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
grep -qE '^4 passed, 4 failed, 1 skipped, 0 timed out$' "$tmp/out"
say "the summary does not count 4 passed, 4 failed, 1 skipped and 0 timed out" $?

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

printf '%s\n' "$skips" | grep -qE '^1 passed, 0 failed, 1 skipped, 0 timed out$'
say "a run of one pass and one skip did not count them apart" $?

# --- the fixture packages ------------------------------------------------------------------
#
# A TEST DECLARES WHAT IT NEEDS AND THE FRAMEWORK BUILDS IT, so what is measured here is what
# a fixture is FOR: that it is built once for the tests that share it, that a chain of them
# stands up in order and comes down in reverse, that a test failing under one does not take
# the rest of the level with it, and that a fixture which cannot be built fails the tests that
# needed it instead of leaving them looking like passes.
#
# THE TREE IS THE FIXTURE. `fixt/fixtures_test.zg` is the file an ancestor contributes
# downward, `fixt/pkg` inherits it, and `fixt/pkg/deep` inherits it from TWO levels up —
# because "every directory below" is the claim, not "the next one".

mkdir -p "$tmp/fixt/pkg/deep" "$tmp/fixt/broke" "$tmp/fell/a" \
	"$tmp/noname/a" "$tmp/cycle/a" "$tmp/mistyped/a" "$tmp/uninherited/pkg/sub"

# the inherited file: two fixtures, one built from the other, each announcing both ends of its
# life so the ORDER they run in is observable from outside
cat >"$tmp/fixt/fixtures_test.zg" <<'EOF'
import (
	"io"
)

struct Conn {
	id: int = 0
}

struct Schema {
	tag: str = ""
}

#[fixture]
fn db(use: fn (Conn)) {
	defer io.println("teardown db")
	io.println("setup db")
	use(Conn(7))
}

#[fixture]
fn schema(db: Conn, use: fn (Schema)) {
	defer io.println("teardown schema")
	io.println("setup schema")
	use(Schema(f"from db {db.id}"))
}
EOF

# THE COUNT IS ASSERTED FROM INSIDE A TEST, not read off a print. `counted` appends one byte to
# a file per build and hands back its length, so "built once for two tests" is two tests that
# both see 1 — a claim that arrives through the REPORT. A driver that built the fixture per
# test would leave the second one reading 2, and only the second one would go red.
#
# The heredoc is UNQUOTED for this one file, because the counter has to live outside the tree
# `zerg test` walks and only the shell knows where that is.
cat >"$tmp/fixt/pkg/pkg_test.zg" <<EOF
import (
	"io"
	"testing"
)

struct Serial {
	n: int = 0
}

fn bump(path: str) -> int {
	mut s := ""
	guard {
		s = str(io.read_file(path))
	}
	s = s + "x"
	guard {
		io.write_file(path, bytearray(s))
	}
	return bytearray(s).len()
}

#[fixture]
fn counted(use: fn (Serial)) {
	use(Serial(bump("$tmp/serial")))
}

#[test]
fn test_a_first_sees_the_first_build(counted: Serial) {
	testing.assert_eq(counted.n, 1)
}

#[test]
fn test_b_second_sees_the_same_one(counted: Serial) {
	testing.assert_eq(counted.n, 1)
}

#[test]
fn test_c_gets_the_whole_chain(schema: Schema) {
	testing.assert_eq(schema.tag, "from db 7")
}

#[test]
fn test_d_fails_under_a_fixture(db: Conn) {
	testing.assert_eq(db.id, 99)
}

#[test]
fn test_e_still_ran_after_it(db: Conn, ctx: testing.Context) {
	testing.assert_eq(ctx.name(), "test_e_still_ran_after_it")
	testing.assert_eq(db.id, 7)
}

#[test]
fn test_f_needs_nothing() {
	testing.assert(true)
}
EOF

# two levels below the file that declares them
cat >"$tmp/fixt/pkg/deep/deep_test.zg" <<'EOF'
import (
	"testing"
)

#[test]
fn test_g_two_levels_down(schema: Schema) {
	testing.assert_eq(schema.tag, "from db 7")
}
EOF

# a fixture that cannot be built, and the two tests that asked for it
cat >"$tmp/fixt/broke/broke_test.zg" <<'EOF'
import (
	"testing"
)

struct Sock {
	fd: int = 0
}

#[fixture]
fn sock(use: fn (Sock)) {
	raise "the socket was refused"
	use(Sock(1))
}

#[test]
fn test_h_needs_the_broken_one(sock: Sock) {
	testing.assert_eq(sock.fd, 1)
}

#[test]
fn test_i_also_needs_it(sock: Sock) {
	testing.assert_eq(sock.fd, 1)
}
EOF

# fell — the fallback, with a fixture over it. `test_j` ends the PROCESS, so `test_k` is re-run
# on its own, and it can only pass if that process built `res` again.
cat >"$tmp/fell/a/a_test.zg" <<'EOF'
import (
	"io"
	"os"
	"testing"
)

struct Res {
	n: int = 0
}

#[fixture]
fn res(use: fn (Res)) {
	io.println("built res")
	use(Res(5))
}

#[test]
fn test_j_ends_the_process(res: Res) {
	os.exit(0)
	print res.n
}

#[test]
fn test_k_after_it(res: Res) {
	testing.assert_eq(res.n, 5)
}
EOF

# the three refusals, each in a tree of its own because each ends the run before it starts
cat >"$tmp/noname/a/a_test.zg" <<'EOF'
import (
	"testing"
)

struct Conn {
	id: int = 0
}

#[fixture]
fn db(use: fn (Conn)) {
	use(Conn(1))
}

#[test]
fn test_asks_for_nobody(conn: Conn) {
	testing.assert_eq(conn.id, 1)
}

#[test]
fn test_would_have_passed() {
	testing.assert(true)
}
EOF

cat >"$tmp/cycle/a/a_test.zg" <<'EOF'
import (
	"testing"
)

struct A {
	n: int = 0
}

struct B {
	n: int = 0
}

#[fixture]
fn one(two: B, use: fn (A)) {
	use(A(two.n))
}

#[fixture]
fn two(one: A, use: fn (B)) {
	use(B(one.n))
}

#[test]
fn test_round(one: A) {
	testing.assert_eq(one.n, 0)
}
EOF

cat >"$tmp/mistyped/a/a_test.zg" <<'EOF'
import (
	"testing"
)

struct Conn {
	id: int = 0
}

#[fixture]
fn db(use: fn (Conn)) {
	use(Conn(1))
}

#[test]
fn test_declares_the_wrong_type(db: int) {
	testing.assert_eq(db, 1)
}
EOF

# uninherited — a fixture in a plain `*_test.zg` serves its OWN directory and no other. That is
# the whole of what the `fixtures_test.zg` name buys, so it is asserted rather than assumed.
cat >"$tmp/uninherited/pkg/pkg_test.zg" <<'EOF'
import (
	"testing"
)

struct Local {
	n: int = 0
}

#[fixture]
fn local(use: fn (Local)) {
	use(Local(1))
}

#[test]
fn test_here_can_have_it(local: Local) {
	testing.assert_eq(local.n, 1)
}
EOF

cat >"$tmp/uninherited/pkg/sub/sub_test.zg" <<'EOF'
import (
	"testing"
)

#[test]
fn test_below_cannot(local: Local) {
	testing.assert_eq(local.n, 1)
}
EOF

# --- fixtures: the run ----------------------------------------------------------------------

fxbefore=$(ls "$tmp/fixt" "$tmp/fixt/pkg" "$tmp/fixt/pkg/deep" "$tmp/fixt/broke")

fx=$("$ZERG" test "$tmp/fixt" 2>&1)
status=$?
printf '%s\n' "$fx" >"$tmp/fx"

[ "$status" -ne 0 ]
say "a run whose fixture could not be built exited 0" $?

# 15. built ONCE for the tests that share it. Both halves are the assertion: the first test
#     seeing build 1 says the fixture ran, and the SECOND one seeing build 1 says it did not
#     run again. A driver that built it per test leaves only the second red.
grep -qE '^  ok    test_a_first_sees_the_first_build$' "$tmp/fx"
say "the first test under a fixture did not get the fixture's first build" $?

grep -qE '^  ok    test_b_second_sees_the_same_one$' "$tmp/fx"
say "the fixture was built again for the second test that needed it — it is built once for the tests that share it" $?

# 16. a dependency chain: `schema` names `db`, and the test names only `schema`.
grep -qE '^  ok    test_c_gets_the_whole_chain$' "$tmp/fx"
say "a fixture built from another fixture did not receive it" $?

# 17. the ORDER, both ways. Setup outermost first and teardown innermost first is what the
#     nesting is for, and it is asserted as the whole sequence rather than as two greps: a
#     driver that tore down in the wrong order still prints all four lines.
seq=$(printf '%s\n' "$fx" | grep -E '^(setup|teardown) (db|schema)$' | tr '\n' '|')
[ "$seq" = "setup db|setup schema|teardown schema|teardown db|setup db|setup schema|teardown schema|teardown db|" ]
say "the fixtures did not stand up outermost-first and come down innermost-first" $?

# 18. and teardown ran EVEN THOUGH a test under it failed. Counted rather than grepped: one
#     teardown per package that built it, and a package that skipped teardown after a failure
#     would leave one.
[ "$(grep -cE '^teardown db$' "$tmp/fx")" -eq 2 ]
say "a fixture was not torn down once per package — a failing test under it swallowed the teardown" $?

# 19. the failing test, and the one after it. A test failing inside `use(...)` must not abort
#     the continuation: the rest of the level runs and the fixture still comes down.
grep -qE '^  FAIL  test_d_fails_under_a_fixture$' "$tmp/fx"
say "a test that failed under a fixture is not reported FAIL" $?

grep -qE '^  ok    test_e_still_ran_after_it$' "$tmp/fx"
say "a test after a failing one under the same fixture did not run — a failure inside \`use\` aborted the continuation" $?

# 20. a test that asks for nothing still runs, beside ones that do.
grep -qE '^  ok    test_f_needs_nothing$' "$tmp/fx"
say "a test with no parameter did not run in a package that has fixtures" $?

# 21. inheritance, TWO levels down — pytest's conftest model, all sub-levels.
grep -qE '^  ok    test_g_two_levels_down$' "$tmp/fx"
say "a fixture two directories up was not available to the tests below it" $?

# 22. a fixture that cannot be built FAILS every test that needed it, by name and with the
#     reason. Asserted as the FAIL line, the message under it, and the ABSENCE of an ok line:
#     a test that could not run must never look like one that passed.
grep -qE '^  FAIL  test_h_needs_the_broken_one$' "$tmp/fx"
say "a test whose fixture could not be built is not reported FAIL" $?

grep -qE '^        fixture `sock` could not be built: the socket was refused$' "$tmp/fx"
say "the failure does not say which fixture could not be built, or why" $?

grep -qE '^  FAIL  test_i_also_needs_it$' "$tmp/fx"
say "only the first test of the two needing a broken fixture was failed" $?

grep -qE '^  ok    test_(h|i)_' "$tmp/fx"
[ $? -ne 0 ]
say "a test that never ran because its fixture broke was counted as passing" $?

# 23. the counts.
grep -qE '^6 passed, 3 failed, 0 skipped, 0 timed out$' "$tmp/fx"
say "the fixture run does not count 6 passed, 3 failed and 0 skipped" $?

# 24. nothing left behind — and for fixtures that is one file more than the driver: an
#     inherited fixture file is COPIED into the package, and a run that does not take the copy
#     away leaves somebody else's declarations in a source directory.
fxafter=$(ls "$tmp/fixt" "$tmp/fixt/pkg" "$tmp/fixt/pkg/deep" "$tmp/fixt/broke")
[ "$fxbefore" = "$fxafter" ]
say "the run left the copy of an inherited fixture file behind in the package" $?

# --- fixtures: the fallback -----------------------------------------------------------------
#
# 25. THE FALLBACK MUST REBUILD WHAT THE TEST IT RE-RUNS NEEDS. `test_j` ends the process, so
#     `test_k` is re-run alone — and it can only pass if that process stood `res` up again.
fell=$("$ZERG" test "$tmp/fell" 2>&1)
printf '%s\n' "$fell" >"$tmp/fell.out"

grep -qE '^  ok    test_k_after_it$' "$tmp/fell.out"
say "the fallback did not rebuild the fixture the test it re-ran needed" $?

# once for the run that died, and once for each of the two re-runs — no more, because a
# fallback process enters only the levels the test it was asked for is under
[ "$(grep -cE '^built res$' "$tmp/fell.out")" -eq 3 ]
say "the fallback built the fixture a number of times that is not once per process that needed it" $?

# --- fixtures: what is refused before anything runs ------------------------------------------

# 26. a parameter naming no fixture. The exit status, a sentence with a PLACE, and — the part
#     that matters — that NOTHING RAN: a resolution failure found halfway through a run would
#     have already reported verdicts for tests whose neighbours never got to run.
noname=$("$ZERG" test "$tmp/noname" 2>&1)
status=$?

[ "$status" -eq 2 ]
say "a test naming no fixture did not end the run with status 2" $?

printf '%s\n' "$noname" | grep -qE 'a_test\.zg:[0-9]+:[0-9]+: the test `test_asks_for_nobody` asks for `conn` and no `#\[fixture\]` of that name is in scope'
say "the parameter naming no fixture was not reported by name and with a place" $?

printf '%s\n' "$noname" | grep -qE '^  ok    '
[ $? -ne 0 ]
say "a package with an unresolvable parameter ran some of its tests anyway" $?

# 27. a circle. Reported ONCE, by name, in the order it was walked — a report per member would
#     be one circle described three ways.
cyc=$("$ZERG" test "$tmp/cycle" 2>&1)
status=$?

[ "$status" -eq 2 ]
say "a circle among fixtures did not end the run with status 2" $?

printf '%s\n' "$cyc" | grep -qE 'a_test\.zg:[0-9]+:[0-9]+: the fixtures `one` -> `two` -> `one` depend on each other in a circle'
say "the circle was not named end to end with a place" $?

[ "$(printf '%s\n' "$cyc" | grep -c 'depend on each other in a circle')" -eq 1 ]
say "one circle was reported once per fixture in it" $?

# 28. a parameter whose type is not what the fixture produces.
mis=$("$ZERG" test "$tmp/mistyped" 2>&1)
status=$?

[ "$status" -eq 2 ]
say "a parameter typed differently from what its fixture produces did not end the run with status 2" $?

printf '%s\n' "$mis" | grep -qE 'declares `db: int` and the fixture `db` produces `Conn`'
say "the type a fixture produces and the type the parameter declared were not both named" $?

# 29. THE FILE NAME IS THE INHERITANCE RULE. A fixture in a plain `*_test.zg` serves its own
#     directory; only `fixtures_test.zg` goes downward. Asserted from below, where a test asks
#     for a sibling directory's fixture and is told there is none.
uni=$("$ZERG" test "$tmp/uninherited" 2>&1)
status=$?

[ "$status" -eq 2 ]
say "a fixture declared in a plain \`*_test.zg\` was inherited by the directory below it" $?

printf '%s\n' "$uni" | grep -qE 'the test `test_below_cannot` asks for `local` and no `#\[fixture\]` of that name is in scope'
say "the test below was not told that the fixture it named is not in its scope" $?

# --- a flat directory of independent modules ------------------------------------------------
#
# THE SHAPE THE STANDARD LIBRARY HAS, and the one a test build used to be unable to reach. A
# `.zg` file beside its neighbours is a module in its own right — `module_at` resolves an
# import to a single `<name>.zg` BEFORE it resolves one to a directory — so `flat` here is
# three independent modules, exactly as `src/stdlib` is sixteen.
#
# `broken.zg` and `clash.zg` are the two failures the pilot actually met, reproduced: a generic
# struct is `E215` in this compiler, and two modules defining one `pub` name is `E705`. Neither
# is imported by anything. A runner that took the DIRECTORY as the package would compile both
# beside `good.zg` and report an error inside a file the author never wrote — before one test
# had run.

mkdir -p "$tmp/flat"

cat >"$tmp/flat/good.zg" <<'EOF'
fn doubled(n: int) -> int {
	return n + n
}

pub fn shared() -> int {
	return doubled(21)
}
EOF

cat >"$tmp/flat/broken.zg" <<'EOF'
struct Box[T] {
	v: T
}

pub fn boxed(v: int) -> Box[int] {
	return Box(v)
}
EOF

cat >"$tmp/flat/clash.zg" <<'EOF'
pub fn shared() -> int {
	return 0
}
EOF

cat >"$tmp/flat/good_test.zg" <<'EOF'
import (
	"testing"
)

#[test]
fn test_reaches_the_modules_private_name() {
	testing.assert_eq(doubled(3), 6)
}

#[test]
fn test_reaches_its_public_one_too() {
	testing.assert_eq(shared(), 42)
}
EOF

flat=$("$ZERG" test "$tmp/flat" 2>&1)
status=$?
printf '%s\n' "$flat" >"$tmp/flat.out"

# 30. the tests of the one-file module RAN — white-box, with no `pub` and no import, which is
#     the whole of what was lost when a suite had to move out to a package of its own.
[ "$status" -eq 0 ]
say "a test beside a module in a flat directory of modules did not exit 0" $?

grep -qE '^  ok    test_reaches_the_modules_private_name$' "$tmp/flat.out"
say "a test beside its module could not reach the module's private name" $?

grep -qE '^  ok    test_reaches_its_public_one_too$' "$tmp/flat.out"
say "a test beside its module could not reach the module's public name" $?

grep -qE '^2 passed, 0 failed, 0 skipped, 0 timed out$' "$tmp/flat.out"
say "the flat-directory run does not count 2 passed" $?

# 31. and the two SIBLINGS were never compiled. Asserted by their error codes rather than by
#     their names: `E215` and `E705` are what reaching them costs, and they are what the pilot
#     was shown instead of a test result.
grep -qF 'E215' "$tmp/flat.out"
[ $? -ne 0 ]
say "an independent module beside the one under test was compiled into the same package (E215)" $?

grep -qF 'E705' "$tmp/flat.out"
[ $? -ne 0 ]
say "a \`pub\` name in an unrelated module of the same directory collided with the one under test (E705)" $?

# --- nothing is left behind on a path that FAILED --------------------------------------------
#
# The success path is asserted above (12, 24). This is the other one, and it is the one that
# actually happened: `zerg test src/stdlib` stopped inside `atomic.zg` and left a generated
# `zerg_test_driver_test.zg` behind — in the directory the compiler resolves the standard
# library by LISTING.
#
# The two failures are the two SHAPES a build has: a raise carried out of the loader (an
# import that resolves nowhere) and a diagnostic reported by the compiler proper (a write to
# an immutable). Neither returns through the end of the run, and neither may leave a file.

mkdir -p "$tmp/left/raises" "$tmp/left/diags"

cat >"$tmp/left/fixtures_test.zg" <<'EOF'
struct Held {
	n: int = 0
}

#[fixture]
fn held(use: fn (Held)) {
	use(Held(1))
}
EOF

cat >"$tmp/left/raises/raises_test.zg" <<'EOF'
import (
	"no_such_module_anywhere"
	"testing"
)

#[test]
fn test_never_gets_to_run() {
	testing.assert_eq(no_such_module_anywhere.value(), 1)
}
EOF

cat >"$tmp/left/diags/diags_test.zg" <<'EOF'
import (
	"testing"
)

#[test]
fn test_writes_to_an_immutable() {
	n := 1
	n = 2
	testing.assert_eq(n, 2)
}
EOF

# 32. the raise: the loader gave up, and the driver AND the copy of the inherited fixture file
#     it had already written are both gone. The copy is the half a `rm` at the end of the happy
#     path would still have missed.
raises=$("$ZERG" test "$tmp/left/raises" 2>&1)
status=$?

[ "$status" -ne 0 ]
say "a test build whose import resolves nowhere exited 0" $?

printf '%s\n' "$raises" | grep -qF 'E502'
say "the unresolvable import was not reported" $?

[ "$(ls "$tmp/left/raises")" = "raises_test.zg" ]
say "a test build that raised left its generated files behind in the package" $?

# 33. the diagnostic: the compiler reported and exited, and the driver is still gone.
diags=$("$ZERG" test "$tmp/left/diags" 2>&1)
status=$?

[ "$status" -ne 0 ]
say "a test build with a compile error exited 0" $?

[ "$(ls "$tmp/left/diags")" = "diags_test.zg" ]
say "a test build that reported a diagnostic left its generated files behind in the package" $?

# --- --only ----------------------------------------------------------------------------------
#
# THE FILTER IS APPLIED BEFORE THE DRIVER IS WRITTEN, which is what makes the claim about
# fixtures exact: a test that was not selected is not resolved, not generated and not compiled,
# so the level that would have stood its fixtures up is never written at all. `setup db` is
# printed BY the fixture, so its absence is the assertion — and `fixt` is used rather than a new
# tree because that is where the fixtures that announce themselves already are.

only=$("$ZERG" test "$tmp/fixt" --only test_f_needs_nothing 2>&1)
status=$?
printf '%s\n' "$only" >"$tmp/only.out"

# 34. the one test named ran, and nothing else did.
[ "$status" -eq 0 ]
say "a run filtered down to one passing test did not exit 0" $?

grep -qE '^  ok    test_f_needs_nothing$' "$tmp/only.out"
say "--only did not run the test it named" $?

grep -qE '^1 passed, 0 failed, 0 skipped, 0 timed out$' "$tmp/only.out"
say "--only ran a test it was not asked for" $?

# 35. AND THE FIXTURES OF THE TESTS IT DROPPED WERE NEVER BUILT. Without this the filter is
#     only a filter on the report: the run would still stand `db` and `schema` up, pay for
#     them, and then skip past the tests that needed them.
grep -qF 'setup db' "$tmp/only.out"
[ $? -ne 0 ]
say "--only built a fixture that only the tests it filtered out had asked for" $?

# 36. a PREFIX selects a family, which is the form a suite is iterated on. `serial` is the
#     counter the `counted` fixture appends to, and it is reset because this run stands that
#     fixture up a second time — the earlier run above left one build in it.
rm -f "$tmp/serial"
fam=$("$ZERG" test "$tmp/fixt" --only test_a 2>&1)
printf '%s\n' "$fam" | grep -qE '^  ok    test_a_first_sees_the_first_build$'
say "--only with a name stem did not select the test under it" $?

# 37. and a filter that selected NOTHING is a typo, not a green run — the one failure mode a
#     filter adds to a runner, and the one every gate here is written against.
none=$("$ZERG" test "$tmp/fixt" --only test_no_such_thing 2>&1)
status=$?

[ "$status" -eq 2 ]
say "--only naming no test did not end the run with status 2" $?

printf '%s\n' "$none" | grep -qF 'has a name beginning with `test_no_such_thing`'
say "--only naming no test did not say so" $?

# --- the per-test timeout ---------------------------------------------------------------------
#
# A HUNG TEST AND A SLOW TEST LOOK IDENTICAL until something decides which is which. With
# nothing deciding, the hung one takes the whole run with it and CI's own timeout kills the job
# with nothing in the log saying which test did it.
#
# `test_a_hangs` sleeps far longer than the limit this run is given, and `test_b_after_it` is
# what says the run went ON — a runner that treated a timeout as the end of the world would
# report nothing after it.

mkdir -p "$tmp/slow/a"

cat >"$tmp/slow/a/a_test.zg" <<'EOF'
import (
	"testing"
)

#[test]
fn test_a_hangs() {
	__zrt_sleep_ns(30000000000)
	testing.assert(true)
}

#[test]
fn test_b_after_it() {
	testing.assert(true)
}
EOF

slow=$("$ZERG" test "$tmp/slow" --timeout 1 2>&1)
status=$?
printf '%s\n' "$slow" >"$tmp/slow.out"

# 38. the verdict is its OWN, and it is not a failure: nothing was checked and nothing was
#     claimed, so a reader sent looking for the assertion that failed would find none.
grep -qE '^  STUCK test_a_hangs$' "$tmp/slow.out"
say "a test that never finishes is not reported STUCK" $?

grep -qE '^  (FAIL |CRASH) test_a_hangs$' "$tmp/slow.out"
[ $? -ne 0 ]
say "a test that timed out was reported as a failed assertion" $?

grep -qE '^        the test did not finish within 1s' "$tmp/slow.out"
say "the timeout does not say how long it waited" $?

# 39. counted apart, in the line a person reads.
grep -qE '^1 passed, 0 failed, 0 skipped, 1 timed out$' "$tmp/slow.out"
say "a timeout is not counted apart from the failures and the passes" $?

# 40. it still fails the RUN — a suite with a hung test in it is not a green suite.
[ "$status" -ne 0 ]
say "a run holding a test that never finished exited 0" $?

# 41. and the run went on. This is the half that makes the limit worth having: the tests after
#     the hung one still ran, in the same process, and answered for themselves.
grep -qE '^  ok    test_b_after_it$' "$tmp/slow.out"
say "the tests after a hung one did not run" $?

# 42. THE SOURCES THIS GATE WRITES ARE CANONICAL. They are the example a reader copies out of
#     here, and a gate whose examples `zerg fmt` would rewrite teaches a spelling the toolchain
#     does not have — which is exactly how `fn(T)` came to be written all through this file for
#     the canonical `fn (T)`, with nothing to say so. No generated driver is among them: every
#     run above took its own away, which the `before`/`after` checks already assert.
find "$tmp" -name '*.zg' -print0 | xargs -0 "$ZERG" fmt --check >/dev/null 2>&1
say "a source this gate holds up as the way to write a test or a fixture is not in canonical form" $?


# --- the floor -----------------------------------------------------------------------------
#
# 79 assertions today. The floor is what keeps this from reporting success after a rewrite
# that stops asserting — the failure every gate here is written against, one level up.
MIN_ASSERTS=${MIN_ASSERTS:-79}
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

printf 'test-runner-check: %s assertions — both paths, a failure, a skip, a crash, an early exit and an empty tree, and a fixture built once, chained, torn down in reverse, broken, unnamed and circular\n' "$total"
