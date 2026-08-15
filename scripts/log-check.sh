#!/usr/bin/env bash
#
# log-check — the claims about `log` that a test running INSIDE the process cannot make.
#
# tests/stdlib/log/log_test.zg asks everything that can be asked from inside: the level gate,
# the builder, the bytes of a rendered line, the global. It reaches them through a `chan[str]`
# sink, which is what makes a logger testable at all — there is no reading a `write(2)` back.
#
# Seven claims are outside that reach, spread over the eight cases below — this numbering is
# the CLAIMS and not the sections, because the one-write rule is asserted in two places. Each
# is the kind nobody notices until it is an incident:
#
#   1. `fatal` EXITS. A test that called it would take the test process with it, so the claim
#      is made about a whole program: it writes its line, and it exits 1. And it exits even at
#      a level where the line is NOT written — silencing a logger changes what is reported,
#      never what is done.
#
#   2. THE DEFAULT DESTINATION IS STANDARD ERROR. A logger writing to stdout would corrupt the
#      output of every program that pipes its own, and nothing inside the process can see
#      which descriptor was used.
#
#   3. A LOG LINE IS ONE WRITE — and this one is asserted over the SOURCE, for a reason that
#      was measured rather than assumed. See below.
#
#   4. COLOUR FOLLOWS THE TERMINAL, and the format does not. `os.isatty(2)` answers about the
#      real descriptor a real process was given, so the only way to ask this is to give a
#      program a pipe and then a pty and read what came out of each. `NO_COLOR` overrides both.
#
#   5. `ZERG_LOG` PICKS THE FORMAT, and nothing else does. The line this module holds is that
#      redirecting output changes its APPEARANCE and never its SHAPE — so the same program,
#      piped and on a terminal, must produce the same FORMAT and differ only in colour.
#
#   6. `ZERG_LOG_LEVEL` PICKS THE LEVEL, BY NAME. `new()` reads the environment through
#      module-level consts, so the answer is frozen before `main` and a test inside the
#      process cannot vary it. Every claim about a DEFAULT therefore lives here — including
#      that an unset variable means INFO, that `1` is not a level, and that a misspelt one
#      does not take the program down.
#
#   7. THE PATTERN THIS MODULE IS THE REFERENCE FOR — one private cell, one writer, readers
#      that only delegate — is a property of the SOURCE. The compiler enforces its rule 1 and
#      nothing else; rules 2 and 3 had nothing but a reader checking them, and this is that
#      reader. Rule 4 is about the caller and cannot be checked here at all.
#
# WHY THE ONE-WRITE CLAIM IS STRUCTURAL AND NOT A STRESS TEST.
#
# The obvious gate is the one this project already ran once: many writers, and every line that
# comes out must be whole. `zrt_report` failed exactly that — 830 lines in 24000 carrying one
# kind with another's message — and `flockfile` fixed it.
#
# That gate was written here first, and then MUTATED to see it fail: `emit` was changed to put
# the line out in two `io.ewrite` calls instead of one, which is precisely the defect. It did
# not tear. Not once, in 12000 lines from 24 coroutines, at one worker and at every worker
# count this host has:
#
#     one write            lines=12000  torn=0   writer switches=11999
#     two writes (mutated) lines=12000  torn=0   writer switches=11992
#
# The switch count is the explanation. Coroutines are COOPERATIVE: one runs until it parks, and
# there is no parking point between two adjacent writes — so the second `write(2)` follows the
# first with nothing able to get in between on that worker, and the window on another worker is
# too narrow to hit. (The 830-line failure was eight OS THREADS in `zrt_report`, not
# coroutines.) A stress case here would therefore be green whether the property held or not,
# which is the one thing a gate must never be. It is not shipped as one.
#
# So the property is asserted where it IS visible: `emit` is the only place a line leaves the
# module, and it makes exactly one write call. That is a claim about the source, and a
# regression to two writes — or to `io.eprintln`, which is two — fails it immediately.
#
# The many-writer program still runs, with the claim it CAN make: 12000 lines go in and 12000
# whole lines come out, none lost, none carrying two writers' fields.
#
# A FLOOR, like every other gate here: each case counts what it actually checked, so a probe
# that stopped building fails rather than passing for having compared nothing.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

ZERG="${ZERG:-./bin/zerg}"
LOG_SRC="${LOG_SRC:-src/stdlib/log.zg}"

# The many-writer program's shape. 24 coroutines is well over the worker count on any machine
# this runs on.
WRITERS="${WRITERS:-24}"
LINES="${LINES:-500}"

# How long a `ctx.log` note gets before case 7 calls it a hang. Generous — the run compiles a
# suite first — and it only has to be finite.
NOTE_TIMEOUT="${NOTE_TIMEOUT:-120}"

[ -x "$ZERG" ] || {
	printf 'log-check: %s is not built — run `make build` first\n' "$ZERG" >&2
	exit 2
}
[ -f "$LOG_SRC" ] || {
	printf 'log-check: %s is not there, and this gate reads it\n' "$LOG_SRC" >&2
	exit 2
}

# EVERY PROGRAM BELOW STARTS UNCONFIGURED. `log` reads these three at module init, and this
# gate is the only place a claim about a DEFAULT can be made — so a developer with
# `ZERG_LOG_LEVEL=debug` exported must not be able to make "the default writes no debug line"
# pass or fail for a reason that is about their shell. A case that wants a value sets it on
# the command line, one run at a time.
unset ZERG_LOG ZERG_LOG_LEVEL NO_COLOR

WORK="$(mktemp -d "${TMPDIR:-/tmp}/zerg-logcheck.XXXXXX")" || exit 2
trap 'rm -rf "$WORK"' EXIT

fail=0
checks=0

note() {
	printf 'log-check: %s\n' "$1" >&2
	fail=1
}

# build <name> — compile $WORK/<name>.zg to $WORK/<name>.
build() {
	rm -f "$WORK/$1"
	if ! "$ZERG" build "$WORK/$1.zg" -o "$WORK/$1" >"$WORK/$1.build.log" 2>&1; then
		note "$1 does not compile"
		cat "$WORK/$1.build.log" >&2
		return 1
	fi
	return 0
}

# --- 1. one line is one write, read off the source -----------------------------------------

# The writers `io` offers, and what each costs a log line. `ewrite` is one `__zrt_write`;
# `eprintln` and `println` are TWO — the text, then the newline — which is the defect this
# whole rule is about, and `write` is the wrong stream besides.
n_ewrite=$(grep -c 'io\.ewrite(' "$LOG_SRC")
n_two=$(grep -cE 'io\.(eprintln|println)\(' "$LOG_SRC")
n_stdout=$(grep -cE 'io\.write\(' "$LOG_SRC")

[ "$n_ewrite" -eq 1 ] || note "$LOG_SRC makes $n_ewrite write calls and a log line is ONE write — build the whole line, then write it once"
[ "$n_two" -eq 0 ] || note "$LOG_SRC calls io.eprintln/println, which is TWO writes: the text, then the newline"
[ "$n_stdout" -eq 0 ] || note "$LOG_SRC writes to standard output, which belongs to the program"
# And the newline is part of the LINE rather than a second write. The count above is what
# stops a second `io.ewrite`; this is what stops the newline being appended at the write site,
# where it would be one call today and two the next time somebody reaches for `eprintln`.
grep -qE 'io\.ewrite\(line\)$' "$LOG_SRC" || note "the write is no longer of a finished line — the newline belongs in the renderer, not at the write"
checks=$((checks + 1))

# --- 2. fatal writes its line and exits 1 ---------------------------------------------------

cat >"$WORK/fatal.zg" <<'ZG'
import "log"

fn main() {
	log.new().info().msg("before")
	log.new().fatal().str("why", "no").msg("stopping")
	log.new().info().msg("unreachable")
}
ZG

if build fatal; then
	"$WORK/fatal" >"$WORK/fatal.out" 2>"$WORK/fatal.err"
	status=$?
	[ "$status" -eq 1 ] || note "fatal exited $status, and the contract is 1"
	grep -q ' FTL stopping  why=no$' "$WORK/fatal.err" || note "fatal wrote no line: $(cat "$WORK/fatal.err")"
	grep -q ' INF before$' "$WORK/fatal.err" || note "the line before fatal is missing"
	grep -q 'unreachable' "$WORK/fatal.err" && note "fatal returned instead of exiting"
	# `fatal`'s own renderings, which the in-process suite cannot ask: rendering one ends the
	# process, so this is the only place `FTL` and `"l":"fatal"` are ever seen.
	ZERG_LOG=json "$WORK/fatal" >/dev/null 2>"$WORK/fatal.json"
	grep -q '"l":"fatal","msg":"stopping","why":"no"' "$WORK/fatal.json" ||
		note "fatal's JSON line is not what it should be: $(cat "$WORK/fatal.json")"
	checks=$((checks + 1))
fi

# --- 3. fatal at a silenced level STILL exits -----------------------------------------------

cat >"$WORK/fatal_off.zg" <<'ZG'
import "log"

fn main() {
	log.new().level(log.Level.OFF).fatal().msg("silenced")
	log.new().info().msg("unreachable")
}
ZG

if build fatal_off; then
	"$WORK/fatal_off" >"$WORK/fatal_off.out" 2>"$WORK/fatal_off.err"
	status=$?
	[ "$status" -eq 1 ] || note "fatal at OFF exited $status — silencing a logger must not change what a program DOES"
	[ -s "$WORK/fatal_off.err" ] && note "fatal at OFF wrote a line: $(cat "$WORK/fatal_off.err")"
	checks=$((checks + 1))
fi

# --- 4. the default destination is standard error -------------------------------------------

cat >"$WORK/streams.zg" <<'ZG'
import "log"

fn main() {
	print("this is the program's own output")
	log.new().info().msg("this is a log line")
}
ZG

if build streams; then
	"$WORK/streams" >"$WORK/streams.out" 2>"$WORK/streams.err"
	grep -q 'log line' "$WORK/streams.out" && note "a log line reached STDOUT, which is the program's own"
	grep -q "program's own output" "$WORK/streams.err" && note "the program's output reached stderr — the probe is wrong, not the logger"
	grep -q ' INF this is a log line$' "$WORK/streams.err" || note "no log line on stderr"
	grep -q "program's own output" "$WORK/streams.out" || note "the program printed nothing on stdout"
	checks=$((checks + 1))
fi

# --- 5. many writers: every line arrives, and arrives whole ----------------------------------
#
# What this CAN see (and the header says what it cannot): a line lost, a line truncated, a line
# carrying two writers' fields, and a logger that falls over when several coroutines share it.

cat >"$WORK/many.zg" <<ZG
import "log"

fn writer(id: int, n: int, done: chan[int]) {
	lg := log.new().with_int("writer", id)
	mut i := 0
	for i < n {
		lg.info().int("seq", i).str("pad", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa").msg("line")
		i = i + 1
	}
	done <- id
}

fn main() {
	done := chan[int]($WRITERS)
	mut w := 0
	for w < $WRITERS {
		spawn writer(w, $LINES, done)
		w = w + 1
	}
	mut got := 0
	for got < $WRITERS {
		if v := <-done {
			got = got + 1
		}
	}
}
ZG

if build many; then
	"$WORK/many" >/dev/null 2>"$WORK/many.err"
	status=$?
	[ "$status" -eq 0 ] || note "the many-writer program exited $status"

	want=$((WRITERS * LINES))
	total=$(wc -l <"$WORK/many.err" | tr -d ' ')
	[ "$total" -eq "$want" ] || note "$total lines came out and $want went in — a line was split or lost"

	shape='^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z INF line  writer=[0-9]+ seq=[0-9]+ pad=a+$'
	whole=$(grep -cE "$shape" "$WORK/many.err")
	if [ "$whole" -ne "$total" ]; then
		note "$((total - whole)) of $total lines are not whole"
		grep -vE "$shape" "$WORK/many.err" | head -5 >&2
	fi

	dup=$(grep -cE 'writer=[0-9]+.*writer=' "$WORK/many.err")
	[ "$dup" -eq 0 ] || note "$dup lines carry two writers' fields"
	checks=$((checks + 1))
fi

# --- 6. colour follows the terminal, and the format does not --------------------------------
#
# `script` is what gives a program a pty on both macOS and Linux, and its argument order
# differs between them — so the two spellings are tried and the case is SKIPPED, loudly, on a
# host with neither. Skipped rather than passed: a check that quietly reports success on a host
# it could not run is the shape this whole file exists to avoid.

cat >"$WORK/colour.zg" <<'ZG'
import "log"

fn main() {
	log.error().str("k", "v").msg("hello")
}
ZG

# on_a_pty <program> — run it with a pty for stdout AND stderr, echoing what came out.
on_a_pty() {
	if script -q /dev/null "$1" >"$WORK/pty.out" 2>&1; then
		return 0
	fi
	if script -q -c "$1" /dev/null >"$WORK/pty.out" 2>&1; then
		return 0
	fi
	return 1
}

esc=$(printf '\033')

if build colour; then
	# piped: no colour, pretty
	"$WORK/colour" >/dev/null 2>"$WORK/pipe.err"
	grep -q "$esc" "$WORK/pipe.err" && note "a piped log line carries ANSI colour — colour must follow the terminal"
	grep -q ' ERR hello  k=v$' "$WORK/pipe.err" || note "a piped line is not the plain pretty line: $(cat "$WORK/pipe.err")"

	# piped, ZERG_LOG=json: the format changed and nothing else did
	ZERG_LOG=json "$WORK/colour" >/dev/null 2>"$WORK/json.err"
	grep -q '^{"t":"[^"]*","l":"error","msg":"hello","k":"v"}$' "$WORK/json.err" ||
		note "ZERG_LOG=json did not pick the JSON format: $(cat "$WORK/json.err")"
	grep -q "$esc" "$WORK/json.err" && note "a JSON line carries ANSI colour, which is corruption and not decoration"

	# ZERG_LOG it does not know is pretty, not an error
	ZERG_LOG=wobble "$WORK/colour" >/dev/null 2>"$WORK/odd.err"
	status=$?
	[ "$status" -eq 0 ] || note "an unrecognised ZERG_LOG took the program down, exit $status"
	grep -q ' ERR hello' "$WORK/odd.err" || note "an unrecognised ZERG_LOG did not fall back to pretty"

	if on_a_pty "$WORK/colour"; then
		grep -q "$esc\[31mERR$esc\[0m" "$WORK/pty.out" ||
			note "a line written to a TERMINAL is not coloured: $(cat "$WORK/pty.out")"

		# NO_COLOR overrides the terminal
		NO_COLOR=1 on_a_pty "$WORK/colour"
		grep -q "$esc" "$WORK/pty.out" && note "NO_COLOR did not turn colour off at a terminal"

		# AND AN EXPORTED-EMPTY `NO_COLOR` COUNTS, which is the whole reason `env_colour` uses a
		# sentinel nobody exports rather than coalescing to "". It is also the one assertion
		# that catches the ORDER of `log.zg`'s module-level consts: a `const` reached through a
		# call is the ZERO value if it is declared later, silently, so a `NO_COLOR_UNSET` that
		# moved below `ENV_COLOUR` would become "" — and an exported-empty NO_COLOR would then
		# be indistinguishable from an unset one and colour would come back on.
		NO_COLOR='' on_a_pty "$WORK/colour"
		grep -q "$esc" "$WORK/pty.out" &&
			note "an exported-empty NO_COLOR did not turn colour off — see no-color.org, and check the const order in $LOG_SRC"

		# and the FORMAT does not follow the terminal: a pty gets pretty, exactly as a pipe did
		on_a_pty "$WORK/colour"
		grep -q '"l":"error"' "$WORK/pty.out" && note "the FORMAT followed the terminal — it must not; only ZERG_LOG picks it"

		# nor does ZERG_LOG stop working at one
		ZERG_LOG=json on_a_pty "$WORK/colour"
		grep -q '"l":"error"' "$WORK/pty.out" || note "ZERG_LOG=json was ignored at a terminal"
		grep -q "$esc" "$WORK/pty.out" && note "a JSON line at a terminal carries colour"
	else
		printf 'log-check: no `script` on this host, so the terminal half of case 6 was not run\n' >&2
	fi
	checks=$((checks + 1))
fi

# --- 7. ZERG_LOG_LEVEL picks the level, by name ---------------------------------------------
#
# THE SUITE CANNOT ASK THIS AT ALL, which is why it is here rather than there. `log.new()`
# reads the environment through module-level consts, so the answer is frozen before `main`
# runs and no test inside the process can vary it. Only a gate that sets an environment and
# then STARTS A PROGRAM can, and a claim nothing can ask is a claim nobody is keeping.
#
# It is read by NAME and never by number: `ZERG_LOG_LEVEL=debug`, not `=1`. A number would pin
# the enum's discriminants, which `log.zg` consults nowhere.

cat >"$WORK/level.zg" <<'ZG'
import "log"

fn main() {
	log.debug().msg("a debug line")
	log.info().msg("an info line")
	log.error().msg("an error line")
}
ZG

if build level; then
	# unset: the documented default is INFO — the debug line is absent and the other two are not
	"$WORK/level" >/dev/null 2>"$WORK/lvl.default"
	grep -q 'a debug line' "$WORK/lvl.default" && note "the default level wrote a DEBUG line — an unconfigured logger starts at INFO"
	grep -q ' INF an info line$' "$WORK/lvl.default" || note "the default level did not write an INFO line: $(cat "$WORK/lvl.default")"

	# by name, turning a level ON without touching the program
	ZERG_LOG_LEVEL=debug "$WORK/level" >/dev/null 2>"$WORK/lvl.debug"
	grep -q ' DBG a debug line$' "$WORK/lvl.debug" || note "ZERG_LOG_LEVEL=debug did not turn the debug line on: $(cat "$WORK/lvl.debug")"

	# and OFF, which writes nothing at all
	ZERG_LOG_LEVEL=off "$WORK/level" >/dev/null 2>"$WORK/lvl.off"
	[ -s "$WORK/lvl.off" ] && note "ZERG_LOG_LEVEL=off wrote a line: $(cat "$WORK/lvl.off")"

	# BY NAME AND NOT BY NUMBER. `1` is DEBUG's discriminant and must mean nothing here, or the
	# declaration order of the enum has quietly become a contract with every user's shell.
	ZERG_LOG_LEVEL=1 "$WORK/level" >/dev/null 2>"$WORK/lvl.num"
	grep -q 'a debug line' "$WORK/lvl.num" && note "ZERG_LOG_LEVEL=1 was read as a level — it must be a NAME, never a discriminant"

	# an unrecognised name is the default, not an error: a logger does not take a program down
	# over a misspelt variable
	ZERG_LOG_LEVEL=wobble "$WORK/level" >/dev/null 2>"$WORK/lvl.odd"
	status=$?
	[ "$status" -eq 0 ] || note "an unrecognised ZERG_LOG_LEVEL took the program down, exit $status"
	grep -q ' INF an info line$' "$WORK/lvl.odd" || note "an unrecognised ZERG_LOG_LEVEL did not fall back to INFO"
	grep -q 'a debug line' "$WORK/lvl.odd" && note "an unrecognised ZERG_LOG_LEVEL turned a level ON"

	# and the case of a name is not guessed at either
	ZERG_LOG_LEVEL=DEBUG "$WORK/level" >/dev/null 2>"$WORK/lvl.upper"
	grep -q 'a debug line' "$WORK/lvl.upper" && note "ZERG_LOG_LEVEL=DEBUG was accepted — the names are lower case, as a JSON line spells them"

	# AND IT DOES NOT REACH A TEST NOTE, which is this variable's blast radius rather than its
	# meaning. `testing.rendered` builds a `log.Logger` for every `ctx.log(…)`, and while that
	# logger inherited the level a `ZERG_LOG_LEVEL=warn` made the entry DEAD: nothing went into
	# a one-slot channel and the receive on it — whose only sender was the same frame — parked
	# forever. Every note in every suite in the tree hung the runner, over a variable that has
	# nothing to do with tests. A hang is not a failure any exit status reports, so this one is
	# asked with a deadline.
	mkdir -p "$WORK/note"
	cat >"$WORK/note/note_test.zg" <<'ZG'
import "testing"

#[test]
fn test_a_note_is_written_at_any_level(ctx: testing.Context) {
	ctx.log("a note")
	assert true
}
ZG
	ZERG_LOG_LEVEL=warn "$ZERG" test "$WORK/note" >"$WORK/note.out" 2>&1 &
	notepid=$!
	(
		sleep "$NOTE_TIMEOUT"
		kill -9 "$notepid" 2>/dev/null
	) >/dev/null 2>&1 &
	killer=$!
	wait "$notepid"
	kill "$killer" 2>/dev/null
	grep -q '1 passed' "$WORK/note.out" ||
		note "a ctx.log note under ZERG_LOG_LEVEL=warn hung or failed: $(tail -3 "$WORK/note.out")"
	checks=$((checks + 1))
fi

# --- 8. the pattern: one cell, one writer, readers that only delegate ------------------------
#
# `log` is this tree's REFERENCE for process-wide mutable state, so the shape is a claim it
# makes and not merely how it happens to be written today. The compiler enforces RULE 1 and
# only rule 1 — `E358` puts the cell inside the group and `E484` keeps it private, which is two
# codes for one rule rather than two rules. Rules 2 and 3 were properties of this source with
# nothing but a reader checking them. This is that reader.
#
# The fourth rule — configure at startup, then read — is about the CALLER and cannot be read
# off this file at all. It is stated in the comment, and nothing here can enforce it; saying so
# is part of stating it.

# The source with every comment line removed, so a `#` example cannot satisfy an assertion
# about code. (`[[:space:]]` and not `[ \t]`: POSIX grep reads the latter as a literal `t`,
# which silently left every indented doc comment in — exactly the shape of gate this file's
# own header warns about.)
code="$WORK/log.code"
grep -vE '^[[:space:]]*#' "$LOG_SRC" >"$code"

# ...and the module-level `unsafe { … }` group on its own, which is where the cell lives. A
# `mut` inside a function body is indented identically, so the group has to be carved out
# rather than matched by shape.
awk '/^unsafe \{$/ { inside = 1; next } inside && /^\}$/ { inside = 0; next } inside' "$code" >"$WORK/log.cell"

n_groups=$(grep -cE '^unsafe \{$' "$code")
[ "$n_groups" -eq 1 ] || note "$LOG_SRC has $n_groups module-level unsafe groups and the pattern has ONE"

n_cells=$(grep -cE '^	mut [a-z_]+ := ' "$WORK/log.cell")
[ "$n_cells" -eq 1 ] || note "$LOG_SRC holds $n_cells mutable globals and the pattern has ONE"

cell=$(sed -n 's/^	mut \([a-z_]*\) := .*/\1/p' "$WORK/log.cell")
case "$cell" in
log_*) ;;
*) note "the cell is called \`$cell\`; name it after the module — two modules that both call theirs \`process\` are E706, private or not" ;;
esac

# EVERY MENTION OF THE CELL IS ONE OF THREE THINGS: the declaration, the ONE assignment inside
# `install`, or a `return <cell>.…` delegation. A `log_cell.lvl = x` anywhere, or a second
# writer, or a reader that does something other than delegate, fails here — which is what turns
# rule 2 and rule 3 from a convention into a check.
bad=$(grep -nE "\\b$cell\\b" "$code" |
	grep -vE "^[0-9]+:	mut $cell := " |
	grep -vE "^[0-9]+:	$cell = lg$" |
	grep -vE "^[0-9]+:	return $cell\.[a-z_]+\(")
[ -z "$bad" ] || note "the cell is touched somewhere that is not the declaration, \`install\`, or a delegation:
$bad"

n_writes=$(grep -cE "^	$cell = " "$code")
[ "$n_writes" -eq 1 ] || note "the cell is written in $n_writes places — \`install\` is the one writer"

n_setters=$(grep -cE '^pub fn set_' "$code")
[ "$n_setters" -eq 0 ] || note "$LOG_SRC exports $n_setters \`set_*\` functions — the family was DELETED for one \`install\`, not renamed"

grep -qE '^pub fn rank\(' "$code" && note "\`rank\` is public — \`rank(OFF)\` is -1, and a caller comparing two of them reads OFF as the most verbose level there is"
grep -qE '^pub fn current\(' "$code" && note "\`current()\` is back — deriving from the installed logger reads as mid-flight reconfiguration, which the cell is not safe for"

# AND THE ONE CONSTRUCTOR IS `new`. `Logger()` exists whatever this module wants (`E482` makes
# every private field carry a default), and it is out of a caller's reach only for as long as
# the consts its defaults name stay private. Publishing one would silently re-open a second
# constructor, so the refusal is compiled here rather than described in a comment.
cat >"$WORK/ctor.zg" <<'ZG'
import "log"

fn main() {
	print(log.Logger().enabled(log.Level.INFO))
}
ZG

rm -f "$WORK/ctor"
if "$ZERG" build "$WORK/ctor.zg" -o "$WORK/ctor" >"$WORK/ctor.log" 2>&1; then
	note "log.Logger() built outside the module — it is a second constructor that skips \`new\`"
else
	grep -q 'E301' "$WORK/ctor.log" ||
		note "log.Logger() was refused, but not by E301: $(head -2 "$WORK/ctor.log")"
fi
checks=$((checks + 1))

[ "$fail" -eq 0 ] || {
	printf 'log-check: the sources and streams are kept in %s\n' "$WORK" >&2
	trap - EXIT
	exit 1
}

[ "$checks" -eq 8 ] || {
	printf 'log-check: only %s of 8 cases ran — a probe stopped building\n' "$checks" >&2
	exit 1
}

printf 'log-check: one write per line in the source, fatal exits 1 and still writes, the default stream is stderr, colour follows the terminal and the format does not, ZERG_LOG_LEVEL names a level, the cell has one writer and %s lines from %s coroutines each arrive whole\n' \
	"$((WRITERS * LINES))" "$WRITERS"
