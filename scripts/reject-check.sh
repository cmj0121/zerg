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
# case still "fails", so no build gate can tell them apart. Hence six assertions per
# case:
#
#   1. a non-zero exit
#   2. the code the rule is reported by, and a sentence too where one case of that code
#      has to be told from another
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
# LOST on the way to self-hosting, which is how this whole class went unnoticed. Only what
# `zerg` prints is normative — the seed merely has to say no — because the two word their
# diagnostics differently and pinning both wordings would pin nothing useful. The seed
# carries no codes at all, for the same reason.

set -u

# shellcheck source=scripts/lib/diag.sh
. "$(dirname "$0")/lib/diag.sh"

ZERG=${ZERG:-./bin/zerg}
ZERG0=${ZERG0:-./bin/zerg0}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

pass=0
fail=0

# reject <name> <code> [<sentence>] [<marker>...] — the program arrives on stdin. It is put
# to BOTH compilers: `zerg` must answer with the wanted code, and the seed must merely refuse
# it, since the two word their diagnostics differently and only the reject list is normative.
#
# Quoting a wanted sentence, which is fiddlier than it looks because these sentences carry
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
	local name=$1 code=$2
	shift 2

	# A SENTENCE IS OPTIONAL, and it is there for one job: telling two cases of the SAME
	# rule apart. The code says which rule fired, so a case that is the only one of its
	# code needs nothing further — and pinning its prose would only mean the gate turns red
	# the next time the prose gets better. Where several cases share a code, what each one
	# proves is which VALUES the rule named (`Line` carries 1 argument and this gives 2, as
	# against 2 and 1), and that lives in the sentence alone.
	#
	# It is told from a marker BY SHAPE: a marker is one word or one `key=value`, and every
	# sentence here has a space in it. Enumerating the markers instead would have put their
	# names in a second place — which is the defect `has_flag` below was written to end, and
	# it would be back the first time a fifth marker was added to one list and not the other.
	local want=""
	case ${1:-} in *" "*)
		want=$1
		shift
		;;
	esac

	# the markers COMPOSE: a case can be both a seed gap and a place the parser owes, and
	# reading one `$3` silently dropped the second — the gate then failed on an exception
	# that was declared right beside the one it honoured.
	local flags=" $* "

	# A CASE MAY BE MORE THAN ONE FILE. A line `--- <relative path>` starts a new one, so a
	# rule about MODULES can be written here at all — a module is a directory, and every case
	# until now was a single file in one. The entry lives in the case's own directory so that
	# a module beside it resolves the way it would in a real tree.
	local dir="$tmp/$name.d"
	rm -rf "$dir"
	mkdir -p "$dir"
	local src="$dir/$name.zg"
	awk -v dir="$dir" -v entry="$src" '
		/^--- / {
			out = dir "/" $2
			d = out
			sub(/\/[^\/]*$/, "", d)
			system("mkdir -p \"" d "\"")
			next
		}
		{ print > (out ? out : entry) }
	' >/dev/null
	touch "$src"

	# at=LINE:COL narrows the place assertion below to a SPECIFIC line — for a case whose
	# whole claim is that the finding sits at the declaration, not at a use site.
	local at="" fl
	for fl in $flags; do
		case $fl in at=*) at=${fl#at=} ;; esac
	done

	local out status first
	# `--emit bin`, not `--emit c`: the C stage stops BEFORE cc, so under it a program
	# only cc would reject looks accepted and assertions 3 and 4 can never fire. Linking
	# for real is what makes "the compiler said so, not cc" a claim this gate can check —
	# and it costs nothing while the gate is green, because a program the compiler rejects
	# never reaches cc anyway.
	out=$("$ZERG" build --emit bin -o "$tmp/$name.bin" "$src" 2>&1 >/dev/null)
	status=$?
	first=${out%%$'\n'*}

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
	if ! opens_with_code "$out" "$code"; then
		echo "CODE      $name — wanted $code to open the message, got: $first"
		fail=$((fail + 1))
		return
	fi
	if [ -n "$want" ]; then
		case $out in
		*"$want"*) ;;
		*)
			echo "MESSAGE   $name — wanted \"$want\", got: $first"
			fail=$((fail + 1))
			return
			;;
		esac
	fi
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
		echo "NO PLACE  $name — the message does not say where: $first"
		fail=$((fail + 1))
		return
	fi
	if [ -n "$at" ] && ! place_is "$out" "$name\.zg:$at"; then
		echo "PLACE     $name — the finding does not sit at $at: $first"
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

reject assign-to-plain-binding E307 <<'EOF'
fn main() {
	x := 1
	x = 2
	print(f"{x}")
}
EOF

reject assign-to-value-parameter E307 <<'EOF'
fn f(a: int) {
	a = 2
	print(f"{a}")
}

fn main() {
	f(1)
}
EOF

reject assign-to-field-of-immutable E308 <<'EOF'
struct P {
	pub x: int
}

fn main() {
	p := P(1)
	p.x = 5
	print(f"{p.x}")
}
EOF

reject assign-to-element-of-immutable E308 <<'EOF'
fn main() {
	xs := [1, 2]
	xs[0] = 9
	print(f"{xs[0]}")
}
EOF

reject assign-to-module-const E303 <<'EOF'
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
reject assign-to-module-binding E305 <<'EOF'
N := 5

fn main() {
	N = 6
	print(f"{N}")
}
EOF

reject assign-to-loop-variable E307 <<'EOF'
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

reject shadow-a-const-from-an-inner-block E356 <<'EOF'
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
reject loop-variable-takes-a-const-name E356 <<'EOF'
const k := 1

fn main() {
	for k in 0..3 {
		print k
	}
}
EOF

reject const-shadowing-an-outer-binding E357 <<'EOF'
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
reject const-collision-in-the-same-block E356 <<'EOF'
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
reject const-rebound-by-a-match-arm E356 one-finding <<'EOF'
const v := 1

enum E {
	A(int)
	B
}

fn main() {
	e := E.A(7)
	x := match e { E.A(v) => v  E.B => 0 }
	print x
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

reject top-level-mut-in-safe-code E358 <<'EOF'
mut counter := 0

fn main() {
	print "ok"
}
EOF

reject top-level-pub-mut E358 <<'EOF'
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
reject top-level-mut-that-is-used E358 at=1:1 one-finding <<'EOF'
mut counter := 0

fn main() {
	counter = counter + 1
	print counter
}
EOF

# A STATEMENT inside the group is ill-formed, not a nop: GRAMMAR#unsafe-item derives
# `unsafe-item ::= decorated-decl | binding` and nothing else, and the top level's
# statement fallback must not bless code into an unsafe CONTEXT nobody runs. Before the
# group's contents were parsed at all, this was dropped one token at a time — the same
# shredder that ate the group's `mut` bindings — so the case pins the refusal that
# replaced the silence.
reject statement-in-unsafe-group E254 <<'EOF'
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
reject unsafe-group-never-closed E256 at=1:1 <<'EOF'
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
reject unsafe-group-nested E253 at=2:2 <<'EOF'
unsafe {
	unsafe {
		mut counter := 0
	}
}

fn main() {
	print "ok"
}
EOF

# A `fn` DECLARED IN THE GROUP IS AN UNSAFE FN, and GRAMMAR group 12 says what that buys:
# "a `fn` is an unsafe fn (unsafe throughout its body, callable only from unsafe)". The
# group was built for its `mut` bindings — the language's only mutable global — and the
# `fn` beside them came along accepted and callable from anywhere, so a safe `main` could
# reach straight into the one context the compiler makes no guarantee about, with no
# diagnostic. That is the trust boundary the keyword exists to draw, erased silently.
#
# The seed refuses both of these by not parsing a module-level group at all, which is its
# own contract (src/bootstrap/README.md) and not a gap in this rule.
reject unsafe-group-fn-called-from-safe E387 'this call is in safe code' <<'EOF'
unsafe {
	fn poke() -> int {
		return 7
	}
}

fn main() {
	print poke()
}
EOF

# THE SAME RULE AT THE NAME, not only at the call. A bare function name where a value is
# wanted IS that function, so binding it and calling the binding would be the identical
# call one line later — a rule that only watched call sites would be one `f := poke` away
# from meaning nothing.
reject unsafe-group-fn-as-a-value E387 'hand safe code the same call' <<'EOF'
unsafe {
	fn poke() -> int {
		return 7
	}
}

fn main() {
	f := poke
	print f()
}
EOF

# --- a top-level annotation is honoured --------------------------------------------
#
# `answer: bool = 42` used to compile: the top level inferred from the value and silently
# discarded the annotation, so the program got an int named answer — an annotation the
# compiler does not check is a comment. The positive half (the annotation DECIDING the
# type, `half: float = 1` printing 0.5 for `half / 2`) is the corpus case topconst_typed.
reject top-level-annotation-mismatch E335 'cannot bind int to a bool binding' at=1:1 seed-gap <<'EOF'
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
reject const-initializer-reports-the-declaration E345 'operator `+` takes numeric operands' at=1:1 <<'EOF'
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

reject pub-on-an-impl-block E249 at=9:1 <<'EOF'
spec S {
	fn f() -> int
}

struct A {
	pub x: int
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

reject pub-before-a-statement E255 at=1:1 <<'EOF'
pub 42

fn main() {
	print "ok"
}
EOF

reject pub-on-an-import E247 at=1:1 <<'EOF'
pub import "std/io"

fn main() {
	print "ok"
}
EOF

reject pub-on-init E248 at=1:1 <<'EOF'
pub init() {
	print "ok"
}

fn main() {
	print "ok"
}
EOF

reject pub-before-a-decorator E250 at=1:1 <<'EOF'
pub #[derive(Eq)]
struct A {
	pub x: int
}

fn main() {
	print "ok"
}
EOF

reject pub-on-an-unsafe-group E252 at=1:1 <<'EOF'
pub unsafe {
	mut counter := 0
}

fn main() {
	print "ok"
}
EOF

# --- an import binds a namespace, and the prefix is real -----------------------------
#
# GRAMMAR#import-stmt: an `import` BINDS A NAMESPACE — the `as` alias, else the path's last
# segment — and the bound name lives in the one value namespace, "so colliding with a local
# name is an error". Neither half was checked.
#
# The prefix was not read AT ALL. A qualified name flattened to its last identifier and that
# identifier was looked up in the one program-wide function space, so with `util/text`
# imported, `bogus.text.shout(…)` and `util.shout(…)` compiled, linked and ran — the first
# naming nothing whatever, the second naming a directory. Every case below built a working
# binary before this branch, which is why they are here and not next door: none of them is a
# form this compiler had not got to, and no future feature makes any of them legal.
#
# The seed has said `undefined name` to all four of them since it was written. That is the
# reason reject-check makes it the oracle, said once more.

reject an-invented-namespace-prefix E372 'undefined name `bogus`' <<'EOF'
import "util/text"

fn main() {
	print bogus.text.shout("x")
}
--- util/text/text.zg
pub fn shout(s: str) -> str {
	return s + "!"
}
EOF

reject a-path-segment-is-not-a-namespace E372 'undefined name `util`' <<'EOF'
import "util/text"

fn main() {
	print util.shout("z")
}
--- util/text/text.zg
pub fn shout(s: str) -> str {
	return s + "!"
}
EOF

# THE NAMING HOLE IS NOT THE VISIBILITY HOLE, and this is the case that tells them apart.
# `hidden` is module-private, and the visibility rule caught it — through the invented
# prefix, reporting "`hidden` is not a public member of module `text`" about a module the
# program never named here. The right answer is about `bogus`, and it is now the only one.
reject an-invented-prefix-onto-a-private-member E372 'undefined name `bogus`' <<'EOF'
import "caller"

fn main() {
	print caller.go()
}
--- caller/caller.zg
import "util/text"

pub fn go() -> str {
	return bogus.hidden()
}
--- util/text/text.zg
fn hidden() -> str {
	return "private"
}
EOF

# `spawn` and `defer` resolve a callee down their own path, so a rule the ordinary call
# enforces is one they get only by asking the same question — which is why this asks it
# once (c_ns_kind) and all three read the answer.
reject an-invented-prefix-in-a-defer E372 'undefined name `bogus`' <<'EOF'
import "util/text"

fn main() {
	defer bogus.shout("a")
	print text.shout("b")
}
--- util/text/text.zg
pub fn shout(s: str) -> str {
	return s + "!"
}
EOF

# A REAL namespace and a member no module declares. It is the other half of the same
# resolution — the prefix resolved, the member did not — and it used to be three different
# answers depending on which shape asked: a placeless raise from a member read, "the method
# `nosuch` on a ?" from a call, and a not-built refusal from a `spawn`.
reject a-namespace-without-that-member E388 <<'EOF'
import "util/text"

fn main() {
	print text.nosuch("a")
}
--- util/text/text.zg
pub fn shout(s: str) -> str {
	return s + "!"
}
EOF

# TWO IMPORTS SHARING A LAST SEGMENT both bound `text`, and both answered through it: one
# namespace holding the union of two modules' members, with no diagnostic. GRAMMAR says
# `as` is "how two imports sharing a last segment coexist", which is only true if not
# renaming them is an error.
reject two-imports-sharing-a-last-segment E389 'is already the namespace of' at=3:2 <<'EOF'
import (
	"alt/text"
	"util/text"
)

fn main() {
	print text.shout("a")
}
--- alt/text/text.zg
pub fn whisper(s: str) -> str {
	return s + "..."
}
--- util/text/text.zg
pub fn shout(s: str) -> str {
	return s + "!"
}
EOF

# AND THE COLLISION WITH A LOCAL NAME, which is the one GRAMMAR states outright. `text()`
# and `text.shout()` both worked, and which of the two a reader reached depended on whether
# they wrote a `(` or a `.`.
reject an-import-colliding-with-a-function E389 'is already a function in this program' at=1:8 <<'EOF'
import "util/text"

fn text() -> str {
	return "local"
}

fn main() {
	print text()
}
--- util/text/text.zg
pub fn shout(s: str) -> str {
	return s + "!"
}
EOF

# --- generics -----------------------------------------------------------------------
#
# Both of these were `refuse` cases until the forms they name were built. What is left when
# a form arrives is not nothing: it is the PROGRAM's own mistake, which is permanent and
# belongs here.

reject explicit-type-args-on-a-plain-fn E275 no-place <<'EOF'
fn id(x: int) -> int {
	return x
}

fn main() {
	print id[int](7)
}
EOF

# THE MULTI-ARGUMENT SHAPE, which never looked like an index at all — a comma is what
# settles the bracket. It is the same rule and it is refused in the same place.
reject explicit-type-args-multi E275 no-place seed-gap <<'EOF'
fn pairup[A, B](a: A, b: B) -> A {
	return a
}

fn main() {
	print pairup[str, int]("k", 9)
}
EOF

# A SPEC CARRIES BEHAVIOUR AND NOTHING ELSE (GRAMMAR#spec-member). These three were
# `NotImplemented` — a form this compiler had not built — and they are not: the grammar
# derives no member but a signature and a provided method, so an associated type, an
# associated value and anything else that is not one are what the language does not have.
#
# The third is the one that was not refused at all. `spec Buf { SIZE := 4096 }` was accepted
# in silence and the member vanished: no impl had to supply it, and nothing said so.
reject associated-type-in-a-spec E230 no-place seed-gap <<'EOF'
spec It {
	type Item

	fn get() -> int
}

fn main() {
	print 1
}
EOF

reject associated-value-in-a-spec E211 no-place seed-gap <<'EOF'
spec Bits {
	BITS: int
}

fn main() {
	print 1
}
EOF

reject a-spec-member-that-is-not-one E276 no-place <<'EOF'
spec Buf {
	SIZE := 4096
	fn f()
}

fn main() {
	print 1
}
EOF

# THE ORPHAN RULE, at the only scope this compiler has one. docs/core/specs.md specifies
# coherence with the orphan rule enforced across PACKAGES; there is no package layer, so it
# lands one scope in — across modules. A spec and a type belong to whoever declared them,
# and an impl belongs with one of the two.
#
# It is the first case here that needs TWO FILES, which is why `reject` learned to split one.
reject an-orphan-impl E277 <<'EOF'
import "far"

impl Show for P {
	fn show() -> str {
		return "p"
	}
}

fn main() {
	print 1
}
--- far/far.zg
pub spec Show {
	fn show() -> str
}

pub struct P {
	pub x: int
}
EOF

# `#[derive(S)]` ON AN ENUM is DELEGATION, and the two shapes it refuses are refused because
# THE REWRITE DOES NOT EXIST — which is the whole test a decorator has to pass here. A method
# taking `This` would have to match the other argument too, and nothing says the two arms
# agree; a variant with no payload has nothing to delegate to.
reject derive-delegation-with-a-self-parameter E278 no-place <<'EOF'
spec Show {
	fn show() -> str
	fn same(o: This) -> bool
}

struct C {
	pub r: int
}

impl Show for C {
	fn show() -> str {
		return "c"
	}

	fn same(o: C) -> bool {
		return true
	}
}

#[derive(Show)]
enum Shape {
	Circle(C)
}

fn main() {
	print 1
}
EOF

reject derive-delegation-over-a-bare-variant E279 no-place <<'EOF'
spec Show {
	fn show() -> str
}

struct C {
	pub r: int
}

impl Show for C {
	fn show() -> str {
		return "c"
	}
}

#[derive(Show)]
enum Shape {
	Circle(C)
	None
}

fn main() {
	print 1
}
EOF

# and the OTHER half of derive is unchanged: on a struct the generated code is read out of
# the type's STRUCTURE, which only compiler-owned code may do.
reject derive-a-user-spec-on-a-struct E437 no-place <<'EOF'
spec Show {
	fn show() -> str
}

#[derive(Show)]
struct P {
	pub x: int
}

fn main() {
	print 1
}
EOF

# `#[obj]` REFUSES BY THE SAME TEST the delegating derive does: does the rewrite exist? A
# `mut fn` would write through a COPY, so the write reaches nothing anybody can read; a
# method taking `This` needs the type an object has forgotten, which is what the enum
# delegation is for; and a `spec` is the only thing with methods to hold as values.
reject obj-on-a-mut-method E281 no-place <<'EOF'
#[obj]
spec Draw {
	mut fn bump()
}

fn main() {
	print 1
}
EOF

reject obj-with-a-self-parameter E282 no-place <<'EOF'
#[obj]
spec Draw {
	fn same(o: This) -> bool
}

fn main() {
	print 1
}
EOF

reject obj-on-something-that-is-not-a-spec E280 no-place <<'EOF'
#[obj]
struct P {
	pub x: int
}

fn main() {
	print 1
}
EOF

# and its mirror: a `spec` has no structure for a derive to read.
reject derive-on-a-spec E283 no-place seed-gap <<'EOF'
#[derive(Eq)]
spec Draw {
	fn draw() -> str
}

fn main() {
	print 1
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

reject redeclare-plain-then-const-in-one-block E357 <<'EOF'
fn main() {
	x := 1
	const x := 2
	print x
}
EOF

# --- conditions -------------------------------------------------------------------
#
# Zerg has no truthiness. Every form that asks a question asks it of a bool.

reject int-as-if-condition E355 "must be bool, and this one is int" <<'EOF'
fn main() {
	if 1 {
		print("yes")
	}
}
EOF

reject str-as-if-condition E355 "must be bool, and this one is str" <<'EOF'
fn main() {
	s := "abc"
	if s {
		print("yes")
	}
}
EOF

reject int-as-for-condition E355 'the condition of a `for` must be bool' <<'EOF'
fn main() {
	mut n := 3
	for n {
		n = n - 1
	}
	print(f"{n}")
}
EOF

reject int-as-conditional-return E355 'the condition of a conditional `return` must be bool' <<'EOF'
fn f() -> int {
	return 1 if 2
	return 0
}

fn main() {
	print(f"{f()}")
}
EOF

reject int-as-if-expression-condition E355 'the condition of an `if` expression must be bool' <<'EOF'
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

reject int-plus-str E345 'operator `+` takes numeric operands' <<'EOF'
fn main() {
	s := "s"
	print(f"{1 + s}")
}
EOF

reject str-plus-int E345 'operator `+` takes numeric operands' <<'EOF'
fn main() {
	s := "s"
	print(f"{s + 1}")
}
EOF

# `//` is the floor-division operator, so its operands are numbers like the rest of the
# arithmetic family — a str is not one. It matters more than the others that this is said:
# `//` opens a comment in most languages a reader arrives from, and silently accepting
# `a // b` on two strings would be the worst possible way to learn that Zerg's is `#`.
reject floor-div-on-str E345 'operator `//` takes numeric operands' <<'EOF'
fn main() {
	s := "s"
	print s // s
}
EOF

reject bool-plus-int E345 'operator `+` takes numeric operands' <<'EOF'
fn main() {
	print(f"{true + 1}")
}
EOF

reject int-as-logical-operand E343 <<'EOF'
fn main() {
	print(f"{1 and 2}")
}
EOF

reject order-int-against-str E346 <<'EOF'
fn main() {
	s := "s"
	print(f"{1 < s}")
}
EOF

# AN AGGREGATE has no ordering either, and `#[derive(Eq)]` does not give it one: equality and
# ordering are two questions, and `Ord` is what would answer the second. This case lived next
# door among the not-yet-built forms, asserting a `NotImplemented` the EMITTER raised — and a
# raise ends the run, so those words replaced this rule's on every program that hit both. The
# rule that answers is this one, and it always was.
reject order-a-struct-that-has-eq E346 'and these are P and P' <<'EOF'
#[derive(Eq)]
struct P {
	pub x: int
}

fn main() {
	print P(1) < P(2)
}
EOF

reject compare-int-with-str E348 <<'EOF'
fn main() {
	s := "s"
	print(f"{1 == s}")
}
EOF

reject add-an-int-to-a-uint E353 <<'EOF'
fn main() {
	i: int = 3
	u: uint = 5
	print(f"{i + u}")
}
EOF

reject compare-an-int-with-a-uint E353 <<'EOF'
fn main() {
	i: int = -1
	u: uint = 1
	print(f"{i < u}")
}
EOF

reject equate-an-int-with-a-uint E353 <<'EOF'
fn main() {
	i: int = -1
	u: uint = 1
	print(f"{i == u}")
}
EOF

reject bitwise-on-float E344 <<'EOF'
fn main() {
	print(f"{3.0 & 1}")
}
EOF

reject bitwise-on-bool E344 <<'EOF'
fn main() {
	print(f"{true & 1}")
}
EOF

reject negate-a-str E351 <<'EOF'
fn main() {
	s := "a"
	print(f"{-s}")
}
EOF

reject not-an-int E350 <<'EOF'
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

reject bind-str-to-int E335 'cannot bind str to a int binding' <<'EOF'
fn main() {
	x: int = "hello"
	print(f"{x}")
}
EOF

reject bind-int-to-bool E335 'cannot bind int to a bool binding' <<'EOF'
fn main() {
	b: bool = 1
	print(f"{b}")
}
EOF

reject bind-float-to-int E335 'cannot bind float to a int binding' <<'EOF'
fn main() {
	x: int = 1.5
	print(f"{x}")
}
EOF

reject bind-int-list-to-str-list E329 <<'EOF'
fn main() {
	ys: list[str] = [1, 2]
	print(f"{ys[0]}")
}
EOF

reject typedef-value-into-its-underlying E340 'argument 1 of `f` is int, and this gives Celsius' <<'EOF'
type Celsius = int

fn f(n: int) -> int {
	return n
}

fn main() {
	print(f"{f(Celsius(20))}")
}
EOF

reject logical-operator-on-a-typedef E342 'has no meaning on Flag and Flag' <<'EOF'
type Flag = bool

fn main() {
	f := Flag(true)
	g := Flag(false)
	print(f"{f and g}")
}
EOF

reject bitwise-operator-on-a-typedef E342 'has no meaning on Mask and int' <<'EOF'
type Mask = int

fn main() {
	m := Mask(3)
	print(f"{m & 1}")
}
EOF

reject prefix-operator-on-a-typedef E349 <<'EOF'
type Flag = bool

fn main() {
	f := Flag(true)
	print(f"{not f}")
}
EOF

reject arithmetic-on-a-typedef E342 <<'EOF'
type Celsius = int

fn main() {
	c := Celsius(20)
	print(f"{c + 1}")
}
EOF

reject typedef-declared-twice E382 seed-gap <<'EOF'
type Celsius = int
type Celsius = float

fn main() {
	print(f"{int(Celsius(1))}")
}
EOF

reject typedef-over-an-undeclared-type E337 <<'EOF'
type Celsius = Nope

fn main() {
	print(f"{int(Celsius(1))}")
}
EOF

reject typedef-conversion-takes-one-value E366 <<'EOF'
type Celsius = int

fn main() {
	print(f"{int(Celsius(1, 2))}")
}
EOF

reject str-sent-on-an-int-channel E338 'the value sent on this channel is int' <<'EOF'
fn main() {
	ch := chan[int](1)
	ch <- "hi"
	print(f"{(<-ch)!}")
}
EOF

reject str-appended-to-an-int-list E338 'the element `append` adds is int' <<'EOF'
fn main() {
	mut xs: list[int] = []
	xs.append("hi")
	print(f"{xs.len()}")
}
EOF

reject str-written-into-an-int-map E338 'the value written into this map is int' <<'EOF'
fn main() {
	mut m: map[str, int] = {:}
	m["a"] = "hi"
	print(f"{m.len()}")
}
EOF

reject str-among-a-map-literals-ints E338 'a value of this map literal is int' <<'EOF'
fn main() {
	m := {"a": 1, "b": "hi"}
	print(f"{m.len()}")
}
EOF

reject str-as-an-int-coalesce-fallback E338 'the `??` fallback is int' <<'EOF'
fn main() {
	x: int? = 1
	print(f"{x ?? "no"}")
}
EOF

reject str-into-an-int-variant-payload E338 'payload 1 of `Line` is int' <<'EOF'
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

reject str-into-an-int-struct-field E338 'field 1 of `P` is int, and this gives str' <<'EOF'
struct P {
	pub x: int
}

fn main() {
	p := P("hi")
	print(f"{p.x}")
}
EOF

reject oversized-literal-into-a-byte-struct-field E330 '`300` is not a value a byte holds' <<'EOF'
struct P {
	pub x: byte
}

fn main() {
	p := P(300)
	print(f"{int(p.x)}")
}
EOF

reject bind-oversized-literal-to-byte E330 '`300` is not a value a byte holds' <<'EOF'
fn main() {
	b: byte = 300
	print(f"{b}")
}
EOF

reject bind-negative-literal-to-uint E330 '`-1` is not a value a uint holds' <<'EOF'
fn main() {
	u: uint = -1
	print(f"{u}")
}
EOF

# --- returned value ---------------------------------------------------------------
#
# A signature is a promise. The conditional `return` is here on its own because it takes a
# different path through the emitter than the plain one.

reject return-str-from-int-fn E333 "this function's answer is int, and this gives str" <<'EOF'
fn f() -> int {
	return "nope"
}

fn main() {
	print(f"{f()}")
}
EOF

reject return-int-from-bool-fn E333 "this function's answer is bool, and this gives int" <<'EOF'
fn f() -> bool {
	return 1
}

fn main() {
	print(f"{f()}")
}
EOF

reject conditional-return-wrong-type E333 "this function's answer is int, and this gives str" <<'EOF'
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

reject call-with-too-few-arguments E328 '`add` needs 2 arguments and this gives 1' <<'EOF'
fn add(a: int, b: int) -> int {
	return a + b
}

fn main() {
	print(f"{add(1)}")
}
EOF

reject call-with-too-many-arguments E327 '`add` takes 2 arguments and this gives 3' <<'EOF'
fn add(a: int, b: int) -> int {
	return a + b
}

fn main() {
	print(f"{add(1, 2, 3)}")
}
EOF

reject call-past-a-default E327 '`scale` takes 2 arguments and this gives 3' <<'EOF'
fn scale(n: int, by: int = 2) -> int {
	return n * by
}

fn main() {
	print(f"{scale(5, 3, 9)}")
}
EOF

reject method-with-too-many-arguments E327 '`P.add` takes 2 arguments and this gives 3' <<'EOF'
struct P {
	pub x: int
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

reject float-argument-into-int-parameter E340 'argument 1 of `f` is int, and this gives float' <<'EOF'
fn f(a: int) -> int {
	return a
}

fn main() {
	print(f"{f(1.5)}")
}
EOF

reject str-argument-into-int-parameter E340 'argument 2 of `add` is int, and this gives str' <<'EOF'
fn add(a: int, b: int) -> int {
	return a + b
}

fn main() {
	s := "x"
	print(f"{add(1, s)}")
}
EOF

reject str-argument-into-a-method E340 'argument 1 of `P.add` is int, and this gives str' <<'EOF'
struct P {
	pub x: int
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

reject int-as-match-arm-guard E355 "the condition of a match arm's guard must be bool" <<'EOF'
fn main() {
	n := 1
	print(match n {
		_ if 1 => "yes"
		_      => "no"
	})
}
EOF

reject wrapping-operator-on-a-bool E345 'operator `+%` takes numeric operands' <<'EOF'
fn main() {
	print(f"{true +% 1}")
}
EOF

reject assign-int-to-a-bool-binding E339 'cannot assign int to `b`, which holds bool' <<'EOF'
fn main() {
	mut b: bool = true
	b = 1
	print(f"{b}")
}
EOF

reject assign-str-to-an-int-field E339 'cannot assign str to that part of `p`, which holds int' <<'EOF'
struct P {
	pub x: int
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

reject bind-a-struct-to-another-struct E335 'cannot bind B to a A binding' <<'EOF'
struct A {
	pub x: int
}

struct B {
	pub y: int
}

fn main() {
	a: A = B(1)
	print(f"{a.x}")
}
EOF

reject pass-a-struct-where-another-goes E340 'argument 1 of `take` is A, and this gives B' <<'EOF'
struct A {
	pub x: int
}

struct B {
	pub y: int
}

fn take(v: A) -> int {
	return v.x
}

fn main() {
	print(f"{take(B(1))}")
}
EOF

reject return-a-struct-the-signature-does-not-name E333 "this function's answer is A, and this gives B" <<'EOF'
struct A {
	pub x: int
}

struct B {
	pub y: int
}

fn f() -> A {
	return B(1)
}

fn main() {
	print(f"{f().x}")
}
EOF

reject bind-a-nested-list-of-the-wrong-element E329 <<'EOF'
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

reject rune-with-two-characters E103 <<'EOF'
fn main() {
	c := 'ba'
	print(f"{c}")
}
EOF

reject byte-with-two-characters E103 <<'EOF'
fn main() {
	c := b'ba'
	print(f"{c}")
}
EOF

reject string-that-never-closes E101 <<'EOF'
fn main() {
	s := "no closing quote
	print(s)
}
EOF

reject a-character-no-token-uses E104 <<'EOF'
fn main() {
	x := 1 @ 2
	print(f"{x}")
}
EOF

reject based-number-with-no-digits E108 <<'EOF'
fn main() {
	n := 0x
	print(f"{n}")
}
EOF

# GRAMMAR group 3 spells the prefix and its first digit as one thing — `'0x' hex-digit ( '_'?
# hex-digit )*` — so the `_` that groups digits has a digit on BOTH sides, and there is no
# digit to its left when it comes first. `0x_1F` used to lex as an Int, and the digits it did
# have were worth 31, so a literal the grammar has no production for compiled and answered a
# number: the one shape of this mistake that never reaches a reader.
#
# The other two prefixes and the DOUBLED underscore ride on this one. `0o_7`, `0b_1` and
# `0x__1F` are all the same question asked at the same place — is the byte after the prefix a
# digit — and differ only in which `based_valid` they ask it with; the seed has answered no to
# all four since it was written, and it is the oracle here.
reject based-number-with-a-leading-underscore E108 <<'EOF'
fn main() {
	n := 0x_1F
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

reject byte-escape-with-non-hex-digits E109 'invalid escape in a byte literal' <<'EOF'
fn main() {
	print(int(b'\xzz'))
}
EOF

reject byte-escape-with-one-hex-digit E109 'invalid escape in a byte literal' <<'EOF'
fn main() {
	print(int(b'\x1'))
}
EOF

reject unicode-escape-in-a-byte-literal E109 'invalid escape in a byte literal' <<'EOF'
fn main() {
	print(int(b'\u{41}'))
}
EOF

reject unknown-escape-in-a-rune-literal E109 'invalid escape in a rune literal' <<'EOF'
fn main() {
	print(int('\q'))
}
EOF

reject unicode-escape-with-no-digits E109 'invalid escape in a rune literal' <<'EOF'
fn main() {
	print(int('\u{}'))
}
EOF

reject unicode-escape-without-braces E109 'invalid escape in a rune literal' <<'EOF'
fn main() {
	print(int('\u41'))
}
EOF

reject unknown-escape-in-a-string E109 'invalid escape in a string literal' <<'EOF'
fn main() {
	print("a\qb")
}
EOF

reject unknown-escape-in-a-triple-string E109 'invalid escape in a string literal' <<'EOF'
fn main() {
	s := """
a\qb
"""
	print(s)
}
EOF

reject string-that-spells-a-nul E110 <<'EOF'
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

reject compare-an-optional-with-nil E341 'an optional is not an operand of `==`' <<'EOF'
fn main() {
	x: int? = nil
	print(f"{x == nil}")
}
EOF

reject compare-an-optional-with-a-value E341 'an optional is not an operand of `==`' <<'EOF'
fn main() {
	x: int? = 1
	print(f"{x == 1}")
}
EOF

reject spawn-with-too-few-arguments E328 '`work` needs 2 arguments and this gives 1' <<'EOF'
fn work(a: int, b: int) {
	print(f"{a + b}")
}

fn main() {
	spawn work(1)
}
EOF

reject defer-with-the-wrong-argument-type E340 'argument 1 of `note` is str, and this gives int' <<'EOF'
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
# And a match arm builds its comparison as TEXT, so `1..=2 =>` is one more spelling that
# meets no rule of its own unless one is hung there. `nil =>` was a second, until it earned
# a refusal that names the PATTERN rather than the `==` nobody wrote; it lives in
# refuse-check now.

reject order-an-optional E341 'an optional is not an operand of `>`' <<'EOF'
fn main() {
	x: int? = 1
	print(f"{x > 0}")
}
EOF

reject add-to-an-optional E341 'an optional is not an operand of `+`' <<'EOF'
fn main() {
	x: int? = 1
	print(f"{x + 1}")
}
EOF

reject and-an-optional E341 'an optional is not an operand of `and`' <<'EOF'
fn main() {
	x: bool? = true
	print(f"{x and true}")
}
EOF

reject match-an-optional-against-a-range E341 'an optional is not an operand of `>=`' seed-gap <<'EOF'
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
# GRAMMAR#param: a `mut &` "cannot ESCAPE (be captured by a spawn or stored past the call)".
# `spawn` refused it in a pass of its own and `defer`, which the same sentence covers,
# reached cc — so the refusal moved to the choke point both of them share.

reject spawn-captures-a-borrow E312 'is a `mut &` and cannot cross a `spawn`' <<'EOF'
fn bump(mut &n: int) {
	n = n + 1
}

fn main() {
	mut k := 1
	spawn bump(k)
	print(f"{k}")
}
EOF

reject defer-captures-a-borrow E312 'is a `mut &` and cannot cross a `defer`' seed-gap <<'EOF'
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

reject a-default-that-does-not-fit E310 seed-gap <<'EOF'
fn f(a: int, b: str = 1) {
	print(f"{a}{b}")
}

fn main() {
	f(2)
}
EOF

# seed-gap: zerg0 accepts this, and a call that USES the default segfaults — it emits the
# literal where a pointer goes. Recorded in src/bootstrap/README.md.
reject a-mut-ref-with-a-default E309 seed-gap <<'EOF'
fn f(a: int, mut &b: int = 0) {
	b = a
}

fn main() {
	print("declared")
}
EOF

reject a-pattern-that-binds-too-much E311 '`Line` carries 1 argument and this pattern binds 2' <<'EOF'
enum Shape {
	Line(int)
	Dot
}

fn area(s: Shape) -> int {
	return match s {
		Shape.Line(n, m) => n
		_          => 0
	}
}

fn main() {
	print(f"{area(Shape.Dot)}")
}
EOF

reject a-construction-that-gives-too-few E311 '`Line` carries 2 arguments and this gives 1' <<'EOF'
enum Shape {
	Line(int, int)
	Dot
}

fn main() {
	s := Shape.Line(7)
	print("built")
}
EOF

reject a-pattern-that-binds-too-few E311 '`Line` carries 2 arguments and this pattern binds 1' <<'EOF'
enum Shape {
	Line(int, int)
	Dot
}

fn area(s: Shape) -> int {
	return match s {
		Shape.Line(n) => n
		_       => 0
	}
}

fn main() {
	print(f"{area(Shape.Dot)}")
}
EOF

reject a-construction-that-gives-too-much E311 '`Line` carries 1 argument and this gives 2' <<'EOF'
enum Shape {
	Line(int)
	Dot
}

fn main() {
	s := Shape.Line(7, 8)
	print("built")
}
EOF

# --- a borrow needs a place ------------------------------------------------------
#
# A `mut &` argument is the caller's own storage handed over to be written. `m["k"]` reads
# like one and is not: it lowers to a statement expression, so `&` on it reached cc.

reject a-borrow-of-a-map-index E323 <<'EOF'
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

reject store-through-a-map-index E313 'cannot store through a map index' <<'EOF'
fn main() {
	mut d: map[str, list[int]] = {"k": [1, 2]}
	d["k"][0] = 7
	print("x")
}
EOF

reject store-through-a-call-result E313 'cannot store through a call result' <<'EOF'
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

reject match-arms-disagree E322 <<'EOF'
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
# GRAMMAR#keyword makes `this` a reserved word, and GRAMMAR#param says it is NOT a parameter,
# that a `fn` whose body uses it with no instance bound is a compile error, and that the self
# type is `This`. The seed enforces all of it. `zerg` enforced none of it: every naming
# position read `cur(p).lexeme` — whatever token was there — so `this` passed as a
# parameter, a field, a function, a type, a variant, a pattern binding. In a METHOD it
# reached cc, because the parser has already put a `this` at parameter 0.

reject this-as-a-parameter E245 'is a reserved word and cannot name a parameter' no-place <<'EOF'
fn f(this: int) -> int {
	return this
}

fn main() {
	print(f(7))
}
EOF

reject this-as-a-method-parameter E245 'is a reserved word and cannot name a parameter' no-place <<'EOF'
struct P {
	pub x: int
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

reject this-as-a-field E245 'is a reserved word and cannot name a struct field' no-place <<'EOF'
struct P {
	pub this: int
}

fn main() {
	p := P(1)
	print(p.this)
}
EOF

reject this-as-a-function E245 'is a reserved word and cannot name a function' no-place <<'EOF'
fn this() -> int {
	return 1
}

fn main() {
	print(this())
}
EOF

reject this-as-a-type E245 'is a reserved word and cannot name a struct' no-place <<'EOF'
struct this {
	pub x: int
}

fn main() {
	print(1)
}
EOF

reject this-as-a-variant E245 'is a reserved word and cannot name an enum variant' no-place <<'EOF'
enum E {
	this
	B
}

fn main() {
	print(1)
}
EOF

reject this-as-a-pattern-binding E245 'is a reserved word and cannot name a pattern binding' no-place <<'EOF'
enum E {
	A(int)
	B
}

fn main() {
	e := E.A(7)
	print(match e {
		E.A(this) => this
		_ => 0
	})
}
EOF

# `This` IS RESERVED TOO, and it is the one reserved word the lexer reads as an ordinary
# identifier: it is the SELF TYPE, written by every `impl` and declared by none. A migration
# is what found it — `enum Kind { … This … }` had a variant with that name, and once a
# variant had to be written through its enum there was no telling `Kind.This` the value from
# `This` the type.
#
# The seed has no reserved-name rule at all for it, which is why these four are marked: it
# refuses `this` only because its lexer makes that a keyword token, and `This` is not one.
reject capital-this-as-a-variant E245 'cannot name an enum variant' no-place seed-gap <<'EOF'
enum E {
	This
	B
}

fn main() {
	print 1
}
EOF

reject capital-this-as-a-type E245 'cannot name a struct' no-place seed-gap <<'EOF'
struct This {
	pub x: int
}

fn main() {
	print 1
}
EOF

reject capital-this-as-a-function E245 'cannot name a function' no-place seed-gap <<'EOF'
fn This() {
	print 1
}

fn main() {
	This()
}
EOF

reject capital-this-as-a-parameter E245 'cannot name a parameter' no-place seed-gap <<'EOF'
fn f(This: int) {
	print This
}

fn main() {
	f(1)
}
EOF

reject this-outside-a-method E371 <<'EOF'
fn f() -> int {
	return this
}

fn main() {
	print(1)
}
EOF

reject self-type-outside-an-impl E364 <<'EOF'
fn f() -> This {
	return 1
}

fn main() {
	print(1)
}
EOF

# --- a borrow needs both halves, and may not alias -------------------------------
#
# GRAMMAR#param — "the CALLER decides whether its variable is `mut`, the CALLEE decides
# via `mut &` whether it writes back, so a caller-visible mutation needs BOTH", and a
# borrow "cannot ALIAS (the same variable to two `mut &` in one call), which keeps it safe
# with no borrow checker". Only the callee's half was ever read.

reject mut-ref-of-an-immutable E325 'writes back to `k`, which is not `mut`' <<'EOF'
fn bump(mut &n: int) {
	n = n + 1
}

fn main() {
	k := 1
	bump(k)
	print(f"{k}")
}
EOF

reject mut-ref-aliased E326 <<'EOF'
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

reject mut-fn-on-an-immutable-receiver E325 'which is a `mut fn`, writes back to `p`' <<'EOF'
struct P {
	pub x: int
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

# GRAMMAR#fn-decl — `mut fn` is meaningful only on a method; "a free function or closure
# has no receiver, so it is never `mut fn`". The token fell into the top-level skip, so the
# `mut` was swallowed and the function compiled as if it had never been written.
reject mut-fn-free-function E251 at=1:1 seed-gap <<'EOF'
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

reject compare-two-enums E347 'cannot compare Color and Fruit' <<'EOF'
enum Color {
	Red
	Green
}

enum Fruit {
	Apple
}

fn main() {
	print(f"{Color.Red == Fruit.Apple}")
}
EOF

reject compare-an-enum-and-an-int E347 'cannot compare Color and int' <<'EOF'
enum Color {
	Red
	Green
}

fn main() {
	c := Color.Red
	print(f"{c == 0}")
}
EOF

reject qualify-with-the-wrong-enum E457 no-place <<'EOF'
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

reject qualify-a-name-that-is-not-a-variant E456 no-place <<'EOF'
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

reject convert-nothing-to-a-list E260 no-place <<'EOF'
fn main() {
	xs := list[int]()
	print(f"{xs.len()}")
}
EOF

reject convert-nothing-to-an-int E258 no-place <<'EOF'
fn main() {
	print(f"{int()}")
}
EOF

reject convert-two-values E259 no-place <<'EOF'
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

reject parse-a-str-as-a-bool E367 <<'EOF'
fn main() {
	print(f"{bool("1")}")
}
EOF

reject parse-a-str-as-a-byte E367 <<'EOF'
fn main() {
	print(f"{byte("65")}")
}
EOF

reject convert-a-list-to-an-int E455 no-place <<'EOF'
fn main() {
	xs := [1, 2]
	print(f"{int(xs)}")
}
EOF

# The SAME rule with a NAMED type as the source, which is a different path through it: the
# conversion asks what the name is REPRESENTED by, and a struct is represented by itself.
# While the type tables were four, "is it a typedef" and "what is it underneath" were the
# same scan of the same list, so a name with no row answered "not a typedef" for free. With
# one table every type name has a row, and the second question had to learn to ask the kind
# — an unguarded read answered UNKNOWN for a struct, which is the one answer that makes this
# rule stand down, and `((int64_t)(zg_p))` went to cc.
#
# No emitted byte moved. A valid program never converts a struct, so the differential that
# accepted that refactor could not have seen it: what a compiler REFUSES is not visible in
# what it emits.
reject convert-a-struct-to-an-int E455 no-place <<'EOF'
struct P {
	pub v: int
}

fn main() {
	p := P(1)
	print(f"{int(p)}")
}
EOF

# --- and a str is not a container to subscript -----------------------------------
#
# docs/core/types.md: a str "iterates as `rune` and is NOT indexable" — it is UTF-8, so a
# subscript would name a byte and not a character. The emitter named every other
# non-container and let a str through to the LIST path: `s[0]` read a `const char*` as a
# list header and printed a different number every run, and `s[1..3]` reached cc.

reject index-a-str E320 <<'EOF'
fn main() {
	s := "hello"
	print(f"{s[0]}")
}
EOF

reject slice-a-str E320 <<'EOF'
fn main() {
	s := "hello"
	print(s[1..3])
}
EOF

# A TEMPLATE SHARES THE ONE FLAT FUNCTION NAMESPACE, and it used to skip the rules that say
# so — it is removed from the program before the pass holding them runs. Every collision was
# therefore silent AND the template won, including against a module: `strconv.to_string` and a
# local `to_string[T]` are one name here, and the local one answered.

reject generic-shadows-a-plain-function E363 <<'EOF'
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

reject generic-declared-twice E362 <<'EOF'
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

reject int-literal-past-i64 E319 <<'EOF'
fn main() {
	print(f"{9223372036854775808}")
}
EOF

reject int-literal-far-past-i64 E319 <<'EOF'
fn main() {
	print(f"{99999999999999999999999}")
}
EOF

reject hex-literal-past-i64 E319 <<'EOF'
fn main() {
	print(f"{0xFFFFFFFFFFFFFFFFF}")
}
EOF

# A RUNE's bound is not a width, so a literal meets a predicate rather than a range: `rune`
# is "a single valid Unicode code point" (docs/core/types.md), and U+D800..U+DFFF are UTF-16
# surrogates, which are not characters. The second case is the one a width test cannot see —
# 55296 fits an i32 comfortably, and no rune holds it.

reject rune-literal-past-the-last-code-point E330 'is not a value a rune holds' <<'EOF'
fn main() {
	r: rune = 1114112
	print(f"{int(r)}")
}
EOF

reject rune-literal-inside-the-surrogates E330 'is not a value a rune holds' <<'EOF'
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

reject impl-misses-a-required-member E318 'does not implement `show`' seed-gap <<'EOF'
struct P {
	pub n: int
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

reject impl-misses-one-of-two E318 'does not implement `tag`' seed-gap <<'EOF'
struct P {
	pub n: int
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

reject impl-returns-the-wrong-type E317 'it returns str, and the spec declares int' seed-gap <<'EOF'
spec Tag {
	fn tag() -> int
}

struct A {
	pub v: int
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

reject impl-takes-the-wrong-count E317 'it takes 0 arguments, and the spec declares 1' seed-gap <<'EOF'
spec Tag {
	fn tag(n: int) -> int
}

struct A {
	pub v: int
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

reject impl-renames-a-parameter E317 'a named argument selects by that name' seed-gap <<'EOF'
spec Tag {
	fn tag(n: int) -> int
}

struct A {
	pub v: int
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

reject impl-drops-the-by-ref E317 'is not `mut &` and the spec' seed-gap <<'EOF'
spec Tag {
	fn tag(mut &n: int) -> int
}

struct A {
	pub v: int
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

reject impl-adds-a-default E317 'has a default and the spec declares none' seed-gap <<'EOF'
spec Tag {
	fn tag(n: int) -> int
}

struct A {
	pub v: int
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

reject impl-drops-the-mut-fn E317 'it is not a `mut fn` and the spec' seed-gap <<'EOF'
spec Bump {
	mut fn bump() -> int
}

struct A {
	pub v: int
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

reject impl-adds-a-mut-fn E317 'it is a `mut fn` and the spec' <<'EOF'
spec Bump {
	fn bump() -> int
}

struct A {
	pub v: int
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

reject impl-breaks-the-self-type E317 'parameter `other` is int, and the spec declares A' seed-gap <<'EOF'
spec Eq {
	fn eq(other: This) -> bool
}

struct A {
	pub v: int
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

reject impl-breaks-a-spec-parameter E317 'parameter `k` is str, and the spec declares int' <<'EOF'
spec Ix[K] {
	fn at(k: K) -> int
}

struct A {
	pub v: int
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

reject super-spec-is-not-satisfied E318 'does not implement `eq`, which `Eq` requires' <<'EOF'
spec Eq {
	fn eq() -> bool
}

spec Ord: Eq {
	fn lt() -> bool
}

struct A {
	pub v: int
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

reject impl-of-a-spec-that-does-not-exist E314 <<'EOF'
struct A {
	pub v: int
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

reject a-type-declares-a-method-twice E451 no-place <<'EOF'
spec Tag {
	fn tag() -> int
}

struct A {
	pub v: int
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

reject impl-does-not-bind-a-spec-parameter E315 <<'EOF'
spec Ix[K] {
	fn at(k: K) -> int
}

struct A {
	pub v: int
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

# --- the top level is one namespace ----------------------------------------------
#
# A struct, an enum, a type declaration, a spec, a free function and a module constant all
# live in it — every module flattens into one scope here, and GRAMMAR's construction note
# puts a type name into the VALUE namespace besides ("a type and a function cannot share a
# name (a duplicate is an error — Zerg has no overloading)"), since `struct User` is what
# makes `User(…)` a call. Nothing checked most of it, and the shapes failed several
# different ways: two structs MERGED into one of both their fields, two of the same reached
# cc as a "typedef redefinition" against .zerg-cache, two specs were simply accepted, and a
# type beside a function reached cc as "redefinition of 'zg_Foo' as different kind of
# symbol" — after `Foo(3)` had already been read as the CONSTRUCTOR rather than the call
# the program wrote.
#
# One walk answers all of it (c_build_top_names), which is why the constant-versus-function
# pair is here rather than in a section of its own: it used to be a second check, in the
# constant walk, and a second check had a second extent — it knew about functions and not
# about types, so `const A := 1` beside `struct A` went straight to cc.

reject a-struct-declared-twice E382 no-place <<'EOF'
struct A {
	pub v: int
}

struct A {
	pub w: str
}

fn main() {
	print(f"{A(7).v}")
}
EOF

reject an-enum-declared-twice E382 no-place seed-gap <<'EOF'
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

reject a-spec-declared-twice E382 seed-gap <<'EOF'
spec Tag {
	fn tag() -> int
}

spec Tag {
	fn other() -> int
}

struct A {
	pub v: int
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

reject a-struct-and-a-spec-share-a-name E381 'once as a struct, once as a spec' seed-gap <<'EOF'
struct A {
	pub v: int
}

spec A {
	fn f() -> int
}

fn main() {
	print(f"{A(1).v}")
}
EOF

# and the pairings a TYPE makes with a FUNCTION, which is the half GRAMMAR states outright
# and nothing enforced. All four kinds of type are here because all four spell a different
# C declaration and each failed cc its own way, and both source orders are here because the
# finding must land on the `fn` either way: a FnDecl knows its place and a struct does not,
# so walking the types first is what makes this whole family a rule that says WHERE.

reject a-struct-and-a-function-share-a-name E381 'once as a struct, once as a function' at=5:1 <<'EOF'
struct A {
	pub v: int
}

fn A(n: int) -> int {
	return n
}

fn main() {
	print "ok"
}
EOF

reject a-function-and-a-struct-share-a-name E381 'once as a struct, once as a function' at=1:1 <<'EOF'
fn A(n: int) -> int {
	return n
}

struct A {
	pub v: int
}

fn main() {
	print "ok"
}
EOF

reject an-enum-and-a-function-share-a-name E381 'once as an enum, once as a function' at=5:1 <<'EOF'
enum E {
	X
}

fn E(n: int) -> int {
	return n
}

fn main() {
	print "ok"
}
EOF

reject a-typedef-and-a-function-share-a-name E381 'once as a type declaration, once as a function' at=3:1 <<'EOF'
type Celsius = int

fn Celsius(n: int) -> int {
	return n
}

fn main() {
	print "ok"
}
EOF

reject a-spec-and-a-function-share-a-name E381 'once as a spec, once as a function' at=5:1 <<'EOF'
spec Tag {
	fn tag() -> int
}

fn Tag(n: int) -> int {
	return n
}

fn main() {
	print "ok"
}
EOF

# A TEMPLATE IS A NAME TOO. `fn Box[T](…)` is removed from the program before the passes
# that lower it, on the grounds that a template is not a function — but it still claims
# `Box` at the top level, and `Box(3)` beside `struct Box` quietly meant the constructor.
# This case is what holds the name walk to the list that still has the templates in it.
reject a-generic-function-and-a-struct-share-a-name E381 'once as a struct, once as a function' at=5:1 <<'EOF'
struct Box {
	pub v: int
}

fn Box[T](x: T) -> T {
	return x
}

fn main() {
	print "ok"
}
EOF

# and the pairings a MODULE CONSTANT makes. The constant-versus-function pair was already
# refused; the constant-versus-type pair is what the separate check could not see, and it
# is the case that would go quiet again the day this rule is asked in two places. Every one
# of them is reported at the CONSTANT, which is where the old check reported it from.
reject a-struct-and-a-module-constant-share-a-name E381 'once as a struct, once as a module constant' at=1:1 <<'EOF'
const A := 1

struct A {
	pub v: int
}

fn main() {
	print "ok"
}
EOF

reject const-taking-a-function-name E381 'once as a function, once as a module constant' at=1:1 <<'EOF'
const f := 1

fn f() {
	print "x"
}

fn main() {
	print "ok"
}
EOF

reject function-taking-a-const-name E381 'once as a function, once as a module constant' at=5:1 <<'EOF'
fn f() {
	print "x"
}

const f := 1

fn main() {
	print "ok"
}
EOF

# and one level down, where the same question has the same answer and three more ways of
# going wrong: a repeated FIELD and a repeated PARAMETER both reached cc as a C redefinition
# against .zerg-cache, and a repeated VARIANT was accepted — the second took the next
# discriminant, so it was unreachable, and a `match` naming the first was "exhaustive" over
# an enum that had two.

reject a-field-declared-twice E453 'declares a field named `v` twice' no-place seed-gap <<'EOF'
struct A {
	pub v: int
	pub v: str
}

fn main() {
	print("x")
}
EOF

reject a-variant-declared-twice E453 'declares a variant named `X` twice' no-place seed-gap <<'EOF'
enum E {
	X
	X
}

fn main() {
	print(f"{int(E.X)}")
}
EOF

reject a-parameter-declared-twice E365 <<'EOF'
fn f(a: int, a: int) -> int {
	return a
}

fn main() {
	print(f"{f(1, 2)}")
}
EOF

# --- a tuple type has two elements ------------------------------------------------
#
# GRAMMAR#tuple-type says two or more. `(T)` is a grouped type everywhere else in the language and
# `()` is not a type at all — a function with no result returns nothing, which `-> ` says by
# being absent. The whole tuple TYPE was unparsed until this branch, so every position that
# wanted one reported whatever token came next instead: five positions, five messages.

reject a-one-element-tuple-type E246 no-place <<'EOF'
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

reject reserved-word-as-a-binding E257 no-place <<'EOF'
fn main() {
	this := 1
	print(f"{this}")
}
EOF

reject reserved-word-as-a-loop-binding E245 'cannot name a loop binding' no-place <<'EOF'
fn main() {
	xs: list[int] = [1, 2]
	for this in xs {
		print(f"{this}")
	}
}
EOF

reject reserved-word-as-an-if-let-binding E245 'cannot name an `if let` binding' no-place <<'EOF'
fn main() {
	o: int? = 5
	if this := o {
		print(f"{this}")
	}
	print("x")
}
EOF

reject statement-keyword-as-a-binding E257 no-place <<'EOF'
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

reject bind-a-byte-list-to-an-int-list E335 'cannot bind list[byte] to a list[int] binding' <<'EOF'
fn main() {
	ys: list[int] = list[byte]("Hi")
	print(f"{ys[0]}")
}
EOF

# `take(1000)` on a `byte` parameter is the CONSTANT layer of Into: `int -> byte` is a real
# conversion, so it type-checks, and then the compiler evaluates it and reports the value
# rather than emitting the truncation cc used to complain about.
reject narrow-an-int-to-a-byte E330 '`1000` is not a value a byte holds' <<'EOF'
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

reject str-into-an-optional-list-element E338 'element 1 of this list literal is int' <<'EOF'
fn main() {
	xs: list[int?] = ["a"]
	print xs.len()
}
EOF

reject str-into-an-optional-struct-field E338 'field 1 of `Box` is int' <<'EOF'
struct Box {
	pub v: int?
}

fn main() {
	b := Box("hi")
	print "ok"
}
EOF

reject str-assigned-into-an-optional E338 '`x` is int, and this gives str' <<'EOF'
fn main() {
	mut x: int? = 1
	x = "hi"
	print x!
}
EOF

reject str-into-a-result-left E338 "this function's answer is int, and this gives str" <<'EOF'
fn f() -> Result[int] {
	return Either.Left("hi")
}

fn main() {
	print f()!
}
EOF

reject int-into-an-either-right E338 "this function's answer is str, and this gives int" <<'EOF'
fn f() -> Either[int, str] {
	return Either.Right(7)
}

fn main() {
	print "ok"
}
EOF

# the two that RAN. An int64_t into a double field and an int64_t into a uint8_t are both
# legal C, so neither cc nor any gate had anything to say — the program simply answered
# something other than what it was written to answer.

# --- a channel does not carry an optional ------------------------------------------
#
# `chan[T?]` is refused by the LANGUAGE (GRAMMAR#chan-type, docs/code/coroutine.md), and
# the reason is the one fact the whole receive story rests on: a receive answers `T?`, and
# a clean close IS nil, so on a channel of optionals "a nil was sent" and "the stream is
# over" are the same observation and no operator can tell them apart.
#
# It used to be checked at the CONSTRUCTOR alone — `chan[int?](1)`, one call in the
# emitter — so every other position carrying the same type was accepted in silence: a
# parameter, a result, a struct field, a typed binding, a typedef, a chan nested inside
# another type. The rule now sits where a channel's element type is READ, which is one
# place per compiler, so each case below is the same rule arriving through a position
# rather than a rule written out per position. That is what makes this list a list of
# POSITIONS and not a list of copies.
#
# The one case that does not come through the parser is the last: a template's `chan[T]`
# becomes a `chan[int?]` by SUBSTITUTION, with nothing written in the source to point at.

reject a-channel-of-optionals-constructed E404 <<'EOF'
fn main() {
	ch := chan[int?](1)
	print 1
}
EOF

reject a-channel-of-optionals-as-a-parameter E404 <<'EOF'
fn f(ch: <-chan[int?]) {
	print 1
}

fn main() {
	print 2
}
EOF

reject a-channel-of-optionals-as-a-result E404 <<'EOF'
fn f() -> chan[int?] {
	return chan[int](1)
}

fn main() {
	print 1
}
EOF

# the place is PINNED on this one, because a struct field is the position that carries no
# statement and no declaration position of its own: the finding has to come from the type
# as it is written, and nothing else here would notice it drifting to the file's first line.
reject a-channel-of-optionals-as-a-field E404 at=2:10 <<'EOF'
struct Box {
	pub ch: chan[int?]
}

fn main() {
	print 1
}
EOF

reject a-channel-of-optionals-in-a-typed-binding E404 <<'EOF'
fn main() {
	ch: chan[int?] = chan[int](1)
	print 1
}
EOF

reject a-channel-of-optionals-under-a-typedef E404 <<'EOF'
type C = chan[int?]

fn main() {
	print 1
}
EOF

reject a-channel-of-optionals-inside-a-list E404 <<'EOF'
struct Box {
	pub xs: list[chan[int?]]
}

fn main() {
	print 1
}
EOF

# by SUBSTITUTION: nothing in this source spells `chan[int?]`, and the specialization of
# `mk` for `int?` is one. The parser never sees that type, so the rule has to be on the
# substitution too — and there is no source position to report, because the type is not
# written anywhere.
reject a-channel-of-optionals-by-substitution E404 no-place <<'EOF'
fn mk[T](v: T) {
	ch := chan[T](1)
	ch <- v
	print 1
}

fn main() {
	mut x: int? = nil
	mk(x)
}
EOF

# by SPEC SUBSTITUTION, which was the FOURTH door and the one that proved a list of doors
# is the wrong shape for this rule. A spec's required signature writes `chan[K]`, and
# `impl Ix[int?]` says `K` is `int?` — so the type the impl is being held to is a
# `chan[int?]`. The checker rebuilt it by hand and asked nothing, so this position quietly
# accepted what the parser and the specializer both refuse, and named the type back in a
# mismatch message as though it were a type. The rule sits on CONSTRUCTION now (ty_chan),
# which is the one door that cannot be walked around.
#
# No place, for the same reason as the case above: nothing in this source spells the type.
reject a-channel-of-optionals-by-spec-substitution E404 no-place <<'EOF'
spec Ix[K] {
	fn c() -> chan[K]
}

struct A {
	pub v: int
}

impl Ix[int?] for A {
	fn c() -> int {
		return this.v
	}
}

fn main() {
	print(A(1).v)
}
EOF

# --- and a channel, a map and a carrier are COMPARED like everything else ------------
#
# `chk_fits` opened with one line — "a carrier, a channel or a map is never a mismatch" —
# and that line answered yes for every pairing whose WANTED side was one of the three,
# whatever stood on the other. It was written to protect three real reshapes: a bare value
# wrapping into a `T?` or a `Result[T]`, a bidirectional channel narrowing to one end, and
# a map literal taking its key and value types from the declaration. All three are still
# reshapes, and each now has its own clause — what they never licensed is the five below.
#
# The seed refuses every one of them, and has all along: this is a rule `zerg` lost on the
# way to self-hosting, which `make oracle` cannot see because oracle compares the two
# compilers on programs the seed ACCEPTS.

reject a-channel-of-another-element E335 'cannot bind chan[int] to a chan[str] binding' <<'EOF'
fn main() {
	ch: chan[str] = chan[int](1)
	print 1
}
EOF

# it compiled to `zrt_chan *zg_ch = 7;`, which cc reported as an integer-to-pointer
# conversion against a line nobody wrote
reject an-int-into-a-channel E335 'cannot bind int to a chan[int] binding' <<'EOF'
fn main() {
	ch: chan[int] = 7
	print 1
}
EOF

reject an-int-into-a-map E335 'cannot bind int to a map[str, int] binding' <<'EOF'
fn main() {
	m: map[str, int] = 7
	print 1
}
EOF

# a map against a map is the pairing the literal escape does NOT cover: both sides are
# maps and neither is a literal, so nothing here is taking its shape from the declaration
reject a-map-of-another-value-type E335 'cannot bind map[str, int] to a map[str, str] binding' <<'EOF'
fn main() {
	a: map[str, int] = {"a": 1}
	b: map[str, str] = a
	print b.len()
}
EOF

# A CARRIER PASSES THROUGH OR IS INJECTED, and a carrier of another shape does neither: an
# `int?` cannot pass through into a `str?` — they are different C types — and cannot be the
# `str` payload either. It reached cc as an incompatible-pointer argument to `zrt_str_retain`.
reject an-optional-of-another-element E335 'cannot bind int? to a str? binding' <<'EOF'
fn main() {
	y: int? = 5
	x: str? = y
	print y ?? 0
}
EOF

# --- a channel operation needs a channel --------------------------------------------
#
# `<-x`, `x <- v`, `close(x)` and a `select` arm all name an END of a channel, and the
# question of whether `x` IS one had an answer at exactly one of those six sites: `close`.
# Everywhere else the emitter asked for the element type of whatever it had been given and
# took the `TUnknown` it got back for a type, so a receive on a non-channel came out as
# `TOpt(TUnknown)` — an optional of nothing, printed `?` — and the program went on being
# compiled around it. What the reader was told about afterwards was the `?`: `` `??` on a
# ? ``, `E434 … over a ?`, sentences about a type nothing in the source had written. Where
# the walk did not touch the broken type at all, the C came out with a carrier struct in a
# `zrt_chan *` slot and cc rejected generated code nobody wrote.
#
# The rule is one sentence in `c_chan_op_check` now, which is the place all six operations
# already went through to be told whether a DIRECTION permits them; "it is not a channel
# end at all" is the first thing that same question asks. So the cases below are again a
# list of POSITIONS, not a list of copies.
#
# The nested receive is the case that motivated the rest, and it is worth reading twice:
# `<-(<-cc)` is what the self-recursive GRAMMAR#recv-base derives, and it is ill-typed
# for a reason that has nothing to do with nesting — a receive answers `T?` (group 9), and
# `chan[int]?` is not a channel. The well-formed spelling unwraps in between, `<-((<-cc)!)`,
# and it lives in the corpus as `chan_of_chan`.

reject a-receive-of-a-receive E478 "chan[int]? is not one" at=6:2 <<'EOF'
fn main() {
	cc := chan[chan[int]](1)
	inner := chan[int](1)
	inner <- 7
	cc <- inner
	x := <-(<-cc)
	print x ?? -1
}
EOF

reject a-receive-on-an-int E478 "and int is not one" <<'EOF'
fn main() {
	n := 3
	x := <-n
	print x ?? -1
}
EOF

reject a-send-on-an-int E478 "and int is not one" <<'EOF'
fn main() {
	mut n := 3
	n <- 7
	print n
}
EOF

reject a-close-on-an-int E478 "and int is not one" <<'EOF'
fn main() {
	n := 3
	close(n)
	print n
}
EOF

reject a-select-receive-arm-on-an-int E478 "and int is not one" <<'EOF'
fn main() {
	n := 3
	select {
		v := <-n => print v
	}
}
EOF

reject a-select-send-arm-on-an-int E478 "and int is not one" <<'EOF'
fn main() {
	n := 3
	select {
		n <- 7 => print "sent"
	}
}
EOF

# --- what only the driver can reject ------------------------------------------------
#
# A program build needs an entry point. `program ::= stmt-list` makes a main-less source
# grammatical — script mode is real, and a module is exactly a source with no `fn main` —
# so no parser or checker rule can own this: what is ill-formed is the BUILD, an entry
# file handed to `--emit bin` that defines no entry (docs/runtime/package.md). Before the
# driver said so, the answer was the LINKER's `undefined symbol _main` against an object
# nobody wrote; the seed has enforced this rule all along, which is why no marker excuses
# it below.

reject program-without-fn-main E501 at=1:1 <<'EOF'
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

reject display-override-with-arguments E359 seed-gap <<'EOF'
struct Point {
	pub x: int
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

reject display-override-wrong-answer E361 seed-gap <<'EOF'
struct Point {
	pub x: int
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
		reject "deep-$shape-in-a-body" E244 seed-gap

	printf 'fn deep[T](v: T) -> int {\n\treturn %s\n}\n\nfn main() {\n\tprint deep(1)\n}\n' "$e" |
		reject "deep-$shape-in-an-instantiated-generic" E244 seed-gap

	printf 'K := %s\n\nfn main() {\n\tprint K\n}\n' "$e" |
		reject "deep-$shape-in-a-module-constant" E244 seed-gap

	printf 'fn f(a: int = %s) -> int {\n\treturn a\n}\n\nfn main() {\n\tprint f()\n}\n' "$e" |
		reject "deep-$shape-in-a-default-parameter" E244 seed-gap
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
	reject deep-composed-by-a-default-splice E454 seed-gap

# --- report ------------------------------------------------------------------------

# --- the bad paths that reached cc ------------------------------------------------------
#
# Each of these compiled to C and was reported by the C compiler, against a file under
# .zerg-cache that nobody wrote — the standing rule ("lowered correctly, or refused by name",
# docs/conformance.md) breached six times in one sweep. They are written here BEFORE the rules
# that turn them away, so the sentence each one is owed is decided by what a reader needs and
# not by whatever the fix happened to produce.

reject add-two-lists E345 'operator `+` takes numeric operands' <<'EOF'
fn main() {
	xs := [1]
	ys := xs + [2]
	print ys.len()
}
EOF

reject subtract-two-lists E345 'operator `-` takes numeric operands' <<'EOF'
fn main() {
	xs := [1]
	ys := xs - [2]
	print ys.len()
}
EOF

reject add-two-maps E345 'operator `+` takes numeric operands' <<'EOF'
fn main() {
	m := {"a": 1}
	n := {"b": 2}
	o := m + n
	print o.len()
}
EOF

# THE SAME HOLE IN FOUR OF THE FIVE OPERATOR FAMILIES. Each family asks "is this a scalar of
# the wrong kind", which answers NO for a value that is not a scalar at all — so every
# aggregate walked past every one of them. Only ORDER had a rule of its own (`Ord`).

reject bitwise-on-two-lists E344 <<'EOF'
fn main() {
	xs := [1]
	ys := xs & [2]
	print ys.len()
}
EOF

reject logical-on-two-lists E343 <<'EOF'
fn main() {
	xs := [1]
	print str(xs and [2])
}
EOF

reject negate-a-list E351 <<'EOF'
fn main() {
	xs := [1]
	ys := -xs
	print ys.len()
}
EOF

reject complement-a-list E352 <<'EOF'
fn main() {
	xs := [1]
	ys := ~xs
	print ys.len()
}
EOF

reject index-a-map-with-the-wrong-key E338 'a key of this map[str, int] is str, and this gives int' <<'EOF'
fn main() {
	m := {"a": 1}
	print m[1]
}
EOF

reject call-a-binding-that-shadows-a-function E369 <<'EOF'
fn f() -> int {
	return 1
}

fn main() {
	f := 2
	print f()
}
EOF

reject field-on-a-non-struct E376 <<'EOF'
fn main() {
	n := 5
	print n.a
}
EOF

# the OTHER half of the same rule, and it used to be a third of a file away with a code of
# its own: a struct that lacks the field, rather than a value that has no fields at all. It
# lived among the not-yet-built forms because it raised without a place, which is what a
# form this compiler has not reached looks like — and it is neither. It is the language's
# answer, and it is permanent.
reject field-the-struct-does-not-have E376 <<'EOF'
struct P {
	pub n: int
}

fn main() {
	p := P(1)
	print p.z
}
EOF

# --- bad paths, sweep two: tuples, slices, iteration ------------------------------------

reject tuple-index-past-its-arity E378 <<'EOF'
fn main() {
	t := (1, 2)
	print t.5
}
EOF

reject tuple-index-on-a-non-tuple E377 <<'EOF'
fn main() {
	n := 5
	print n.0
}
EOF

# SILENT: a str bound on a slice was accepted and lowered, so the range walked from a pointer
reject slice-with-a-str-bound E374 <<'EOF'
fn main() {
	xs := [1, 2]
	ys := xs["a"..1]
	print ys.len()
}
EOF

# The message named the LOOP VARIABLE as undefined, which is the consequence: the loop gave it
# no type because the thing being walked is not walkable, and `x` was blamed for it.
reject for-over-a-non-iterable E379 <<'EOF'
fn main() {
	n := 5
	for x in n {
		print x
	}
}
EOF

# --- bad paths, sweep three: a crash and a silent import --------------------------------

# THE COMPILER SEGFAULTED on this one-line program. The cycle detector catches an indirect
# cycle (`A` holding `B` holding `A`) and one through a carrier (`p: P?`), and skipped the
# simplest case of all — a field of the struct's own type — so the copy helper recursed until
# the stack ran out. A compiler that dies says nothing at all, about anything.
reject struct-holding-itself-by-value E452 no-place <<'EOF'
struct P {
	pub p: P
}

fn main() {
	print 1
}
EOF

# SILENT: the import resolved to nothing and the program ran. Using the module then reported
# "the method `thing` on a ?", which names neither the module nor the import.
reject import-a-module-that-does-not-exist E502 no-place <<'EOF'
import "nope"

fn main() {
	print 1
}
EOF

# --- bad paths, sweep four: what `raise` takes ------------------------------------------
#
# `raise e` carries an `Err`, and `raise "…"` is the shorthand that builds one from a message
# (docs/code/errors.md). Anything else was handed to the runtime's unwind as though it were an
# Err — `raise 5` reached cc as an incompatible-type argument, and a struct the same way.

reject raise-an-int E380 <<'EOF'
fn main() {
	raise 5
}
EOF

reject raise-a-struct E380 <<'EOF'
struct P {
	pub a: int
}

fn main() {
	raise P(1)
}
EOF

# --- bad paths, sweep five: a constant known to fail ------------------------------------
#
# docs/core/types.md: "A conversion the compiler can carry out is carried out. `byte(300)` is
# well-formed — and then fails as a CONSTANT: the value is known, the conversion is known to
# raise, and it is reported at compile time rather than left to run." That was implemented for
# a literal ADOPTING a type (`b: byte = 300`) and not for the WRITTEN conversion beside it,
# which is the spelling the sentence uses.

reject written-byte-conversion-out-of-range E330 'is not a value a byte holds' <<'EOF'
fn main() {
	print int(byte(300))
}
EOF

reject written-rune-conversion-out-of-range E330 'is not a value a rune holds' <<'EOF'
fn main() {
	print int(rune(1114112))
}
EOF

reject written-uint-conversion-negative E330 'is not a value a uint holds' <<'EOF'
fn main() {
	print int(uint(-1))
}
EOF

# --- bad paths, sweep six: what an assignment may be written to -------------------------
#
# An assignment needs a PLACE — a name, a field, an index. A call's result and a literal are
# values with nowhere to live, and both were rendered to the left of a C `=`.

reject assign-to-a-call E302 <<'EOF'
fn f() -> int {
	return 1
}

fn main() {
	f() = 2
	print 1
}
EOF

reject assign-to-a-literal E302 <<'EOF'
fn main() {
	5 = 2
	print 1
}
EOF

# --- a list index is a typed position too -----------------------------------------------
#
# The map's KEY gained its rule and the list's INDEX did not, so the subscript accepted
# anything at all: `xs["a"]` reached cc, and `xs[1.5]` and `xs[true]` COMPILED AND RAN — the
# float silently truncated to 1 by a C conversion cc only warned about, the bool read as 1.

reject index-a-list-with-a-str E375 <<'EOF'
fn main() {
	xs := [1, 2]
	print xs["a"]
}
EOF

reject index-a-list-with-a-float E375 <<'EOF'
fn main() {
	xs := [1, 2]
	print xs[1.5]
}
EOF

reject index-a-list-with-a-bool E375 <<'EOF'
fn main() {
	xs := [1, 2]
	print xs[true]
}
EOF

reject index-assign-a-list-with-a-str E375 <<'EOF'
fn main() {
	mut xs := [1, 2]
	xs["a"] = 5
	print xs.len()
}
EOF

# An ANNOTATION HAS TO NAME A TYPE. A parameter's is checked where it is resolved and a
# `type X = Y`'s underlying name too; a local's was the one that did not have to, and reported
# "cannot bind int to a Nope binding" — a sentence about the VALUE that treats an unknown name
# as a type the value failed to be.
reject local-annotation-names-no-type E334 <<'EOF'
fn main() {
	x: Nope = 5
	print 1
}
EOF

# --- one step past each edge ------------------------------------------------------------
#
# The refusals above use comfortable numbers — 300 for a byte, 1000 for a narrowing — and a
# comfortable number cannot tell `<=` from `<`. These are the FIRST value each rule turns away,
# paired one for one with the last it admits in test-data/codegen/type_boundaries.zg. A range
# rule's defects live entirely at its ends.

reject byte-one-past-the-last E330 'is not a value a byte holds' <<'EOF'
fn main() {
	x: byte = 256
	print int(x)
}
EOF

reject rune-one-past-the-last E330 'is not a value a rune holds' <<'EOF'
fn main() {
	r: rune = 1114112
	print int(r)
}
EOF

reject uint-one-past-the-last E319 <<'EOF'
fn main() {
	u: uint = 18446744073709551616
	print u
}
EOF

# AND THE FOLD'S OWN EDGE, which has two: `200 + 55` is `255` and adopts, `200 + 56` is `256`
# and does not — the answer measured after the operands, both of which fit either way.
reject fold-one-past-the-last E330 'is not a value a byte holds' <<'EOF'
fn main() {
	x: byte = 200 + 56
	print int(x)
}
EOF

# --- the rules no case had reached ------------------------------------------------------
#
# THE CODES FOUND THESE. Giving every rule an identity made it possible to ask a question no
# reading of this file could answer — which rules the compiler reports and no case here
# provokes — and `scripts/error-codes-check.sh` answered it with thirteen. Each was a rule
# written, shipped and never once made to fire: not a gap in the language, a gap in the
# evidence that the language does what it says.
#
# They are gathered here rather than filed beside their neighbours because what they have in
# common is how they were found, and that is worth being able to see.

reject a-list-conversion-of-two-values E261 no-place <<'EOF'
fn main() {
	b := list[byte]("a", "b")
	print b.len()
}
EOF

reject a-module-private-name E301 <<'EOF'
import "std/strings"

fn main() {
	print strings.pad_count("a", 3, " ")
}
EOF

# THE SAME NAME, ONE KEYWORD EARLIER. `spawn` and `defer` resolve their callee down a path
# of their own (c_callee_raw), and that path asked neither of the two rules the ordinary
# call asks about the function it found — so a module-private name and an `unsafe { … }`
# group's function were both reachable by writing `spawn` in front of the call that is
# refused without it. Three cases, because they are the two shapes the path resolves (a
# plain name and a namespaced one) and the two rules it skipped.
reject a-module-private-name-spawned E301 <<'EOF'
import "util/text"

fn main() {
	spawn text.hidden("a")
	print text.shout("b")
}
--- util/text/text.zg
fn hidden(s: str) {
	print s
}

pub fn shout(s: str) -> str {
	return s + "!"
}
EOF

reject a-module-private-name-deferred E301 <<'EOF'
import "util/text"

fn main() {
	defer text.hidden("a")
	print text.shout("b")
}
--- util/text/text.zg
fn hidden(s: str) {
	print s
}

pub fn shout(s: str) -> str {
	return s + "!"
}
EOF

reject unsafe-group-fn-spawned E387 'this `spawn` is in safe code' <<'EOF'
unsafe {
	fn poke() {
		print 7
	}
}

fn main() {
	spawn poke()
	print 1
}
EOF

reject assign-to-the-receiver E306 <<'EOF'
struct P {
	pub x: int
}
impl P {
	fn set() {
		this = P(1)
	}
}
fn main() {
	p := P(0)
	p.set()
}
EOF

reject a-spec-extending-no-spec E316 <<'EOF'
spec A: Nope {
	fn f()
}
struct P {
	pub x: int
}
impl A for P {
	fn f() {
		print this.x
	}
}
fn main() {
	p := P(1)
	p.f()
}
EOF

reject an-if-expression-with-two-types E321 <<'EOF'
fn main() {
	x := if true { 1 } else { "s" }
	print x
}
EOF

# the borrow reaches `this` THROUGH a field, so the method mutates its receiver without
# saying `mut fn` — the half of the rule that is not about the argument at all.
reject a-borrow-of-a-field-of-an-immutable-receiver E324 <<'EOF'
struct P {
	pub x: int
}
fn bump(mut &n: int) {
	n = n + 1
}
impl P {
	fn go() {
		bump(this.x)
	}
}
fn main() {
	p := P(1)
	p.go()
}
EOF

# the seed RAISES this one at run time, where `zerg` reads the constant and answers now.
reject divide-by-a-constant-zero E331 seed-gap <<'EOF'
fn main() {
	x := 1 / 0
	print x
}
EOF

reject a-fold-past-int-measured-against-a-byte E332 <<'EOF'
fn main() {
	x: byte = 9223372036854775807 * 2
	print int(x)
}
EOF

reject a-binding-of-nil E336 <<'EOF'
fn main() {
	x := nil
	print 1
}
EOF

reject an-if-on-an-optional E354 <<'EOF'
fn main() {
	x: int? = 1
	if x {
		print 1
	}
}
EOF

reject a-display-that-mutates E360 <<'EOF'
struct P {
	pub x: int
}
impl P {
	mut fn display() -> str {
		return "p"
	}
}
fn main() {
	p := P(1)
	print p.display()
}
EOF

reject a-struct-literal-missing-a-field E370 <<'EOF'
struct P {
	pub a: int
	pub b: int
}
fn main() {
	p := P(1)
	print p.a
}
EOF

# A DEFAULT MAKES ONE FIELD OPTIONAL, NOT THE ONES BEFORE IT. Backfilling runs from the end
# of the written arguments forward, so a construction that stops short of a field with no
# default is still short — the same rule a `fn` parameter default follows, and the one that
# would go quiet if a defaulted field anywhere in the type were read as "this may be empty".
reject a-required-field-before-a-defaulted-one E370 '`Box` needs a value for field 1' <<'EOF'
struct Box {
	pub w: int
	pub h: int = 4
}

fn main() {
	b := Box()
	print b.h
}
EOF

# --- a private field must carry a default -----------------------------------------------
#
# GRAMMAR's FIELD VISIBILITY & DEFAULTS note: a non-`pub` field is module-private, and MUST
# carry a default. The field-wise `T(...)` constructor is public and there are no zero
# values, so a field with no default is one every construction has to supply — and outside
# the module a private field cannot even be read, which makes the type unbuildable from
# where it is used.
#
# The rule was UNENFORCEABLE until field defaults existed, so the compiler accepted this and
# the MUST was dead in both directions: no program could obey it, and none was asked to.
reject a-private-field-with-no-default E482 '`m` of `Q`' <<'EOF'
struct Q {
	pub n: int
	m: int
}

fn main() {
	q := Q(9, 4)
	print q.m
}
EOF

# A `T?` IS THE EXCEPTION THE NOTE NAMES — its implicit default is `nil` — so the rule has to
# be written against a field that is NOT one, and this pins that it still fires when the
# struct has an optional beside it.
reject a-private-field-beside-an-optional E482 '`m` of `R`' <<'EOF'
struct R {
	pub n: int
	o: int?
	m: str
}

fn main() {
	r := R(9, 1, "x")
	print r.m
}
EOF

reject an-undefined-name E372 <<'EOF'
fn main() {
	print nosuchname
}
EOF

# THE STR BRIDGES UNDER THEIR OWN NAMES. `bytearray` is `list[byte]` and `runearray` is
# `list[rune]`, so each converts exactly one value — a name that IS a type is a conversion,
# not a constructor, and `[]` is what builds an empty list.
reject a-bytearray-of-nothing E272 no-place <<'EOF'
fn main() {
	b := bytearray()
	print b.len()
}
EOF

reject a-bytearray-of-two-values E273 no-place <<'EOF'
fn main() {
	b := bytearray("a", "b")
	print b.len()
}
EOF

# A VARIANT PATTERN WRITTEN WITHOUT ITS ENUM, which is the mistake the pattern rule is really
# about — the author meant two variants and wrote two BARE names, and a bare name in pattern
# position is always a fresh binding (GRAMMAR#pattern). So `Red` binds the subject and covers
# everything, and `Green` below it can never run.
#
# It is E458 rather than a rule of its own, and that is the point: this is refused for what
# the arms DO and not for how the first letter is typed. The rule that used to stand here
# read the capital instead, so `n := 3; match n { 1 => …  Zzz => … }` was refused with
# "`Zzz` is a variant of some enum" in a program that declared no enum at all.
reject a-bare-name-pattern-covers-the-arms-below E458 no-place <<'EOF'
enum Color {
	Red
	Green
}

fn main() {
	c := Color.Red
	print match c {
		Red   => "r"
		Green => "g"
	}
}
EOF

# THE VALUE POSITION, the other half of the same rule. A bare variant here is not a silent
# meaning change the way a pattern is — it simply resolves, which is the problem: two enums
# that both declare a `Red` cannot both have it, and an enum that GAINS one takes a name the
# program was already using.
reject a-bare-variant-value E383 seed-gap <<'EOF'
enum Color {
	Red
	Green
}

fn main() {
	c := Red
	print 1
}
EOF

# AND THE BUILT-IN IS NOT AN EXCEPTION. `Left` and `Right` are variants of `Either`, and
# they were the last two names in this language that read as themselves — for a mechanical
# reason rather than a decided one: they are context-typed, so this compiler matched them by
# name instead of resolving them through their type.
# A CALL THROUGH A FUNCTION VALUE COUNTS ITS ARGUMENTS. chk_call_arity's table is keyed by
# NAME, and a value has none — its comment said so and handed the case to emit, which never
# took it, so both spellings of the wrong count were rendered into the cast and reported by
# cc. `reject-fuzz` found it the first time the corpus held a program that calls one.
reject too-many-arguments-through-a-fn-value E386 <<'EOF'
fn apply(f: fn (int) -> int, v: int) -> int {
	return f(v, 1)
}

fn main() {
	print apply(fn (x: int) -> int { return x }, 1)
}
EOF

reject too-few-arguments-through-a-fn-value E386 <<'EOF'
fn apply(f: fn (int) -> int) -> int {
	return f()
}

fn main() {
	print apply(fn (x: int) -> int { return x })
}
EOF

# CARVE-OUT (c)'s second sentence. A closure's omitted parameter types come from the
# function type it is checked against, so a closure that meets no such position has nowhere
# to take them from — and the specification says that is an error rather than a guess. It
# was a parser NotImplemented (E209) while the carve-out was unbuilt; now the form is read,
# and this is what is left of it: a rule, with a place, at the closure.
reject a-closure-with-no-position-to-type-it E385 <<'EOF'
fn main() {
	f := fn (x) { return x + 1 }
	print f(1)
}
EOF

reject a-bare-either-side E384 <<'EOF'
fn f(n: int) -> Either[int, str] {
	return Right("negative") if n < 0

	return Either.Left(n)
}

fn main() {
	print 1
}
EOF

# the FIFTH sentence of `c_eq_says`, and the one its own comment did not know it had: the
# helper said "three different sentences" while answering five, and the two it had not
# counted were the two with no code. A comment cannot count a function's branches; a gate
# that asks which codes nothing provokes can.
reject equality-on-a-carrier E473 <<'EOF'
fn main() {
	a: Either[int, str] = Either.Left(1)
	b: Either[int, str] = Either.Left(2)
	if a == b {
		print 1
	}
}
EOF

# --- a diverge on a `??`'s right takes no postfix guard ---------------------------------
#
# GRAMMAR#coalesce-rhs takes the bare DIVERGE, and GRAMMAR#raise-stmt is where the postfix
# `if` guard lives: "'p ?? raise Err' takes no trailing 'if': the guard would read as the
# coalesce's, not the raise's."
#
# The parser read it anyway, and what came out was UNDEFINED BEHAVIOUR FROM SAFE CODE —
# the only such case the audit that found this turned up. The emitter lowers a `??` whose
# right side diverges on the assumption that the diverge is unconditional, so it writes no
# `else` for the temp the coalesce answers with: `q := p ?? raise ValueError("x") if false`
# compiled clean and printed whatever the stack happened to hold. Every one of the five
# shapes below did.
#
# Five cases and not one, because the guard is folded in at three different places —
# parse_jump_guard for `break`/`continue`/`raise`, and two branches of parse_return for a
# `return` with a value and one without — and a rule written against only the first would
# have left the other two silently compiling.

for kw in break continue; do
	reject "a-coalesce-${kw}-with-a-guard" E284 "a \`??\` right-hand \`${kw}\` takes no trailing \`if\`" <<EOF
fn main() {
	mut total := 0
	for i in 0..3 {
		p: int? = nil
		v := p ?? ${kw} if false
		total = total + v
	}
	print total
}
EOF
done

reject a-coalesce-raise-with-a-guard E284 'a `??` right-hand `raise` takes no trailing `if`' <<'EOF'
fn get() -> int? {
	return nil
}

fn main() {
	p := get()
	q := p ?? raise ValueError("x") if false
	print q
}
EOF

reject a-coalesce-return-with-a-guard E284 'a `??` right-hand `return` takes no trailing `if`' <<'EOF'
fn f() -> int {
	p: int? = nil
	q := p ?? return 7 if false
	return q
}

fn main() {
	print f()
}
EOF

# the same rule reached through parse_return's OTHER branch: a bare `return` under a guard
# is desugared into `if c { return }`, so it comes back as an `if` statement where the one
# above comes back as a conditional return. Both are the guard, and neither is the form.
reject a-coalesce-bare-return-with-a-guard E284 'a `??` right-hand `return` takes no trailing `if`' <<'EOF'
fn f() {
	p: int? = nil
	q := p ?? return if false
	print q
}

fn main() {
	f()
}
EOF

# --- a compile-time position takes a CONST-EXPR, and only a const-expr -------------------
#
# GRAMMAR group 7: a const-expr is an ordinary expression "RESTRICTED to what the compiler
# folds with no evaluation engine: literals, other const-foldable names, an enum's
# discriminant, and the arithmetic / bitwise / comparison / logical operators. There are NO
# function calls and no runtime values". The two positions below are the ones it names by
# hand — an enum discriminant and a fill count — and each is rejected two ways, because the
# two failures are different: a name whose binding is not constant, and a call, which no
# binding can make constant.
#
# A MATCH ARM'S RANGE BOUND is the third position, and it fails the same two ways. Its call
# form is turned away one step earlier than the other two: `range-bound ::= '-'? literal |
# identifier` derives no call at all, so `f()..300` is not a range arm and never reaches a
# fold — which is why that one is a parse rejection and its siblings are not.
#
# The fill count's runtime form is the one worth having a case for: the SEED used to accept
# it. It lowers `[v; N]` to a loop bounded by N, so `[0; n]` for an n read at run time built
# a list of whatever n happened to be — a form the language does not have, compiled in
# silence, and one the shipping compiler always refused.

reject a-discriminant-that-names-a-non-constant E474 'the discriminant of `E.A`' <<'EOF'
fn size() -> int {
	return 3
}

n := size()

enum E {
	A = n
	B
}

fn main() {
	print int(E.A)
}
EOF

reject a-discriminant-that-calls E474 'the discriminant of `E.B`' <<'EOF'
fn size() -> int {
	return 3
}

enum E {
	A
	B = size()
}

fn main() {
	print int(E.B)
}
EOF

reject a-fill-count-read-at-run-time E475 <<'EOF'
fn size() -> int {
	return 3
}

fn main() {
	n := size()
	xs := [0; n]
	print xs.len()
}
EOF

reject a-fill-count-that-calls E475 <<'EOF'
fn size() -> int {
	return 3
}

fn main() {
	xs := [0; size()]
	print xs.len()
}
EOF

# A COUNT IS A COUNT. The parser read one integer token here, so `-1` was refused as "not an
# integer literal" and the case never reached a rule; the fold reaches it, and a negative
# count that quietly built the empty list is not what `[0; -1]` asks for.
reject a-negative-fill-count E476 <<'EOF'
fn main() {
	xs := [0; -1]
	print xs.len()
}
EOF

reject a-range-bound-read-at-run-time E477 '`lo`' <<'EOF'
fn size() -> int {
	return 3
}

fn main() {
	lo := size()
	n := 5
	print match n {
		lo..10 => "in"
		_      => "out"
	}
}
EOF

# --- the forms GRAMMAR does not derive --------------------------------------------------
#
# A production is a contract about what the language HAS, and a parser that reads past it
# is one that silently invents. The three below are each a form `GRAMMAR` states in so many
# words is not there, and each one compiled and ran.
#
# GRAMMAR#tuple-lit is `'(' expr ',' expr ( ',' expr )* ')'` — two or more elements, with
# the prose beside it saying that "a single `( expr )` is just grouping". So `(1, )` is not
# a tuple; it built one, of arity one, whose `.0` read back the element.
reject a-one-tuple E288 <<'EOF'
fn main() {
	x := (1, )
	print x.0
}
EOF

# NO comma-separated list in GRAMMAR derives a trailing comma — not `tuple-lit`, not
# `list-lit`, not `map-lit`, not `arg-list` — and all four read one silently. `zerg fmt`
# already removed them, which is the tell: the toolchain knew the form was not canonical
# and the parser was the one place that did not.
#
# Four cases and not one, because the four are four separate loops: the tuple's is the
# comma loop in parse_primary, the list's is that primary's `[` branch, the map's is
# parse_map_lit, and the argument list's is parse_call_args. A rule written against one of
# them leaves the other three accepting.
reject a-trailing-comma-in-a-tuple E289 'the closing `)` of a tuple literal' <<'EOF'
fn main() {
	t := (1, 2,)
	print t.0
}
EOF

reject a-trailing-comma-in-a-list E289 'the closing `]` of a list literal' <<'EOF'
fn main() {
	xs := [1, 2, ]
	print xs.len()
}
EOF

reject a-trailing-comma-in-a-map E289 'the closing `}` of a map literal' <<'EOF'
fn main() {
	m := {"a": 1,}
	print m.len()
}
EOF

reject a-trailing-comma-in-an-argument-list E289 'the closing `)` of an argument list' <<'EOF'
fn add(a: int, b: int) -> int {
	return a + b
}

fn main() {
	print add(1, 2,)
}
EOF

# THE THREE LIST READERS THAT WERE STILL SPELLING THE RULE THEIR OWN WAY. Twelve readers
# were brought to "the comma is required between elements and absent before the closer" and
# these three were not, which is the N+1 that a rule stated per site always has: a spec's
# own type parameters, a function's, and a call's type arguments.
#
# The spec's list was the SILENT one — `spec Ix[K V]` read as two parameters and said
# nothing, so a correct `impl Ix[int]` was then told the spec is "parameterized by K, V"
# about a list nobody wrote. The other two were not silent, but the `]` below the loop was
# what complained, so a missing separator was reported as a missing bracket.
reject a-spec-type-parameter-list-without-a-comma E204 'expected `,`' no-place <<'EOF'
spec Ix[K V] {
	fn at(k: K) -> int
}

struct A {
	pub v: int
}

impl Ix[int, int] for A {
	fn at(k: int) -> int {
		return k
	}
}

fn main() {
	print(A(1).at(2))
}
EOF

reject a-trailing-comma-in-a-spec-type-parameter-list E289 "the closing \`]\` of a spec's type parameter list" <<'EOF'
spec Ix[K,] {
	fn at(k: K) -> int
}

struct A {
	pub v: int
}

impl Ix[int] for A {
	fn at(k: int) -> int {
		return k
	}
}

fn main() {
	print(A(1).at(2))
}
EOF

reject a-type-parameter-list-without-a-comma E204 'expected `,`' no-place <<'EOF'
fn f[T U](a: T, b: U) -> int {
	return 1
}

fn main() {
	print(f(1, 2))
}
EOF

reject a-trailing-comma-in-a-type-parameter-list E289 'the closing `]` of a type parameter list' <<'EOF'
fn f[T,](a: T) -> int {
	return 1
}

fn main() {
	print(f(1))
}
EOF

# The type ARGUMENT list, which is reached only through a built-in type's own arguments:
# a program's own `f[int, str](…)` is refused as a form before the list is read (E275), so
# `map[K, V]` is where this loop still runs.
reject a-trailing-comma-in-a-type-argument-list E289 'the closing `]` of a type argument list' <<'EOF'
fn main() {
	m := map[str, int,]()
	print m.len()
}
EOF

# GRAMMAR, group 7: "ANY '{'-opening expression — a block OR a map literal — at the start
# of an if/for/with/match head must be parenthesized", which follows from `{` being the
# block opener. The `if` head read the brace as a map literal and ran the program; the
# `for` head read it as the loop's BODY and reported a misparse from inside it — "a binding
# needs a name, and `"a"` is not one", about a key nobody wrote as a binding.
#
# Both say the same thing now, because it is the same rule: a head that begins with `{`
# needs parentheses. The `for` case is the one with something to decide, since `for { … }`
# with no head IS the infinite loop — what separates them is whether the brace is CONTINUED
# as an expression, which is the only reading under which it was a head at all.
reject a-brace-opening-if-head E290 'the start of an `if` head' <<'EOF'
fn main() {
	if {"a": 1}.len() == 1 {
		print "yes"
	}
}
EOF

# THE INNERMOST BINDING ANSWERS, and it answers even when the answer is "not a constant".
# A module binding named `lo` is a perfectly good bound everywhere else in the program; here a
# local of the same name shadows it, and a fold that reached past the shadow to the module
# would compile this arm against a number the reader cannot see on any line in scope.
reject a-range-bound-shadowed-by-a-local E477 '`lo`' <<'EOF'
fn size() -> int {
	return 3
}

lo := 1

fn main() {
	lo := size()
	n := 5
	print match n {
		lo..10 => "in"
		_      => "out"
	}
}
EOF

# A CALL IS NOT A BOUND, and it is refused by the parser rather than by the fold: the arm head
# `f()` is read as an ordinary pattern, and the `..` after it is then a token no arm can hold.
# The other two positions parse their const-expr and reject it on its value; this one has no
# const-expr to parse.
reject a-range-bound-that-calls E204 'found `..`' no-place <<'EOF'
fn lo() -> int {
	return 3
}

fn main() {
	n := 5
	print match n {
		lo()..10 => "in"
		_        => "out"
	}
}
EOF

reject a-brace-opening-for-head E290 'the start of a `for` head' <<'EOF'
fn main() {
	for {"a": 1}.len() == 1 {
		break
	}
}
EOF

# --- a block is an expression, and what is left over -------------------------------------
#
# A `{ … }` where an expression is wanted is a BLOCK (GRAMMAR, group 2, and `block` is in
# `primary`), and only a `:` makes it a map instead. Settling that in the parser leaves one
# ill-formed program behind that used to be reported as something else.
#
# THE MAP ENTRY WITHOUT A `:`. It used to be where the block/map ambiguity was
# reported — `a block used as an expression`, which is what a match arm's block body got —
# and with that ambiguity gone what reaches it is a genuine map literal missing a value.
reject a-map-entry-with-no-colon E479 'a map entry is `key: value`' at=2:30 <<'EOF'
fn main() {
	m: map[str, int] = {"a": 1, "b"}
	print m.len()
}
EOF

# --- a binding belongs to the construct that made it -------------------------------------
#
# docs/code/control-flow.md gives the if-let binding one scope: "`x` is not in scope in the
# `else`, nor after the `if`". Only the second half was enforced, and the first half's failure
# was worse than an ordinary miss — the name resolved to a C local declared inside the `if`
# body, so `else { print x }` escaped to cc as `use of undeclared identifier 'zg_x'`, an error
# against generated code nobody wrote, wearing the `.zg` filename the `#line` directives give
# it. Assertions 4 and 5 are what these cases are here for.
#
# A `select` arm's receive binding is the same rule at the other binding site, and it was the
# one construct that opened no scope at all: the name stayed live in every LATER arm and after
# the whole `select`. The later arm is the case worth having twice over, because it was not an
# escape but a silent ACCEPT — it compiled, and read the slot another arm's receive would fill.

reject an-if-let-bind-in-the-else E372 'undefined name `x`' at=9:3 <<'EOF'
fn find() -> int? {
	return nil
}

fn main() {
	if x := find() {
		print x
	} else {
		print x
	}
}
EOF

# the `else if` HEAD, which is a distinct place rather than a distinct rule: a chain is a
# nested if STATEMENT in the else BODY, so the one removal covers it — and the head is where
# the place assertion has teeth, since that statement is built outside parse_block.
reject an-if-let-bind-in-an-else-if-head E372 'undefined name `x`' at=8:9 <<'EOF'
fn a() -> int? {
	return 1
}

fn main() {
	if x := a() {
		print x
	} else if x > 0 {
		print 2
	} else {
		print 3
	}
}
EOF

# and the far end of a chain — past a second if-let, whose own binding must not be what puts
# the first one back within reach
reject an-if-let-bind-in-the-final-else-of-a-chain E372 'undefined name `x`' at=15:3 <<'EOF'
fn a() -> int? {
	return 1
}

fn b() -> int? {
	return 2
}

fn main() {
	if x := a() {
		print x
	} else if y := b() {
		print y
	} else {
		print x
	}
}
EOF

# the same name bound again INSIDE the outer else. Both bindings are out of scope at the read,
# and the inner one is what makes this more than a repeat: it took a C name of its own
# (`zg_x__1`) precisely because the outer one was still in the environment, so a fix that only
# hid the outer name would leave this reading the inner one.
reject an-if-let-bind-shadowed-in-a-nested-else E372 'undefined name `x`' at=16:4 <<'EOF'
fn a() -> int? {
	return 1
}

fn b() -> int? {
	return 2
}

fn main() {
	if x := a() {
		print x
	} else {
		if x := b() {
			print x
		} else {
			print x
		}
	}
}
EOF

reject a-select-arm-bind-after-the-select E372 'undefined name `v`' at=12:2 <<'EOF'
fn feed(ch: chan[int]) {
	ch <- 1
	close(ch)
}

fn main() {
	ch := chan[int]()
	spawn feed(ch)
	for select {
		v := <-ch => print v
	}
	print v
}
EOF

reject a-select-arm-bind-in-a-later-arm E372 'undefined name `v`' at=13:15 <<'EOF'
fn feed(ch: chan[int]) {
	ch <- 1
	close(ch)
}

fn main() {
	a := chan[int]()
	b := chan[int]()
	spawn feed(a)
	spawn feed(b)
	for select {
		v := <-a => print v
		w := <-b => print v
	}
}
EOF

if [ $fail -ne 0 ]; then
	echo "reject-check: $fail case(s) the compiler did not reject by itself"
	exit 1
fi
echo "reject-check: $pass ill-formed programs rejected by the compiler, none left to cc"
