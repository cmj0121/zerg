#!/usr/bin/env bash
#
# reject-check — every program here is ILL-FORMED, and the compiler must say so itself.
#
# This is the sibling of refuse-check.sh, and the two ask different questions on purpose:
#
#   refuse   a form this compiler has not BUILT yet. `NotImplemented: …`, and the case
#            disappears from that script when the feature lands.
#   reject   a program that is not Zerg. The answer is the LANGUAGE's, it is permanent,
#            and no future feature makes it legal.
#
# They are separate files because those two lists have different lifetimes, and mixing a
# temporary refusal with a normative rejection makes it impossible to read either as a
# contract.
#
# The gate is not that these fail. Every one of them failed before this script existed —
# what differed is WHO said so. A program the compiler emits anyway reaches cc, which
# rejects generated C at a line in a file nobody wrote, and before and after a fix the
# case still "fails", so no build gate can tell them apart. Hence five assertions per
# case:
#
#   1. a non-zero exit
#   2. the expected sentence
#   3. no mention of .zerg-cache
#   4. nothing shaped like a cc diagnostic (`<file>:LINE:COL: error:` opening a line)
#   5. a `--> file:line:col` line, so the reader is told WHERE
#   6. the SEED refuses it too — unless the case says `seed-gap`, naming a rule the seed
#      does not enforce (its gaps are its own contract, in src/bootstrap/README.md)
#
# The fourth is not redundant with the third. A build given `-o` puts its intermediate C
# beside the output rather than in the cache, so a cc error can carry no cache path at all
# and still be a cc error — which is exactly the failure this gate exists to catch.
#
# The fifth is what this branch's diagnostics work bought: every rule in check.zg reports
# through one place that knows the statement's file, line and column, so a rule that loses
# its position is caught here rather than noticed by a user. Nothing else would see it —
# the sentence still matches.
#
# The sixth makes zerg0 the ORACLE. The seed has had a semantic-analysis pass all along
# and diagnoses every rule here; a rule it enforces and `zerg` does not is a rule `zerg`
# LOST on the way to self-hosting, which is how this whole class went unnoticed. Only the
# sentence `zerg` prints is normative — the seed merely has to say no — because the two
# word their diagnostics differently and pinning both wordings would pin nothing useful.

set -u

# shellcheck source=scripts/lib/diag.sh
. "$(dirname "$0")/lib/diag.sh"

ZERG=${ZERG:-./bin/zerg}
ZERG0=${ZERG0:-./bin/zerg0}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

pass=0
fail=0

# reject <name> <wanted-substring> — the program arrives on stdin. It is put to BOTH
# compilers: `zerg` must answer with the wanted sentence, and the seed must merely refuse
# it, since the two word their diagnostics differently and only the reject list is
# normative.
#
# Quoting a wanted string, which is fiddlier than it looks because these sentences carry
# both backticks and apostrophes:
#
#   contains a backtick    SINGLE-quote it. In double quotes bash reads a backtick as
#                          command substitution, which does NOT fail — it silently shortens
#                          the string being matched, so a correct message reports as a
#                          mismatch. Most messages here quote source in backticks.
#   contains an apostrophe DOUBLE-quote it, or the apostrophe closes the single quote and
#                          the rest of the file is read as one string.
#   contains both          rephrase the assertion to a substring that has only one.
reject() {
	local name=$1 want=$2
	shift 2

	# the markers COMPOSE: a case can be both a seed gap and a place the parser owes, and
	# reading one `$3` silently dropped the second — the gate then failed on an exception
	# that was declared right beside the one it honoured.
	local flags=" $* "
	local src="$tmp/$name.zg"
	cat >"$src"

	local out status
	# `--emit bin`, not `--emit c`: the C stage stops BEFORE cc, so under it a program
	# only cc would reject looks accepted and assertions 3 and 4 can never fire. Linking
	# for real is what makes "the compiler said so, not cc" a claim this gate can check —
	# and it costs nothing while the gate is green, because a program the compiler rejects
	# never reaches cc anyway.
	out=$("$ZERG" build --emit bin -o "$tmp/$name.bin" "$src" 2>&1 >/dev/null)
	status=$?

	if [ $status -eq 0 ]; then
		echo "ACCEPTED  $name — the compiler emitted an ill-formed program instead of rejecting it"
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

	# every rule in check.zg reports through chk_at, which knows the statement's place —
	# so a case that comes back without one is a rule that lost it, and nothing else here
	# would notice: the sentence would still match.
	#
	# A rule the PARSER enforces is the exception, and it is marked. The parser has no diag
	# channel — it raises — so none of its refusals carries a place; that is one gap, owed
	# once, and `reject-fuzz` counts the whole class. Marking the case keeps a permanent
	# LANGUAGE rule in this file, where its lifetime says it belongs, instead of filing it
	# with the not-yet-built forms next door to dodge one assertion.
	case $flags in *" no-place "*)
		if has_place "$out"; then
			echo "PLACE GAINED  $name — it says where now; drop the no-place marker"
			fail=$((fail + 1))
			return
		fi
		;;
	*)
		if ! has_place "$out"; then
			echo "NO PLACE  $name — the message does not say where: $(echo "$out" | head -1)"
			fail=$((fail + 1))
			return
		fi
		;;
	esac

	# A third argument names a rule the SEED does not enforce yet, and says which. The
	# oracle is worth having and the seed is not perfect; recording the exception beside the
	# case is better than dropping the case or weakening the assertion for everything.
	#
	# It asserts the OPPOSITE, so the exception retires itself: an xfail that merely returns
	# `pass` can never report an unexpected pass, and the day the seed learns the rule the
	# marker and its entry in the seed's README would rot with nothing to say so.
	if [ "${flags#* seed-gap }" != "$flags" ]; then
		if seed_refuses "$name" "$src"; then
			echo "SEED GAP CLOSED  $name — the seed now rejects this; drop the seed-gap marker and its src/bootstrap/README.md entry"
			fail=$((fail + 1))
		else
			pass=$((pass + 1))
		fi
		return
	fi

	if ! seed_refuses "$name" "$src"; then
		echo "SEED      $name — the seed did not reject a program the language rejects"
		fail=$((fail + 1))
		return
	fi

	pass=$((pass + 1))
}

# seed_refuses is whether the SEED itself turned the program away — not whether its build
# failed somehow. The difference is a whole platform: `defer poke(k)` on a `mut &` produced
# C that clang rejects (`-Wint-conversion` is an error there) and gcc only warns about, so
# this gate read as green on macOS and red on Linux for one program and one seed. A cc
# diagnostic is the seed EMITTING the program, which is the thing being asserted against.
seed_refuses() {
	local name=$1 src=$2 out
	out=$("$ZERG0" build "$src" -o "$tmp/$name.seed.o" 2>&1 >/dev/null)
	[ $? -ne 0 ] || return 1
	! is_cc_diag "$out"
}

# --- mutability -------------------------------------------------------------------
#
# A binding is immutable unless it says `mut`, and a write to a field or an element is a
# write to the binding that owns it.

reject assign-to-plain-binding "it is immutable" <<'EOF'
fn main() {
	x := 1
	x = 2
	print(f"{x}")
}
EOF

reject assign-to-value-parameter "it is immutable" <<'EOF'
fn f(a: int) {
	a = 2
	print(f"{a}")
}

fn main() {
	f(1)
}
EOF

reject assign-to-field-of-immutable "a write to a field or element writes the binding" <<'EOF'
struct P {
	x: int
}

fn main() {
	p := P(1)
	p.x = 5
	print(f"{p.x}")
}
EOF

reject assign-to-element-of-immutable "a write to a field or element writes the binding" <<'EOF'
fn main() {
	xs := [1, 2]
	xs[0] = 9
	print(f"{xs[0]}")
}
EOF

reject assign-to-module-const "a constant is never written" <<'EOF'
const N: int = 5

fn main() {
	N = 6
	print(f"{N}")
}
EOF

reject assign-to-loop-variable "it is immutable" <<'EOF'
fn main() {
	for i in 0..2 {
		i = 5
		print(f"{i}")
	}
}
EOF


# --- redeclaration ----------------------------------------------------------------
#
# A name is bound once per block. Shadowing across blocks is legal and load bearing, so
# the sibling and nested cases below are NOT here — they belong to the corpus, which is
# where programs that must run live.

reject redeclare-in-one-block "already declared in this block" <<'EOF'
fn main() {
	x := 1
	x := 2
	print(f"{x}")
}
EOF

reject redeclare-after-inner-block "already declared in this block" <<'EOF'
fn main() {
	a := 1
	if true {
		b := 2
		print(f"{b}")
	}
	a := 3
	print(f"{a}")
}
EOF

# --- conditions -------------------------------------------------------------------
#
# Zerg has no truthiness. Every form that asks a question asks it of a bool.

reject int-as-if-condition "must be bool, and this one is int" <<'EOF'
fn main() {
	if 1 {
		print("yes")
	}
}
EOF

reject str-as-if-condition "must be bool, and this one is str" <<'EOF'
fn main() {
	s := "abc"
	if s {
		print("yes")
	}
}
EOF

reject int-as-for-condition 'the condition of a `for` must be bool' <<'EOF'
fn main() {
	mut n := 3
	for n {
		n = n - 1
	}
	print(f"{n}")
}
EOF

reject int-as-conditional-return 'the condition of a conditional `return` must be bool' <<'EOF'
fn f() -> int {
	return 1 if 2
	return 0
}

fn main() {
	print(f"{f()}")
}
EOF

reject int-as-if-expression-condition 'the condition of an `if` expression must be bool' <<'EOF'
fn main() {
	x := if 5 {
		1
	} else {
		2
	}
	print(f"{x}")
}
EOF

# --- operands ---------------------------------------------------------------------
#
# An operator says what it takes, and C's answer to the same question is not this
# language's: `1 + "s"` was pointer arithmetic and printed an address, `true + 1` printed
# 2, `not 1` printed false. A str operand is BOUND first rather than written inline: a
# quote inside an f-string hole is its own unrelated argument.

reject int-plus-str 'operator `+` takes numeric operands' <<'EOF'
fn main() {
	s := "s"
	print(f"{1 + s}")
}
EOF

reject str-plus-int 'operator `+` takes numeric operands' <<'EOF'
fn main() {
	s := "s"
	print(f"{s + 1}")
}
EOF

reject bool-plus-int 'operator `+` takes numeric operands' <<'EOF'
fn main() {
	print(f"{true + 1}")
}
EOF

reject int-as-logical-operand 'operator `and` takes bool operands' <<'EOF'
fn main() {
	print(f"{1 and 2}")
}
EOF

reject order-int-against-str 'orders two numbers or two strs' <<'EOF'
fn main() {
	s := "s"
	print(f"{1 < s}")
}
EOF

reject compare-int-with-str 'cannot compare int and str' <<'EOF'
fn main() {
	s := "s"
	print(f"{1 == s}")
}
EOF

reject bitwise-on-float 'operator `&` takes int operands' <<'EOF'
fn main() {
	print(f"{3.0 & 1}")
}
EOF

reject bitwise-on-bool 'operator `&` takes int operands' <<'EOF'
fn main() {
	print(f"{true & 1}")
}
EOF

reject negate-a-str 'operator `-` takes a numeric operand' <<'EOF'
fn main() {
	s := "a"
	print(f"{-s}")
}
EOF

reject not-an-int 'operator `not` takes a bool operand' <<'EOF'
fn main() {
	print(f"{not 1}")
}
EOF

# --- declared type versus value ---------------------------------------------------
#
# `b: bool = 1` is the one that reached nothing at all: a bool and an int are both
# int64_t in C, so it compiled and printed `true`. The others reached cc.
#
# The legal neighbours are NOT here — `x: float = 1` widens, `x: int? = 5` wraps,
# `xs: list[str] = []` has nothing to disagree with — because those must run.

reject bind-str-to-int 'cannot bind str to a int binding' <<'EOF'
fn main() {
	x: int = "hello"
	print(f"{x}")
}
EOF

reject bind-int-to-bool 'cannot bind int to a bool binding' <<'EOF'
fn main() {
	b: bool = 1
	print(f"{b}")
}
EOF

reject bind-float-to-int 'cannot bind float to a int binding' <<'EOF'
fn main() {
	x: int = 1.5
	print(f"{x}")
}
EOF

reject bind-int-list-to-str-list 'cannot bind list[int] to a list[str] binding' <<'EOF'
fn main() {
	ys: list[str] = [1, 2]
	print(f"{ys[0]}")
}
EOF

# --- returned value ---------------------------------------------------------------
#
# A signature is a promise. The conditional `return` is here on its own because it takes a
# different path through the emitter than the plain one.

reject return-str-from-int-fn 'this function answers int, and this returns str' <<'EOF'
fn f() -> int {
	return "nope"
}

fn main() {
	print(f"{f()}")
}
EOF

reject return-int-from-bool-fn 'this function answers bool, and this returns int' <<'EOF'
fn f() -> bool {
	return 1
}

fn main() {
	print(f"{f()}")
}
EOF

reject conditional-return-wrong-type 'this function answers int, and this returns str' <<'EOF'
fn f(n: int) -> int {
	return "x" if n > 0
	return 0
}

fn main() {
	print(f"{f(1)}")
}
EOF

# --- call shape -------------------------------------------------------------------
#
# The counts are knowable where the call and the signature are both in hand. They used to
# be cc's answer, about a C prototype this compiler wrote.
#
# The METHOD case is here because its receiver already fills parameter 0, so the count a
# call is measured against is shifted by one — and the message says `P.add`, which is what
# was written, not the `P#add` the signature table keys on.

reject call-with-too-few-arguments '`add` needs 2 arguments and this gives 1' <<'EOF'
fn add(a: int, b: int) -> int {
	return a + b
}

fn main() {
	print(f"{add(1)}")
}
EOF

reject call-with-too-many-arguments '`add` takes 2 arguments and this gives 3' <<'EOF'
fn add(a: int, b: int) -> int {
	return a + b
}

fn main() {
	print(f"{add(1, 2, 3)}")
}
EOF

reject call-past-a-default '`scale` takes 2 arguments and this gives 3' <<'EOF'
fn scale(n: int, by: int = 2) -> int {
	return n * by
}

fn main() {
	print(f"{scale(5, 3, 9)}")
}
EOF

reject method-with-too-many-arguments '`P.add` takes 2 arguments and this gives 3' <<'EOF'
struct P {
	x: int
}

impl P {
	fn add(k: int) -> int {
		return this.x + k
	}
}

fn main() {
	p := P(3)
	print(f"{p.add(1, 2)}")
}
EOF

# --- argument versus parameter ----------------------------------------------------
#
# The method case pins the two numbers apart: its receiver fills parameter 0 without being
# written, so the parameter index and the argument's place on the line differ by one, and
# the message says the one the reader can count.

reject float-argument-into-int-parameter '`f` takes int as argument 1, and this gives float' <<'EOF'
fn f(a: int) -> int {
	return a
}

fn main() {
	print(f"{f(1.5)}")
}
EOF

reject str-argument-into-int-parameter '`add` takes int as argument 2, and this gives str' <<'EOF'
fn add(a: int, b: int) -> int {
	return a + b
}

fn main() {
	s := "x"
	print(f"{add(1, s)}")
}
EOF

reject str-argument-into-a-method '`P.add` takes int as argument 1, and this gives str' <<'EOF'
struct P {
	x: int
}

impl P {
	fn add(k: int) -> int {
		return this.x + k
	}
}

fn main() {
	p := P(3)
	s := "z"
	print(f"{p.add(s)}")
}
EOF

# --- the shapes a review found, after the eight rules were written -----------------
#
# A match arm's GUARD is a condition and reached no checker — it is rendered in three
# places and none of them was c_cond. The WRAPPING arithmetic operators (`+%`, `-%`, `*%`)
# differ from the checked ones in what they do on overflow, not in what they take, and the
# family list had been retyped rather than derived. And an ASSIGNMENT is the fourth slot a
# value enters: `mut b: bool = true` then `b = 1` is the declaration bug one statement
# later, and the rule set had covered the other three.

reject int-as-match-arm-guard "the condition of a match arm's guard must be bool" <<'EOF'
fn main() {
	n := 1
	print(match n {
		_ if 1 => "yes"
		_      => "no"
	})
}
EOF

reject wrapping-operator-on-a-bool 'operator `+%` takes numeric operands' <<'EOF'
fn main() {
	print(f"{true +% 1}")
}
EOF

reject assign-int-to-a-bool-binding 'cannot assign int to `b`, which holds bool' <<'EOF'
fn main() {
	mut b: bool = true
	b = 1
	print(f"{b}")
}
EOF

reject assign-str-to-an-int-field 'cannot assign str to that part of `p`, which holds int' <<'EOF'
struct P {
	x: int
}

fn main() {
	mut p := P(1)
	p.x = "s"
	print(f"{p.x}")
}
EOF

# --- the aggregates ty_name could not tell apart ------------------------------------
#
# `chk_fits` compared with `ty_name`, which is the DIAGNOSTIC SPELLING of a type and
# collapses TUnknown, TTuple and TMap onto one name — so every caller had to restrict
# itself to the scalars, and a struct bound to the wrong struct went to cc. `ty_eq` is a
# real comparison over the Ty enum, and these are the shapes it reaches that the spelling
# could not: two unrelated structs through all three slots, and a nested list whose
# elements differ two levels down.

reject bind-a-struct-to-another-struct 'cannot bind B to a A binding' <<'EOF'
struct A {
	x: int
}

struct B {
	y: int
}

fn main() {
	a: A = B(1)
	print(f"{a.x}")
}
EOF

reject pass-a-struct-where-another-goes '`take` takes A as argument 1, and this gives B' <<'EOF'
struct A {
	x: int
}

struct B {
	y: int
}

fn take(v: A) -> int {
	return v.x
}

fn main() {
	print(f"{take(B(1))}")
}
EOF

reject return-a-struct-the-signature-does-not-name 'this function answers A, and this returns B' <<'EOF'
struct A {
	x: int
}

struct B {
	y: int
}

fn f() -> A {
	return B(1)
}

fn main() {
	print(f"{f().x}")
}
EOF

reject bind-a-nested-list-of-the-wrong-element 'cannot bind list[list[int]] to a list[list[str]] binding' <<'EOF'
fn main() {
	xs: list[list[str]] = [[1]]
	print(f"{xs.len()}")
}
EOF

# --- the text the lexer could not read ---------------------------------------------
#
# These are LEXICAL errors and used to be reported by the parser, as "`b'b` is not an
# expression this compiler reads" — the wrong layer, the wrong problem, and a lexeme that
# is a fragment of what was written. `0x` did not report at all: it lowered to a C `0x`,
# which cc read as zero, so a malformed literal compiled and answered 0.
#
# `b'ba'` is here beside `'ba'` because a bad rune literal used to leave the lexer standing
# INSIDE it, and the surviving quote opened a second one — one mistake, two messages.

reject rune-with-two-characters 'E103 a rune literal holds exactly one character' <<'EOF'
fn main() {
	c := 'ba'
	print(f"{c}")
}
EOF

reject byte-with-two-characters 'E103 a rune literal holds exactly one character' <<'EOF'
fn main() {
	c := b'ba'
	print(f"{c}")
}
EOF

reject string-that-never-closes 'E101 a string literal is not closed' <<'EOF'
fn main() {
	s := "no closing quote
	print(s)
}
EOF

reject a-character-no-token-uses 'E104 this character is not part of any Zerg token' <<'EOF'
fn main() {
	x := 1 @ 2
	print(f"{x}")
}
EOF

reject based-number-with-no-digits 'E108 a based number needs at least one digit' <<'EOF'
fn main() {
	n := 0x
	print(f"{n}")
}
EOF

# --- the forms that used to escape to cc -------------------------------------------
#
# `x == nil` is the first thing anyone reaches for to test an optional, and it lowered to a
# comparison between the carrier struct and 0. `spawn` and `defer` resolve a callee and
# render its arguments with a loop of their own, so neither passed through the one place
# every other call is measured — and they are the two forms whose bad arguments are
# hardest to read back from generated C, since the call cc sees is a thunk.

reject compare-an-optional-with-nil 'an optional is not an operand of `==`' <<'EOF'
fn main() {
	x: int? = nil
	print(f"{x == nil}")
}
EOF

reject compare-an-optional-with-a-value 'an optional is not an operand of `==`' <<'EOF'
fn main() {
	x: int? = 1
	print(f"{x == 1}")
}
EOF

reject spawn-with-too-few-arguments '`work` needs 2 arguments and this gives 1' <<'EOF'
fn work(a: int, b: int) {
	print(f"{a + b}")
}

fn main() {
	spawn work(1)
}
EOF

reject defer-with-the-wrong-argument-type '`note` takes str as argument 1, and this gives int' <<'EOF'
fn note(s: str) {
	print(s)
}

fn main() {
	defer note(1)
	print("body")
}
EOF

# --- an optional is nobody's operand -----------------------------------------------
#
# The first version of this rule went under the EQUALITY branch and closed one operator
# family of five: a carrier is not a scalar, so every other family's own test answered
# "not my business" and `x > 0`, `x + 1`, `x & 1` and `x and true` all still reached cc.
# And a match arm builds its comparison as TEXT, so `nil =>` and `1..=2 =>` are two more
# spellings that meet no rule of their own unless one is hung there.

reject order-an-optional 'an optional is not an operand of `>`' <<'EOF'
fn main() {
	x: int? = 1
	print(f"{x > 0}")
}
EOF

reject add-to-an-optional 'an optional is not an operand of `+`' <<'EOF'
fn main() {
	x: int? = 1
	print(f"{x + 1}")
}
EOF

reject and-an-optional 'an optional is not an operand of `and`' <<'EOF'
fn main() {
	x: bool? = true
	print(f"{x and true}")
}
EOF

reject match-an-optional-against-nil 'an optional is not an operand of `==`' <<'EOF'
fn f(x: int?) -> int {
	return match x {
		nil => 0
		_   => 1
	}
}

fn main() {
	print(f"{f(nil)}")
}
EOF

reject match-an-optional-against-a-range 'an optional is not an operand of `>=`' seed-gap <<'EOF'
fn f(x: int?) -> int {
	return match x {
		1..=2 => 10
		_     => 0
	}
}

fn main() {
	print(f"{f(nil)}")
}
EOF

# --- a borrow may not be captured --------------------------------------------------
#
# GRAMMAR:314: a `mut &` "cannot ESCAPE (be captured by a spawn or stored past the call)".
# `spawn` refused it in a pass of its own and `defer`, which the same sentence covers,
# reached cc — so the refusal moved to the choke point both of them share.

reject spawn-captures-a-borrow 'is a `mut &` and cannot cross a `spawn`' <<'EOF'
fn bump(mut &n: int) {
	n = n + 1
}

fn main() {
	mut k := 1
	spawn bump(k)
	print(f"{k}")
}
EOF

reject defer-captures-a-borrow 'is a `mut &` and cannot cross a `defer`' seed-gap <<'EOF'
fn bump(mut &n: int) {
	n = n + 1
}

fn main() {
	mut k := 1
	defer bump(k)
	print(f"{k}")
}
EOF

# --- a variant's arity, from both sides, and a default judged where it is written ---
#
# `Line(w, h) =>` against a `Line(int)` and `Line(7, 8)` are the same disagreement between
# an arm and a declaration, and both used to write a union member that was never declared.
# A DEFAULT is judged at the signature rather than at the calls that omit it: `spawn` and
# `defer` capture the defaults they backfill, so a bad one was diagnosed only when spawned,
# with a message naming an argument nobody wrote.

reject a-default-that-does-not-fit 'the default for `b` of `f` is int, and the parameter is str' seed-gap <<'EOF'
fn f(a: int, b: str = 1) {
	print(f"{a}{b}")
}

fn main() {
	f(2)
}
EOF

# seed-gap: zerg0 accepts this, and a call that USES the default segfaults — it emits the
# literal where a pointer goes. Recorded in src/bootstrap/README.md.
reject a-mut-ref-with-a-default 'is a `mut &` and cannot have a default' seed-gap <<'EOF'
fn f(a: int, mut &b: int = 0) {
	b = a
}

fn main() {
	print("declared")
}
EOF

reject a-pattern-that-binds-too-much '`Line` carries 1 argument and this pattern binds 2' <<'EOF'
enum Shape {
	Line(int)
	Dot
}

fn area(s: Shape) -> int {
	return match s {
		Line(n, m) => n
		_          => 0
	}
}

fn main() {
	print(f"{area(Dot)}")
}
EOF

reject a-construction-that-gives-too-few '`Line` carries 2 arguments and this gives 1' <<'EOF'
enum Shape {
	Line(int, int)
	Dot
}

fn main() {
	s := Line(7)
	print("built")
}
EOF

reject a-pattern-that-binds-too-few '`Line` carries 2 arguments and this pattern binds 1' <<'EOF'
enum Shape {
	Line(int, int)
	Dot
}

fn area(s: Shape) -> int {
	return match s {
		Line(n) => n
		_       => 0
	}
}

fn main() {
	print(f"{area(Dot)}")
}
EOF

reject a-construction-that-gives-too-much '`Line` carries 1 argument and this gives 2' <<'EOF'
enum Shape {
	Line(int)
	Dot
}

fn main() {
	s := Line(7, 8)
	print("built")
}
EOF

# --- a borrow needs a place ------------------------------------------------------
#
# A `mut &` argument is the caller's own storage handed over to be written. `m["k"]` reads
# like one and is not: it lowers to a statement expression, so `&` on it reached cc.

reject a-borrow-of-a-map-index 'borrows a map index, which is a value rather than a place' <<'EOF'
fn poke(mut &n: int) {
	n = 5
}

fn main() {
	mut m: map[str, int] = {"k": 1}
	poke(m["k"])
	print(f"{m["k"]}")
}
EOF

# --- a write needs storage all the way down -------------------------------------
#
# `chk_root_name` asks what the target is rooted at; these are the STEPS between. Each of
# them stores into a value C has no address for, and cc said "expression is not assignable"
# about a statement expression this compiler wrote.

reject store-through-a-map-index 'cannot store through a map index' <<'EOF'
fn main() {
	mut d: map[str, list[int]] = {"k": [1, 2]}
	d["k"][0] = 7
	print("x")
}
EOF

reject store-through-a-call-result 'cannot store through a call result' seed-gap <<'EOF'
fn get() -> list[int] {
	return [1, 2]
}

fn main() {
	get()[0] = 99
	print("x")
}
EOF

# --- a match answers one type ----------------------------------------------------
#
# The whole match took its type from the FIRST arm, the only one anything looked at, so a
# later arm answering something else went to cc — as a WARNING under clang, which links.

reject match-arms-disagree 'its arms give int and str' <<'EOF'
fn pick(n: int) -> int {
	return match n {
		0 => 1
		_ => "other"
	}
}

fn main() {
	print(pick(0))
}
EOF

# --- `this` is a method's receiver, and nothing else -----------------------------
#
# GRAMMAR:117 makes `this` a reserved word, :330 says it is NOT a parameter, :332 that a
# `fn` whose body uses it with no instance bound is a compile error, and :333 that the self
# type is `This`. The seed enforces all of it. `zerg` enforced none of it: every naming
# position read `cur(p).lexeme` — whatever token was there — so `this` passed as a
# parameter, a field, a function, a type, a variant, a pattern binding. In a METHOD it
# reached cc, because the parser has already put a `this` at parameter 0.

reject this-as-a-parameter 'is a reserved word and cannot name a parameter' no-place <<'EOF'
fn f(this: int) -> int {
	return this
}

fn main() {
	print(f(7))
}
EOF

reject this-as-a-method-parameter 'is a reserved word and cannot name a parameter' no-place <<'EOF'
struct P {
	x: int
}

impl P {
	fn get(this: int) -> int {
		return 1
	}
}

fn main() {
	print(1)
}
EOF

reject this-as-a-field 'is a reserved word and cannot name a struct field' no-place <<'EOF'
struct P {
	this: int
}

fn main() {
	p := P(1)
	print(p.this)
}
EOF

reject this-as-a-function 'is a reserved word and cannot name a function' no-place <<'EOF'
fn this() -> int {
	return 1
}

fn main() {
	print(this())
}
EOF

reject this-as-a-type 'is a reserved word and cannot name a struct' no-place <<'EOF'
struct this {
	x: int
}

fn main() {
	print(1)
}
EOF

reject this-as-a-variant 'is a reserved word and cannot name an enum variant' no-place <<'EOF'
enum E {
	this
	B
}

fn main() {
	print(1)
}
EOF

reject this-as-a-pattern-binding 'is a reserved word and cannot name a pattern binding' no-place <<'EOF'
enum E {
	A(int)
	B
}

fn main() {
	e := A(7)
	print(match e {
		A(this) => this
		_ => 0
	})
}
EOF

reject this-outside-a-method "a method's receiver, and this function has none" <<'EOF'
fn f() -> int {
	return this
}

fn main() {
	print(1)
}
EOF

reject self-type-outside-an-impl "is the self type" <<'EOF'
fn f() -> This {
	return 1
}

fn main() {
	print(1)
}
EOF

# --- a borrow needs both halves, and may not alias -------------------------------
#
# GRAMMAR:308-313 — "the CALLER decides whether its variable is `mut`, the CALLEE decides
# via `mut &` whether it writes back, so a caller-visible mutation needs BOTH", and a
# borrow "cannot ALIAS (the same variable to two `mut &` in one call), which keeps it safe
# with no borrow checker". Only the callee's half was ever read.

reject mut-ref-of-an-immutable 'writes back to `k`, which is not `mut`' <<'EOF'
fn bump(mut &n: int) {
	n = n + 1
}

fn main() {
	k := 1
	bump(k)
	print(f"{k}")
}
EOF

reject mut-ref-aliased 'is given to two `mut &` parameters' <<'EOF'
fn two(mut &a: int, mut &b: int) {
	a = a + 1
	b = b + 10
}

fn main() {
	mut k := 0
	two(k, k)
	print(f"{k}")
}
EOF

reject mut-fn-on-an-immutable-receiver 'which is a `mut fn`, writes back to `p`' <<'EOF'
struct P {
	x: int
}

impl P {
	mut fn bump() {
		this.x = this.x + 1
	}
}

fn main() {
	p := P(1)
	p.bump()
	print(f"{p.x}")
}
EOF

# GRAMMAR:301-304 — `mut fn` is meaningful only on a method; "a free function or closure
# has no receiver, so it is never `mut fn`". The token fell into the top-level skip, so the
# `mut` was swallowed and the function compiled as if it had never been written.
reject mut-fn-free-function 'a free function is never `mut fn`' no-place seed-gap <<'EOF'
mut fn f() -> int {
	return 1
}

fn main() {
	print(f())
}
EOF

# --- an enum is a strong type, and a qualification must be true ------------------
#
# `Red == Apple` — one variant of each of two unrelated enums — answered `true`, because an
# enum is not a scalar and the equality branch returned early for it, leaving the tags to be
# compared as whatever C made of them. Both are tag 0.

reject compare-two-enums 'cannot compare Color and Fruit' <<'EOF'
enum Color {
	Red
	Green
}

enum Fruit {
	Apple
}

fn main() {
	print(f"{Red == Apple}")
}
EOF

reject compare-an-enum-and-an-int 'cannot compare Color and int' <<'EOF'
enum Color {
	Red
	Green
}

fn main() {
	c := Red
	print(f"{c == 0}")
}
EOF

reject qualify-with-the-wrong-enum 'is a variant of `Fruit`, not of `Color`' no-place <<'EOF'
enum Color {
	Red
}

enum Fruit {
	Apple
}

fn main() {
	c := Color.Apple
	print(f"{int(c)}")
}
EOF

reject qualify-a-name-that-is-not-a-variant 'is not a variant of `Color`' no-place <<'EOF'
enum Color {
	Red
}

fn main() {
	c := Color.Purple
	print(f"{int(c)}")
}
EOF

# --- a place is a place all the way down, and a conversion takes a value ---------
#
# Four shapes reached cc as "cannot take the address of an rvalue": `c_is_place` asked
# about the LAST step of a path, and the map-index lowering never bound a non-place at all.
# `list[int]()` was worse — the parser indexed an empty argument list, so the COMPILER
# aborted with its own IndexError, no place and no form named.

reject convert-nothing-to-a-list 'converts a VALUE and was given none' no-place <<'EOF'
fn main() {
	xs := list[int]()
	print(f"{xs.len()}")
}
EOF

reject convert-nothing-to-an-int 'converts a VALUE and was given none' no-place <<'EOF'
fn main() {
	print(f"{int()}")
}
EOF

reject convert-two-values 'converts one value, and this gives 2' no-place <<'EOF'
fn main() {
	print(f"{int(1, 2)}")
}
EOF

# --- and it takes a value it can READ --------------------------------------------
#
# `int(s)`, `uint(s)` and `float(s)` parse a number out of a str; no other target does
# (docs/runtime/builtins.md, "Parsing a string"). `bool(s)` and `byte(s)` fell through to
# a C cast OF THE POINTER, so `bool(s)` was every non-empty string's `true` and `byte(s)`
# was an address's low octet — both silent, both wrong.
#
# A composite is not a scalar to re-construct at all, and that one reached cc.

reject parse-a-str-as-a-bool 'does not parse a `str`' no-place <<'EOF'
fn main() {
	print(f"{bool("1")}")
}
EOF

reject parse-a-str-as-a-byte 'does not parse a `str`' no-place <<'EOF'
fn main() {
	print(f"{byte("65")}")
}
EOF

reject convert-a-list-to-an-int 'converts a scalar, and list[int] is not one' no-place <<'EOF'
fn main() {
	xs := [1, 2]
	print(f"{int(xs)}")
}
EOF

# --- and a str is not a container to subscript -----------------------------------
#
# docs/core/types.md: a str "iterates as `rune` and is NOT indexable" — it is UTF-8, so a
# subscript would name a byte and not a character. The emitter named every other
# non-container and let a str through to the LIST path: `s[0]` read a `const char*` as a
# list header and printed a different number every run, and `s[1..3]` reached cc.

reject index-a-str 'a `str` is not indexable' <<'EOF'
fn main() {
	s := "hello"
	print(f"{s[0]}")
}
EOF

reject slice-a-str 'a `str` is not indexable' <<'EOF'
fn main() {
	s := "hello"
	print(s[1..3])
}
EOF

# --- and a literal is a value the type can hold -----------------------------------
#
# docs/core/types.md: "A literal that does not fit its required type is a COMPILE ERROR …
# never a runtime overflow", and the chapter's own deviation note says an int literal past
# i64 "is still rejected". It was not: the lexer accumulated the digits in a wrapping i64,
# so three literals gave three wrong numbers with no diagnostic anywhere.

reject int-literal-past-i64 'does not fit an `int`' <<'EOF'
fn main() {
	print(f"{9223372036854775808}")
}
EOF

reject int-literal-far-past-i64 'does not fit an `int`' <<'EOF'
fn main() {
	print(f"{99999999999999999999999}")
}
EOF

reject hex-literal-past-i64 'does not fit an `int`' <<'EOF'
fn main() {
	print(f"{0xFFFFFFFFFFFFFFFFF}")
}
EOF

# --- a declared interface means its members exist -------------------------------
#
# A `spec` is otherwise read and DROPPED — it is not a type and nothing dispatches on it —
# so `impl Show for P { }` with no `show` compiled and ran, and the declared interface meant
# nothing at all. This is a LANGUAGE rule and no future feature makes it legal, which is why
# it lives here and not with the not-yet-built forms next door.

reject impl-misses-a-required-member 'does not implement `show`' seed-gap <<'EOF'
struct P {
	n: int
}

spec Show {
	fn show() -> int
}

impl Show for P {
}

fn main() {
	print("x")
}
EOF

reject impl-misses-one-of-two 'does not implement `tag`' seed-gap <<'EOF'
struct P {
	n: int
}

spec Show {
	fn show() -> int
	fn tag() -> int
}

impl Show for P {
	fn show() -> int {
		return this.n
	}
}

fn main() {
	print("x")
}
EOF

# --- and a member that is there but is not the one the spec asked for -------------
#
# A spec guarantees a SIGNATURE. The check compared NAMES — a spec was a token scan into a
# comma-joined list of them — so every one of these compiled and ran:
#
#   fn tag() -> str          satisfying   fn tag(n: int) -> int
#   spec Ord: Eq             requiring    nothing at all of Eq
#   impl Nope for A          naming       a spec that does not exist
#
# Position matters because Zerg has positional calls: if the order were free, every call
# through a spec would have to be written with named arguments.

reject impl-returns-the-wrong-type 'it returns str, and the spec declares int' seed-gap <<'EOF'
spec Tag {
	fn tag() -> int
}

struct A {
	v: int
}

impl Tag for A {
	fn tag() -> str {
		return "x"
	}
}

fn main() {
	print(A(1).tag())
}
EOF

reject impl-takes-the-wrong-count 'it takes 0 arguments, and the spec declares 1' seed-gap <<'EOF'
spec Tag {
	fn tag(n: int) -> int
}

struct A {
	v: int
}

impl Tag for A {
	fn tag() -> int {
		return 1
	}
}

fn main() {
	print(A(1).tag())
}
EOF

reject impl-renames-a-parameter 'a named argument selects by that name' seed-gap <<'EOF'
spec Tag {
	fn tag(n: int) -> int
}

struct A {
	v: int
}

impl Tag for A {
	fn tag(m: int) -> int {
		return m
	}
}

fn main() {
	print(A(1).tag(2))
}
EOF

reject impl-drops-the-by-ref 'is not `mut &` and the spec' seed-gap <<'EOF'
spec Tag {
	fn tag(mut &n: int) -> int
}

struct A {
	v: int
}

impl Tag for A {
	fn tag(n: int) -> int {
		return n
	}
}

fn main() {
	mut k := 1
	print(A(1).tag(k))
}
EOF

reject impl-adds-a-default 'has a default and the spec declares none' seed-gap <<'EOF'
spec Tag {
	fn tag(n: int) -> int
}

struct A {
	v: int
}

impl Tag for A {
	fn tag(n: int = 5) -> int {
		return n
	}
}

fn main() {
	print(A(1).tag(2))
}
EOF

reject impl-drops-the-mut-fn 'it is not a `mut fn` and the spec' seed-gap <<'EOF'
spec Bump {
	mut fn bump() -> int
}

struct A {
	v: int
}

impl Bump for A {
	fn bump() -> int {
		return 2
	}
}

fn main() {
	mut a := A(1)
	print(f"{a.bump()}")
}
EOF

reject impl-adds-a-mut-fn 'it is a `mut fn` and the spec' <<'EOF'
spec Bump {
	fn bump() -> int
}

struct A {
	v: int
}

impl Bump for A {
	mut fn bump() -> int {
		return 2
	}
}

fn main() {
	mut a := A(1)
	print(f"{a.bump()}")
}
EOF

reject impl-breaks-the-self-type 'parameter `other` is int, and the spec declares A' seed-gap <<'EOF'
spec Eq {
	fn eq(other: This) -> bool
}

struct A {
	v: int
}

impl Eq for A {
	fn eq(other: int) -> bool {
		return this.v == other
	}
}

fn main() {
	print(A(1).eq(1))
}
EOF

reject impl-breaks-a-spec-parameter 'parameter `k` is str, and the spec declares int' <<'EOF'
spec Ix[K] {
	fn at(k: K) -> int
}

struct A {
	v: int
}

impl Ix[int] for A {
	fn at(k: str) -> int {
		return this.v
	}
}

fn main() {
	print(A(1).at("x"))
}
EOF

reject super-spec-is-not-satisfied 'does not implement `eq`, which `Eq` requires' <<'EOF'
spec Eq {
	fn eq() -> bool
}

spec Ord: Eq {
	fn lt() -> bool
}

struct A {
	v: int
}

impl Ord for A {
	fn lt() -> bool {
		return false
	}
}

fn main() {
	print(A(1).lt())
}
EOF

reject impl-of-a-spec-that-does-not-exist 'no spec named `Nope`' <<'EOF'
struct A {
	v: int
}

impl Nope for A {
	fn f() -> int {
		return 1
	}
}

fn main() {
	print(A(1).f())
}
EOF

reject a-type-declares-a-method-twice 'declares `tag` twice' no-place <<'EOF'
spec Tag {
	fn tag() -> int
}

struct A {
	v: int
}

impl Tag for A {
	fn tag() -> int {
		return 1
	}
}

impl Tag for A {
	fn tag() -> int {
		return 2
	}
}

fn main() {
	print(A(1).tag())
}
EOF

reject impl-does-not-bind-a-spec-parameter 'is parameterized by K' <<'EOF'
spec Ix[K] {
	fn at(k: K) -> int
}

struct A {
	v: int
}

impl Ix for A {
	fn at(k: int) -> int {
		return k
	}
}

fn main() {
	print(A(1).at(2))
}
EOF

# --- a type name is one name -----------------------------------------------------
#
# A struct, an enum and a spec share one namespace — every module flattens into one scope
# here, which is the argument that already refuses a duplicate function and a duplicate
# module constant. Nothing checked it, and the shapes failed four different ways: two
# structs MERGED into one of both their fields, two of the same reached cc as a "typedef
# redefinition" against .zerg-cache, and two specs were simply accepted.

reject a-struct-declared-twice 'is declared twice' no-place <<'EOF'
struct A {
	v: int
}

struct A {
	w: str
}

fn main() {
	print(f"{A(7).v}")
}
EOF

reject an-enum-declared-twice 'is declared twice' no-place seed-gap <<'EOF'
enum E {
	X
}

enum E {
	Y
}

fn main() {
	print(f"{int(E.X)}")
}
EOF

reject a-spec-declared-twice 'is declared twice' seed-gap <<'EOF'
spec Tag {
	fn tag() -> int
}

spec Tag {
	fn other() -> int
}

struct A {
	v: int
}

impl Tag for A {
	fn tag() -> int {
		return 1
	}
}

fn main() {
	print(f"{A(1).tag()}")
}
EOF

reject a-struct-and-a-spec-share-a-name 'once as a struct, once as a spec' seed-gap <<'EOF'
struct A {
	v: int
}

spec A {
	fn f() -> int
}

fn main() {
	print(f"{A(1).v}")
}
EOF

# and one level down, where the same question has the same answer and three more ways of
# going wrong: a repeated FIELD and a repeated PARAMETER both reached cc as a C redefinition
# against .zerg-cache, and a repeated VARIANT was accepted — the second took the next
# discriminant, so it was unreachable, and a `match` naming the first was "exhaustive" over
# an enum that had two.

reject a-field-declared-twice 'declares a field named `v` twice' no-place seed-gap <<'EOF'
struct A {
	v: int
	v: str
}

fn main() {
	print("x")
}
EOF

reject a-variant-declared-twice 'declares a variant named `X` twice' no-place seed-gap <<'EOF'
enum E {
	X
	X
}

fn main() {
	print(f"{int(E.X)}")
}
EOF

reject a-parameter-declared-twice 'declares a parameter named `a` twice' <<'EOF'
fn f(a: int, a: int) -> int {
	return a
}

fn main() {
	print(f"{f(1, 2)}")
}
EOF

# --- a tuple type has two elements ------------------------------------------------
#
# GRAMMAR:480 says two or more. `(T)` is a grouped type everywhere else in the language and
# `()` is not a type at all — a function with no result returns nothing, which `-> ` says by
# being absent. The whole tuple TYPE was unparsed until this branch, so every position that
# wanted one reported whatever token came next instead: five positions, five messages.

reject a-one-element-tuple-type 'a tuple type has two elements or more' no-place <<'EOF'
fn f() -> (int) {
	return 1
}

fn main() {
	print(f())
}
EOF

# --- a reserved word cannot name a binding either -------------------------------
#
# The last naming position that read whatever token was there. A binding is recognised by
# the SHAPE `name := …`, so a keyword in the name slot matched no arm, fell through to the
# expression fallback, and was reported as `` `:=` is not an expression`` — the token after
# the name rather than the reserved word in it. `print := 1` never even got that far: the
# `print` statement arm answered first and read `:= 1` as the thing to print.

reject reserved-word-as-a-binding 'is a reserved word and cannot name a binding' no-place <<'EOF'
fn main() {
	this := 1
	print(f"{this}")
}
EOF

reject reserved-word-as-a-loop-binding 'cannot name a loop binding' no-place <<'EOF'
fn main() {
	xs: list[int] = [1, 2]
	for this in xs {
		print(f"{this}")
	}
}
EOF

reject reserved-word-as-an-if-let-binding 'cannot name an `if let` binding' no-place <<'EOF'
fn main() {
	o: int? = 5
	if this := o {
		print(f"{this}")
	}
	print("x")
}
EOF

reject statement-keyword-as-a-binding 'is a reserved word and cannot name a binding' no-place <<'EOF'
fn main() {
	print := 1
	print(f"{print}")
}
EOF

# --- a byte widens; a list of them does not -------------------------------------
#
# A `byte` fits an `int` slot, in the direction the language already mixes `int` into
# `float`. A `list[byte]` does NOT fit a `list[int]`: a buffer's stride is its element's
# size, so reading one as the other walks 8 bytes per 1-byte slot. `ys: list[int] =
# list[byte]("Hi")` printed 26952 and a longer string read past the end.

reject bind-a-byte-list-to-an-int-list 'cannot bind list[byte] to a list[int] binding' <<'EOF'
fn main() {
	ys: list[int] = list[byte]("Hi")
	print(f"{ys[0]}")
}
EOF

# and the narrowing the other way is not a fit either: `take(1000)` on a `byte` parameter
# compiled to a truncation, and cc printed its own warning about generated C to say so
reject narrow-an-int-to-a-byte '`take` takes byte as argument 1, and this gives int' seed-gap <<'EOF'
fn take(b: byte) -> int {
	return int(b)
}

fn main() {
	print(f"{take(1000)}")
}
EOF

# --- report ------------------------------------------------------------------------

if [ $fail -ne 0 ]; then
	echo "reject-check: $fail case(s) the compiler did not reject by itself"
	exit 1
fi
echo "reject-check: $pass ill-formed programs rejected by the compiler, none left to cc"
