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
		v := <-a => { print v }
		_        => { break }
	}
}
EOF

# The terminal arm is gone: a select PICKS a ready arm and the loop ENDS. `close` in that
# position was an arm until this change, so it is refused by name rather than read as
# something else — and the message names the form that replaced it.
expect "$ZERG" terminal-arm-in-a-select "is not a select arm head" <<'EOF'
fn main() {
	ch := chan[int](1)
	select {
		v := <-ch => print v
		close => print 0
	}
}
EOF

expect "$ZERG0" seed-terminal-arm-in-a-select "is not a select arm head" <<'EOF'
fn main() {
	ch := chan[int](1)
	select {
		v := <-ch => print v
		close => print 0
	}
}
EOF

# A receive answers `T?`, so a channel of optionals would make `nil` mean both "the value
# that was sent" and "the stream is over".
# GRAMMAR says `select-arm+`, and the reason is not pedantry: an empty select parks on no
# channel, so it never wakes — a hang with no cause to find. Both compilers say so.
# A cause chain is made of Errs. The `from` operand used to be read as whatever C type it
# happened to be and handed straight to the runtime, which takes a pointer — so an int cause
# came out as a cc warning about a generated file, not a Zerg diagnostic.
expect "$ZERG" from-cause-that-is-not-an-err "a \`from\` cause is an \`Err\`" <<'EOF'
fn f(n: int) -> int {
	raise ValueError("x") from n
}

fn main() {
	print f(1)
}
EOF

# A jump out of a guard would leave its handler installed on a frame that has returned.
expect "$ZERG" jump-out-of-a-guard "leaving a \`guard\` block" <<'EOF'
fn f() -> int {
	r := guard {
		return 1
	}
	return r ?? -1
}

fn main() {
	print f()
}
EOF

# The seed LOWERS guard, so it has to agree about this one. It used to accept the jump and
# emit a program that printed the right answer while leaving the handler installed on a frame
# that had returned — the next abort anywhere in the caller would longjmp into a stack frame
# that is gone.
expect "$ZERG0" seed-jump-out-of-a-guard "leaving a \`guard\` block" <<'EOF'
fn f(n: int) -> int {
	r := guard {
		if n > 0 {
			return 99
		}
		n
	}
	return r ?? -1
}

fn main() {
	print f(5)
}
EOF

expect "$ZERG" empty-select "at least one arm" <<'EOF'
fn main() {
	for select {
	}
}
EOF

expect "$ZERG0" seed-empty-select "at least one arm" <<'EOF'
fn main() {
	select {
	}
}
EOF

expect "$ZERG" channel-of-optionals "a channel of optionals is refused" <<'EOF'
fn main() {
	ch := chan[int?](1)
	print 1
}
EOF

# A select arm head is `_`, a receive or a send — and nothing else. This used to be
# accepted: ANY identifier before `=>` became the `_` arm, so a typo (or the old `done`
# spelling) silently made the select non-blocking AND dropped its terminal arm. Both compilers
# refuse it now, which is why it is checked twice.
expect "$ZERG" select-arm-head-typo "is not a select arm head" <<'EOF'
fn gen(out: chan[int]<-) { out <- 1 }
fn main() {
	a := chan[int](1)
	spawn gen(a)
	for {
		select {
			v := <-a => print v!
			closed => break
		}
	}
}
EOF

expect "$ZERG0" seed-select-arm-head-typo "is not a select arm head" <<'EOF'
fn gen(out: chan[int]<-) { out <- 1 }
fn main() {
	a := chan[int](1)
	spawn gen(a)
	for {
		select {
			v := <-a => print v!
			closed => break
		}
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

# A match lowers to a chain of ternaries whose LAST arm is the final `else`, untested. So a
# match missing a case never failed — it answered the last arm's body for everything nothing
# else matched: `C` came back "b" below, and `f(3)` came back "b" in the int case. A wrong
# answer with no diagnostic is the outcome this repo does not tolerate, and the seed has
# refused both since it had enums.
expect "$ZERG" non-exhaustive-enum-match "missing variant K.C" <<'EOF'
enum K {
	A
	B
	C
}

fn name(k: K) -> str {
	return match k {
		A => "a"
		B => "b"
	}
}

fn main() {
	print name(A)
}
EOF

expect "$ZERG" non-exhaustive-int-match "missing a catch-all" <<'EOF'
fn f(n: int) -> str {
	return match n {
		1 => "a"
		2 => "b"
	}
}

fn main() {
	print f(3)
}
EOF

# A guard makes an arm conditional, so it covers nothing: the compiler cannot prove the guard
# holds (GRAMMAR:410), and `A` below is uncovered even though it is named.
expect "$ZERG" guarded-arm-covers-nothing "missing variant K.A" <<'EOF'
enum K {
	A
	B
}

fn f(k: K) -> str {
	return match k {
		A if true => "a"
		B => "b"
	}
}

fn main() {
	print f(A)
}
EOF

# An arm after an unguarded catch-all is dead, and worse: the lowering hands the LAST arm the
# `else`, so the arm that can never match is the one that runs by default.
expect "$ZERG" arm-after-the-catch-all "makes the following arms unreachable" <<'EOF'
enum K {
	A
	B
}

fn f(k: K) -> str {
	return match k {
		_ => "rest"
		A => "a"
	}
}

fn main() {
	print f(B)
}
EOF

# A discriminant belongs to a C-style integer enum, and only to one (GRAMMAR:573): a payload
# enum's tag is opaque and match-only, so neither direction of the reading is offered on it.
expect "$ZERG" discriminant-of-a-payload-enum "tag is opaque" <<'EOF'
enum E {
	P(int)
	Q
}

fn main() {
	print int(Q)
}
EOF

expect "$ZERG" reverse-of-a-payload-enum "tag is opaque" <<'EOF'
enum E {
	P(int)
	Q
}

fn main() {
	print E.of(1) ?? Q
}
EOF

expect "$ZERG" discriminant-on-a-payload-declaration "a discriminant" <<'EOF'
enum E {
	P(int) = 3
	Q
}

fn main() {
	print "x"
}
EOF

expect "$ZERG" repeated-discriminant "repeats one already given" <<'EOF'
enum K {
	A = 1
	B = 1
}

fn main() {
	print int(A)
}
EOF

# Every carrier in this backend has a `tag`, so a variant pattern applied to the wrong type
# COMPILED and compared unrelated numbers. Over a `K?` the optional's present-tag is 0 and
# the first variant's tag is 0, so this took the `A` arm for every present value.
expect "$ZERG" variant-pattern-on-an-optional "cannot match a subject of type" <<'EOF'
enum K {
	A
	B
}

fn main() {
	k := K.of(1)
	print match k {
		A => "a"
		_ => "other"
	}
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

# Left and Right name the two SIDES of an Either and have no type of their own, so they are
# read where the wanted type is known. Written where there is none, the compiler says which
# of the two problems it is — a form used without its context, not a form that does not exist.
expect "$ZERG" carrier-constructor-without-a-context "needs a declared one to be" <<'EOF'
fn main() {
	x := Left(1)
	print 1
}
EOF

# The two sides of an Either must DIFFER: an injection could otherwise reach both, and
# nothing at the match would tell which one it took.
expect "$ZERG" either-with-equal-sides "has the same type on both sides" <<'EOF'
fn f(n: int) -> Either[int, int] {
	return Left(n)
}
fn main() { print 1 }
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

expect "$ZERG" try-without-a-carrier-result "must answer a carrier with the same right" <<'EOF'
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
