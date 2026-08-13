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

# shellcheck source=scripts/lib/diag.sh
. "$(dirname "$0")/lib/diag.sh"

ZERG=${ZERG:-./bin/zerg}
ZERG0=${ZERG0:-./bin/zerg0}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

pass=0
fail=0

# expect <compiler> <name> <code|sentence> [sentence] — the program arrives on stdin.
#
# A PLACE IS NO LONGER A FLAG. It was `place`, opt-in, and it had to be while the emitter's
# raises said only what and never where — the flag went from 73 to 149 the day the parser
# moved onto one channel, and each of those 149 was somebody remembering. All three stages
# report through a channel now (chk_at in check.zg, p_diag in parser.zg, c_diag in emit.zg),
# so the assertion is made of every `zerg` case below and there is no marker to forget. What
# a marker bought was the ability to leave one out, and there is nothing left to leave out.
#
# The seed is exempt for the reason its wording is: it is the tool that builds the shipping
# compiler, and its diagnostics are not part of the language's contract.
expect() {
	local cc=$1 name=$2 want=$3
	shift 3

	# A SENTENCE MAY FOLLOW THE CODE, and it is there for one job: telling two cases of the
	# same rule apart. The code says which rule fired, so a case that is the only one of its
	# code needs nothing further — pinning its prose would only mean this turns red the next
	# time the prose gets better.
	#
	# WHATEVER FOLLOWS THE CODE IS THAT SENTENCE, and nothing else. It used to be told from a
	# MARKER by shape — a marker is one word, a sentence has a space in it — the way
	# reject-check still does it, and with `place` gone there is no marker here for the shape
	# rule to sort. What the rule was doing instead was dropping assertions on the floor:
	# `` '`#[obj]`' `` has no space in it, so it read as a marker, and the `flags` variable
	# that caught it was read by nothing. Five cases asserted their sentence and were checked
	# against none of it.
	local says=${1:-}
	shift || true

	# and an argument after it is a mistake rather than a marker. This is the half that keeps
	# the paragraph above true: a `place` typed out of habit, or a marker invented for a
	# future rule, would otherwise be accepted in silence — which is exactly how the five
	# above came to assert nothing.
	if [ $# -ne 0 ]; then
		echo "ARGS      $name — unrecognised argument \"$1\"; a case is <compiler> <name> <code|sentence> [sentence]"
		fail=$((fail + 1))
		return
	fi

	local src="$tmp/$name.zg"
	cat >"$src"

	# `--emit bin`, not `--emit c`. The C stage stops BEFORE cc, so under it the two
	# assertions below about cc can NEVER FIRE — a program the compiler emits anyway and
	# only cc rejects looks ACCEPTED, and the cache path this script was written to watch
	# for cannot appear because cc never runs. That was true of all 75 cases here from the
	# day the script was written, and it was only noticed when reject-check.sh was written
	# beside it and had to answer the same question. Linking for real costs nothing while
	# the gate is green: a program the compiler refuses never reaches cc at all.
	local out status
	out=$("$cc" build --emit bin -o "$tmp/$name.bin" "$src" 2>&1 >/dev/null)
	status=$?

	if [ $status -eq 0 ]; then
		echo "ACCEPTED  $name — the compiler emitted it instead of refusing"
		fail=$((fail + 1))
		return
	fi
	if is_crash "$status"; then
		echo "CRASHED   $name — the compiler died of signal $((status - 128)) instead of refusing"
		fail=$((fail + 1))
		return
	fi
	# A CODE, not a sentence, when the shipping compiler is the one being asked. `E107` is
	# the identity of a refusal and the sentence beside it is prose that may be improved
	# without a gate turning red — which is the whole reason a code exists. A `want` that
	# looks like a code is compared as a code, by the one predicate that knows where this
	# compiler puts a code in a line (scripts/lib/diag.sh).
	#
	# The seed keeps sentence matching. Codes are the LANGUAGE's contract and the seed is
	# the tool that builds the shipping compiler, so its diagnostics are not part of it —
	# the same line docs/conformance.md draws when it declines to mark the seed's gaps.
	case $want in
	E[0-9][0-9][0-9])
		if ! opens_with_code "$out" "$want"; then
			echo "CODE      $name — wanted $want to open the message, got: $(echo "$out" | head -1)"
			fail=$((fail + 1))
			return
		fi
		;;
	*)
		# THE SHIPPING COMPILER'S CASES PIN A CODE, and this is what keeps that true as cases
		# are added: prose is what every case here used to assert, and a list that is mostly
		# codes with a few sentences left in it looks finished from the outside.
		if [ "$cc" = "$ZERG" ]; then
			echo "PROSE     $name — a \`zerg\` case asserts the rule's code, not its wording"
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
		;;
	esac
	if [ -n "$says" ]; then
		case $out in
		*"$says"*) ;;
		*)
			echo "MESSAGE   $name — wanted \"$says\", got: $(echo "$out" | head -1)"
			fail=$((fail + 1))
			return
			;;
		esac
	fi
	# CC MUST NOT BE THE ONE ANSWERING. The cache path is not the only shape a cc error takes
	# — a build given `-o` puts its intermediate C beside the output, and `#line` directives
	# point cc at the `.zg` the programmer wrote — so the SHAPE of the line is the tell that
	# survives both. Both questions are one predicate in diag.sh, asked the same way by the
	# three gates that judge a refusal; this script asked one of them for years before
	# reject-check.sh was written beside it and asked the other.
	if cc_answered "$out"; then
		echo "VIA CC    $name — cc answered this, not the compiler against the source"
		fail=$((fail + 1))
		return
	fi

	if [ "$cc" = "$ZERG" ] && ! has_place "$out"; then
		echo "NO PLACE  $name — the refusal does not say where: $(echo "$out" | head -1)"
		fail=$((fail + 1))
		return
	fi

	pass=$((pass + 1))
}

# --- the shipped compiler ---------------------------------------------------------

expect "$ZERG" break-outside-loop E401 <<'EOF'
fn main() {
	print "a"
	break
}
EOF

expect "$ZERG" break-in-select-arm E401 <<'EOF'
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
expect "$ZERG" terminal-arm-in-a-select E201 <<'EOF'
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
expect "$ZERG" from-cause-that-is-not-an-err E402 <<'EOF'
fn f(n: int) -> int {
	raise ValueError("x") from n
}

fn main() {
	print f(1)
}
EOF

# A jump out of a guard would leave its handler installed on a frame that has returned.
expect "$ZERG" jump-out-of-a-guard E403 <<'EOF'
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

expect "$ZERG" empty-select E202 <<'EOF'
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

# `chan[T?]` was checked here, and it does not belong here: it is not a form this compiler
# has yet to build, it is a form the LANGUAGE refuses (GRAMMAR group 9), so no future
# feature makes it legal and the case can never retire. It lives in reject-check.sh now,
# where a permanent rejection's lifetime says it belongs — with one case per POSITION that
# can spell the type, which is what the single-position check here was hiding.

# A select arm head is `_`, a receive or a send — and nothing else. This used to be
# accepted: ANY identifier before `=>` became the `_` arm, so a typo (or the old `done`
# spelling) silently made the select non-blocking AND dropped its terminal arm. Both compilers
# refuse it now, which is why it is checked twice.
expect "$ZERG" select-arm-head-typo E203 <<'EOF'
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
expect "$ZERG" or-pattern-in-a-match-arm E241 <<'EOF'
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
expect "$ZERG" non-exhaustive-enum-match E428 <<'EOF'
enum K {
	A
	B
	C
}

fn name(k: K) -> str {
	return match k {
		K.A => "a"
		K.B => "b"
	}
}

fn main() {
	print name(A)
}
EOF

expect "$ZERG" non-exhaustive-int-match E467 <<'EOF'
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

# An arm's guard goes BEFORE the `=>` (GRAMMAR#match-arm). Written after the body it was silently
# DROPPED, so the arm compiled unconditional AND counted toward exhaustiveness as if it had
# no guard — the two halves of the guard rule, both wrong, from one easy typo.
expect "$ZERG" arm-guard-after-the-body E262 "goes before the \`=>\`" <<'EOF'
fn f(n: int) -> str {
	return match n {
		1 => "one" if n > 0
		_ => "rest"
	}
}

fn main() {
	print f(1)
}
EOF

# A guard makes an arm conditional, so it covers nothing: the compiler cannot prove the guard
# holds (GRAMMAR#match-arm), and `A` below is uncovered even though it is named.
expect "$ZERG" guarded-arm-covers-nothing E428 <<'EOF'
enum K {
	A
	B
}

fn f(k: K) -> str {
	return match k {
		K.A if true => "a"
		K.B => "b"
	}
}

fn main() {
	print f(A)
}
EOF

# An arm after an unguarded catch-all is dead, and worse: the lowering hands the LAST arm the
# `else`, so the arm that can never match is the one that runs by default.
expect "$ZERG" arm-after-the-catch-all E458 <<'EOF'
enum K {
	A
	B
}

fn f(k: K) -> str {
	return match k {
		_ => "rest"
		K.A => "a"
	}
}

fn main() {
	print f(B)
}
EOF

# A discriminant belongs to a C-style integer enum, and only to one (GRAMMAR#variant): a payload
# enum's tag is opaque and match-only, so neither direction of the reading is offered on it.
expect "$ZERG" discriminant-of-a-payload-enum E407 <<'EOF'
enum E {
	P(int)
	Q
}

fn main() {
	print int(Q)
}
EOF

expect "$ZERG" reverse-of-a-payload-enum E420 <<'EOF'
enum E {
	P(int)
	Q
}

fn main() {
	# not `print` of the enum itself: a composite has no rendering, and that rule now
	# answers first, which would make this case measure the wrong refusal
	e := E.of(1) ?? Q
	print 1
}
EOF

expect "$ZERG" discriminant-on-a-payload-declaration E214 <<'EOF'
enum E {
	P(int) = 3
	Q
}

fn main() {
	print "x"
}
EOF

# ...including one whose declared value HAPPENS to equal its position, which is otherwise
# indistinguishable from declaring nothing — so it was read and then quietly dropped.
expect "$ZERG" discriminant-that-looks-like-its-position E214 <<'EOF'
enum E {
	P(int) = 0
	Q = 1
}

fn main() {
	print "x"
}
EOF

# A carrier is not a value to convert: `int(k)` over a `K?` cast the carrier STRUCT, which
# cc rejected against generated code. The unwrap belongs to the programmer.
expect "$ZERG" conversion-of-a-carrier E418 <<'EOF'
enum K {
	A
	B
}

fn main() {
	k := K.of(1)
	print int(k)
}
EOF

# An enum's ONE conversion is `int`, its discriminant. Every other one fell through to a
# plain C cast of the tagged-union STRUCT.
expect "$ZERG" non-int-conversion-of-an-enum E419 <<'EOF'
enum K {
	A
	B
}

fn main() {
	print float(A)
}
EOF

# Each side of an Either holds exactly one value.
expect "$ZERG" either-side-with-two-values E405 <<'EOF'
fn f() -> Result[int] {
	return Either.Left(1, 2)
}

fn main() { print f() ?? 0 }
EOF

expect "$ZERG" repeated-discriminant E213 <<'EOF'
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
expect "$ZERG" variant-pattern-on-an-optional E427 <<'EOF'
enum K {
	A
	B
}

fn main() {
	k := K.of(1)
	print match k {
		K.A => "a"
		_ => "other"
	}
}
EOF

# An open-ended range is a legal RANGE ARM (`20.. =>`), and nothing else yet. The missing bound
# reads as nil, and `c_expr(ENil)` is "0" — so a `for` over one ran zero times and a slice came
# back empty, both in silence.
expect "$ZERG" open-range-in-a-for E423 <<'EOF'
fn main() {
	mut n := 0
	for i in 20.. {
		n = n + i
	}
	print n
}
EOF

expect "$ZERG" open-range-in-a-slice E423 <<'EOF'
fn main() {
	xs := [1, 2, 3]
	ys := xs[1..]
	print ys.len()
}
EOF

# `Ref[T]` in the two TYPE positions. Both used to answer "no type named `Ref`" — the
# misspelling message — because a type position asked only about the fixed-width ladder
# while `Ref(v)` in a CALL had been named all along. They now go through the one built-in
# namer, so a name is answered the same way wherever it is written.
expect "$ZERG" ref-type-in-result E446 <<'EOF'
fn mk(v: int) -> Ref[int] {
	return Ref(v)
}
fn main() { print "x" }
EOF

expect "$ZERG" ref-type-in-param E446 <<'EOF'
fn load(a: Ref[int]) -> int {
	return 0
}
fn main() { print "x" }
EOF

# A generic ENUM has been refused by name since it was written; a generic STRUCT was read
# and dropped, so a field of type `T` reported `no type named T` — a message about the
# consequence, two steps from the form the compiler had already decided not to support.
expect "$ZERG" generic-struct E215 <<'EOF'
struct B[T] {
	pub n: T
}
fn main() { print "x" }
EOF

# A `spec` is read and DROPPED — it is not a type and nothing dispatches on it — so this
# compiled and ran with no `show` at all, and the declared interface meant nothing.
# Enforcing the required members is the least it can mean.
# and the other half: a member supplied by a SEPARATE inherent block, declared after the
# spec impl. Checking at the `impl` made the answer depend on where the blocks sat.
# Left and Right name the two SIDES of an Either and have no type of their own, so they are
# read where the wanted type is known. Written where there is none, the compiler says which
# of the two problems it is — a form used without its context, not a form that does not exist.
expect "$ZERG" carrier-constructor-without-a-context E459 <<'EOF'
fn main() {
	x := Either.Left(1)
	print 1
}
EOF

# The two sides of an Either must DIFFER: an injection could otherwise reach both, and
# nothing at the match would tell which one it took.
expect "$ZERG" either-with-equal-sides E206 <<'EOF'
fn f(n: int) -> Either[int, int] {
	return Either.Left(n)
}
fn main() { print 1 }
EOF

# --- every form is HANDLED: implemented, or refused by name ------------------------
#
# `parse_primary` used to end `p_advance(p); return ENil` — an unread token was consumed and
# the expression became nil, which emits `0`. So every form this parser did not know became
# the number zero, in silence. These are the forms that landed there, plus the ones that
# reached cc or the linker instead. None of them may do either now.

# `print 1 as 2` is TWO statements with nothing between them: `as` joins an import spec and a
# pattern and is not an operator, so it can neither continue the expression nor start a
# statement. It used to be read as a second statement and reported as "`as` is not an
# expression this compiler reads" — true, and one step past the reason. The separator is what
# the seed answers, and now both compilers do.
expect "$ZERG" unread-token-in-an-expression E205 <<'EOF'
fn main() {
	print 1 as 2
}
EOF

expect "$ZERG" generic-enum E212 <<'EOF'
enum E[T] {
	A(T)
	B
}

fn main() { print 1 }
EOF

expect "$ZERG" associated-value-binding E218 <<'EOF'
struct B {
	pub n: int
}

impl B {
	LIMIT := 5
}

fn main() { print 1 }
EOF

expect "$ZERG" unknown-decorator E217 <<'EOF'
#[dyn]
struct P {
	pub x: int
}

fn main() { print 1 }
EOF

# `#[derive(Eq)]` + `==` used to be refused here. It is a FEATURE now, and what a working
# form owes is a program that runs, so it moved to the codegen corpus. What is left refused
# on this subject is the derive this compiler still does not write — see
# derive-of-an-unbuilt-spec and payload-enum-equality below.

expect "$ZERG" equality-with-no-eq E430 <<'EOF'
struct P {
	pub x: int
}

fn main() {
	print P(1) == P(1)
}
EOF

expect "$ZERG" equality-over-a-container E445 <<'EOF'
fn main() {
	print [1, 2] == [1, 2]
}
EOF

# A CHANNEL AND A CARRIER are not values to compare, and both used to slip past the guard —
# a `chan` fell to the numeric path and compared two POINTERS. They are here beside the
# container because one predicate answers all three, and it is the same one `T: Eq` asks.

expect "$ZERG" equality-on-a-channel E460 <<'EOF'
fn main() {
	c := chan[int](1)
	d := chan[int](1)
	print c == d
}
EOF

expect "$ZERG" generic-bound-on-a-carrier E412 <<'EOF'
fn same[T: Eq](a: T, b: T) -> bool {
	return a == b
}

fn main() {
	x: int? = 1
	print same(x, x)
}
EOF

expect "$ZERG" payload-enum-equality E438 <<'EOF'
#[derive(Eq)]
enum Shape {
	Circle(int)
}

fn main() {
	print Shape.Circle(1) == Shape.Circle(1)
}
EOF

expect "$ZERG" derive-of-a-user-spec E437 <<'EOF'
spec Show {
	fn show() -> str
}

#[derive(Show)]
struct P {
	pub x: int
}

fn main() { print 1 }
EOF

# A DECORATOR WITH NOTHING UNDER IT AT ALL — the last item in the file. This is the whole of
# what E208 says, and it used to be said about `#[derive(Eq)] fn main()` as well, where there
# very much IS a declaration under it: the pending list was only ever drained by a `struct`,
# an `enum` or a `spec`, so every other declaration walked past it and the leftover was
# reported at `Eof` as though nothing had followed.
expect "$ZERG" derive-with-no-declaration E208 <<'EOF'
fn main() { print 1 }

#[derive(Eq)]
EOF

# THE DECLARATION THAT FOLLOWS IS ONE A DECORATOR CANNOT GO ON. Four spellings, one rule —
# and the `impl` one was not refused at all: the pending derive survived the block and landed
# on whichever `struct` was declared next, which is a decorator silently changing a type the
# reader never decorated.
expect "$ZERG" derive-on-a-function E487 '`#[derive(Eq)]`' <<'EOF'
#[derive(Eq)]
fn f() {
	print 1
}

fn main() { f() }
EOF

expect "$ZERG" derive-on-a-type-alias E487 '`#[derive(Eq)]`' <<'EOF'
#[derive(Eq)]
type X = int

fn main() { print 1 }
EOF

# `#[obj]` RIDES THE SAME PENDING LIST under a marker no spec name can be, and every sentence
# about that list spelled it as a derive — so `#[obj] fn f()` reported `#[derive(#obj)]`,
# quoting a decorator the program does not contain.
expect "$ZERG" obj-on-a-function E487 '`#[obj]`' <<'EOF'
#[obj]
fn f() {
	print 1
}

fn main() { f() }
EOF

expect "$ZERG" derive-on-an-impl E487 '`#[derive(Eq)]`' <<'EOF'
struct P {
	pub x: int
}

#[derive(Eq)]
impl P {
	fn get() -> int {
		return this.x
	}
}

fn main() { print P(1).get() }
EOF

# A FIELD DEFAULT IS BUILT; a field default that reads ANOTHER FIELD is not, and it is the
# same shape docs/code/functions.md records for a parameter default reading an earlier
# parameter. The default is materialised at the construction, where a field is not a name in
# scope — so with a module constant of the same name it would quietly read that instead.
expect "$ZERG" field-default-reading-a-field E483 <<'EOF'
a := 100

struct P {
	pub a: int
	pub b: int = a * 2
}

fn main() { print P(3).b }
EOF

expect "$ZERG" struct-pattern-binding E221 <<'EOF'
struct P {
	pub x: int
}

fn main() {
	P{x} := P(3)
	print x
}
EOF

expect "$ZERG" for-mut-binding E242 <<'EOF'
fn main() {
	for mut v in [1, 2] {
		print v
	}
}
EOF

# A body that declares a result and falls off the end used to emit a C function with no
# return: the call answered whatever was in the return register.
expect "$ZERG" body-falls-off-the-end E435 <<'EOF'
fn f(n: int) -> int {
	if n > 0 {
		return 1
	}
}

fn main() { print f(1) }
EOF

expect "$ZERG" struct-cycle-by-value E452 <<'EOF'
struct A {
	pub b: B
}

struct B {
	pub a: A
}

fn main() { print 1 }
EOF

# A NAME NOTHING BINDS is the commonest mistake anyone makes, and it used to be spelled
# `zg_<n>` and handed to cc. So did a call to a function nothing declares — which is also
# how the specified-but-unbuilt raw-pointer builtins arrived.
expect "$ZERG" undefined-name E372 <<'EOF'
fn main() {
	print nope
}
EOF

expect "$ZERG" undefined-function E425 <<'EOF'
fn main() {
	print nope(1)
}
EOF

# THE BUILT-IN SET IS CLOSED (docs/runtime/builtins.md): a user cannot add to it, so a
# program naming one of these has not made a typo, and "undefined name `sizeof`" told the
# reader the language does not have a form the documentation describes and the SEED builds.
# Every one of these was reported as an unknown name until the emitter learned the list.
expect "$ZERG" raw-pointer-builtin E413 <<'EOF'
fn main() {
	mut n := 1
	print addr(n)
}
EOF

expect "$ZERG" refcounted-box-builtin E446 <<'EOF'
fn main() {
	r := Ref(7)
	print deref(r)
}
EOF

expect "$ZERG" deref-builtin E446 <<'EOF'
fn main() {
	print deref(7)
}
EOF

expect "$ZERG" sizeof-builtin E414 <<'EOF'
fn main() {
	print sizeof[int]
}
EOF

expect "$ZERG" alignof-builtin E414 <<'EOF'
fn main() {
	print alignof[int]
}
EOF

# A `spec` HAS three roles (docs/core/specs.md): the bound on a generic parameter, the
# interface a type conforms to, and a TYPE in its own right. The third is not built, and
# saying "no type named `Tag`" about a spec declared three lines above invited the reader to
# go and declare it again. An `impl` on a primitive is the same shape of answer.

expect "$ZERG" spec-used-as-a-type E416 <<'EOF'
spec Tag {
	fn tag() -> int
}

struct A {
	pub v: int
}

impl Tag for A {
	fn tag() -> int {
		return this.v
	}
}

fn show(t: Tag) -> int {
	return t.tag()
}

fn main() {
	print show(A(7))
}
EOF

expect "$ZERG" impl-on-a-primitive E415 <<'EOF'
spec Tag {
	fn tag() -> int
}

impl Tag for int {
	fn tag() -> int {
		return 1
	}
}

fn main() {
	print 1
}
EOF

# GRAMMAR#impl-decl derives type parameters on the `impl` itself — `impl generics? …` —
# and its comment says why they sit there: a `T` introduced at that position is what the
# TARGET is allowed to spell. There was no parse path for it at all, so the head began at
# a `[` where a name was expected and the compiler answered "an `impl` needs a name, and
# `[` is not one": a complaint about the token under the cursor, for a production the
# grammar derives in full. The parameters are read now, and the form is named.

expect "$ZERG" impl-with-its-own-type-parameters E291 <<'EOF'
spec Size {
	fn size() -> int
}

impl[T] Size for list[T] {
	fn size() -> int {
		return 1
	}
}

fn main() {
	print 1
}
EOF

# The concrete half of the same gap, and the worse of the two: the target's type arguments
# were SKIPPED, so `impl Size for list[int]` reached the emitter as an `impl` on a type
# named `list` and was refused with "no type named `list`" — a sentence that is false about
# a language whose grammar spells `list[T]`. An inherent `impl Box[int] { … }` went the
# other way and was accepted with its `[int]` silently erased. Reading the arguments makes
# both of them one refusal, about the form that was written and at the place it was.

expect "$ZERG" impl-on-a-target-with-type-arguments E292 'on `list[int]`' <<'EOF'
spec Size {
	fn size() -> int
}

impl Size for list[int] {
	fn size() -> int {
		return 1
	}
}

fn main() {
	print 1
}
EOF

expect "$ZERG" inherent-impl-on-a-target-with-type-arguments E292 'on `Box[int]`' <<'EOF'
struct Box {
	pub v: int
}

impl Box[int] {
	fn get() -> int {
		return this.v
	}
}

fn main() {
	print 1
}
EOF

expect "$ZERG" parameterized-bound E207 <<'EOF'
spec Eq[T] {
	fn eq(o: T) -> bool
}

spec Ix[K: Eq[int]] {
	fn at(k: K) -> int
}

fn main() {
	print 1
}
EOF

# A CALLEE THAT IS NOT A NAME. Three forms reached the emitter as `ECall("", args)` and were
# reported as ``undefined function ` ``` — an empty name, naming nothing, for a program with
# no typo in it. They are one root cause and three separate unbuilt features.

expect "$ZERG" call-a-fn-value-from-a-list E222 <<'EOF'
fn dbl(x: int) -> int {
	return x * 2
}

fn main() {
	fs := [dbl]
	print fs[0](5)
}
EOF

# A NAMED ARGUMENT is GRAMMAR#arg's `( identifier ':' )? expr`, the sanctioned way to skip
# the middle (docs/code/functions.md). This compiler is positional-only, and the `:` used to
# reach parse_primary, which answered "`:` is not an expression this compiler reads" — a
# token, about a form the language specifies and the seed builds.

expect "$ZERG" named-argument-in-a-call E223 <<'EOF'
fn f(a: int, b: int) -> int {
	return a - b
}

fn main() {
	print f(b: 1, a: 5)
}
EOF

expect "$ZERG" named-field-in-a-construction E223 <<'EOF'
struct P {
	pub x: int
	pub y: int
}

fn main() {
	p := P(y: 2, x: 1)
	print p.x
}
EOF

expect "$ZERG" optional-method-call E222 <<'EOF'
struct P {
	pub x: int
}

impl P {
	fn get() -> int {
		return this.x
	}
}

fn main() {
	p: P? = P(3)
	print p?.get() ?? 0
}
EOF

# `expect` used to advance on a match and say NOTHING otherwise, so every truncated form
# derailed quietly and whatever the parser built from the wreckage reached the emitter.
expect "$ZERG" truncated-guard E204 <<'EOF'
fn main() {
	print guard
}
EOF

expect "$ZERG" truncated-fn E204 <<'EOF'
fn main() {
	print fn
}
EOF

expect "$ZERG" truncated-chan-type E204 <<'EOF'
fn main() {
	chan
	print 1
}
EOF

expect "$ZERG" associated-type-binding E231 <<'EOF'
struct B {
	pub n: int
}

impl B {
	type Item = int
}

fn main() { print 1 }
EOF

expect "$ZERG" impl-item-that-is-not-a-method E219 <<'EOF'
struct B {
	pub n: int
}

impl B {
	print 1
}

fn main() { print 1 }
EOF

# A method that MUTATES its receiver cannot be served from a materialised temp: the edit
# lands on the copy and is lost. `m["a"].append(3)` compiled and silently did nothing.
expect "$ZERG" mutating-method-on-a-map-index E422 <<'EOF'
fn main() {
	mut m: map[str, list[int]] = {:}
	m["a"] = [1, 2]
	m["a"].append(3)
	print m["a"].len()
}
EOF

# Too FEW constructor arguments has been named for a while; too many reached cc as an
# "excess elements" WARNING, so it compiled and the extra values were dropped.
expect "$ZERG" too-many-constructor-arguments E426 <<'EOF'
struct P {
	pub n: int
}

fn main() {
	print P(1, 2).n
}
EOF

expect "$ZERG" derive-of-an-unbuilt-spec E436 <<'EOF'
#[derive(Ord)]
struct P {
	pub x: int
}

fn main() {
	print P(1) < P(2)
}
EOF

expect "$ZERG" rendering-a-composite E449 <<'EOF'
struct P {
	pub x: int
}

fn main() {
	print str(P(1))
}
EOF

expect "$ZERG" unknown-list-method E444 <<'EOF'
fn main() {
	xs := [1, 2, 3]
	print xs.slice(0, 2).len()
}
EOF

expect "$ZERG" tuple-pattern-in-an-arm E232 <<'EOF'
fn main() {
	t := (1, 2)
	print match t {
		(a, b) => a
		_      => 0
	}
}
EOF

# --- the seed ---------------------------------------------------------------------

# The seed TYPES a map for-in (it binds the key) and does not lower one. The unchecked
# assertion that followed was a nil dereference: it crashed with a Go stack trace instead
# of reporting anything.
expect "$ZERG0" seed-map-for-in "does not lower a for-in over" <<'EOF'
fn main() {
	mut m: map[str, int] = {:}
	m["a"] = 1
	for k in m {
		print k
	}
}
EOF


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

expect "$ZERG" optional-into-a-value E432 <<'EOF'
fn main() {
	x: int? = 5
	y: int = x
	print y
}
EOF

expect "$ZERG" print-of-an-optional E433 <<'EOF'
fn main() {
	x: int? = nil
	print x
}
EOF

expect "$ZERG" chain-through-a-value E406 <<'EOF'
struct P {
	pub n: int
}
fn main() {
	p := P(1)
	print p?.n
}
EOF

expect "$ZERG" missing-required-field E370 <<'EOF'
struct P {
	pub n: int
	pub m: int
}
fn main() {
	p := P(1)
	print p.n
}
EOF

expect "$ZERG" try-without-a-carrier-result E408 <<'EOF'
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

# expect_no_lint is the other half. A rule with only positive cases can be widened until it
# fires on everything and every case still passes — which is how L301 came to report
# `show(k)`, a READ, as a write. What a rule refuses to say is as much its definition as
# what it says.
expect_no_lint() {
	local name=$1 unwanted=$2
	local src="$tmp/$name.zg"
	cat >"$src"

	local out
	out=$("$ZERG" lint "$src" 2>&1)
	case $out in
	*"$unwanted"*)
		echo "FALSE     $name — lint said $unwanted about a program it should be quiet about: $(echo "$out" | head -1)"
		fail=$((fail + 1))
		;;
	*) pass=$((pass + 1)) ;;
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


# GRAMMAR#param — "There is NO plain `mut x` parameter". It was accepted and the keyword
# dropped, so a write in the body said `cannot assign through b: it is immutable` about a
# parameter the programmer had marked `mut`.
expect "$ZERG" a-plain-mut-parameter E263 "a parameter is \`mut &\` or nothing" <<'EOF'
struct Bag {
	pub n: int
}

fn f(mut b: Bag) {
	print(f"{b.n}")
}

fn main() {
	f(Bag(1))
}
EOF

# GRAMMAR#impl-decl — `Type.f(…)` is an ASSOCIATED FUNCTION, the named-constructor form
# (`User.from_json(…)`). The parser gives every `fn` in an `impl` a receiver, so there is
# no such function to call; the answer used to be "the method `make` on a ?", which points
# at inference having nothing to say rather than at the form.
expect "$ZERG" associated-function E424 <<'EOF'
struct P {
	pub x: int
}

impl P {
	fn make(n: int) -> P {
		return P(n)
	}
}

fn main() {
	p := P.make(7)
	print(f"{p.x}")
}
EOF

# EIGHT FORMS whose refusal named a TOKEN and not the form. A reader could not tell "this
# is not built" from "you made a typo", which is the whole of the implemented-or-named
# contract — every one of these is in GRAMMAR and none of them was being turned away by
# the name GRAMMAR gives it.
expect "$ZERG" array-type E233 <<'EOF'
fn main() {
	xs: [int; 3] = [1, 2, 3]
	print xs[0]
}
EOF

expect "$ZERG" array-type-parameter E233 <<'EOF'
fn f(xs: [int; 3]) -> int {
	return xs[0]
}

fn main() { print 1 }
EOF

expect "$ZERG" struct-pattern E243 <<'EOF'
struct P {
	pub x: int
}

fn main() {
	p := P(1)
	print match p {
		P{x: a} => a
		_ => 0
	}
}
EOF

expect "$ZERG" as-binding-in-an-arm E234 <<'EOF'
enum E {
	A(int)
	B
}

fn main() {
	e := E.A(5)
	print match e {
		E.A(n) as whole => n
		_ => 0
	}
}
EOF

expect "$ZERG" interpolating-command-literal E235 <<'EOF'
fn main() {
	n := "hi"
	print f`echo {n}`
}
EOF

# Every module flattens into ONE namespace, so two that declare the same constant mangle to
# one symbol. The FUNCTION case has been refused since the tables were written; this one
# LINKED — two definitions of `zg_N`, tolerated by a linker that still allows a common
# symbol — and the reader got whichever one it chose. `-fno-common` makes it a duplicate
# symbol instead, so the same program built two ways gave two answers and then an error.
#
# It needs two modules, which this harness writes one file for, so the case lives in the
# multi-module example instead: see examples/1g/reexport.

# A KEY NEEDS `Hash`, and this compiler has one for `int` and one for `str`. A `byte` key
# took the INT hash and eq, which read 8 bytes out of a 1-byte slot — so `{b'a': 1}` built
# and the lookup that followed raised `KeyError` for a key that was right there.
expect "$ZERG" map-key-without-a-hash E431 <<'EOF'
fn main() {
	m := {b'a': 1}
	print m.len()
}
EOF

# THE REST OF THE `[not yet]` TABLE in docs/surface/grammar.md. That table claims a case
# holds every entry; half of them had none, so the claim was the third unsynchronised copy
# of a list that already lives in the parser's raises and in this file.
expect "$ZERG" command-literal E236 <<'EOF'
fn main() {
	c := `echo hi`
	print c
}
EOF

expect "$ZERG" fstring-conversion E226 <<'EOF'
fn main() {
	n := 42
	print f"{n!r}"
}
EOF

expect "$ZERG" fstring-self-documenting E227 <<'EOF'
fn main() {
	n := 42
	print f"{n=}"
}
EOF

expect "$ZERG" fstring-format-spec E225 <<'EOF'
fn main() {
	pi := 3.5
	print f"{pi:.2f}"
}
EOF

# A generic FUNCTION is built — it monomorphizes, one specialization per set of type
# arguments — so its case moved to the codegen corpus, where a working form belongs. What
# is still refused is everything around it, and each shape is here rather than one standing
# for the rest: a generic TYPE, a generic METHOD, a bound this compiler cannot carry, a call
# that decides nothing, and a bound the argument does not meet.

expect "$ZERG" generic-struct E215 <<'EOF'
struct Box[T] {
	pub v: T
}

fn main() { print 1 }
EOF

expect "$ZERG" generic-enum E212 <<'EOF'
enum Opt2[T] {
	Some(T)
	None
}

fn main() { print 1 }
EOF

expect "$ZERG" generic-method E409 <<'EOF'
struct P {
	pub x: int
}

impl P {
	fn get[T](v: T) -> T {
		return v
	}
}

fn main() { print 1 }
EOF

# A bound is a CONJUNCTION — `T: Eq + Show` asks for both — and the one that is not met is
# the one named. The form itself is built; what is refused here is the type that does not
# keep the promise.
expect "$ZERG" generic-bound-unmet-in-a-conjunction E412 'does not implement `Show`' <<'EOF'
spec Show {
	fn show() -> str
}

fn f[T: Eq + Show](a: T) -> T {
	return a
}

fn main() {
	print f(1)
}
EOF

expect "$ZERG" generic-undecidable E411 <<'EOF'
fn f[T](n: int) -> int {
	return n
}

fn main() { print f(1) }
EOF

expect "$ZERG" generic-bound-unmet E412 <<'EOF'
struct P {
	pub x: int
}

fn same[T: Eq](a: T, b: T) -> bool {
	return a == b
}

fn main() { print same(P(1), P(1)) }
EOF

# POLYMORPHIC RECURSION — a template calling itself at a LARGER type. Its specializations are
# `int`, `list[int]`, `list[list[int]]`, … without end, and whether the program stops depends
# on `n`, a runtime value. No monomorphizing compiler can answer that by looking, so each
# stops at a depth; this one used to spin until it was killed, saying nothing at all.
expect "$ZERG" generic-polymorphic-recursion E410 <<'EOF'
fn deep[T](x: T, n: int) {
	if n > 0 {
		deep([x], n - 1)
	}
}

fn main() { deep(1, 3) }
EOF

expect "$ZERG" if-let-over-an-enum E434 <<'EOF'
enum E {
	A(int)
	B
}

fn main() {
	e := A(5)
	if v := e {
		print 1
	}
	print 2
}
EOF

expect "$ZERG" spec-member-with-a-body E210 <<'EOF'
spec Show {
	fn show() -> int {
		return 1
	}
}

fn main() { print 1 }
EOF

expect "$ZERG" unsafe-block E224 <<'EOF'
fn main() {
	n := unsafe {
		5
	}
	print n
}
EOF

# A standalone `unsafe fn` is a DECLARATION — GRAMMAR#fn-decl is where `unsafe` sits — and
# it is refused as itself, with a place. It used to fall into the top-level statement
# fallback and answer "NotImplemented: unsafe", the block-expression's sentence about a
# form that is not a block; and reading the `fn` as safe instead would erase the one thing
# the keyword says while the trust boundary stays unenforced (docs/runtime/ffi.md).
expect "$ZERG" unsafe-fn-declaration E264 <<'EOF'
unsafe fn g() -> int {
	return 2
}

fn main() {
	print g()
}
EOF

# `pub unsafe fn` is the SAME declaration with its visibility marker, and it earns the
# same sentence. It used to be told "`pub` binds to a declaration, and a statement takes
# none" — which is false twice over: it IS a declaration, and the statement fallback was
# never the right reader for it.
expect "$ZERG" pub-unsafe-fn-declaration E264 <<'EOF'
pub unsafe fn g() -> int {
	return 2
}

fn main() {
	print g()
}
EOF

expect "$ZERG" raw-pointer-type E413 <<'EOF'
fn f(p: ptr) -> int {
	return 1
}

fn main() { print 1 }
EOF

# THE THIRD POSITION, and the one a signature reads last: a raw pointer as the RESULT. The
# funnel that names every built-in this compiler has not got is one function, so a place is
# owed at each of the three the same way — and none of them carried one.
expect "$ZERG" raw-pointer-return-type E413 <<'EOF'
fn f() -> ptr[int] {
	return 0
}

fn main() { print 1 }
EOF

expect "$ZERG" destructuring-binding E238 <<'EOF'
fn main() {
	(a, b) := (1, 2)
	print a + b
}
EOF

expect "$ZERG" destructuring-binding-mut E238 <<'EOF'
fn main() {
	mut (a, b) := (1, 2)
	print a + b
}
EOF

# THE THIRD SPELLING IS A DIFFERENT FORM. It BUILT and did nothing — the tuple was
# evaluated, assigned to no one, and the program printed the values it started with — and
# once it was refused it borrowed the binding's sentence, which quotes a `:=` at a reader
# who wrote `=`. GRAMMAR#assign-target derives the tuple form on its own, so it is an unbuilt
# form of its own and owes its own sentence.
expect "$ZERG" destructuring-assignment E486 'a destructuring assignment' <<'EOF'
fn main() {
	mut a := 1
	mut b := 2
	(a, b) = (3, 4)
	print a + b
}
EOF

expect "$ZERG" open-range-with-no-lower-bound E239 <<'EOF'
fn main() {
	xs: list[int] = [1, 2, 3]
	print xs[..2].len()
}
EOF

expect "$ZERG" list-pattern E240 <<'EOF'
fn main() {
	xs: list[int] = [1, 2, 3]
	print match xs {
		[a, ..] => a
		_ => 0
	}
}
EOF

# An INDEX needs a list or a map. `a[0]` on a `list[int]?` handed the runtime the carrier
# struct where a header goes, which cc reported as a WARNING — so the program linked and
# segfaulted. A warning is not a gate.
expect "$ZERG" index-an-optional E421 <<'EOF'
fn main() {
	a: list[int]? = [1, 2, 3]
	print(a[0])
}
EOF

# A strong typedef is built over a SCALAR this phase, which docs/core/types.md records as a
# deviation and the seed refuses too. It is named rather than emitted because it LOOKED like
# it worked: a str is a refcounted cell and the typedef name is not a str to c_is_str, so
# nothing retained it, nothing released it, and `str(l)` printed the pointer as a number.
expect "$ZERG" typedef-over-a-str E304 <<'EOF'
type Label = str

fn main() {
	print(f"{str(Label("hi"))}")
}
EOF


# `str(…)` over a list bridges bytes or code points. Without the element check it reinterpreted any
# buffer as characters: `f"{xs}"` on a `list[int]` printed the low byte of each element,
# and on a `list[list[int]]` printed the low bytes of a heap POINTER. `print xs` refuses a
# composite; this let the same value out through an f-string hole.
expect "$ZERG" render-a-list-of-ints E417 <<'EOF'
fn main() {
	xs: list[int] = [65, 66, 67]
	print(f"{xs}")
}
EOF

# `print` has no two-argument form, so `print(a, b)` builds a TUPLE and prints that. A
# composite has no rendering — the structural one is `Display`'s job and this compiler
# generates none — and the cast reached cc as "operand of type 'zg_tup_...' where
# arithmetic or pointer type is required". The mutation fuzzer is what found it.
expect "$ZERG" print-a-tuple E449 <<'EOF'
fn main() {
	print(1, 2)
}
EOF

# L301: the snapshot semantics of a captured argument. It is here rather than in the reject
# list because the program is CORRECT — this is the one thing in the language a competent
# reader has to ask about, so the tool answers instead of waiting to be asked.
expect_lint spawn-captures-a-value-then-writes-it "L301" <<'EOF'
fn show(n: int) {
	print(f"{n}")
}

fn main() {
	mut k := 5
	spawn show(k)
	k = 99
}
EOF

expect_lint defer-captures-a-value-then-writes-it "L301" <<'EOF'
fn show(n: int) {
	print(f"{n}")
}

fn main() {
	mut j := 1
	defer show(j)
	j = 2
}
EOF

# What L301 is, said from both sides. A method that writes through its receiver is a write
# — a captured `list` is snapshotted by deep copy, so an append after the capture is exactly
# the misreading — and a REBINDING of a channel is a write, because after it the coroutine
# holds the old handle. A read, a send, and a write BEFORE the capture are not.

expect_lint spawn-captures-a-list-then-appends "L301" <<'EOF'
fn take(xs: list[int]) {
	print(f"{xs[0]}")
}

fn main() {
	mut xs: list[int] = [1]
	spawn take(xs)
	xs.append(2)
}
EOF

expect_lint spawn-captures-a-channel-then-rebinds-it "L301" <<'EOF'
fn work(ch: chan[int]) {
	print("w")
}

fn main() {
	mut ch := chan[int](1)
	spawn work(ch)
	ch = chan[int](1)
}
EOF

expect_lint spawn-inside-a-closure "L301" <<'EOF'
fn show(n: int) {
	print(f"{n}")
}

fn run(f: fn()) {
	f()
}

fn main() {
	run(fn() {
		mut k := 5
		spawn show(k)
		k = 99
	})
}
EOF

expect_no_lint a-read-after-the-capture "L301" <<'EOF'
fn show(n: int) {
	print(f"{n}")
}

fn main() {
	mut k := 5
	spawn show(k)
	show(k)
}
EOF

expect_no_lint a-send-after-the-capture "L301" <<'EOF'
fn work(ch: chan[int]<-) {
	ch <- 1
}

fn main() {
	mut ch := chan[int](1)
	spawn work(ch)
	ch <- 2
}
EOF

expect_no_lint a-write-before-the-capture "L301" <<'EOF'
fn show(n: int) {
	print(f"{n}")
}

fn main() {
	mut k := 5
	k = 99
	spawn show(k)
}
EOF

# --- a second `Into` on one type ------------------------------------------------
#
# The language means for a type to have several: the built-in matrix has four out of `int`
# alone. This compiler keys a method by its NAME, so one type carries one `into` — and the
# collision reported itself as two methods sharing a namespace, which is true and about the
# wrong thing. Named here until a spec method is keyed by (spec, arguments), which is also
# what would let a written-out `x.into()` say which one is meant.

expect "$ZERG" a-second-into-on-one-type E461 <<'EOF'
struct Celsius {
	pub deg: int
}

struct Kelvin {
	pub deg: int
}

impl Into[int] for Celsius {
	fn into() -> int {
		return this.deg
	}
}

impl Into[Kelvin] for Celsius {
	fn into() -> Kelvin {
		return Kelvin(this.deg + 273)
	}
}

fn main() {
	c := Celsius(20)
	n: int = c
	print n
}
EOF

# --- `in` over a set this compiler does not read ---------------------------------
#
# `in` tests MEMBERSHIP, and what a set is depends on what names it: a container names its
# elements, a range its members, an error kind itself and everything below it. A RANGE is the
# one that is not built — the grammar makes `v in 0..10` sugar for `r.contains(v)` over stdlib
# machinery that does not exist — so it is named rather than left to be read as something else,
# and the message says which set was written.

# an ELEMENT that the list cannot hold: the same rule every typed position uses, which is what
# makes `in` refuse a str looked for among ints rather than compare a pointer to a number
expect "$ZERG" in-over-a-list-of-the-wrong-element E338 'the value looked for by `in` is int' <<'EOF'
fn main() {
	xs := [1, 2]
	print str("a" in xs)
}
EOF

# and a STRUCT element, which has no `==` for the same reason `a == b` refuses one
expect "$ZERG" in-over-a-list-of-structs E462 <<'EOF'
struct P {
	pub x: int
}

fn main() {
	ps := [P(1)]
	print str(P(2) in ps)
}
EOF

# A range of NUMBERS is built — the corpus case in_range_once is that half — and what is
# left is a range whose bounds the bounds test cannot compare. A `str` one is the shape that
# matters: C's `>=` on two `const char *` compares the POINTERS and answers, so lowering it
# would give a wrong answer rather than an error, which is what the refusal is for.
expect "$ZERG" in-over-a-range-of-str E463 <<'EOF'
fn main() {
	print str("c" in "a".."z")
}
EOF

# And a set that is no set at all, which is the rest of the same code.
expect "$ZERG" in-over-a-plain-int E463 <<'EOF'
fn main() {
	print str(3 in 5)
}
EOF

# --- a position wraps a value; it never converts one --------------------------------
#
# docs/core/type-system.md's second rule, at every position that used to break it. Each of
# these compiled until the type-system pass: the value was converted where it landed, the
# lint said so afterwards (`L501`, retired with the route), and the source said nothing. The
# fix in every sentence is the same one — write the conversion — so the cases are here as a
# GROUP, because the failure they guard against is one position being forgotten rather than
# the rule being lost. That is the shape this file has caught four times.
#
# The seed refuses all of them too, and has all along; `place` is asked of every one because
# a refusal a reader cannot locate is half a diagnostic.

expect "$ZERG" position-binding-int-to-float E335 'cannot bind int to a float binding' <<'EOF'
fn main() {
	i := 5
	x: float = i
	print x
}
EOF

expect "$ZERG" position-binding-int-to-byte E335 'cannot bind int to a byte binding' <<'EOF'
fn main() {
	n := 5
	b: byte = n
	print int(b)
}
EOF

expect "$ZERG" position-argument E340 <<'EOF'
fn f(x: float) -> float {
	return x
}

fn main() {
	i := 5
	print f(i)
}
EOF

expect "$ZERG" position-return E333 <<'EOF'
fn f() -> float {
	i := 5
	return i
}

fn main() {
	print f()
}
EOF

expect "$ZERG" position-assignment E339 <<'EOF'
fn main() {
	mut acc: float = 0.0
	i := 5
	acc = i
	print acc
}
EOF

expect "$ZERG" position-struct-field E338 'is float, and this gives int' <<'EOF'
struct R {
	pub deg: float
}

fn main() {
	i := 5
	print R(i).deg
}
EOF

expect "$ZERG" position-list-element E329 <<'EOF'
fn main() {
	i := 5
	xs: list[float] = [i]
	print xs[0]
}
EOF

# A CARRIER WRAPS, and the value inside it is at the position one level in — so the payload
# is checked exactly as the bare binding above is. This is the case the old route reached
# LAST: `x: float? = i` printed 5 for a year after the bare form had a rule.
expect "$ZERG" position-carrier-payload E338 'is float, and this gives int' <<'EOF'
fn main() {
	i := 5
	x: float? = i
	print x!
}
EOF

# A CALL SOLVES ITS OWN PARAMETERS and the demand neither solves them nor converts the
# answer, so `T` is `int` here and the `int` is refused at the binding. It used to raise
# `OverflowError` at RUN TIME, one monomorphization later.
expect "$ZERG" position-generic-answer E335 'cannot bind int to a byte binding' <<'EOF'
fn id[T](x: T) -> T {
	return x
}

fn main() {
	b: byte = id(300)
	print int(b)
}
EOF

# A USER `Into` IS A SPEC, not a position's licence. The method exists and `c.into()` runs
# (test-data/codegen/into_user.zg); what is refused is the position performing it.
expect "$ZERG" position-user-into E335 'cannot bind C to a int binding' <<'EOF'
struct C {
	pub deg: int
}

impl Into[int] for C {
	fn into() -> int {
		return this.deg
	}
}

fn main() {
	c := C(20)
	n: int = c
	print n
}
EOF

# --- an operator's operands are already one type ------------------------------------
#
# `i + u` was refused and `i + f` was promoted, and the difference between them was a
# containment table nothing in the source mentions. One rule, one sentence, one fix.

expect "$ZERG" operands-int-and-float E353 <<'EOF'
fn main() {
	i := 5
	f := 1.5
	print i + f
}
EOF

expect "$ZERG" operands-int-and-uint E353 <<'EOF'
fn main() {
	i := 5
	u := uint(2)
	print i + u
}
EOF

expect "$ZERG" operands-byte-and-int E353 <<'EOF'
fn main() {
	b := b'A'
	n := 200
	print b + n
}
EOF

# ORDER ASKS THE SAME QUESTION, which is where the C trap lives: in C the signed operand
# converts to unsigned, so `-1 < 1u` is false.
expect "$ZERG" operands-compared E353 <<'EOF'
fn main() {
	i := 0 - 1
	u := uint(1)
	print str(i < u)
}
EOF

# --- a form with no type, and no position to take one from ---------------------------
#
# "ambiguity is an error" — the consequence with no demand and no declared default to fall
# back on. All three were quiet: `[]` reached cc against generated C in a cache file nobody
# wrote, and the other two compiled in silence.

expect "$ZERG" typeless-empty-list E336 'the empty list `[]`' <<'EOF'
fn main() {
	x := []
	print 1
}
EOF

expect "$ZERG" typeless-empty-map E336 'the empty map `{:}`' <<'EOF'
fn main() {
	x := {:}
	print 2
}
EOF

expect "$ZERG" typeless-nil E336 '`nil`' <<'EOF'
fn main() {
	x := nil
	print 3
}
EOF

# --- a folded literal is measured in the type it guessed ------------------------------
#
# An all-literal expression takes the type its position asks for and the arithmetic then
# happens IN that type, so every step is measured against it — the operands first, and then
# what they make. The sentence names the number the reader has to change, which is not always
# the one the expression folds to: `300 - 100` folds to `200`, a perfectly good byte, and what
# is wrong with the line is the `300`.

expect "$ZERG" folded-result-out-of-range E330 '`300` is not a value a byte holds' <<'EOF'
fn main() {
	x: byte = 200 + 100
	print int(x)
}
EOF

expect "$ZERG" folded-operand-out-of-range E330 '`300` is not a value a byte holds' <<'EOF'
fn main() {
	x: byte = 300 - 100
	print int(x)
}
EOF

expect "$ZERG" folded-negative-into-uint E330 '`-1` is not a value a uint holds' <<'EOF'
fn main() {
	x: uint = 0 - 1
	print int(x)
}
EOF

# A SHAPE THE TARGET CANNOT CARRY is not a value out of range, and gets the ordinary sentence
# rather than a false one about `1` not being a float: no double carries `%`, so the tree does
# not adopt at all.
expect "$ZERG" folded-shape-a-float-cannot-carry E335 'cannot bind int to a float binding' <<'EOF'
fn main() {
	x: float = 1 % 2
	print x
}
EOF

# A LITERAL TREE IS RENDERED WHOLE, so it is the one expression c_expr never walks — and both
# questions that walk asks had to be carried to it by hand. Neither is hypothetical: the first
# printed `inf`, the second printed `1`.
expect "$ZERG" folded-divisor-at-the-other-operand E331 <<'EOF'
fn main() {
	n: float = 4.0
	print n + 1 / 0
}
EOF

expect "$ZERG" folded-leaf-past-int E319 <<'EOF'
fn main() {
	x: float = 99999999999999999999 + 1
	print x
}
EOF

# THE FOLD LEAVES i64 while every leaf fits it, which is not a value out of range and does not
# get that sentence: 2^63 is a perfectly good `uint`, and the compiler never worked it out.
expect "$ZERG" folded-past-what-an-int-holds E332 "past what an \`int\` holds" <<'EOF'
fn main() {
	x: uint = 9223372036854775807 + 1
	print int(x)
}
EOF

# --- an `if` expression answers ONE type -----------------------------------------------
#
# `match` has had this rule since its arms could answer at all; `if` did not, so
# `x := if false { 1 } else { 2.5 }` printed `2` — the float arm truncated into the int the
# first branch settled on — and a pair with no C conversion between them escaped to cc.

expect "$ZERG" if-branches-int-and-float E321 <<'EOF'
fn main() {
	x := if false { 1 } else { 2.5 }
	print x
}
EOF

expect "$ZERG" if-branches-int-and-bool E321 <<'EOF'
fn main() {
	x := if false { 1 } else { true }
	print x
}
EOF

expect "$ZERG" if-branches-int-and-str E321 <<'EOF'
fn main() {
	x := if false { 1 } else { "s" }
	print x
}
EOF

# --- no built-in type implements `Into` -----------------------------------------------
#
# It is a REFUSAL and not a gap, which is why it has a sentence of its own rather than the
# generic unknown-method one: between numbers the conversion is written `T(x)`, and to text it
# is `str(x)`, which every type answers through `display`. An `.into()` beside either would
# need the position to say which target it meant, and a demand never does that.

expect "$ZERG" into-on-an-int E464 <<'EOF'
fn main() {
	print 1.into()
}
EOF

expect "$ZERG" into-on-a-str E464 <<'EOF'
fn main() {
	s := "a"
	print s.into()
}
EOF

# --- a bound names the spec AND its arguments ------------------------------------------
#
# The arguments are what the bound MEANS, so a type meeting `Into[int]` does not meet
# `Into[str]` — and the registry keys on them too, or the two impls would be one entry.

expect "$ZERG" bound-with-the-wrong-argument E412 'does not implement `Into[str]`' <<'EOF'
struct S {
	pub v: int
}

impl Into[int] for S {
	fn into() -> int {
		return this.v
	}
}

fn take[T: Into[str]](x: T) -> str {
	return x.into()
}

fn main() {
	print take(S(3))
}
EOF

# --- the fixed-width ladder is unbuilt, and says so ------------------------------------
#
# A `[not yet]` is a legitimate state; an UNNAMED refusal is not. Because a width is an
# ordinary identifier rather than a keyword, `i32(x)` reported "undefined function `i32`" — the
# message any misspelled call gets — so a reader was told the name is unknown rather than that
# the ladder is not built, and would go looking for their own typo.
#
# HERE AND NOT IN reject-check, which is the file for programs that are not Zerg. These ARE
# Zerg: the SEED builds and runs every one of them. What they are is a feature the shipping
# compiler has not caught up to, which is exactly what this file is for.

expect "$ZERG" fixed-width-conversion E465 <<'EOF'
fn main() {
	print i32(5)
}
EOF

expect "$ZERG" fixed-width-annotation E465 <<'EOF'
fn main() {
	x: u8 = 5
	print int(x)
}
EOF

expect "$ZERG" fixed-width-float E465 <<'EOF'
fn main() {
	print f32(1.5)
}
EOF

expect "$ZERG" fixed-width-typedef E465 <<'EOF'
type W = u8

fn main() {
	print 1
}
EOF

# --- five grammar forms that read as a typo ---------------------------------------------
#
# Each is a form GRAMMAR has and this compiler does not, and each was refused with the message
# a MISSPELLING gets — "no type named `ptr`", "undefined function `set`" — so a reader was told
# their own name was unknown and would go looking for a typo that is not there. The same class
# as the fixed-width ladder above, and found the same way: by writing the happy path and
# reading the answer.

# the NAME shape of the or-pattern, and the reason the rule lives in the parser. Its sibling
# `or-pattern-in-a-match-arm` above is the LITERAL shape, where parse_expr swallows the `|`
# into a bitwise-or; a name pattern stops before the `|` instead, so the arm never reached
# the emitter at all and died on "expected `=>`". One rule, and only the parser sees both.
expect "$ZERG" or-pattern-of-variant-names E241 <<'EOF'
enum E {
	A
	B
}

fn main() {
	print match E.A {
		E.A | B => 1
	}
}
EOF

# `ptr` INSIDE an `unsafe` group, which is the one place the language says it belongs — so
# this is not `raw-pointer-type` above with extra braces: that case shows the bare signature
# is refused, this one shows the refusal is about the type not being built rather than about
# where it was written.
expect "$ZERG" ptr-type-in-an-unsafe-group E413 <<'EOF'
unsafe {
	fn f(p: ptr) -> int {
		return 1
	}
}

fn main() {
	print 1
}
EOF

expect "$ZERG" associated-type-projection E265 <<'EOF'
spec It {
	fn next() -> int
}

fn f(x: It.Item) -> int {
	return 1
}

fn main() {
	print 1
}
EOF

expect "$ZERG" value-generic-parameter E266 <<'EOF'
fn f[N: int]() -> int {
	return N
}

fn main() {
	print f[3]()
}
EOF

expect "$ZERG" set-constructor E466 <<'EOF'
fn main() {
	s := set([1, 2])
	print s.len()
}
EOF

# --- forms whose failure used to escape this compiler ----------------------------------
#
# They are here rather than beside their chapter because what they have in common is not
# the form. Most built, crashed, or exited 0 with nothing to show for it: the compiler had
# no opinion, and something further down the line — cc, the loader, a deadlocked runtime —
# was left to have one. The last two had an answer already, and it was not true of the
# program it was about.

# A function declared `-> T` whose `return` carries nothing emitted `return;` into a
# non-void C function. cc reported it, against generated source and a mangled name. The
# rule one level up — a body that FALLS OFF THE END — already existed; this was its
# other slot. `return if c` is the same statement with GRAMMAR's postfix `if`.
expect "$ZERG" bare-return-in-a-non-void-fn E468 <<'EOF'
fn f() -> int {
	return
}

fn main() {
	print f()
}
EOF

expect "$ZERG" bare-conditional-return-in-a-non-void-fn E468 <<'EOF'
fn f(n: int) -> int {
	return if n < 0
	return 5
}

fn main() {
	print f(3)
}
EOF

# A `mut &` parameter cannot survive being turned into a bare function pointer: the call
# site reads a signature from the callee's NAME, and a value has not got one. Both
# spellings SEGFAULTED — the argument went in by value where a `T*` was declared.
expect "$ZERG" mut-ref-param-on-a-closure E469 <<'EOF'
fn main() {
	f := fn(mut &a: int) {
		a = a + 1
	}
	mut x := 1
	f(x)
	print x
}
EOF

expect "$ZERG" mut-ref-fn-taken-as-a-value E469 <<'EOF'
fn bump(mut &a: int) {
	a = a + 1
}

fn main() {
	g := bump
	mut x := 1
	g(x)
	print x
}
EOF

# GRAMMAR#del-stmt gives `del` a second meaning on a channel — drop a sender reference,
# close the stream if it was the last. This compiler releases a channel where its
# binding's scope ends, so `del ch` revoked the name and emitted nothing: the program
# built, said nothing, and DEADLOCKED waiting on an end the writer thought it had closed.
expect "$ZERG" del-on-a-channel E470 <<'EOF'
fn consume(rx: <-chan[int], done: chan[int]<-) {
	mut n := 0
	for v in rx {
		n = n + v
	}
	done <- n
}

fn main() {
	ch := chan[int](4)
	done := chan[int](1)
	spawn consume(ch, done)
	ch <- 1
	del ch
	print <-done!
}
EOF

# GRAMMAR#import-path is a str-lit and nothing else. The bare spelling answered "no spec
# here", so the import was not made and `util/text` fell through to the statement loop as
# a top-level expression — which compile mode treats as a nop. Built, printed, exited 0,
# and had imported nothing.
expect "$ZERG" bare-import-path E267 'write `import "util/text"`' <<'EOF'
import util/text

fn main() {
	print 1
}
EOF

# A GUESS IS A PATH OR IT IS NOTHING. The reassembly used to run to the next `;` — and ASI
# inserts none after `import`, a keyword that cannot END an item — so a bare `import` with
# no path at all swept up the following declarations and offered `import "structQ{puba:int"`
# as the spelling to write. What is quoted back at a reader has to be something they wrote.
expect "$ZERG" import-with-no-path-at-all E267 'derives a str-lit and nothing else' <<'EOF'
import

struct Q {
	pub a: int
}

fn main() {
	print 1
}
EOF

# The same branch, one token along: a path is `identifier ( '/' identifier )*` and `3` starts
# none, so there is nothing of the reader's to quote back.
expect "$ZERG" import-path-that-is-not-a-path E267 'derives a str-lit and nothing else' <<'EOF'
import 3

fn main() {
	print 1
}
EOF

# A built-in container's type arguments are not a genericity it lacks: `map` takes exactly
# two. What it has not got is a no-argument constructor, and saying "`map` is not generic"
# was a false statement about the program.
expect "$ZERG" map-as-a-constructor E471 <<'EOF'
fn main() {
	m := map[str, int]()
	print 1
}
EOF

# GRAMMAR#postfix puts type arguments in the postfix chain, so `f[A, B]` with no call is
# grammatical. This compiler instantiates a generic at the call and has nothing for a bare
# one to be.
expect "$ZERG" type-arguments-with-no-call E268 <<'EOF'
fn main() {
	m := map[str, int]
	print 1
}
EOF

# GRAMMAR#stmt-list separates statements with `stmt-sep+`, and the parser asked for one only
# if it was there. So a second statement on the same line was read, quietly: `print "a" "b"`
# printed `a` and `x := 1 2` bound 1. The seed has refused this since it was written, so the
# two compilers disagreed about which programs exist.
expect "$ZERG" two-statements-on-one-line E205 <<'EOF'
fn main() {
	print "a" "b"
}
EOF

expect "$ZERG" two-bindings-on-one-line E205 <<'EOF'
fn main() {
	x := 1 2
	print x
}
EOF

# `\u{…}` past U+10FFFF, or inside the surrogate block, is not a code point — so not an
# escape. BOTH compilers used to substitute U+FFFD and say nothing, so a program that named
# one character got another: these two printed 65533.
expect "$ZERG" unicode-escape-past-the-code-space E109 <<'EOF'
fn main() {
	print int('\u{110000}')
}
EOF

expect "$ZERG" unicode-escape-on-a-surrogate E109 <<'EOF'
fn main() {
	print int('\u{D800}')
}
EOF

# The four unclosed-literal codes. Each is a rule the lexer has reported since it was
# written and NOTHING asserted — found by the meta-gate over the catalogue, which is the
# failure that gate exists for: a code with no case is an identity nobody checks, and it
# breaks nothing while it drifts.
expect "$ZERG" empty-rune-literal E102 <<'EOF'
fn main() {
	x := ''
	print x
}
EOF

expect "$ZERG" triple-quoted-string-never-closed E105 <<'EOF'
fn main() {
	x := """abc
	print x
}
EOF

expect "$ZERG" raw-string-with-no-closing-quote E106 <<'EOF'
fn main() {
	x := r"abc
	print x
}
EOF

expect "$ZERG" command-literal-with-no-closing-backtick E107 <<'EOF'
fn main() {
	x := `ls
	print x
}
EOF

# The two if-EXPRESSION shapes that need more than a parse. Both used to be reported as the
# token that could not continue the condition — "expected `}`, found `:=`" and "expected
# `{`, found `:=`" — for forms GRAMMAR#if-expr and GRAMMAR#if-head derive. The `else if`
# chain, which is the third, now parses (codegen/if_expr_forms).
expect "$ZERG" if-expression-with-a-multi-statement-branch E269 <<'EOF'
fn main() {
	c := true
	x := if c {
		a := 2
		a * 3
	} else {
		0
	}
	print x
}
EOF

expect "$ZERG" if-expression-with-a-binding-head E270 <<'EOF'
fn get() -> int? {
	return 3
}

fn main() {
	x := if v := get() { v } else { -1 }
	print x
}
EOF

# `NotImplemented: unsafe` and `NotImplemented: asm` — the keyword and nothing else. A
# marker that names no form, gives no place, and does not say that the module-level
# `unsafe { … }` GROUP spelled the same way does work.
expect "$ZERG" unsafe-as-an-expression E224 <<'EOF'
fn main() {
	x := unsafe { 3 + 4 }
	print x
}
EOF

expect "$ZERG" inline-assembly E271 <<'EOF'
fn main() {
	asm("nop")
	print 1
}
EOF

# `nil` AS A PATTERN. GRAMMAR#literal makes `nil` a literal and GRAMMAR#literal-pat makes a
# literal a pattern, so this is a well-formed program and belongs here rather than in
# reject-check, where it sat: it was asserting "an optional is not an operand of `==`" —
# the synthesised comparison the arm lowers to, and an operator the program never wrote.
expect "$ZERG" nil-as-a-match-pattern E472 <<'EOF'
fn f(x: int?) -> int {
	return match x {
		nil => 0
		_ => 1
	}
}

fn main() {
	print f(nil)
}
EOF

# GRAMMAR#decorator is a COMMA LIST of items, and this read the brackets as one item with a
# bag of identifiers in it: every name that was not `derive` went on as a derive argument,
# so `#[derive(Eq), frobnicate]` asked to derive `frobnicate`. Each item now gets its own
# answer, and an unknown one alone has had one all along.
#
# The second item is an UNKNOWN name and not `sealed`, which is what it used to be: `sealed`
# now has a code of its own (E496, a reserved decorator that is not built), so asserting E217
# through it would have stopped testing the unknown-decorator rule the moment it got one.
expect "$ZERG" second-decorator-in-a-comma-list E217 <<'EOF'
#[derive(Eq), frobnicate]
struct P {
	pub v: int
}

fn main() {
	print 1
}
EOF

# `type Alias = P` three lines under `struct P` was told "`P` is not declared in this
# program". c_build_typedefs runs before the struct and enum registries exist, so the
# lookup answered no for every type the program declares — a diagnostic stating something
# false about the reader's own source, which sends them looking for a declaration that is
# already there. The rule under it is the true one, and the one this case pins.
expect "$ZERG" typedef-over-a-user-struct E304 <<'EOF'
struct P {
	pub v: int
}

type Alias = P

fn main() {
	print 1
}
EOF

# --- three productions GRAMMAR derives and this compiler does not build ------------
#
# Each of these had NO parse path at all, so the answer was whatever token the reader
# tripped over — the message a TYPO gets, about a form the grammar plainly derives. That
# breaks the contract in docs/conformance.md: a form is either lowered correctly or refused
# BY NAME. The parser now READS each one to its end and refuses it as itself, with a place.

# GRAMMAR#closure-param gives a closure parameter a default — `( '=' expr )?`, the same
# tail GRAMMAR#param gives a named declaration's. A named `fn` has had defaults all along,
# so this is the closure half of one feature; the reader stopped at the name and answered
# "a parameter needs a name, and `=` is not one", which says the `=` is in the wrong place
# rather than that the form is unbuilt.
expect "$ZERG" closure-parameter-default E285 <<'EOF'
fn main() {
	f := fn (x: int = 5) -> int {
		return x
	}
	print f(1)
}
EOF

# GRAMMAR#param-type puts `mut &` in a function TYPE — `param-type ::= ( 'mut' '&' )? type`
# — and docs/code/functions.md says the distinction is real and cannot be written down. It
# was refused by the tuple/parameter-list reader's `expect(Comma)`, so `fn(mut &int) -> bool`
# answered "expected `,`, found `&`": a punctuation complaint about a type the language has.
expect "$ZERG" mut-ref-in-a-fn-type E286 <<'EOF'
fn bump(mut &n: int) -> bool {
	n = n + 1
	return true
}

fn main() {
	g: fn(mut &int) -> bool = bump
	print 1
}
EOF

# GRAMMAR#fn-sig opens a spec member with `'unsafe'? 'mut'? 'fn'`, so `unsafe fn f()` in a
# spec IS a derivation. It used to be turned away by E276 — the catch-all for a token that
# starts no member at all — which DENIED the derivation and cited GRAMMAR#spec-member while
# doing it. A top-level `unsafe fn` gets E264 and a place, so the two spellings of one
# unenforced trust boundary now answer alike.
expect "$ZERG" unsafe-in-a-spec-signature E287 <<'EOF'
spec Raw {
	unsafe fn peek() -> int
}

fn main() {
	print 1
}
EOF

# The SEED had the opposite failure on the same form, and it is the worse one: it read the
# signature, recorded `unsafe` on it, and then dropped the requirement — a plain `fn peek()`
# satisfied `unsafe fn peek()`, built, and RAN. Its Tier 2 table has claimed `unsafe` is
# refused since it was written; this is the path where that was not true.
expect "$ZERG0" seed-unsafe-in-a-spec-signature "an \`unsafe\` spec signature is not yet supported" <<'EOF'
spec Raw {
	unsafe fn peek() -> int
}

struct T {
	pub v: int
}

impl Raw for T {
	fn peek() -> int {
		return this.v
	}
}

fn main() {
	t := T(7)
	print t.peek()
}
EOF

# --- what a block expression still cannot do ---------------------------------------------
#
# A `{ … }` is an expression now (GRAMMAR, group 2), and these two are what the form owes.
#
# A BLOCK'S VALUE NEEDS A TYPE THE COMPILER CAN NAME. The last statement is typed against
# what the block binds, so a name bound inside it is no longer the problem it once was —
# but a value with no type at all still is, and it is said rather than emitted as whatever
# `int64_t` would make of it.
expect "$ZERG" a-block-whose-value-has-no-type E480 <<'EOF'
fn main() {
	xs := {
		[]
	}
	print xs.len()
}
EOF

# A MATCH ARM'S BINDING IS SPLICED IN AS TEXT, not opened as a scope: every read of the
# name inside the arm answers from the substitution before it asks what is bound. So a
# binding taking that same name inside the arm's block would declare a C local nothing ever
# reads — the arm's payload answering in its place, silently — and it is refused instead.
expect "$ZERG" a-binding-shadowing-a-match-arms-pattern E481 <<'EOF'
enum Shape {
	Dot
	Line(int)
}

fn main() {
	s := Shape.Line(4)
	print match s {
		Shape.Dot     => 0
		Shape.Line(n) => {
			n := 1
			n
		}
	}
}
EOF

# GRAMMAR#fn-type carries an `unsafe` marker, and `unsafe` is a trust boundary this compiler
# does not enforce — so the TYPE is refused rather than spelled and never honoured. Both type
# positions reported the token after the keyword before this: neither named the form.
expect "$ZERG" an-unsafe-fn-type E488 <<'EOF'
fn f(x: int) -> int {
	return x
}

fn main() {
	g: unsafe fn(int) -> int = f
	print g(1)
}
EOF

# GRAMMAR#impl-decl's target is a `type`, which derives a dotted name — but an implementation
# is keyed here by the target's bare name, which a type reached through an import has not got.
expect "$ZERG" an-impl-on-a-dotted-target E489 <<'EOF'
import "text"

impl text.Pair {
	fn sum() -> int {
		return 0
	}
}

fn main() {
	print 0
}
EOF

# The other half, and not the same question: GRAMMAR writes an `impl`'s SPEC as a bare
# `type-name`, so a dotted name there is a form the grammar does not derive at all.
expect "$ZERG" an-impl-spec-reached-through-an-import E490 <<'EOF'
import "text"

struct Bot {
	pub n: int
}

impl text.Named for Bot {
	fn label() -> str {
		return "bot"
	}
}

fn main() {
	print 0
}
EOF

# A parameterized alias is one instantiation per argument, the reason a generic `enum` is
# E212 and a generic `struct` E215. This one had no code and no place until it had this.
expect "$ZERG" a-generic-type-alias E491 <<'EOF'
type Pairs[T] = list[T]

fn main() {
	print 0
}
EOF

# GRAMMAR#variant-pat's payload is a `pattern-list`, so a pattern inside a pattern is derived.
# This compiler takes a binding name or `_` there, and said so with a bare parser complaint.
expect "$ZERG" a-sub-pattern-in-a-variant-payload E492 <<'EOF'
enum Inner {
	A(int)
	B
}

enum Outer {
	Wrap(Inner)
}

fn main() {
	o := Outer.Wrap(Inner.A(3))
	print match o {
		Outer.Wrap(Inner.A(v)) => v
		_                      => 0
	}
}
EOF

# A range is an iterable and a membership test, and not a value that can be bound.
expect "$ZERG" a-range-bound-as-a-value E493 <<'EOF'
fn main() {
	r := 2..5
	print 3 in r
}
EOF

# `is` names one of the built-in error kinds here, where GRAMMAR#cmp-expr takes any type-name.
expect "$ZERG" an-is-test-on-a-non-error-type E494 <<'EOF'
fn main() {
	x := 3
	print x is int
}
EOF

# GRAMMAR#decorator is one item or more. The loop that reads them never ran when the `]` was
# already there, so `#[]` was read, dropped, and the declaration compiled unchanged.
expect "$ZERG" an-empty-decorator E495 <<'EOF'
#[]
fn work() {
	nop
}

fn main() {
	work()
}
EOF

# `#[sealed]` is RESERVED — GRAMMAR group 7 gives it a meaning — so it is not the sentence an
# unknown decorator gets. The word is right and the behaviour is not built, which is what a
# reader who wrote it needs to know: nothing is protecting the constructor they sealed.
expect "$ZERG" the-reserved-sealed-decorator E496 <<'EOF'
#[sealed]
struct R {
	pub x: int
}

fn main() {
	print R(3).x
}
EOF

# The arguments are what a derive IS. A bare `#[derive]` or `#[derive()]` was read and dropped,
# so the type went on with no impls while the line above it said it had some.
expect "$ZERG" a-derive-with-no-specs E497 <<'EOF'
#[derive()]
struct P {
	pub x: int
}

fn main() {
	print P(1).x
}
EOF

# GRAMMAR#chan-type has three alternatives and `<-chan[T]<-` is none of them. The trailing
# arrow was read after the `]` with no regard for the leading one, so a send-only signature
# was honoured as a receive-only one.
expect "$ZERG" a-channel-facing-both-ways E498 <<'EOF'
fn take(c: <-chan[int]<-) {
	nop
}

fn main() {
	ch := chan[int](1)
	take(ch)
}
EOF

# --- the parser's channel: forms that used to be refused with no code ------------------
#
# Each of these was already named — a sentence, and nothing a case here could pin, because
# refuse-check asserts a `zerg` case's CODE and these had none. They are the not-yet half of
# the split docs/conformance.md names; the permanent half is in reject-check.sh under the
# same heading.

expect "$ZERG" a-statement-where-an-expression-is-wanted E605 <<'EOF'
fn main() {
	x := break
	print x
}
EOF

expect "$ZERG" a-token-that-opens-no-expression E606 <<'EOF'
fn main() {
	x := =
	print x
}
EOF

expect "$ZERG" a-reassignment-as-a-match-arm-body E607 'a reassignment in an arm' <<'EOF'
fn main() {
	mut y := 0
	match 1 {
		1 => y = 2
		_ => y = 3
	}
	print y
}
EOF

expect "$ZERG" a-send-as-a-match-arm-body E607 'a send in an arm' <<'EOF'
fn main() {
	ch := chan[int](1)
	match 1 {
		1 => ch <- 2
		_ => print "n"
	}
}
EOF

if [ $fail -ne 0 ]; then
	echo "refuse-check: $fail of $((pass + fail)) cases were not refused as they should be"
	exit 1
fi
echo "refuse-check: $pass cases refused by name, none left to cc"
