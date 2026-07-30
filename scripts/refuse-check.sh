#!/usr/bin/env bash
#
# refuse-check — every program here must be turned away BY THE COMPILER, by name.
#
# The gate is not that these fail. Most of them always failed; what differed is who said
# so. A program the compiler emits anyway reaches cc, which rejects generated C at a line
# number in a file under .zerg-cache that nobody wrote — a real error reported against a
# file the programmer cannot open. So each case asserts three things: a non-zero exit, the
# expected sentence, and NO mention of the cache. The third is the one that regresses
# silently, because a case that starts being emitted again still "fails".
#
# Cases are written here rather than in the test-data corpus because that corpus is a set
# of programs that must RUN; these are programs that must not build.

set -u

ZERG=${ZERG:-./bin/zerg}
ZERG0=${ZERG0:-./bin/zerg0}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

pass=0
fail=0

# expect <compiler> <name> <wanted-substring> — the program arrives on stdin.
expect() {
	local cc=$1 name=$2 want=$3
	local src="$tmp/$name.zg"
	cat >"$src"

	local out status
	out=$("$cc" build --emit c "$src" 2>&1 >/dev/null)
	status=$?

	if [ $status -eq 0 ]; then
		echo "ACCEPTED  $name — the compiler emitted it instead of refusing"
		fail=$((fail + 1))
		return
	fi
	case $out in
	*"$want"*) ;;
	*)
		echo "MESSAGE   $name — wanted \"$want\", got: $(echo "$out" | head -1)"
		fail=$((fail + 1))
		return
		;;
	esac
	case $out in
	*.zerg-cache*)
		echo "VIA CC    $name — cc reported it against generated C, not the compiler against the source"
		fail=$((fail + 1))
		return
		;;
	esac
	pass=$((pass + 1))
}

# --- the shipped compiler ---------------------------------------------------------

expect "$ZERG" break-outside-loop "outside of a loop" <<'EOF'
fn main() {
	print "a"
	break
}
EOF

expect "$ZERG" break-in-select-arm "outside of a loop" <<'EOF'
fn gen(out: chan[int]<-) { out <- 1 }
fn main() {
	a := chan[int](1)
	spawn gen(a)
	select {
		v := <-a => { print v! }
		done     => { break }
	}
}
EOF

# GRAMMAR derives an or-pattern; neither compiler lowers one, and `|` in pattern position is
# read as the bitwise operator — so `1 | 2` folded to `3` and the arm matched neither side,
# compiled and run. Refusing it is the whole difference between a gap and a wrong answer.
expect "$ZERG" or-pattern-in-a-match-arm "an or-pattern" <<'EOF'
fn f(n: int) -> str {
	return match n {
		1 | 2 => "lo"
		_ => "hi"
	}
}

fn main() {
	print f(3)
}
EOF

# An open-ended range is a legal RANGE ARM (`20.. =>`), and nothing else yet. The missing bound
# reads as nil, and `c_expr(ENil)` is "0" — so a `for` over one ran zero times and a slice came
# back empty, both in silence.
expect "$ZERG" open-range-in-a-for "needs an upper bound" <<'EOF'
fn main() {
	mut n := 0
	for i in 20.. {
		n = n + i
	}
	print n
}
EOF

expect "$ZERG" open-range-in-a-slice "needs an upper bound" <<'EOF'
fn main() {
	xs := [1, 2, 3]
	ys := xs[1..]
	print ys.len()
}
EOF

expect "$ZERG" undeclared-type-in-result "no type named \`Ref\`" <<'EOF'
fn mk(v: int) -> Ref[int] {
	return Ref(v)
}
fn main() { print "x" }
EOF

expect "$ZERG" undeclared-type-in-param "no type named \`Ref\`" <<'EOF'
fn load(a: Ref[int]) -> int {
	return 0
}
fn main() { print "x" }
EOF

expect "$ZERG" generic-field-type "no type named \`T\`" <<'EOF'
struct B[T] {
	n: T
}
fn main() { print "x" }
EOF

# Left and Right name the two sides of the carrier a receive answers with, and neither
# compiler offers them as constructors: a Result[T] comes from `<-ch` or from `guard`.
expect "$ZERG" carrier-is-not-a-constructor "no type named \`Left\` to construct" <<'EOF'
fn f() -> Result[int] {
	return Left(1)
}
fn main() { print "x" }
EOF

# --- the seed ---------------------------------------------------------------------

# Tier 2 of src/bootstrap/README.md: the seed carries no opinion about concurrency, because
# the self-host chain contains none of it and a compiler that lowers what it is not the
# authority on is a second implementation that can disagree.
expect "$ZERG0" seed-concurrency "the bootstrap seed does not lower" <<'EOF'
fn feed(o: chan[int]) {
	o <- 1
}
fn main() {
	ch := chan[int](1)
	spawn feed(ch)
	print (<-ch)!
}
EOF

expect "$ZERG0" seed-close-on-receive-only "cannot close a receive-only channel <-chan[int]" <<'EOF'
fn bad(rx: <-chan[int]) {
	defer close(rx)
}
fn main() { print "x" }
EOF

expect "$ZERG0" seed-send-on-receive-only "cannot send on a receive-only channel <-chan[int]" <<'EOF'
fn bad(rx: <-chan[int]) { rx <- 1 }
fn main() { print "x" }
EOF

expect "$ZERG0" seed-recv-on-send-only "cannot receive from a send-only channel chan[int]<-" <<'EOF'
fn bad(tx: chan[int]<-) { print (<-tx)! }
fn main() { print "x" }
EOF



# --- null safety: an optional says what it is at every edge ------------------------

expect "$ZERG" optional-into-a-value "unwrap it with" <<'EOF'
fn main() {
	x: int? = 5
	y: int = x
	print y
}
EOF

expect "$ZERG" print-of-an-optional "may not have one" <<'EOF'
fn main() {
	x: int? = nil
	print x
}
EOF

expect "$ZERG" chain-through-a-value "is not one" <<'EOF'
struct P {
	n: int
}
fn main() {
	p := P(1)
	print p?.n
}
EOF

expect "$ZERG" missing-required-field "needs a value for field" <<'EOF'
struct P {
	n: int
	m: int
}
fn main() {
	p := P(1)
	print p.n
}
EOF

expect "$ZERG" try-without-an-optional-result "must answer a \`T?\`" <<'EOF'
fn head(x: int?) -> int {
	return x?
}
fn main() { print head(1) }
EOF

# --- lint: what the toolchain must SAY something about ----------------------------
#
# A finding is not a refusal — these programs compile and run — so they are checked
# through `zerg lint`, which exits non-zero when it has something to report. Same three
# assertions: it must speak, it must say the expected thing, and it must be the compiler
# saying it.
expect_lint() {
	local name=$1 want=$2
	local src="$tmp/$name.zg"
	cat >"$src"

	local out status
	out=$("$ZERG" lint "$src" 2>&1)
	status=$?

	if [ $status -eq 0 ]; then
		echo "SILENT    $name — lint had nothing to say"
		fail=$((fail + 1))
		return
	fi
	case $out in
	*"$want"*) pass=$((pass + 1)) ;;
	*)
		echo "MESSAGE   $name — wanted \"$want\", got: $(echo "$out" | head -1)"
		fail=$((fail + 1))
		;;
	esac
}

expect_lint coalesce-with-nil "L201" <<'EOF'
fn keep(x: int?) -> int? {
	return x ?? nil
}
fn main() { print keep(1) ?? -1 }
EOF

expect_lint force-where-try-fits "L202" <<'EOF'
fn forced(x: int?) -> int? {
	return x!
}
fn main() { print forced(2) ?? -1 }
EOF

if [ $fail -ne 0 ]; then
	echo "refuse-check: $fail of $((pass + fail)) cases were not refused as they should be"
	exit 1
fi
echo "refuse-check: $pass cases refused by name, none left to cc"
