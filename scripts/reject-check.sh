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
# has_flag <flags> <marker> — one BOOLEAN marker is present. The flags arrive as a
# space-padded list, so a marker is matched as a whole word: `no-place` must not answer
# true for a hypothetical `no-place-yet`. It exists because the three boolean markers were
# each asked for in a different way — a `case`, and two spellings of `${flags#* … }` — and
# the fourth would have picked a fourth.
has_flag() {
	case $1 in *" $2 "*) return 0 ;; esac
	return 1
}

reject() {
	local name=$1 want=$2
	shift 2

	# the markers COMPOSE: a case can be both a seed gap and a place the parser owes, and
	# reading one `$3` silently dropped the second — the gate then failed on an exception
	# that was declared right beside the one it honoured.
	local flags=" $* "
	local src="$tmp/$name.zg"
	cat >"$src"

	# at=LINE:COL narrows the place assertion below to a SPECIFIC line — for a case whose
	# whole claim is that the finding sits at the declaration, not at a use site.
	local at="" fl
	for fl in $flags; do
		case $fl in at=*) at=${fl#at=} ;; esac
	done

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
	if is_crash "$status"; then
		echo "CRASHED   $name — the compiler died of signal $((status - 128)) instead of refusing"
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
	if has_flag "$flags" no-place; then
		if has_place "$out"; then
			echo "PLACE GAINED  $name — it says where now; drop the no-place marker"
			fail=$((fail + 1))
			return
		fi
	elif ! has_place "$out"; then
		echo "NO PLACE  $name — the message does not say where: $(echo "$out" | head -1)"
		fail=$((fail + 1))
		return
	fi
	if [ -n "$at" ] && ! place_is "$out" "$name\.zg:$at"; then
		echo "PLACE     $name — the finding does not sit at $at: $(echo "$out" | head -1)"
		fail=$((fail + 1))
		return
	fi

	# one-finding pins the COUNT, for a case whose claim is "exactly one message": a rule
	# that keeps a refused name resolvable ON PURPOSE (so its uses do not each add a
	# second finding) is asserted here, since a second message would match every other
	# assertion and still be the regression.
	if has_flag "$flags" one-finding; then
		local found
		found=$(printf '%s\n' "$out" | grep -c '^error:')
		if [ "$found" -ne 1 ]; then
			echo "COUNT     $name — wanted exactly one finding, got $found"
			fail=$((fail + 1))
			return
		fi
	fi

	# A third argument names a rule the SEED does not enforce yet, and says which. The
	# oracle is worth having and the seed is not perfect; recording the exception beside the
	# case is better than dropping the case or weakening the assertion for everything.
	#
	# It asserts the OPPOSITE, so the exception retires itself: an xfail that merely returns
	# `pass` can never report an unexpected pass, and the day the seed learns the rule the
	# marker and its entry in the seed's README would rot with nothing to say so.
	if has_flag "$flags" seed-gap; then
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

# A plain top-level binding is a module binding too, and immutable for a DIFFERENT reason:
# not because `const` says so, but because the top level is (docs/runtime/package.md). Both
# used to answer "it is a module `const`", which sends the reader to look up a keyword they
# did not write; the two sentences are asserted apart because one message covering two
# rules is how a message comes to be wrong about one of them.
reject assign-to-module-binding "it is a module binding" <<'EOF'
N := 5

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

# --- `const` is shadow-proof, in BOTH directions ----------------------------------
#
# GRAMMAR group 4: a `const` binding is immutable and SHADOW-PROOF — no later binding may
# take its name, and it may not itself take a name a visible binding holds. Neither
# direction used to be checked: `k := 2` in a block under `const k := 1` compiled and
# printed 2, silently, which is the exact answer the keyword exists to rule out. The
# negative half — a plain `:=` may still be shadowed and `mut n := n` still works — is
# pinned by the corpus (const_shadow_allowed), because a program that must COMPILE belongs
# there, not here.

reject shadow-a-const-from-an-inner-block 're-binds a `const`' <<'EOF'
const k := 1

fn main() {
	if true {
		k := 2
		print k
	}
}
EOF

# a LOOP VARIABLE is a binding too. The rule rides c_add_var — the one gate every
# name-introducing site enters the environment through — because D103 desugars this loop
# to a core form that CONTAINS a binding: refusing the desugared program and accepting the
# surface one would break `make desugar`'s contract that the two behave the same, and the
# seed refuses the surface form as well.
reject loop-variable-takes-a-const-name 're-binds a `const`' <<'EOF'
const k := 1

fn main() {
	for k in 0..3 {
		print k
	}
}
EOF

reject const-shadowing-an-outer-binding 'shadows a binding already visible here' <<'EOF'
fn main() {
	x := 1
	if true {
		const x := 2
		print x
	}
	print x
}
EOF

# the SAME-block collision is one mistake and gets one message — the const one, whose
# advice is right. The redeclaration message says "an inner block may shadow it", which is
# exactly what a `const` does not allow.
reject const-collision-in-the-same-block 're-binds a `const`' <<'EOF'
fn main() {
	const k := 1
	k := 2
	print k
}
EOF

# a match ARM's bind is registered up to three times — condition, body, and the body's
# type inference — so this one mistake used to collect SIX findings, one per pass, all
# identical. One mistake earns one message (chk_at_place drops an exact repeat), which is
# what `one-finding` pins; the place is the whole STATEMENT's, the SAt marker's
# documented grain, so the arm itself is not pointed at and this case does not claim it.
reject const-rebound-by-a-match-arm 're-binds a `const`' one-finding <<'EOF'
const v := 1

enum E {
	A(int)
	B
}

fn main() {
	e := E.A(7)
	x := match e { A(v) => v  B => 0 }
	print x
}
EOF

# --- a module constant may not take a function's name --------------------------------
#
# The top level is ONE namespace: a module constant and a free function both mangle to
# `zg_<name>`, so `const f := 1` beside `fn f()` reached cc as "redefinition of 'zg_f'"
# against generated code — in either source order, which is why both are here. A LOCAL
# named after a function is not this (it shadows, and stays legal); the corpus pins that
# half. The seed emits the collision too (its gap, src/bootstrap/README.md).
reject const-taking-a-function-name 'declared as both a module constant and a function' at=1:1 seed-gap <<'EOF'
const f := 1

fn f() {
	print "x"
}

fn main() {
	print "ok"
}
EOF

reject function-taking-a-const-name 'declared as both a module constant and a function' at=5:1 seed-gap <<'EOF'
fn f() {
	print "x"
}

const f := 1

fn main() {
	print "ok"
}
EOF

# --- the top level is immutable in safe code --------------------------------------
#
# docs/runtime/package.md: a top-level binding may not be `mut` outside a module-level
# `unsafe { … }` group. The declaration used to be dropped by the parser's top-level skip,
# which split the mistake into two wrong answers: unused, the program compiled; used,
# every use site said `undefined name` about a name the program plainly declares. Inside
# the group the same spelling IS the language's mutable global — the corpus case
# unsafe_group_mut pins that half, so the refusal here cannot leak into the group without
# a gate noticing from both sides.

reject top-level-mut-in-safe-code 'may not be `mut` outside a module-level' <<'EOF'
mut counter := 0

fn main() {
	print "ok"
}
EOF

reject top-level-pub-mut 'may not be `mut` outside a module-level' <<'EOF'
pub mut counter := 0

fn main() {
	print "ok"
}
EOF

# A USED `mut` global earns exactly ONE finding, at the declaration — the name stays
# resolvable and writable on purpose, so the program is told the one line to fix rather
# than a second `undefined name` or `cannot assign` at every use site. `at=` and
# `one-finding` are those two claims as markers; the case used to fork the helper's first
# half by hand, and the fork silently lost the cache, cc-shape and seed assertions.
reject top-level-mut-that-is-used 'may not be `mut` outside a module-level' at=1:1 one-finding <<'EOF'
mut counter := 0

fn main() {
	counter = counter + 1
	print counter
}
EOF

# A STATEMENT inside the group is ill-formed, not a nop: GRAMMAR:777 derives
# `unsafe-item ::= decorated-decl | binding` and nothing else, and the top level's
# statement fallback must not bless code into an unsafe CONTEXT nobody runs. Before the
# group's contents were parsed at all, this was dropped one token at a time — the same
# shredder that ate the group's `mut` bindings — so the case pins the refusal that
# replaced the silence.
reject statement-in-unsafe-group 'a statement runs in a function body, not in a group' <<'EOF'
unsafe {
	print "hello"
}

fn main() {
	print "ok"
}
EOF

# THE GROUP HAS TO END. The context was tracked as loop state that nothing checked at
# `Eof`, so a missing `}` left every declaration below it inside the group — and this
# program built, ran and printed 1: `counter` became the language's mutable global and
# chk_top_muts' refusal, the whole reason the flag exists, was switchable by a typo with
# no diagnostic anywhere. The place is the `unsafe` that was never closed, which is where
# the reader has to go, so `at=` pins it rather than accepting an answer at the last line.
reject unsafe-group-never-closed 'group is never closed' at=1:1 <<'EOF'
unsafe {
	fn raw() -> int { return 1 }

mut counter := 0

fn main() {
	counter = counter + raw()
	print counter
}
EOF

# `unsafe-item ::= decorated-decl | binding` derives no GROUP, so a group inside a group is
# not a form. It used to nest, which is the other half of the same counter: the inner `}`
# closed the outer one as far as it could tell, leaving the tail of the file outside a
# context the reader thinks it is in.
reject unsafe-group-nested 'does not nest' at=2:2 <<'EOF'
unsafe {
	unsafe {
		mut counter := 0
	}
}

fn main() {
	print "ok"
}
EOF

# --- a top-level annotation is honoured --------------------------------------------
#
# `answer: bool = 42` used to compile: the top level inferred from the value and silently
# discarded the annotation, so the program got an int named answer — an annotation the
# compiler does not check is a comment. The positive half (the annotation DECIDING the
# type, `half: float = 1` printing 0.5 for `half / 2`) is the corpus case topconst_typed.
reject top-level-annotation-mismatch 'cannot bind int to a bool binding' at=1:1 seed-gap <<'EOF'
answer: bool = 42

fn main() {
	print answer
}
EOF

# A finding INSIDE a module constant's initializer sits at the declaration, with a file. A
# module constant belongs to no function, so no SAt marker ever covers its value: the
# emitter used to clear the statement marker's path around the initializer walk (for the
# visibility rule's sake) and a rule firing in there reported a bare `line:col` — the line
# a STALE marker still held, near some use inside `main`. `at=1:1` is the whole claim:
# the constant's own place, not a marker left over from whatever was emitted before it.
reject const-initializer-reports-the-declaration 'operator `+` takes numeric operands' at=1:1 <<'EOF'
const answer := 1 + "s"

fn main() {
	print 1
}
EOF

# --- `pub` binds to a declaration ---------------------------------------------------
#
# `pub` is a visibility flag on the declaration that follows it. The parser used to read
# it in a second, hand-copied dispatch that fell off its end silently — `pub impl` dropped
# the `pub` on the floor and picked the `impl` up as if the marker were never written.
# There is now ONE dispatch, and a form that takes no `pub` (GRAMMAR derives none for an
# `import`, an `impl`, an `init()`, a decorator, an `unsafe { … }` group, or a statement)
# says so by name — and every one of them says WHERE, at the declaration's own first
# token. `at=` pins that: the place must be the marker the reader wrote, not wherever the
# parser happened to stop.
#
# ONE ARM PER FORM, because the dispatch is a chain and a form that takes no `pub` is
# exactly a form whose arm has to say so; a case for one of them proves nothing about the
# next. These are the six.

reject pub-on-an-impl-block 'does not go on an `impl` block' at=9:1 <<'EOF'
spec S {
	fn f() -> int
}

struct A {
	x: int
}

pub impl S for A {
	fn f() -> int {
		return 1
	}
}

fn main() {
	print "ok"
}
EOF

reject pub-before-a-statement '`pub` binds to a declaration' at=1:1 <<'EOF'
pub 42

fn main() {
	print "ok"
}
EOF

reject pub-on-an-import '`pub import` is not a form' at=1:1 <<'EOF'
pub import "std/io"

fn main() {
	print "ok"
}
EOF

reject pub-on-init 'does not go on `init()`' at=1:1 <<'EOF'
pub init() {
	print "ok"
}

fn main() {
	print "ok"
}
EOF

reject pub-before-a-decorator 'a decorator leads its declaration' at=1:1 <<'EOF'
pub #[derive(Eq)]
struct A {
	x: int
}

fn main() {
	print "ok"
}
EOF

reject pub-on-an-unsafe-group 'does not go on an `unsafe { … }` group' at=1:1 <<'EOF'
pub unsafe {
	mut counter := 0
}

fn main() {
	print "ok"
}
EOF

# --- generics -----------------------------------------------------------------------
#
# Both of these were `refuse` cases until the forms they name were built. What is left when
# a form arrives is not nothing: it is the PROGRAM's own mistake, which is permanent and
# belongs here.

reject explicit-type-args-on-a-plain-fn "is not generic" <<'EOF'
fn id(x: int) -> int {
	return x
}

fn main() {
	print id[int](7)
}
EOF

# --- redeclaration ----------------------------------------------------------------
#
# Two cases stood here pinning "a name is bound once per block", and their retirement is
# the rare one this file's header says must be justified: the rejection was never the
# LANGUAGE's. docs/core/memory.md has always specified re-declaration as legal in the
# same block — declare-del-declare, the RHS reading the old binding — and worked through
# an example this compiler refused; the refusal was a compiler rule miscarrying a marker,
# carried in the spec as a [deviation], and a [deviation] is a bug with a fix owed, not a
# documented state. The legal half now lives where programs that must run live: the
# corpus case redeclare_same_block. What survives HERE is the boundary that really is the
# language's — a `const` is shadow-proof, so a re-declaration may not cross one in either
# direction, same block included (the const cases above and below).

reject redeclare-plain-then-const-in-one-block 'shadows a binding already visible here' <<'EOF'
fn main() {
	x := 1
	const x := 2
	print x
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

# `//` is the floor-division operator, so its operands are numbers like the rest of the
# arithmetic family — a str is not one. It matters more than the others that this is said:
# `//` opens a comment in most languages a reader arrives from, and silently accepting
# `a // b` on two strings would be the worst possible way to learn that Zerg's is `#`.
reject floor-div-on-str 'operator `//` takes numeric operands' <<'EOF'
fn main() {
	s := "s"
	print s // s
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

reject add-an-int-to-a-uint 'has int on one side and uint on the other' <<'EOF'
fn main() {
	i: int = 3
	u: uint = 5
	print(f"{i + u}")
}
EOF

reject compare-an-int-with-a-uint 'has int on one side and uint on the other' <<'EOF'
fn main() {
	i: int = -1
	u: uint = 1
	print(f"{i < u}")
}
EOF

reject equate-an-int-with-a-uint 'has int on one side and uint on the other' <<'EOF'
fn main() {
	i: int = -1
	u: uint = 1
	print(f"{i == u}")
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
#
# `x: float = 1` and `y: float = i` are the pair worth reading together: the LITERAL adopts
# its context like every untyped literal, and the already-typed int VALUE does not. That is
# the line docs/core/types.md draws, and it is drawn inside one type pair rather than
# between two, which is why the accepting half lives in the examples and the four rejecting
# ones are below — a binding, an assignment, a return and an argument, because a rule that
# holds in one slot and not the others is the shape this whole rule set exists to catch.

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

reject typedef-value-into-its-underlying '`f` takes int as argument 1, and this gives Celsius' <<'EOF'
type Celsius = int

fn f(n: int) -> int {
	return n
}

fn main() {
	print(f"{f(Celsius(20))}")
}
EOF

reject logical-operator-on-a-typedef 'has no meaning on Flag and Flag' <<'EOF'
type Flag = bool

fn main() {
	f := Flag(true)
	g := Flag(false)
	print(f"{f and g}")
}
EOF

reject bitwise-operator-on-a-typedef 'has no meaning on Mask and int' <<'EOF'
type Mask = int

fn main() {
	m := Mask(3)
	print(f"{m & 1}")
}
EOF

reject prefix-operator-on-a-typedef 'has no meaning on Flag' <<'EOF'
type Flag = bool

fn main() {
	f := Flag(true)
	print(f"{not f}")
}
EOF

reject arithmetic-on-a-typedef 'a `type … = …` keeps its own identity' <<'EOF'
type Celsius = int

fn main() {
	c := Celsius(20)
	print(f"{c + 1}")
}
EOF

reject typedef-declared-twice 'is declared twice' seed-gap <<'EOF'
type Celsius = int
type Celsius = float

fn main() {
	print(f"{int(Celsius(1))}")
}
EOF

reject typedef-over-an-undeclared-type 'names no type' <<'EOF'
type Celsius = Nope

fn main() {
	print(f"{int(Celsius(1))}")
}
EOF

reject typedef-conversion-takes-one-value 'converts ONE value' <<'EOF'
type Celsius = int

fn main() {
	print(f"{int(Celsius(1, 2))}")
}
EOF

reject str-sent-on-an-int-channel 'the value sent on this channel is int' <<'EOF'
fn main() {
	ch := chan[int](1)
	ch <- "hi"
	print(f"{(<-ch)!}")
}
EOF

reject str-appended-to-an-int-list 'the element `append` adds is int' <<'EOF'
fn main() {
	mut xs: list[int] = []
	xs.append("hi")
	print(f"{xs.len()}")
}
EOF

reject str-written-into-an-int-map 'the value written into this map is int' <<'EOF'
fn main() {
	mut m: map[str, int] = {:}
	m["a"] = "hi"
	print(f"{m.len()}")
}
EOF

reject str-among-a-map-literals-ints 'a value of this map literal is int' <<'EOF'
fn main() {
	m := {"a": 1, "b": "hi"}
	print(f"{m.len()}")
}
EOF

reject str-as-an-int-coalesce-fallback 'the `??` fallback is int' <<'EOF'
fn main() {
	x: int? = 1
	print(f"{x ?? "no"}")
}
EOF

reject str-into-an-int-variant-payload 'payload 1 of `Line` is int' <<'EOF'
enum Shape {
	Line(int)
}

fn take(s: Shape) -> int {
	return 0
}

fn main() {
	print(f"{take(Shape.Line("hi"))}")
}
EOF

reject str-into-an-int-struct-field 'field 1 of `P` is int, and this gives str' <<'EOF'
struct P {
	x: int
}

fn main() {
	p := P("hi")
	print(f"{p.x}")
}
EOF

reject oversized-literal-into-a-byte-struct-field '`300` is not a value a byte holds' seed-gap <<'EOF'
struct P {
	x: byte
}

fn main() {
	p := P(300)
	print(f"{int(p.x)}")
}
EOF

reject bind-oversized-literal-to-byte '`300` is not a value a byte holds' seed-gap <<'EOF'
fn main() {
	b: byte = 300
	print(f"{b}")
}
EOF

reject bind-negative-literal-to-uint '`-1` is not a value a uint holds' seed-gap <<'EOF'
fn main() {
	u: uint = -1
	print(f"{u}")
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

# --- the escapes the decoder cannot read -------------------------------------------
#
# A malformed escape used to abort the COMPILER: `b'\xzz'` reached `byte(hi * 16 + lo)`
# with hex_val's -1 in it and died as `OverflowError`, `'\u{}'` decoded to a NUL that the
# str bridge refused as `EncodingError` — no file, no line, no form named. Worse were the
# quiet ones: `b'\x1z'` compiled and meant 15, `\q` compiled and meant `q`, `'\u41'` read
# its digits without the braces and meant whatever they said. One case per DISTINCT path
# through the decoder and its callers; the shapes that share one (`b'\xz1'`, `'\x41'`,
# `'\u{41'`) are the same fix and ride on these.

reject byte-escape-with-non-hex-digits 'E109 invalid escape in a byte literal' <<'EOF'
fn main() {
	print(int(b'\xzz'))
}
EOF

reject byte-escape-with-one-hex-digit 'E109 invalid escape in a byte literal' <<'EOF'
fn main() {
	print(int(b'\x1'))
}
EOF

reject unicode-escape-in-a-byte-literal 'E109 invalid escape in a byte literal' <<'EOF'
fn main() {
	print(int(b'\u{41}'))
}
EOF

reject unknown-escape-in-a-rune-literal 'E109 invalid escape in a rune literal' <<'EOF'
fn main() {
	print(int('\q'))
}
EOF

reject unicode-escape-with-no-digits 'E109 invalid escape in a rune literal' <<'EOF'
fn main() {
	print(int('\u{}'))
}
EOF

reject unicode-escape-without-braces 'E109 invalid escape in a rune literal' <<'EOF'
fn main() {
	print(int('\u41'))
}
EOF

reject unknown-escape-in-a-string 'E109 invalid escape in a string literal' <<'EOF'
fn main() {
	print("a\qb")
}
EOF

reject unknown-escape-in-a-triple-string 'E109 invalid escape in a string literal' <<'EOF'
fn main() {
	s := """
a\qb
"""
	print(s)
}
EOF

reject string-that-spells-a-nul 'E110 a string literal may not contain a NUL' <<'EOF'
fn main() {
	print("a\0b")
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
reject mut-fn-free-function 'a free function is never `mut fn`' at=1:1 seed-gap <<'EOF'
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

reject parse-a-str-as-a-bool 'does not parse a `str`' <<'EOF'
fn main() {
	print(f"{bool("1")}")
}
EOF

reject parse-a-str-as-a-byte 'does not parse a `str`' <<'EOF'
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

# A TEMPLATE SHARES THE ONE FLAT FUNCTION NAMESPACE, and it used to skip the rules that say
# so — it is removed from the program before the pass holding them runs. Every collision was
# therefore silent AND the template won, including against a module: `strconv.to_string` and a
# local `to_string[T]` are one name here, and the local one answered.

reject generic-shadows-a-plain-function "declared both as a generic and as a plain function" <<'EOF'
fn idg[T](x: T) -> T {
	return x
}

fn idg(x: int) -> int {
	return x + 100
}

fn main() {
	print(f"{idg(1)}")
}
EOF

reject generic-declared-twice "is declared twice, once as a generic" <<'EOF'
fn idg[T](x: T) -> T {
	return x
}

fn idg[U](x: U, y: U) -> U {
	return y
}

fn main() {
	print(f"{idg(1)}")
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

# A RUNE's bound is not a width, so a literal meets a predicate rather than a range: `rune`
# is "a single valid Unicode code point" (docs/core/types.md), and U+D800..U+DFFF are UTF-16
# surrogates, which are not characters. The second case is the one a width test cannot see —
# 55296 fits an i32 comfortably, and no rune holds it.

reject rune-literal-past-the-last-code-point 'is not a value a rune holds' <<'EOF'
fn main() {
	r: rune = 1114112
	print(f"{int(r)}")
}
EOF

reject rune-literal-inside-the-surrogates 'is not a value a rune holds' <<'EOF'
fn main() {
	r: rune = 55296
	print(f"{int(r)}")
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

# --- a byte converts; a LIST of them does not ------------------------------------
#
# `Into` carries a `byte` into an `int` slot one value at a time. A `list[byte]` does not
# become a `list[int]`, and that is not an omission from the matrix — a conversion BUILDS the
# target value, and a container handed over whole is not built, it is reinterpreted. The
# buffer's stride is its element's size, so reading one as the other walks 8 bytes per 1-byte
# slot: `ys: list[int] = list[byte]("Hi")` printed 26952, and a longer string read past the
# end. Convert the elements, or take the bytes as bytes.

reject bind-a-byte-list-to-an-int-list 'cannot bind list[byte] to a list[int] binding' <<'EOF'
fn main() {
	ys: list[int] = list[byte]("Hi")
	print(f"{ys[0]}")
}
EOF

# `take(1000)` on a `byte` parameter is the CONSTANT layer of Into: `int -> byte` is a real
# conversion, so it type-checks, and then the compiler evaluates it and reports the value
# rather than emitting the truncation cc used to complain about.
reject narrow-an-int-to-a-byte '`1000` is not a value a byte holds' seed-gap <<'EOF'
fn take(b: byte) -> int {
	return int(b)
}

fn main() {
	print(f"{take(1000)}")
}
EOF

# --- a carrier's PAYLOAD is a typed position too ----------------------------------
#
# docs/core/types.md lists the positions where a value meets a declared type, and a
# carrier's payload is one of them: `int?` and `Result[int]` both promise an int, one
# indirection down. Nothing asked. The check was hand-attached at the positions somebody
# thought of, and the payload is reached by a DIFFERENT route — the declaration is fitted
# by c_carrier_fit, which lowers the payload through a second call that had no rule on it.
#
# Five of the seven below reached cc, which reported an int-conversion warning against
# generated C. The last two compiled and RAN: an int in a `float?` printed `5`, and 300 in
# a `Result[byte]` truncated in silence. Both are the same omission — the payload, not the
# carrier, is where the declared type lives.

reject str-into-an-optional-list-element 'an element of this list literal is int' <<'EOF'
fn main() {
	xs: list[int?] = ["a"]
	print xs.len()
}
EOF

reject str-into-an-optional-struct-field 'field 1 of `Box` is int' <<'EOF'
struct Box {
	v: int?
}

fn main() {
	b := Box("hi")
	print "ok"
}
EOF

reject str-assigned-into-an-optional '`x` is int, and this gives str' <<'EOF'
fn main() {
	mut x: int? = 1
	x = "hi"
	print x!
}
EOF

reject str-into-a-result-left 'what this function answers is int, and this gives str' <<'EOF'
fn f() -> Result[int] {
	return Left("hi")
}

fn main() {
	print f()!
}
EOF

reject int-into-an-either-right 'what this function answers is str, and this gives int' <<'EOF'
fn f() -> Either[int, str] {
	return Right(7)
}

fn main() {
	print "ok"
}
EOF

# the two that RAN. An int64_t into a double field and an int64_t into a uint8_t are both
# legal C, so neither cc nor any gate had anything to say — the program simply answered
# something other than what it was written to answer.

# --- what only the driver can reject ------------------------------------------------
#
# A program build needs an entry point. `program ::= stmt-list` makes a main-less source
# grammatical — script mode is real, and a module is exactly a source with no `fn main` —
# so no parser or checker rule can own this: what is ill-formed is the BUILD, an entry
# file handed to `--emit bin` that defines no entry (docs/runtime/package.md). Before the
# driver said so, the answer was the LINKER's `undefined symbol _main` against an object
# nobody wrote; the seed has enforced this rule all along, which is why no marker excuses
# it below.

reject program-without-fn-main 'declares no `fn main`' at=1:1 <<'EOF'
x := 1
EOF

# The SAME source under `--emit lib` must stay accepted — a module is exactly a source
# with no entry point. Nothing else in the repo builds a main-less object, so the
# acceptance half of the build rule is asserted here, beside its rejection half: the
# regression this catches is the entry-point check drifting somewhere every emit stage
# passes through.
printf 'x := 1\n' >"$tmp/module-without-main.zg"
if "$ZERG" build --emit lib -o "$tmp/module-without-main" "$tmp/module-without-main.zg" >/dev/null 2>&1 &&
	[ -f "$tmp/module-without-main.o" ]; then
	pass=$((pass + 1))
else
	echo "LIB       module-without-main — a main-less module no longer builds with --emit lib"
	fail=$((fail + 1))
fi

# --- the rendering overrides -------------------------------------------------------
#
# docs/runtime/format.md: a `display` / `debug` override is `fn display() -> str` — the
# value alone in, the text it shows as out. The shape is what the render sites dispatch
# on, so a mis-shaped one would be silently passed over; chk_render_overrides makes it a
# finding at the declaration instead. The seed has no rendering dispatch and builds both
# programs, hence seed-gap. The `mut fn` half of the same rule is pinned by the working
# corpus case's boundary (a rendering may not mutate), and the seed refuses `mut fn` for
# reasons of its own, so that spelling needs no case here.

reject display-override-with-arguments 'takes no arguments beyond the value' seed-gap <<'EOF'
struct Point {
	x: int
}

impl Point {
	fn display(width: int) -> str {
		return "bad"
	}
}

fn main() {
	print Point(1).x
}
EOF

reject display-override-wrong-answer 'answers the `str` the value shows as' seed-gap <<'EOF'
struct Point {
	x: int
}

impl Point {
	fn display() -> int {
		return 3
	}
}

fn main() {
	print Point(1).x
}
EOF

# --- translation limits --------------------------------------------------------------
#
# Deep nesting is refused by count, not parsed until the stack runs out. Every pass that
# handles an expression recurses once per level — the parser, the checker, the emitter,
# monomorphization — and each of them died of SIGSEGV, no diagnostic, exit 139, somewhere
# between 300 and 500 levels on an 8 MB stack. The bound is ZG_MAX_NESTING in parser.zg,
# a documented translation limit of this implementation (docs/conformance.md), so
# exceeding it is a permanent answer and the cases live here rather than with the
# not-yet-built forms.
#
# The claim is UNIVERSAL — however a program reaches 200 levels, the answer is this one
# refusal — so it is asserted as a matrix rather than as an example. Two SHAPES, because
# they reach the parser by different routes: `(((…)))` recurses without deepening the
# tree, and `1 + 1 + …` deepens the tree without recursing, since the precedence levels
# build a left-deep chain in a LOOP. Four PLACES, because the shape reaches a different
# set of walks in each: an ordinary body is emitted directly, a generic template body is
# rewritten by subst_expr BEFORE the emitter counts anything (a 400-term chain there was
# an unreported SIGSEGV, and it took `zerg lsp` down with it, since a guard catches a
# raise and cannot catch a signal), a module constant is emitted on its own path, and a
# default parameter is spliced into each call site.
#
# The refusal comes from the PARSER for all eight, because that is where the bound now
# is: a tree too deep is refused where it is built, so no later walk needs a counter to
# stay honest. `reject` asserts a non-zero exit AND the sentence, so a case that returned
# to crashing would report as a message mismatch on empty output — and the signal check
# in `reject` names it as the crash it is.
#
# The sources are GENERATED because a program that nests 240 levels deep is not something
# to hand-maintain; 240 clears the 200 bound while staying far under where any of the
# walks used to crash, so the refusal is the counter answering, not luck. The seed
# inherits Go's growable stack and accepts all eight — a gap its own contract records
# (src/bootstrap/README.md).

# deep_expr <nested|flat> writes ONE too-deep expression, with no statement around it, so
# the four placements below differ only in where they put it.
deep_expr() {
	awk -v shape="$1" 'BEGIN {
		s = "1"
		for (i = 0; i < 240; i++) {
			if (shape == "nested") s = "(" s ")"
			else s = s " + 1"
		}
		printf "%s", s
	}'
}

for shape in nested flat; do
	e=$(deep_expr "$shape")

	printf 'fn main() {\n\tprint %s\n}\n' "$e" |
		reject "deep-$shape-in-a-body" 'nests more than 200 levels deep' seed-gap

	printf 'fn deep[T](v: T) -> int {\n\treturn %s\n}\n\nfn main() {\n\tprint deep(1)\n}\n' "$e" |
		reject "deep-$shape-in-an-instantiated-generic" 'nests more than 200 levels deep' seed-gap

	printf 'K := %s\n\nfn main() {\n\tprint K\n}\n' "$e" |
		reject "deep-$shape-in-a-module-constant" 'nests more than 200 levels deep' seed-gap

	printf 'fn f(a: int = %s) -> int {\n\treturn a\n}\n\nfn main() {\n\tprint f()\n}\n' "$e" |
		reject "deep-$shape-in-a-default-parameter" 'nests more than 200 levels deep' seed-gap
done

# The ninth case, and the only one the PARSER cannot answer: a tree the compiler COMPOSES.
# Neither expression below is 200 levels deep as written, so p_expr_root passes both — but
# a call that omits a defaulted parameter has the default spliced in (c_defaults_from), and
# the emitter then walks 190 terms of default underneath 190 terms of call. The bound that
# fires is the EMITTER's (c_deeper), which is why its sentence differs, and why the counter
# is not the defence-in-depth its own header used to claim: it is the only mechanism that
# sees a depth no source expression states.
#
# 190 is chosen to sit UNDER the 200 bound on each half and over it once composed; the two
# halves are the same chain so the arithmetic is visible rather than tuned.
half=$(awk 'BEGIN { s = "1"; for (i = 0; i < 190; i++) s = s " + 1"; printf "%s", s }')
printf 'fn f(a: int = %s) -> int {\n\treturn a\n}\n\nfn main() {\n\tprint f() + %s\n}\n' "$half" "$half" |
	reject deep-composed-by-a-default-splice 'chains more than 200 levels deep' seed-gap

# --- report ------------------------------------------------------------------------

if [ $fail -ne 0 ]; then
	echo "reject-check: $fail case(s) the compiler did not reject by itself"
	exit 1
fi
echo "reject-check: $pass ill-formed programs rejected by the compiler, none left to cc"
