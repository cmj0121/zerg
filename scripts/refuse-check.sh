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

# expect <compiler> <name> <wanted-substring> — the program arrives on stdin.
expect() {
	local cc=$1 name=$2 want=$3
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

	# The cache path is not the only shape a cc error takes: a build given `-o` puts its
	# intermediate C beside the output instead, so a cc diagnostic can carry no cache path
	# at all and still be one. reject-check.sh has had this assertion since it was written.
	# A cc diagnostic and one of ours are told apart by SHAPE, not by the path in them.
	# `#line` directives now point cc at the `.zg`, so a cc error can name the source file
	# the programmer wrote — which is better for a user and blinds the older test, since
	# neither `.zerg-cache` nor a `.c:` appears. What still differs is the layout: cc opens
	# a line with `path:line:col: error:`, and this compiler opens with `error:` and puts
	# the place on an indented `-->` line beneath it.
	if is_cc_diag "$out"; then
		echo "VIA CC    $name — the message is a cc diagnostic, not this compiler's"
		fail=$((fail + 1))
		return
	fi

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

# An arm's guard goes BEFORE the `=>` (GRAMMAR:422). Written after the body it was silently
# DROPPED, so the arm compiled unconditional AND counted toward exhaustiveness as if it had
# no guard — the two halves of the guard rule, both wrong, from one easy typo.
expect "$ZERG" arm-guard-after-the-body "goes before the \`=>\`" <<'EOF'
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

# ...including one whose declared value HAPPENS to equal its position, which is otherwise
# indistinguishable from declaring nothing — so it was read and then quietly dropped.
expect "$ZERG" discriminant-that-looks-like-its-position "a discriminant" <<'EOF'
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
expect "$ZERG" conversion-of-a-carrier "may not have one" <<'EOF'
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
expect "$ZERG" non-int-conversion-of-an-enum "converts to \`int\`" <<'EOF'
enum K {
	A
	B
}

fn main() {
	print float(A)
}
EOF

# Each side of an Either holds exactly one value.
expect "$ZERG" either-side-with-two-values "holds exactly one value" <<'EOF'
fn f() -> Result[int] {
	return Left(1, 2)
}

fn main() { print f() ?? 0 }
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

# --- every form is HANDLED: implemented, or refused by name ------------------------
#
# `parse_primary` used to end `p_advance(p); return ENil` — an unread token was consumed and
# the expression became nil, which emits `0`. So every form this parser did not know became
# the number zero, in silence. These are the forms that landed there, plus the ones that
# reached cc or the linker instead. None of them may do either now.

expect "$ZERG" unread-token-in-an-expression "is not an expression this compiler reads" <<'EOF'
fn main() {
	print 1 as 2
}
EOF

expect "$ZERG" non-ascii-rune-literal "non-ASCII rune literal" <<'EOF'
fn main() {
	print int('\u{20AC}')
}
EOF

expect "$ZERG" mut-fn-method "receiver is not written back" <<'EOF'
struct C {
	n: int
}

impl C {
	mut fn bump() {
		this.n = this.n + 1
	}
}

fn main() { print 1 }
EOF

expect "$ZERG" generic-enum "a generic enum" <<'EOF'
enum E[T] {
	A(T)
	B
}

fn main() { print 1 }
EOF

expect "$ZERG" associated-value-binding "an associated value binding" <<'EOF'
struct B {
	n: int
}

impl B {
	LIMIT := 5
}

fn main() { print 1 }
EOF

expect "$ZERG" unknown-decorator "the decorator" <<'EOF'
#[dyn]
struct P {
	x: int
}

fn main() { print 1 }
EOF

expect "$ZERG" equality-on-an-aggregate "which this compiler does not generate" <<'EOF'
#[derive(Eq)]
struct P {
	x: int
}

fn main() {
	print P(1) == P(1)
}
EOF

expect "$ZERG" field-default "a default on field" <<'EOF'
struct P {
	x: int = 7
}

fn main() { print 1 }
EOF

expect "$ZERG" nested-block-statement "block as a statement" <<'EOF'
fn main() {
	{
		print 1
	}
}
EOF

expect "$ZERG" struct-pattern-binding "a struct pattern" <<'EOF'
struct P {
	x: int
}

fn main() {
	P{x} := P(3)
	print x
}
EOF

expect "$ZERG" for-mut-binding "the mutable loop binding" <<'EOF'
fn main() {
	for mut v in [1, 2] {
		print v
	}
}
EOF

# A body that declares a result and falls off the end used to emit a C function with no
# return: the call answered whatever was in the return register.
expect "$ZERG" body-falls-off-the-end "falls off the end" <<'EOF'
fn f(n: int) -> int {
	if n > 0 {
		return 1
	}
}

fn main() { print f(1) }
EOF

expect "$ZERG" struct-cycle-by-value "part of a cycle of by-value declarations" <<'EOF'
struct A {
	b: B
}

struct B {
	a: A
}

fn main() { print 1 }
EOF

# A NAME NOTHING BINDS is the commonest mistake anyone makes, and it used to be spelled
# `zg_<n>` and handed to cc. So did a call to a function nothing declares — which is also
# how the specified-but-unbuilt raw-pointer builtins arrived.
expect "$ZERG" undefined-name "undefined name" <<'EOF'
fn main() {
	print nope
}
EOF

expect "$ZERG" undefined-function "undefined function" <<'EOF'
fn main() {
	print nope(1)
}
EOF

expect "$ZERG" raw-pointer-builtin "undefined function \`addr\`" <<'EOF'
fn main() {
	mut n := 1
	print addr(n)
}
EOF

# `expect` used to advance on a match and say NOTHING otherwise, so every truncated form
# derailed quietly and whatever the parser built from the wreckage reached the emitter.
expect "$ZERG" truncated-guard "expected \`{\`" <<'EOF'
fn main() {
	print guard
}
EOF

expect "$ZERG" truncated-fn "expected \`(\`" <<'EOF'
fn main() {
	print fn
}
EOF

expect "$ZERG" truncated-chan-type "found \`print\`" <<'EOF'
fn main() {
	chan
	print 1
}
EOF

expect "$ZERG" associated-type-binding "an associated type binding" <<'EOF'
struct B {
	n: int
}

impl B {
	type Item = int
}

fn main() { print 1 }
EOF

expect "$ZERG" impl-item-that-is-not-a-method "as an \`impl\` item" <<'EOF'
struct B {
	n: int
}

impl B {
	print 1
}

fn main() { print 1 }
EOF

# A method that MUTATES its receiver cannot be served from a materialised temp: the edit
# lands on the copy and is lost. `m["a"].append(3)` compiled and silently did nothing.
expect "$ZERG" mutating-method-on-a-map-index "MUTATES its list" <<'EOF'
fn main() {
	mut m: map[str, list[int]] = {:}
	m["a"] = [1, 2]
	m["a"].append(3)
	print m["a"].len()
}
EOF

# A field the type does not have was spelled `zg_<fld>` and handed to cc — the same class
# as an undefined name, answerable from the same tables.
expect "$ZERG" field-the-struct-does-not-have "no field" <<'EOF'
struct P {
	n: int
}

fn main() {
	p := P(1)
	print p.z
}
EOF

# Too FEW constructor arguments has been named for a while; too many reached cc as an
# "excess elements" WARNING, so it compiled and the extra values were dropped.
expect "$ZERG" too-many-constructor-arguments "fields and this gives" <<'EOF'
struct P {
	n: int
}

fn main() {
	print P(1, 2).n
}
EOF

# Five forms the specification described as working that the shipped compiler does not have.
# Each reached cc, or named a symptom two steps from what was written.
expect "$ZERG" for-in-over-a-str "binds each code point" <<'EOF'
fn main() {
	for c in "ab" {
		print 1
	}
}
EOF

expect "$ZERG" ordering-on-an-aggregate "does not generate" <<'EOF'
#[derive(Ord)]
struct P {
	x: int
}

fn main() {
	print P(1) < P(2)
}
EOF

expect "$ZERG" rendering-a-composite "as text" <<'EOF'
struct P {
	x: int
}

fn main() {
	print str(P(1))
}
EOF

expect "$ZERG" unknown-list-method "the list method" <<'EOF'
fn main() {
	xs := [1, 2, 3]
	print xs.slice(0, 2).len()
}
EOF

expect "$ZERG" tuple-pattern-in-an-arm "a tuple pattern in a" <<'EOF'
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

if [ $fail -ne 0 ]; then
	echo "refuse-check: $fail of $((pass + fail)) cases were not refused as they should be"
	exit 1
fi
echo "refuse-check: $pass cases refused by name, none left to cc"
