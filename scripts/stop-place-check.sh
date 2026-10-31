#!/usr/bin/env bash
#
# stop-place-check — a stop in a running program names the `.zg` statement that reached it, and
# `zerg build -g` lets a debugger resolve a Zerg line (docs/conformance.md, "Runtime abort
# contract").
#
# Each case writes its own program and names the ONE line it must report. What makes these
# worth having is that the program always holds a second statement the answer could wrongly
# be: the place is read off an address, so what can go wrong is the record just before the
# address being somebody else's. Every case is a shape that puts another statement's record
# there — a call that returned, a block that ended, an `elif` behind its `then`.
#
# The debugger half asks without launching a process, because a CI runner may refuse a debugger
# that attaches: lldb resolves a breakpoint from the file alone, and on Linux the line table is
# readable with objdump.

set -u

ROOT=$(cd "$(dirname "$0")/.." && pwd)
ZERG=$ROOT/bin/zerg
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cd "$tmp" || exit 2

export ZERG_CACHE="$tmp/cache"

pass=0
fail=0
FLOOR=22

note() {
	echo "stop-place: $1"
	fail=$((fail + 1))
}

# expect <name> <kind-line> <line:col> — program text on stdin. The stderr has to be exactly
# the kind line and then the place: a place that moved, or an extra line, is a failure.
expect() {
	local name=$1 kind=$2 at=$3 got
	cat >"$name.zg"
	if ! "$ZERG" build "$name.zg" -o "$name.bin" >"$name.build" 2>&1; then
		note "$name did not build: $(head -1 "$name.build")"
		return
	fi
	"./$name.bin" >/dev/null 2>"$name.err"
	local rc=$?
	got=$(cat "$name.err")
	if [ "$rc" != 1 ]; then
		note "$name exited $rc, wanted 1"
	elif [ "$got" != "$kind
  --> $name.zg:$at" ]; then
		note "$name wanted $at, got: $got"
	else
		pass=$((pass + 1))
	fi
}

expect index_in_callee 'IndexError: index out of range' 2:5 <<'ZG'
fn at(xs: list[int], i: int) -> int {
    return xs[i]
}

fn main() {
    print at([1, 2], 5)
}
ZG

# the call's own statements ran first, and their records lie nearer than a stale store would be
expect after_a_call 'IndexError: index out of range' 7:5 <<'ZG'
fn f(n: int) -> int {
    m := n + 1
    return m
}

fn g(xs: list[int], a: int) -> int {
    return f(a) + xs[a + 5]
}

fn main() {
    print g([1], 1)
}
ZG

# an `else if` is a statement of its own, so the condition behind the `then` has its own place
expect elif_condition 'IndexError: index out of range' 5:12 <<'ZG'
fn pick(xs: list[int], a: int) -> int {
    mut r := 0
    if a > 5 {
        r = 1
    } else if xs[a + 5] > 0 {
        r = 2
    }
    return r
}

fn main() {
    print pick([1], 1)
}
ZG

# the condition runs again after the body, whose last statement is the record behind it only if
# the text were laid out that way: this is the case that says it is not
expect loop_condition 'IndexError: index out of range' 4:5 <<'ZG'
fn walk(xs: list[int]) -> int {
    mut i := 0
    mut sum := 0
    for xs[i] > 0 {
        sum = sum + 1
        i = i + 1
    }
    return sum
}

fn main() {
    print walk([1, 2])
}
ZG

expect block_in_expression 'IndexError: index out of range' 2:5 <<'ZG'
fn f(xs: list[int], a: int) -> int {
    x := (match a {
        0 => 0
        _ => {
            b := a + 1
            b
        }
    }) + xs[a + 5]
    return x
}

fn main() {
    print f([1], 1)
}
ZG

expect after_a_guard 'IndexError: index out of range' 6:5 <<'ZG'
fn keep(r: Result[int], b: int) -> int {
    return b
}

fn f(xs: list[int], a: int) -> int {
    return keep(guard {
        y := xs[a + 7]
        y
    }, xs[a + 5])
}

fn main() {
    print f([1], 1)
}
ZG

# a raise that ends a block is the block's last instruction on gcc, and the statement below is next
expect raise_ends_a_block 'ValueError: boom' 3:9 <<'ZG'
fn f(a: int) -> int {
    if a > 0 {
        raise ValueError("boom")
    }
    return a
}

fn main() {
    print f(1)
}
ZG

expect division 'DivideByZeroError: division by zero' 2:5 <<'ZG'
fn f(a: int, b: int) -> int {
    return a / b
}

fn main() {
    print f(1, 0)
}
ZG

# the runtime's void call is the statement's last instruction, so its return address IS the next
# statement's record: looked up as it is, this names `print 9`
expect send_ends_a_statement 'SendOnClosedError: send on a closed channel' 4:5 <<'ZG'
fn main() {
    c := chan[int](1)
    close(c)
    c <- 1
    print 9
}
ZG

# a coroutine resumes on whichever worker is free: the channel wait in front of the abort is
# what a place held per thread would lose
cat >coro.zg <<'ZG'
fn work(c: chan[int], d: chan[int], xs: list[int]) {
    v := (<-c)!
    print xs[v]
    d <- 0
}

fn other(c: chan[int]) {
    n := 1
    c <- n + 4
}

fn main() {
    c := chan[int](0)
    d := chan[int](0)
    spawn other(c)
    spawn work(c, d, [1])
    _ := <-d
}
ZG
if "$ZERG" build coro.zg -o coro.bin >coro.build 2>&1; then
	./coro.bin >/dev/null 2>coro.err
	if grep -qF '  --> coro.zg:3:5' coro.err; then
		pass=$((pass + 1))
	else
		note "coro wanted 3:5, got: $(cat coro.err)"
	fi
else
	note "coro did not build: $(head -1 coro.build)"
fi

# two modules: the place is the file the statement is in, not the entry's
mkdir -p two
cat >two/low.zg <<'ZG'
pub fn at(xs: list[int], i: int) -> int {
    return xs[i]
}
ZG
cat >two/main.zg <<'ZG'
import "./low"

fn main() {
    print low.at([1], 5)
}
ZG
if (cd two && "$ZERG" build main.zg -o main.bin) >two.build 2>&1; then
	(cd two && ./main.bin) >/dev/null 2>two.err
	if grep -qE '  --> ([^ ]*/)?low\.zg:2:5$' two.err; then
		pass=$((pass + 1))
	else
		note "the imported module's file was not named: $(cat two.err)"
	fi
else
	note "two modules did not build: $(head -1 two.build)"
fi

# overflow: from the signal handler, on main's stack and on a coroutine's. Mutual recursion, so
# that naming the wrong one of the two functions is a thing that can happen; either statement
# is honest, since where the page runs out depends on frame sizes, but nothing else is.
cat >over.zg <<'ZG'
fn a(n: int) -> int {
    return b(n + 1) + 1
}

fn b(n: int) -> int {
    return a(n + 1) + 1
}

fn main() {
    print a(0)
}
ZG
cat >overco.zg <<'ZG'
fn a(n: int) -> int {
    return a(n + 1) + 1
}

fn run(d: chan[int]) {
    d <- a(0)
}

fn main() {
    d := chan[int](0)
    spawn run(d)
    _ := <-d
}
ZG
for t in 'over:2:5|6:5' 'overco:2:5'; do
	name=${t%%:*}
	want=${t#*:}
	if ! "$ZERG" build "$name.zg" -o "$name.bin" >"$name.build" 2>&1; then
		note "$name did not build: $(head -1 "$name.build")"
		continue
	fi
	"./$name.bin" >/dev/null 2>"$name.err"
	rc=$?
	if [ "$rc" = 1 ] && [ "$(head -1 "$name.err")" = 'StackOverflowError: stack overflow' ] &&
		sed -n 2p "$name.err" | grep -qE "^  --> $name\.zg:($want)\$"; then
		pass=$((pass + 1))
	else
		note "$name exited $rc, wanted a place in ($want), got: $(cat "$name.err")"
	fi
done

# what a guard catches was never reported, and the place does not ride in the message: a program
# that could read it could depend on it
cat >caught.zg <<'ZG'
fn f(xs: list[int]) -> int {
    return xs[5]
}

fn main() {
    r := guard {
        f([1])
    }
    s := match r {
        Either.Left(_)  => "no abort"
        Either.Right(e) => e.message()
    }
    print s
}
ZG
if "$ZERG" build caught.zg -o caught.bin >caught.build 2>&1; then
	./caught.bin >caught.out 2>caught.err
	if [ -s caught.err ] || [ "$(cat caught.out)" != 'index out of range' ]; then
		note "a caught abort wrote [$(cat caught.err)] and read [$(cat caught.out)]"
	else
		pass=$((pass + 1))
	fi
else
	note "caught did not build: $(head -1 caught.build)"
fi

# a statement behind a `return` is dead text, and a label in it is what gcc moves up
expect dead_text 'IndexError: index out of range' 3:9 <<'ZG'
fn f(xs: list[int], a: int) -> int {
    if a > 0 {
        return xs[a + 5]
    } else {
        return 0
    }
}

fn main() {
    print f([1], 1)
}
ZG

# the fault is raised inside `io`, and the statement it is about is the caller's
expect through_the_stdlib 'IOError: cannot open file' 4:5 <<'ZG'
import "io"

fn main() {
    b := io.read_file("/no/such/file")
    print b.len()
}
ZG

# no kind either, but no sentence of the program's: the fault is in the statement
expect force_unwrap 'force-unwrap of nil' 3:5 <<'ZG'
fn main() {
    o: int? = nil
    print o!
}
ZG

# an error with no kind is a sentence the program wrote for its user, and the compiler's own
# refusals are this: one place, in the user's file, and none in the compiler's source
cat >bare.zg <<'ZG'
fn main() {
    raise "stop here"
}
ZG
if "$ZERG" build bare.zg -o bare.bin >bare.build 2>&1; then
	./bare.bin >/dev/null 2>bare.err
	if [ "$(cat bare.err)" != 'stop here' ]; then
		note "a bare raise wrote: $(cat bare.err)"
	else
		pass=$((pass + 1))
	fi
else
	note "bare did not build: $(head -1 bare.build)"
fi
printf 'fn main() {\n    close c\n}\n' >refused.zg
"$ZERG" build refused.zg -o refused.bin >refused.err 2>&1
if [ "$(grep -c '^  --> ' refused.err)" != 1 ] || ! grep -qF '  --> refused.zg:' refused.err; then
	note "a compile refusal should name one place, in its own file: $(cat refused.err)"
else
	pass=$((pass + 1))
fi

# --- zerg build -g ------------------------------------------------------------------------

# resolves <binary> <file> <line> — the line maps to code. Answers 2 with no tool to ask.
resolves() {
	if [ "$(uname)" = Darwin ]; then
		command -v lldb >/dev/null 2>&1 || return 2
		lldb -b -o "breakpoint set --file $2 --line $3" -- "$1" 2>&1 |
			grep -qE '^Breakpoint 1: ([0-9]+ locations|where = )'
	else
		command -v objdump >/dev/null 2>&1 || return 2
		objdump --dwarf=decodedline "$1" 2>/dev/null | grep -qE "^$2 +$3 "
	fi
}

cat >dbg.zg <<'ZG'
fn at(xs: list[int], i: int) -> int {
    return xs[i]
}

fn main() {
    print at([1, 2], 1)
}
ZG

# the same source, the same cache, in both orders: the object must not be read back across -g,
# which would leave a breakpoint pending with nothing to say why
"$ZERG" build dbg.zg -o plain1.bin >/dev/null 2>&1 || note "the plain build failed"
"$ZERG" build -g dbg.zg -o debug.bin >/dev/null 2>&1 || note "the -g build failed"
"$ZERG" build dbg.zg -o plain2.bin >/dev/null 2>&1 || note "the second plain build failed"

resolves debug.bin dbg.zg 2
case $? in
0) pass=$((pass + 1)) ;;
2) note "no debugger or objdump to ask, and this gate does not pass on an unasked question" ;;
*) note "under -g, dbg.zg:2 does not resolve to code" ;;
esac
for b in plain1 plain2; do
	if resolves "$b.bin" dbg.zg 2; then
		note "$b resolved a line with no -g: the debug object came back from the cache"
	else
		pass=$((pass + 1))
	fi
done

if [ "$pass" -lt "$FLOOR" ] && [ "$fail" = 0 ]; then
	note "$pass cases passed, below the floor of $FLOOR"
fi
if [ "$fail" != 0 ]; then
	echo "stop-place: $fail failed, $pass passed"
	exit 1
fi
echo "stop-place: $pass cases name the statement, and -g resolves a Zerg line"
