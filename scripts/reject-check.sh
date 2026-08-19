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
# case still "fails", so no build gate can tell them apart. Hence seven assertions per
# case:
#
#   1. a non-zero exit
#   2. the code the rule is reported by, and a sentence too where one case of that code
#      has to be told from another
#   3. no mention of .zerg-cache
#   4. nothing shaped like a cc diagnostic (`<file>:LINE:COL: error:` opening a line)
#   5. no name the COMPILER minted — a message may quote what is in the file and nothing else
#   6. a `--> file:line:col` line, so the reader is told WHERE
#   7. the SEED refuses it too — unless the case says `seed-gap`, naming a rule the seed
#      does not enforce (its gaps are its own contract, in src/bootstrap/README.md)
#
# The fourth is not redundant with the third. A build given `-o` puts its intermediate C
# beside the output rather than in the cache, so a cc error can carry no cache path at all
# and still be a cc error — which is exactly the failure this gate exists to catch.
#
# The fifth is the one a reader can act on, and it is the one no other gate can see: a
# message that names a compiler temporary is still a refusal, still carries its code and
# still says where, so every other assertion here goes green while the reader is told about
# a binding that is in no file they can open.
#
# The sixth is what this branch's diagnostics work bought: every rule in check.zg reports
# through one place that knows the statement's file, line and column, so a rule that loses
# its position is caught here rather than noticed by a user. Nothing else would see it —
# the sentence still matches.
#
# The seventh makes zerg0 the ORACLE. The seed has had a semantic-analysis pass all along
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

# ABSOLUTE, because the `bare-entry` marker below runs the compiler from the case's OWN
# directory — and a `./bin/zerg` resolved from there names nothing. Computed once, from
# whatever the two variables above ended up holding, so an overridden $ZERG travels too.
abspath() {
	case $1 in
	/*) printf '%s\n' "$1" ;;
	*) printf '%s/%s\n' "$(pwd)" "${1#./}" ;;
	esac
}
ZERG_ABS=$(abspath "$ZERG")

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
# true for a hypothetical `no-place-yet`. It exists because the three boolean markers there
# were then were each asked for in a different way — a `case`, and two spellings of
# `${flags#* … }` — and the fourth would have picked a fourth. There are five now, and all
# five ask through here, which is the whole of what this bought.
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

	# LC_ALL=C, because this is a byte pipe and not a text one. A case is a PROGRAM, and one
	# class of ill-formed program is ill-formed exactly because its bytes are not text: under
	# the ambient UTF-8 locale macOS awk stops on `towc: multibyte conversion failure` and
	# writes a TRUNCATED file, so the case that reaches the compiler is not the case that was
	# written and the gate reports a mismatch on a rule it never exercised. In the C locale a
	# record is bytes, `/^--- /` matches bytes, and every case — UTF-8, ASCII or neither —
	# arrives as it was typed.
	LC_ALL=C awk -v dir="$dir" -v entry="$src" '
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

	# `--emit bin`, not `--emit c`: the C stage stops BEFORE cc, so under it a program
	# only cc would reject looks accepted and assertions 3 and 4 can never fire. Linking
	# for real is what makes "the compiler said so, not cc" a claim this gate can check —
	# and it costs nothing while the gate is green, because a program the compiler rejects
	# never reaches cc anyway.
	#
	# THE ENTRY PATH'S SPELLING IS PART OF THE PROGRAM, for one class of rule: a module is a
	# directory, so a rule that asks "which module is this declaration in" answers from the
	# path a file was read at — and a BARE filename has no directory component to answer
	# with. Every case above hands the compiler `$tmp/<name>.d/<name>.zg`, which always has
	# one, so the whole class was invisible here. `bare-entry` runs the build from the case's
	# own directory and names the entry with no directory at all, which is what a reader
	# standing in their own source tree types.
	# `emit-lib` BUILDS THE CASE AS A LIBRARY, and it is here because the two paths do not
	# check the same program. `--emit bin` emits unit by unit, so every module of the build is
	# walked; `--emit lib` emits ONE unit (zergc.zg's `emit_unit_at(merge_asts(trees),
	# trees[last])`), and a declaration rule that only reads the unit being emitted therefore
	# stops seeing every module the entry imported. That is not hypothetical: narrowing the
	# declaration passes to `own` sent a dependency's bad return type to cc under this flag
	# and nothing here noticed, because no case built a multi-module program this way.
	local out status first
	if has_flag "$flags" emit-lib; then
		out=$("$ZERG" build --emit lib -o "$tmp/$name.o" "$src" 2>&1 >/dev/null)
		status=$?
	elif has_flag "$flags" bare-entry; then
		out=$(cd "$dir" && "$ZERG_ABS" build --emit bin -o "$tmp/$name.bin" "$name.zg" 2>&1 >/dev/null)
		status=$?
	else
		out=$("$ZERG" build --emit bin -o "$tmp/$name.bin" "$src" 2>&1 >/dev/null)
		status=$?
	fi
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
	# CC MUST NOT BE THE ONE ANSWERING. Both tells — the message's SHAPE and a path into the
	# build cache — are one predicate in diag.sh, asked the same way by the three gates that
	# judge a refusal. They used to be two `case`/`if` pairs written out per script, which
	# is exactly how one of them came to be asked in two places and not in the third.
	if cc_answered "$out"; then
		echo "VIA CC    $name — cc answered this, not the compiler against the source"
		fail=$((fail + 1))
		return
	fi

	# AND IT MUST SPEAK THE READER'S VOCABULARY. A rule may quote any name in the file and no
	# name that is not — see `names_a_temp` in diag.sh for what the compiler mints and why
	# `assert` is the form that made this reachable from rules with nothing to do with it.
	if names_a_temp "$out"; then
		echo "NAMES TEMP  $name — the message quotes \`$(temp_named "$out")\`, which is in no file the reader can open"
		fail=$((fail + 1))
		return
	fi

	# every rule in check.zg reports through chk_at, which knows the statement's place —
	# so a case that comes back without one is a rule that lost it, and nothing else here
	# would notice: the sentence would still match.
	#
	# The EMITTER's refusals were the exception, and the PARSER's before them; both report
	# through a channel of their own now (p_diag in parser.zg, c_diag in emit.zg), which takes
	# the code and reads the place, so every one of them says where. Thirty-one of these
	# markers came off with the parser's channel and the rest with the emitter's. That is what
	# the marker is for — it retires itself, because a case that gains a place while still
	# carrying one is a failure by name rather than a quiet pass.
	#
	# WHAT IS LEFT CARRIES THE MARKER, and it is no longer one class: the DRIVER refuses
	# before a source is a tree at all — an unreadable file, an import that resolves to
	# nothing, a cycle between modules (zergc.zg) — and ast.zg refuses a `chan[T?]` that a
	# substitution built, where the type is written nowhere for a place to point at. Each of
	# those cases says so at itself. Marking them keeps a permanent LANGUAGE rule in this file,
	# where its lifetime says it belongs, instead of filing it with the not-yet-built forms
	# next door to dodge one assertion.
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

reject assign-to-plain-binding E3006 <<'EOF'
fn main() {
	x := 1
	x = 2
	print(f"{x}")
}
EOF

reject assign-to-value-parameter E3006 <<'EOF'
fn f(a: int) {
	a = 2
	print(f"{a}")
}

fn main() {
	f(1)
}
EOF

reject assign-to-field-of-immutable E3007 <<'EOF'
struct P {
	pub x: int
}

fn main() {
	p := P(1)
	p.x = 5
	print(f"{p.x}")
}
EOF

reject assign-to-element-of-immutable E3007 <<'EOF'
fn main() {
	xs := [1, 2]
	xs[0] = 9
	print(f"{xs[0]}")
}
EOF

reject assign-to-module-const E3003 <<'EOF'
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
reject assign-to-module-binding E3004 <<'EOF'
N := 5

fn main() {
	N = 6
	print(f"{N}")
}
EOF

reject assign-to-loop-variable E3006 <<'EOF'
fn main() {
	for i in 0..2 {
		i = 5
		print(f"{i}")
	}
}
EOF

# --- the frozen half of the collection model ---------------------------------------
#
# docs/code/collections.md, in two halves. Only a `mut` collection can modify its
# elements — and that must hold for a METHOD's receiver, not only for the assignment
# forms above: `xs.append(4)` on a plain `xs` compiled and really grew the list, because
# a method call was never asked whether its receiver is `mut`. And within `for … in xs`,
# `xs` is frozen against STRUCTURAL change — growing, shrinking or rebinding — for the
# loop's whole extent, `mut` or not, so an iterator can never be invalidated;
# `for x in xs { xs = [9] }` rebound the very buffer the cursor was walking, which is
# reachable undefined behaviour from safe code. The receiver rule already held for a
# user-declared `mut fn` (mut-fn-on-an-immutable-receiver, below) — what these pin is the
# built-in method and the freeze.

reject append-on-a-plain-binding E3088 <<'EOF'
fn main() {
	xs := [1, 2, 3]
	xs.append(4)
	print(f"{xs.len()}")
}
EOF

reject append-inside-its-own-for E3089 'cannot `append` to' <<'EOF'
fn main() {
	mut xs := [1, 2, 3]
	for x in xs {
		xs.append(x)
	}
	print(f"{xs.len()}")
}
EOF

reject rebind-inside-its-own-for E3089 'cannot rebind' <<'EOF'
fn main() {
	mut xs := [1, 2, 3]
	for x in xs {
		xs = [9]
	}
	print(f"{xs.len()}")
}
EOF

# `m[k] = v` INSERTS when `k` is new, and whether it is new is a runtime fact — so inside
# the map's own loop the whole form is refused. The LIST spelling of the same syntax stays
# legal there (`xs[0] = 9` moves no cursor), which is why the case pins the map.
reject map-index-assign-inside-its-own-for E3089 'cannot assign into' <<'EOF'
fn main() {
	mut m := {"a": 1}
	for k in m {
		m["b"] = 2
	}
	print(f"{m.len()}")
}
EOF

# The freeze is the loop's OWN collection and nothing wider: appending to a DIFFERENT
# collection while reading `xs` is the accumulation idiom the spec itself recommends, so
# it is asserted to stay accepted right beside the rules that could over-reach into it.
cat >"$tmp/append-to-a-different-collection.zg" <<'EOF'
fn main() {
	mut xs := [1, 2, 3]
	mut out: list[int] = []
	for x in xs {
		out.append(x)
	}
	print(f"{out.len()}")
}
EOF
if "$ZERG" build --emit bin -o "$tmp/append-to-a-different-collection.bin" "$tmp/append-to-a-different-collection.zg" >/dev/null 2>&1; then
	pass=$((pass + 1))
else
	echo "FROZEN    append-to-a-different-collection — the freeze over-reached into the accumulation idiom the spec blesses"
	fail=$((fail + 1))
fi

# And the same edge one step in: a DIFFERENT collection may wear the SAME NAME. A plain `:=`
# is shadowable (GRAMMAR group 4 exempts only `const`), so a body declaring its own `xs`
# inside `for x in xs` names a second collection — and a freeze keyed on the spelling
# refused an append to it, in a sentence that was not true of that `xs`. The case above
# could not see it, because it only ever appends to another NAME. The seed accepts this and
# prints 2 2 2.
cat >"$tmp/append-to-a-shadowing-binding.zg" <<'EOF'
fn main() {
	mut xs: list[int] = [1, 2, 3]
	for x in xs {
		mut xs: list[str] = ["a"]
		xs.append("b")
		print(f"{xs.len()}")
	}
}
EOF
if "$ZERG" build --emit bin -o "$tmp/append-to-a-shadowing-binding.bin" "$tmp/append-to-a-shadowing-binding.zg" >/dev/null 2>&1; then
	pass=$((pass + 1))
else
	echo "FROZEN    append-to-a-shadowing-binding — the freeze matched a NAME, not the loop's binding"
	fail=$((fail + 1))
fi

# --- `const` is shadow-proof, in BOTH directions ----------------------------------
#
# GRAMMAR group 4: a `const` binding is immutable and SHADOW-PROOF — no later binding may
# take its name, and it may not itself take a name a visible binding holds. Neither
# direction used to be checked: `k := 2` in a block under `const k := 1` compiled and
# printed 2, silently, which is the exact answer the keyword exists to rule out. The
# negative half — a plain `:=` may still be shadowed and `mut n := n` still works — is
# pinned by the corpus (const_shadow_allowed), because a program that must COMPILE belongs
# there, not here.

reject shadow-a-const-from-an-inner-block E3054 <<'EOF'
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
reject loop-variable-takes-a-const-name E3054 <<'EOF'
const k := 1

fn main() {
	for k in 0..3 {
		print k
	}
}
EOF

reject const-shadowing-an-outer-binding E3055 <<'EOF'
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
reject const-collision-in-the-same-block E3054 <<'EOF'
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
reject const-rebound-by-a-match-arm E3054 one-finding <<'EOF'
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

# a `select` ARM's receive binding is a binding too, and it was the one name-introducing
# site in the language that did not go through c_add_var: it wrote the environment's
# columns out by hand, so it asked neither this rule nor the substitution rule beside it.
# Its real cost was worse than a missed refusal — one of the eight columns was left off
# and every later binding read the wrong row of it — but this is the half a gate can see.
reject const-rebound-by-a-select-arm E3054 <<'EOF'
fn gen(out: chan[int]<-) {
	out <- 1
}

fn main() {
	const v := 9
	a := chan[int](1)
	spawn gen(a)
	select {
		v := <-a => print v
	}
	print v
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

reject top-level-mut-in-safe-code E3056 <<'EOF'
mut counter := 0

fn main() {
	print "ok"
}
EOF

reject top-level-pub-mut E3056 <<'EOF'
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
reject top-level-mut-that-is-used E3056 at=1:1 one-finding <<'EOF'
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
reject statement-in-unsafe-group E2022 <<'EOF'
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
reject unsafe-group-never-closed E2024 at=1:1 <<'EOF'
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
reject unsafe-group-nested E2021 at=2:2 <<'EOF'
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
reject unsafe-group-fn-called-from-safe E3083 'this call is in safe code' <<'EOF'
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
reject unsafe-group-fn-as-a-value E3083 'hand safe code the same call' <<'EOF'
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
reject top-level-annotation-mismatch E3033 'cannot bind int to a bool binding' at=1:1 seed-gap <<'EOF'
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
reject const-initializer-reports-the-declaration E3043 'operator `+` takes numeric operands' at=1:1 <<'EOF'
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

reject pub-on-an-impl-block E2017 at=9:1 <<'EOF'
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

reject pub-before-a-statement E2023 at=1:1 <<'EOF'
pub 42

fn main() {
	print "ok"
}
EOF

reject pub-on-an-import E2015 at=1:1 <<'EOF'
pub import "io"

fn main() {
	print "ok"
}
EOF

reject pub-on-init E2016 at=1:1 <<'EOF'
pub init() {
	print "ok"
}

fn main() {
	print "ok"
}
EOF

reject pub-before-a-decorator E2018 at=1:1 <<'EOF'
pub #[derive(Eq)]
struct A {
	pub x: int
}

fn main() {
	print "ok"
}
EOF

# --- one decorator per item, and which decorators lead a statement -------------------
#
# A decorator may lead a STATEMENT now (GRAMMAR#statement), which is what `#[allow(…)]` is
# for. That makes `#[derive(Eq)]` above a statement SYNTACTICALLY legal and semantically
# nothing, so it owes its own refusal — a decorator changes what a declaration means, and
# there is no declaration in a body for one to change.
#
# It is a rejection rather than a refusal: no future feature makes `#[derive]` on a binding
# mean something. The seed refuses it too, by not reading a decorator inside a body at all.
reject derive-leading-a-statement E2062 '`#[derive(Eq)]`' at=2:2 <<'EOF'
fn main() {
	#[derive(Eq)]
	n := 1
	print n
}
EOF

# `#[test]` gets the sentence about a `fn`, not the derive's about a type — the same split
# E2066 makes one position up, and the reason is the same: advice for the other decorator is
# advice the reader cannot act on.
reject test-leading-a-statement E2062 '`#[test]`' at=2:2 <<'EOF'
fn main() {
	#[test]
	n := 1
	print n
}
EOF

# ONE DECORATOR PER ITEM. Stacking parsed in both compilers and said exactly what the comma
# list says — two spellings for one thing, which is what `zerg fmt` exists to remove and what
# it cannot remove once both are legal.
reject stacked-decorators-on-a-declaration E2063 at=2:1 <<'EOF'
#[derive(Eq)]
#[obj]
spec Draw {
	fn draw() -> str
}

fn main() {
	print "ok"
}
EOF

# and in the position that opened this: `#[allow]` puts nothing on the pending list, so a
# stack that began with one would have read as no decorator at all without a flag of its own
reject stacked-decorators-on-a-statement E2063 at=3:2 <<'EOF'
fn main() {
	#[allow(L103)]
	#[allow(L104)]
	n := 1
	print n
}
EOF

# `#[allow]` NAMES THE CODES IT SUPPRESSES. With none it suppresses nothing while reading as
# though it did — E2069's argument for `#[derive]`, one decorator over.
reject an-allow-with-no-codes E2064 at=2:4 <<'EOF'
fn main() {
	#[allow]
	n := 1
	print n
}
EOF

reject pub-on-an-unsafe-group E2020 at=1:1 <<'EOF'
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

reject an-invented-namespace-prefix E3069 'undefined name `bogus`' <<'EOF'
import "./util/text"

fn main() {
	print bogus.text.shout("x")
}
--- util/text/text.zg
pub fn shout(s: str) -> str {
	return s + "!"
}
EOF

reject a-path-segment-is-not-a-namespace E3069 'undefined name `util`' <<'EOF'
import "./util/text"

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
reject an-invented-prefix-onto-a-private-member E3069 'undefined name `bogus`' <<'EOF'
import "./caller"

fn main() {
	print caller.go()
}
--- caller/caller.zg
import "./util/text"

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
reject an-invented-prefix-in-a-defer E3069 'undefined name `bogus`' <<'EOF'
import "./util/text"

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
reject a-namespace-without-that-member E3084 <<'EOF'
import "./util/text"

fn main() {
	print text.nosuch("a")
}
--- util/text/text.zg
pub fn shout(s: str) -> str {
	return s + "!"
}
EOF

# THE SAME MEMBER, ONE POSITION ALONG: the missing member is a method call's RECEIVER rather
# than the whole expression. `io.stdout.write(…)` is how a reader spells the stream surface
# docs/runtime/io.md marks `[not yet]`, and the answer was "the method `write` on a ?" — the
# fourth shape of the finding above, and the one that survived it, because the method path
# INFERS its receiver's type and never LOWERS it, so the receiver's own rule was never asked.
reject a-namespace-member-that-does-not-exist-as-a-receiver E3084 'has no `stdout`' <<'EOF'
import "io"

fn main() {
	io.stdout.write("x")
}
EOF

# TWO IMPORTS SHARING A LAST SEGMENT both bound `text`, and both answered through it: one
# namespace holding the union of two modules' members, with no diagnostic. GRAMMAR says
# `as` is "how two imports sharing a last segment coexist", which is only true if not
# renaming them is an error.
reject two-imports-sharing-a-last-segment E3085 'is already the namespace of' at=3:2 <<'EOF'
import (
	"./alt/text"
	"./util/text"
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
reject an-import-colliding-with-a-function E3085 'is already a function in this program' at=1:8 <<'EOF'
import "./util/text"

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

# A TEMPLATE IS A NAME AT THE TOP LEVEL even though it is not a function to lower, and this
# is the case that says which walk the rule reads. The import rule used to ask what a name
# is by re-walking the top-level lists of the program with its TEMPLATES ALREADY REMOVED, so
# a generic took an import's name in silence — while the identical non-generic pair above
# was refused. It reads the table c_build_top_names records now, and that walk runs over the
# whole program, templates and all, for exactly this reason.
reject an-import-colliding-with-a-generic-function E3085 'is already a function in this program' at=1:8 <<'EOF'
import "./util/text"

fn text[T](v: T) -> T {
	return v
}

fn main() {
	print text(1)
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

reject explicit-type-args-on-a-plain-fn E2035 <<'EOF'
fn id(x: int) -> int {
	return x
}

fn main() {
	print id[int](7)
}
EOF

# THE MULTI-ARGUMENT SHAPE, which never looked like an index at all — a comma is what
# settles the bracket. It is the same rule and it is refused in the same place.
reject explicit-type-args-multi E2035 seed-gap <<'EOF'
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
reject associated-type-in-a-spec E2011 seed-gap <<'EOF'
spec It {
	type Item

	fn get() -> int
}

fn main() {
	print 1
}
EOF

reject associated-value-in-a-spec E2008 seed-gap <<'EOF'
spec Bits {
	BITS: int
}

fn main() {
	print 1
}
EOF

reject a-spec-member-that-is-not-one E2036 <<'EOF'
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
reject an-orphan-impl E3096 <<'EOF'
import "./far"

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
reject derive-delegation-with-a-self-parameter E3097 <<'EOF'
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

reject derive-delegation-over-a-bare-variant E3098 <<'EOF'
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
reject derive-a-user-spec-on-a-struct E4024 <<'EOF'
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
reject obj-on-a-mut-method E3100 <<'EOF'
#[obj]
spec Draw {
	mut fn bump()
}

fn main() {
	print 1
}
EOF

reject obj-with-a-self-parameter E3101 <<'EOF'
#[obj]
spec Draw {
	fn same(o: This) -> bool
}

fn main() {
	print 1
}
EOF

reject obj-on-something-that-is-not-a-spec E3099 <<'EOF'
#[obj]
struct P {
	pub x: int
}

fn main() {
	print 1
}
EOF

# and its mirror: a `spec` has no structure for a derive to read.
reject derive-on-a-spec E3102 seed-gap <<'EOF'
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

reject redeclare-plain-then-const-in-one-block E3055 <<'EOF'
fn main() {
	x := 1
	const x := 2
	print x
}
EOF

# --- conditions -------------------------------------------------------------------
#
# Zerg has no truthiness. Every form that asks a question asks it of a bool.

reject int-as-if-condition E3053 "must be bool, and this one is int" <<'EOF'
fn main() {
	if 1 {
		print("yes")
	}
}
EOF

reject str-as-if-condition E3053 "must be bool, and this one is str" <<'EOF'
fn main() {
	s := "abc"
	if s {
		print("yes")
	}
}
EOF

reject int-as-for-condition E3053 'the condition of a `for` must be bool' <<'EOF'
fn main() {
	mut n := 3
	for n {
		n = n - 1
	}
	print(f"{n}")
}
EOF

reject int-as-conditional-return E3053 'the condition of a conditional `return` must be bool' <<'EOF'
fn f() -> int {
	return 1 if 2
	return 0
}

fn main() {
	print(f"{f()}")
}
EOF

reject int-as-if-expression-condition E3053 'the condition of an `if` expression must be bool' <<'EOF'
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

reject int-plus-str E3043 'operator `+` takes numeric operands' <<'EOF'
fn main() {
	s := "s"
	print(f"{1 + s}")
}
EOF

reject str-plus-int E3043 'operator `+` takes numeric operands' <<'EOF'
fn main() {
	s := "s"
	print(f"{s + 1}")
}
EOF

# `//` is the floor-division operator, so its operands are numbers like the rest of the
# arithmetic family — a str is not one. It matters more than the others that this is said:
# `//` opens a comment in most languages a reader arrives from, and silently accepting
# `a // b` on two strings would be the worst possible way to learn that Zerg's is `#`.
reject floor-div-on-str E3043 'operator `//` takes numeric operands' <<'EOF'
fn main() {
	s := "s"
	print s // s
}
EOF

reject bool-plus-int E3043 'operator `+` takes numeric operands' <<'EOF'
fn main() {
	print(f"{true + 1}")
}
EOF

reject int-as-logical-operand E3041 <<'EOF'
fn main() {
	print(f"{1 and 2}")
}
EOF

reject order-int-against-str E3044 <<'EOF'
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
reject order-a-struct-that-has-eq E3044 'and these are P and P' <<'EOF'
#[derive(Eq)]
struct P {
	pub x: int
}

fn main() {
	print P(1) < P(2)
}
EOF

reject compare-int-with-str E3046 <<'EOF'
fn main() {
	s := "s"
	print(f"{1 == s}")
}
EOF

# AN AGGREGATE IS A PAIR TOO, and equality was the last family that only judged the scalars.
# The relation it asked was `ty_name(a) == ty_name(b)` — a type's diagnostic SPELLING, which
# is unique only where a type has a name of its own — so every composite fell out of the rule
# and was left to cc, which reported `invalid operands to binary expression
# ('zgt_tup_int64_t_int64_t' and 'zgt_tup_constcharp_constcharp')` against generated C.
#
# FOUR SHAPES, because ty_eq answers structurally and a mismatch can be at any depth: a tuple
# against a tuple of other components, a map against a map of other values, a list against a
# list of other elements, a function against a function of another signature. The seed has
# refused all four since it had a semantic pass, with the same words minus the code.

reject compare-two-different-tuples E3046 'cannot compare (int, int) and (str, str)' <<'EOF'
fn main() {
	t := (1, 2)
	u := ("a", "b")
	print t == u
}
EOF

reject compare-two-different-maps E3046 'cannot compare map[str, int] and map[str, str]' <<'EOF'
fn main() {
	a: map[str, int] = {"x": 1}
	b: map[str, str] = {"x": "y"}
	print a == b
}
EOF

reject compare-two-different-lists E3046 'cannot compare list[int] and list[str]' <<'EOF'
fn main() {
	xs := [1, 2]
	ys := ["a"]
	print xs == ys
}
EOF

reject compare-two-different-fns E3046 'cannot compare fn(int) -> int and fn(str) -> str' <<'EOF'
fn takes_int(x: int) -> int {
	return x
}

fn takes_str(s: str) -> str {
	return s
}

fn main() {
	a := takes_int
	b := takes_str
	print a == b
}
EOF

reject add-an-int-to-a-uint E3051 <<'EOF'
fn main() {
	i: int = 3
	u: uint = 5
	print(f"{i + u}")
}
EOF

reject compare-an-int-with-a-uint E3051 <<'EOF'
fn main() {
	i: int = -1
	u: uint = 1
	print(f"{i < u}")
}
EOF

reject equate-an-int-with-a-uint E3051 <<'EOF'
fn main() {
	i: int = -1
	u: uint = 1
	print(f"{i == u}")
}
EOF

reject bitwise-on-float E3042 <<'EOF'
fn main() {
	print(f"{3.0 & 1}")
}
EOF

reject bitwise-on-bool E3042 <<'EOF'
fn main() {
	print(f"{true & 1}")
}
EOF

reject negate-a-str E3049 <<'EOF'
fn main() {
	s := "a"
	print(f"{-s}")
}
EOF

reject not-an-int E3048 <<'EOF'
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

reject bind-str-to-int E3033 'cannot bind str to a int binding' <<'EOF'
fn main() {
	x: int = "hello"
	print(f"{x}")
}
EOF

reject bind-int-to-bool E3033 'cannot bind int to a bool binding' <<'EOF'
fn main() {
	b: bool = 1
	print(f"{b}")
}
EOF

reject bind-float-to-int E3033 'cannot bind float to a int binding' <<'EOF'
fn main() {
	x: int = 1.5
	print(f"{x}")
}
EOF

reject bind-int-list-to-str-list E3028 <<'EOF'
fn main() {
	ys: list[str] = [1, 2]
	print(f"{ys[0]}")
}
EOF

reject typedef-value-into-its-underlying E3038 'argument 1 of `f` is int, and this gives Celsius' <<'EOF'
type Celsius = int

fn f(n: int) -> int {
	return n
}

fn main() {
	print(f"{f(Celsius(20))}")
}
EOF

reject logical-operator-on-a-typedef E3040 'has no meaning on Flag and Flag' <<'EOF'
type Flag = bool

fn main() {
	f := Flag(true)
	g := Flag(false)
	print(f"{f and g}")
}
EOF

reject bitwise-operator-on-a-typedef E3040 'has no meaning on Mask and int' <<'EOF'
type Mask = int

fn main() {
	m := Mask(3)
	print(f"{m & 1}")
}
EOF

reject prefix-operator-on-a-typedef E3047 <<'EOF'
type Flag = bool

fn main() {
	f := Flag(true)
	print(f"{not f}")
}
EOF

reject arithmetic-on-a-typedef E3040 <<'EOF'
type Celsius = int

fn main() {
	c := Celsius(20)
	print(f"{c + 1}")
}
EOF

reject typedef-declared-twice E3078 seed-gap <<'EOF'
type Celsius = int
type Celsius = float

fn main() {
	print(f"{int(Celsius(1))}")
}
EOF

reject typedef-over-an-undeclared-type E3035 <<'EOF'
type Celsius = Nope

fn main() {
	print(f"{int(Celsius(1))}")
}
EOF

reject typedef-conversion-takes-one-value E3064 <<'EOF'
type Celsius = int

fn main() {
	print(f"{int(Celsius(1, 2))}")
}
EOF

reject str-sent-on-an-int-channel E3036 'the value sent on this channel is int' <<'EOF'
fn main() {
	ch := chan[int](1)
	ch <- "hi"
	print(f"{(<-ch)!}")
}
EOF

reject str-appended-to-an-int-list E3036 'the element `append` adds is int' <<'EOF'
fn main() {
	mut xs: list[int] = []
	xs.append("hi")
	print(f"{xs.len()}")
}
EOF

reject str-written-into-an-int-map E3036 'the value written into this map is int' <<'EOF'
fn main() {
	mut m: map[str, int] = {:}
	m["a"] = "hi"
	print(f"{m.len()}")
}
EOF

reject str-among-a-map-literals-ints E3036 'a value of this map literal is int' <<'EOF'
fn main() {
	m := {"a": 1, "b": "hi"}
	print(f"{m.len()}")
}
EOF

reject str-as-an-int-coalesce-fallback E3036 'the `??` fallback is int' <<'EOF'
fn main() {
	x: int? = 1
	print(f"{x ?? "no"}")
}
EOF

reject str-into-an-int-variant-payload E3036 'payload 1 of `Line` is int' <<'EOF'
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

reject str-into-an-int-struct-field E3036 'the field `x` of `P` is int, and this gives str' <<'EOF'
struct P {
	pub x: int
}

fn main() {
	p := P("hi")
	print(f"{p.x}")
}
EOF

reject oversized-literal-into-a-byte-struct-field E3029 '`300` is not a value a byte holds' <<'EOF'
struct P {
	pub x: byte
}

fn main() {
	p := P(300)
	print(f"{int(p.x)}")
}
EOF

reject bind-oversized-literal-to-byte E3029 '`300` is not a value a byte holds' <<'EOF'
fn main() {
	b: byte = 300
	print(f"{b}")
}
EOF

reject bind-negative-literal-to-uint E3029 '`-1` is not a value a uint holds' <<'EOF'
fn main() {
	u: uint = -1
	print(f"{u}")
}
EOF

# The same rule reached through a GENERIC. `y: T = 300` says nothing about a range until T is
# known, and it is known only in the specialization — so this is what "the constant rule survives
# monomorphization" means, and nothing pinned it.
#
# WHAT IS PINNED HERE IS `zerg`'s CONTRACT, and only `zerg`'s. It needs no `seed-gap` because the
# seed refuses the program too — but for a different reason, and the difference is worth writing
# down rather than letting the shared exit status imply agreement. `zerg` substitutes and then
# reports `E3029` against the `byte` it substituted; the seed says `cannot bind int to a T binding`,
# which is the whole FORM turned away with no substitution having happened. The harness only asks
# the seed to say no, so the case is honest; the seed is not evidence for the rule.
reject oversized-literal-in-a-specialization E3029 '`300` is not a value a byte holds' <<'EOF'
fn hold[T](v: T) -> T {
	y: T = 300
	return v
}

fn main() {
	b: byte = 1
	print(f"{int(hold(b))}")
}
EOF

# --- the conversion's constant layer -------------------------------------------------
#
# docs/core/types.md reports a conversion at compile time when "THE VALUE IS KNOWN", and what the
# compiler means by known is one notion, shared with the fill count `[v; N]`: a literal, a plain
# binding, a `const`, and constant arithmetic over any of them. The conversion used to ask a
# narrower question — the written literal tree alone — so `[0; big]` folded while `byte(big)` died
# at run time, which is one sentence of spec with two answers.
#
# All three are `seed-gap`: the seed folds a literal and nothing else, so it builds each of these
# and raises where it runs. Where the line ENDS is held by test-data/codegen/conv_const_line.zg,
# because a rule that only ever appears here is a rule nobody has watched stop.

reject oversized-binding-converted E3029 '`big`, which is 300' seed-gap <<'EOF'
fn main() {
	big := 300
	print(f"{int(byte(big))}")
}
EOF

reject oversized-const-converted E3029 '`N`, which is 300' seed-gap <<'EOF'
const N := 300

fn main() {
	print(f"{int(byte(N))}")
}
EOF

# The sentence is pinned because it is the one that must NOT quote `300`: nothing on this line
# says 300, and a reader shown it in backticks goes looking for it. A name can be quoted — it is
# written — and the two cases above pin that half.
reject oversized-const-arithmetic-converted E3029 'this expression, whose value is 300' seed-gap <<'EOF'
const N := 100

fn main() {
	print(f"{int(byte(N * 3))}")
}
EOF

# --- the conversion table, closed --------------------------------------------------
#
# docs/core/types.md lists the pairs `T(x)` accepts "and no others", and `int` is the hub
# every one of them has on a side. A pair that is not on it is not a conversion this
# language has, and the two ways to be off the table read differently:
#
#   E3090  the source is a FLOAT. Dropping a fraction is a decision, not a step, so it is
#         spelled with a verb — `math.trunc` / `floor` / `ceil` / `round`, each answering
#         an `int` — and there is no second conversion to send a reader to.
#   E3091  any other absent pair, which is the two steps through `int` written as one.
#
# Every case here is `seed-gap`. The seed lowers a conversion by SHAPE, and a shape has an
# answer for every pair of scalars; this is a chapter where `zerg` is the stricter compiler.

reject int-of-a-float E3090 '`int(…)` drops the fraction of a `float`' seed-gap <<'EOF'
fn main() {
	print(f"{int(1.9)}")
}
EOF

reject byte-of-a-float E3090 '`byte(…)` drops the fraction of a `float`' seed-gap <<'EOF'
fn main() {
	print(f"{int(byte(3.5))}")
}
EOF

reject uint-of-a-float E3090 '`uint(…)` drops the fraction of a `float`' seed-gap <<'EOF'
fn main() {
	print(f"{int(uint(3.5))}")
}
EOF

reject rune-of-a-float E3090 '`rune(…)` drops the fraction of a `float`' seed-gap <<'EOF'
fn main() {
	print(f"{int(rune(65.5))}")
}
EOF

reject float-of-a-byte E3091 '`byte` -> `float`' seed-gap <<'EOF'
fn main() {
	b: byte = 65
	print(f"{float(b)}")
}
EOF

reject rune-of-a-byte E3091 '`byte` -> `rune`' seed-gap <<'EOF'
fn main() {
	b: byte = 65
	print(f"{int(rune(b))}")
}
EOF

reject uint-of-a-byte E3091 '`byte` -> `uint`' seed-gap <<'EOF'
fn main() {
	b: byte = 65
	print(f"{int(uint(b))}")
}
EOF

reject byte-of-a-uint E3091 '`uint` -> `byte`' seed-gap <<'EOF'
fn main() {
	u: uint = 65
	print(f"{int(byte(u))}")
}
EOF

reject rune-of-a-uint E3091 '`uint` -> `rune`' seed-gap <<'EOF'
fn main() {
	u: uint = 65
	print(f"{int(rune(u))}")
}
EOF

reject float-of-a-uint E3091 '`uint` -> `float`' seed-gap <<'EOF'
fn main() {
	u: uint = 65
	print(f"{float(u)}")
}
EOF

reject byte-of-a-rune E3091 '`rune` -> `byte`' seed-gap <<'EOF'
fn main() {
	r: rune = 'A'
	print(f"{int(byte(r))}")
}
EOF

reject uint-of-a-rune E3091 '`rune` -> `uint`' seed-gap <<'EOF'
fn main() {
	r: rune = 'A'
	print(f"{int(uint(r))}")
}
EOF

reject float-of-a-rune E3091 '`rune` -> `float`' seed-gap <<'EOF'
fn main() {
	r: rune = 'A'
	print(f"{float(r)}")
}
EOF

# THE SOURCE IS A BINDING and not a literal, which is the same rule reached down the other
# path. A written `1.9` is a literal tree, and the folding rule types it from the position it
# stands in before this rule ever looks; a `float` binding arrives already typed. The four
# cases above are all literals, so the whole of E3090 rested on one of its two approaches —
# and the half nobody wrote a case for is the half a change to constant folding moves.

reject int-of-a-float-binding E3090 '`int(…)` drops the fraction of a `float`' seed-gap <<'EOF'
fn main() {
	f: float = 1.9
	print(f"{int(f)}")
}
EOF

reject byte-of-a-float-binding E3090 '`byte(…)` drops the fraction of a `float`' seed-gap <<'EOF'
fn main() {
	f: float = 3.5
	print(f"{int(byte(f))}")
}
EOF

reject uint-of-a-float-binding E3090 '`uint(…)` drops the fraction of a `float`' seed-gap <<'EOF'
fn main() {
	f: float = 3.5
	print(f"{int(uint(f))}")
}
EOF

reject rune-of-a-float-binding E3090 '`rune(…)` drops the fraction of a `float`' seed-gap <<'EOF'
fn main() {
	f: float = 65.5
	print(f"{int(rune(f))}")
}
EOF

# A COMPILER PRIMITIVE IS NOT A NAME A PROGRAM INVENTS. The `__zrt_…` set is closed — it is the
# standard library's way down to the runtime — and the emitter used to lower ANY name spelled with
# two leading underscores by stripping them, so a typo reached cc as a call to a symbol nothing
# declares. The seed turns the same program away (as an undefined function, which is its own
# sentence rather than this rule), so there is no gap to mark.
#
# THE PREFIX IS `__zrt_` AND NOT `__`. GRAMMAR#identifier reserves no prefix at all, so
# `fn __my_helper()` is an ordinary declaration; the first cut of this rule refused it, which is a
# legal program turned away and exactly what this list must not grow. There is a codegen case for
# that spelling, because a narrowing nobody exercises is a narrowing nobody checks.
reject an-unknown-compiler-primitive E3092 <<'EOF'
fn main() {
	__zrt_trunk(1.5)
}
EOF

# The NAME being in the set does not make the CALL one. Arity was one half escaping: the emitter
# wrote the operands out as given, so `__zrt_trunc()` reached clang as a call with no argument,
# against generated C, with no code and no place.
reject a-compiler-primitive-with-no-argument E3093 'takes 1 argument and this gives 0' <<'EOF'
fn main() {
	print __zrt_trunc()
}
EOF

reject a-compiler-primitive-with-too-many-arguments E3093 'takes 1 argument and this gives 2' <<'EOF'
fn main() {
	print __zrt_trunc(1.5, 2.5)
}
EOF

# and the operand TYPES were the other half, escaping in both directions. A primitive is
# lowered by NAME to a C function with a real signature, so a wrong operand is either a cc
# diagnostic against a file nobody wrote, or an answer that is quietly wrong where C converts
# it for you: `__zrt_trunc(true)` built under BOTH compilers and printed `1`.
#
# Both carry `seed-gap`. The seed has the machinery — `unaryIntrinsic(n, Float, Int)` names the
# argument type — and it does not fire, so it builds the bool and hands the str to cc.
reject a-compiler-primitive-given-a-bool E3094 'is float, and this gives bool' seed-gap <<'EOF'
fn main() {
	print __zrt_trunc(true)
}
EOF

reject a-compiler-primitive-given-a-str E3094 'is float, and this gives str' seed-gap <<'EOF'
fn main() {
	print __zrt_trunc("hello")
}
EOF

# The set is CLOSED and it also GROWS, and both halves need a case. `__zrt_isatty` is the
# newest leaf — the one `os.isatty` lowers onto — and the row that gives it an operand type is
# a different line from the row that gives it a result type. A leaf added to one and not the
# other is accepted with the wrong arity or handed the wrong operand, which reaches cc as a
# diagnostic against a file nobody wrote.
reject the-newest-primitive-given-a-str E3094 'is int, and this gives str' seed-gap <<'EOF'
fn main() {
	print __zrt_isatty("2")
}
EOF

reject the-newest-primitive-with-no-argument E3093 'takes 1 argument and this gives 0' <<'EOF'
fn main() {
	print __zrt_isatty()
}
EOF

# AND A TWO-OPERAND LEAF IS A DIFFERENT SHAPE, which is why `__zrt_set_env` gets its own three
# rather than riding on the unary ones above. `__zrt_write` has been two-operand since the `io`
# floor was written and no case ever exercised that row, so until the environment became
# writable nothing here asked whether an arity above one is checked at all — or whether the
# SECOND operand's type is, which is a different index into a different list.
#
# The seed grew a `binaryIntrinsic` for the same leaf, and it is the operand half of it that
# does not fire, exactly as `unaryIntrinsic`'s does not: hence `seed-gap` on the two type cases
# and not on the arity one.
reject a-two-operand-primitive-given-one E3093 'takes 2 arguments and this gives 1' <<'EOF'
fn main() {
	__zrt_set_env("K")
}
EOF

reject a-two-operand-primitives-second-operand E3094 'operand 2 of the compiler primitive `__zrt_set_env` is str, and this gives int' seed-gap <<'EOF'
fn main() {
	__zrt_set_env("K", 1)
}
EOF

reject the-environment-removal-given-an-int E3094 'is str, and this gives int' seed-gap <<'EOF'
fn main() {
	print __zrt_del_env(2)
}
EOF

# --- returned value ---------------------------------------------------------------
#
# A signature is a promise. The conditional `return` is here on its own because it takes a
# different path through the emitter than the plain one.

reject return-str-from-int-fn E3032 "this function's answer is int, and this gives str" <<'EOF'
fn f() -> int {
	return "nope"
}

fn main() {
	print(f"{f()}")
}
EOF

reject return-int-from-bool-fn E3032 "this function's answer is bool, and this gives int" <<'EOF'
fn f() -> bool {
	return 1
}

fn main() {
	print(f"{f()}")
}
EOF

reject conditional-return-wrong-type E3032 "this function's answer is int, and this gives str" <<'EOF'
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

reject call-with-too-few-arguments E3027 '`add` needs 2 arguments and this gives 1' <<'EOF'
fn add(a: int, b: int) -> int {
	return a + b
}

fn main() {
	print(f"{add(1)}")
}
EOF

reject call-with-too-many-arguments E3026 '`add` takes 2 arguments and this gives 3' <<'EOF'
fn add(a: int, b: int) -> int {
	return a + b
}

fn main() {
	print(f"{add(1, 2, 3)}")
}
EOF

reject call-past-a-default E3026 '`scale` takes 2 arguments and this gives 3' <<'EOF'
fn scale(n: int, by: int = 2) -> int {
	return n * by
}

fn main() {
	print(f"{scale(5, 3, 9)}")
}
EOF

reject method-with-too-many-arguments E3026 '`P.add` takes 2 arguments and this gives 3' <<'EOF'
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

reject float-argument-into-int-parameter E3038 'argument 1 of `f` is int, and this gives float' <<'EOF'
fn f(a: int) -> int {
	return a
}

fn main() {
	print(f"{f(1.5)}")
}
EOF

reject str-argument-into-int-parameter E3038 'argument 2 of `add` is int, and this gives str' <<'EOF'
fn add(a: int, b: int) -> int {
	return a + b
}

fn main() {
	s := "x"
	print(f"{add(1, s)}")
}
EOF

reject str-argument-into-a-method E3038 'argument 1 of `P.add` is int, and this gives str' <<'EOF'
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

reject int-as-match-arm-guard E3053 "the condition of a match arm's guard must be bool" <<'EOF'
fn main() {
	n := 1
	print(match n {
		_ if 1 => "yes"
		_      => "no"
	})
}
EOF

reject wrapping-operator-on-a-bool E3043 'operator `+%` takes numeric operands' <<'EOF'
fn main() {
	print(f"{true +% 1}")
}
EOF

reject assign-int-to-a-bool-binding E3037 'cannot assign int to `b`, which holds bool' <<'EOF'
fn main() {
	mut b: bool = true
	b = 1
	print(f"{b}")
}
EOF

reject assign-str-to-an-int-field E3037 'cannot assign str to that part of `p`, which holds int' <<'EOF'
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

reject bind-a-struct-to-another-struct E3033 'cannot bind B to a A binding' <<'EOF'
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

reject pass-a-struct-where-another-goes E3038 'argument 1 of `take` is A, and this gives B' <<'EOF'
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

reject return-a-struct-the-signature-does-not-name E3032 "this function's answer is A, and this gives B" <<'EOF'
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

reject bind-a-nested-list-of-the-wrong-element E3028 <<'EOF'
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

reject rune-with-two-characters E1003 <<'EOF'
fn main() {
	c := 'ba'
	print(f"{c}")
}
EOF

reject byte-with-two-characters E1003 <<'EOF'
fn main() {
	c := b'ba'
	print(f"{c}")
}
EOF

reject string-that-never-closes E1001 <<'EOF'
fn main() {
	s := "no closing quote
	print(s)
}
EOF

reject a-character-no-token-uses E1004 <<'EOF'
fn main() {
	x := 1 @ 2
	print(f"{x}")
}
EOF

# THE SAME RULE, WRITTEN IN A CHARACTER THAT IS NOT ASCII. `GRAMMAR#identifier` derives ASCII
# letters, digits and `_` and nothing else, so a character outside a string, a rune or a
# comment is the same lexical error `@` is — and this used to KILL the compiler instead:
# `EncodingError: bytes are not valid UTF-8 for a str`, an uncaught runtime abort with no
# code, no place and no form named. The lexer had reached its own E1004 and then built the
# diagnostic's lexeme out of ONE byte of a three-byte character.
#
# It is in the reject list rather than the refuse list because no future feature makes it
# legal: `GRAMMAR#letter` says the source is UTF-8, which is what lets this character be WRITTEN
# in a comment or a literal, and identifier says which characters can spell a name.
reject a-character-that-is-not-ascii E1004 <<'EOF'
fn main() {
	x := 1
	print 值
}
EOF

# A FILE THAT IS NOT UTF-8 AT ALL, which is the same abort one stage earlier: the driver turns
# the bytes it read into a `str` before the lexer sees a character, and `str(bytes)` checks the
# invariant and KILLS the process on a violation. `zerg build`, `zerg fmt`, `zerg desugar`,
# `zerg lint` and the language server all died the same way, on `EncodingError: bytes are not
# valid UTF-8 for a str` — no code, no place, and not even the name of the file it was reading.
#
# `GRAMMAR#letter` says the source is UTF-8, so a file that is not is not a Zerg source file, and
# WHICH FILE is the whole of the answer — there is no line to name, the thing that would read
# one being what refused. Hence `no-place`.
#
# The byte is spelled through the shell rather than written here, because a script holding an
# invalid sequence is a script no editor, formatter or diff opens twice.
#
# `seed-gap`: the seed is byte-oriented and has no str invariant to violate, so it emits
# `"\377"` into the C and answers nothing at all — the one direction where zerg is the
# stricter compiler on an encoding.
reject a-source-file-that-is-not-utf8 E1011 no-place seed-gap <<EOF
fn main() {
	print "$(printf '\377')"
}
EOF

reject based-number-with-no-digits E1008 <<'EOF'
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
reject based-number-with-a-leading-underscore E1008 <<'EOF'
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

reject byte-escape-with-non-hex-digits E1009 'invalid escape in a byte literal' <<'EOF'
fn main() {
	print(int(b'\xzz'))
}
EOF

reject byte-escape-with-one-hex-digit E1009 'invalid escape in a byte literal' <<'EOF'
fn main() {
	print(int(b'\x1'))
}
EOF

reject unicode-escape-in-a-byte-literal E1009 'invalid escape in a byte literal' <<'EOF'
fn main() {
	print(int(b'\u{41}'))
}
EOF

reject unknown-escape-in-a-rune-literal E1009 'invalid escape in a rune literal' <<'EOF'
fn main() {
	print(int('\q'))
}
EOF

reject unicode-escape-with-no-digits E1009 'invalid escape in a rune literal' <<'EOF'
fn main() {
	print(int('\u{}'))
}
EOF

reject unicode-escape-without-braces E1009 'invalid escape in a rune literal' <<'EOF'
fn main() {
	print(int('\u41'))
}
EOF

reject unknown-escape-in-a-string E1009 'invalid escape in a string literal' <<'EOF'
fn main() {
	print("a\qb")
}
EOF

reject unknown-escape-in-a-triple-string E1009 'invalid escape in a string literal' <<'EOF'
fn main() {
	s := """
a\qb
"""
	print(s)
}
EOF

reject string-that-spells-a-nul E1010 <<'EOF'
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

reject compare-an-optional-with-nil E3039 'an optional is not an operand of `==`' <<'EOF'
fn main() {
	x: int? = nil
	print(f"{x == nil}")
}
EOF

reject compare-an-optional-with-a-value E3039 'an optional is not an operand of `==`' <<'EOF'
fn main() {
	x: int? = 1
	print(f"{x == 1}")
}
EOF

reject spawn-with-too-few-arguments E3027 '`work` needs 2 arguments and this gives 1' <<'EOF'
fn work(a: int, b: int) {
	print(f"{a + b}")
}

fn main() {
	spawn work(1)
}
EOF

reject defer-with-the-wrong-argument-type E3038 'argument 1 of `note` is str, and this gives int' <<'EOF'
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

reject order-an-optional E3039 'an optional is not an operand of `>`' <<'EOF'
fn main() {
	x: int? = 1
	print(f"{x > 0}")
}
EOF

reject add-to-an-optional E3039 'an optional is not an operand of `+`' <<'EOF'
fn main() {
	x: int? = 1
	print(f"{x + 1}")
}
EOF

reject and-an-optional E3039 'an optional is not an operand of `and`' <<'EOF'
fn main() {
	x: bool? = true
	print(f"{x and true}")
}
EOF

reject match-an-optional-against-a-range E3039 'an optional is not an operand of `>=`' seed-gap <<'EOF'
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

reject spawn-captures-a-borrow E3011 'is a `mut &` and cannot cross a `spawn`' <<'EOF'
fn bump(mut &n: int) {
	n = n + 1
}

fn main() {
	mut k := 1
	spawn bump(k)
	print(f"{k}")
}
EOF

reject defer-captures-a-borrow E3011 'is a `mut &` and cannot cross a `defer`' seed-gap <<'EOF'
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

reject a-default-that-does-not-fit E3009 seed-gap <<'EOF'
fn f(a: int, b: str = 1) {
	print(f"{a}{b}")
}

fn main() {
	f(2)
}
EOF

# seed-gap: zerg0 accepts this, and a call that USES the default segfaults — it emits the
# literal where a pointer goes. Recorded in src/bootstrap/README.md.
reject a-mut-ref-with-a-default E3008 seed-gap <<'EOF'
fn f(a: int, mut &b: int = 0) {
	b = a
}

fn main() {
	print("declared")
}
EOF

reject a-pattern-that-binds-too-much E3010 '`Line` carries 1 argument and this pattern binds 2' <<'EOF'
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

reject a-construction-that-gives-too-few E3010 '`Line` carries 2 arguments and this gives 1' <<'EOF'
enum Shape {
	Line(int, int)
	Dot
}

fn main() {
	s := Shape.Line(7)
	print("built")
}
EOF

reject a-pattern-that-binds-too-few E3010 '`Line` carries 2 arguments and this pattern binds 1' <<'EOF'
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

reject a-construction-that-gives-too-much E3010 '`Line` carries 1 argument and this gives 2' <<'EOF'
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

reject a-borrow-of-a-map-index E3022 <<'EOF'
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

reject store-through-a-map-index E3012 'cannot store through a map index' <<'EOF'
fn main() {
	mut d: map[str, list[int]] = {"k": [1, 2]}
	d["k"][0] = 7
	print("x")
}
EOF

reject store-through-a-call-result E3012 'cannot store through a call result' <<'EOF'
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

reject match-arms-disagree E3021 <<'EOF'
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

reject this-as-a-parameter E2013 'is a reserved word and cannot name a parameter' <<'EOF'
fn f(this: int) -> int {
	return this
}

fn main() {
	print(f(7))
}
EOF

reject this-as-a-method-parameter E2013 'is a reserved word and cannot name a parameter' <<'EOF'
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

reject this-as-a-field E2013 'is a reserved word and cannot name a struct field' <<'EOF'
struct P {
	pub this: int
}

fn main() {
	p := P(1)
	print(p.this)
}
EOF

reject this-as-a-function E2013 'is a reserved word and cannot name a function' <<'EOF'
fn this() -> int {
	return 1
}

fn main() {
	print(this())
}
EOF

reject this-as-a-type E2013 'is a reserved word and cannot name a struct' <<'EOF'
struct this {
	pub x: int
}

fn main() {
	print(1)
}
EOF

reject this-as-a-variant E2013 'is a reserved word and cannot name an enum variant' <<'EOF'
enum E {
	this
	B
}

fn main() {
	print(1)
}
EOF

reject this-as-a-pattern-binding E2013 'is a reserved word and cannot name a pattern binding' <<'EOF'
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
reject capital-this-as-a-variant E2013 'cannot name an enum variant' seed-gap <<'EOF'
enum E {
	This
	B
}

fn main() {
	print 1
}
EOF

reject capital-this-as-a-type E2013 'cannot name a struct' seed-gap <<'EOF'
struct This {
	pub x: int
}

fn main() {
	print 1
}
EOF

reject capital-this-as-a-function E2013 'cannot name a function' seed-gap <<'EOF'
fn This() {
	print 1
}

fn main() {
	This()
}
EOF

reject capital-this-as-a-parameter E2013 'cannot name a parameter' seed-gap <<'EOF'
fn f(This: int) {
	print This
}

fn main() {
	f(1)
}
EOF

# A TYPE ALIAS is a declaration like any other, and it was the one that never asked. Every
# naming position above reaches p_name and is answered there; `type X = Y` read its own
# identifier with a bare `expect`, so `type This = int` declared the self type as a synonym
# for `int` at module level while `This` inside an `impl` went on meaning the implementing
# type — the same word with two meanings in one program, which is exactly what reserving it
# is for.
reject capital-this-as-a-type-alias E2013 'cannot name a type alias' seed-gap <<'EOF'
type This = int

fn main() {
	print 1
}
EOF

reject this-outside-a-method E3068 <<'EOF'
fn f() -> int {
	return this
}

fn main() {
	print(1)
}
EOF

reject self-type-outside-an-impl E3062 <<'EOF'
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

reject mut-ref-of-an-immutable E3024 'writes back to `k`, which is not `mut`' <<'EOF'
fn bump(mut &n: int) {
	n = n + 1
}

fn main() {
	k := 1
	bump(k)
	print(f"{k}")
}
EOF

reject mut-ref-aliased E3025 <<'EOF'
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

reject mut-fn-on-an-immutable-receiver E3024 'which is a `mut fn`, writes back to `p`' <<'EOF'
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
reject mut-fn-free-function E2019 at=1:1 seed-gap <<'EOF'
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

reject compare-two-enums E3045 'cannot compare Color and Fruit' <<'EOF'
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

reject compare-an-enum-and-an-int E3045 'cannot compare Color and int' <<'EOF'
enum Color {
	Red
	Green
}

fn main() {
	c := Color.Red
	print(f"{c == 0}")
}
EOF

reject qualify-with-the-wrong-enum E4031 <<'EOF'
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

reject qualify-a-name-that-is-not-a-variant E4030 <<'EOF'
enum Color {
	Red
}

fn main() {
	c := Color.Purple
	print(f"{int(c)}")
}
EOF

# AND THE SENTENCE HAS TO BE TRUE ABOUT THE PROGRAM. E4031 used to read the FLAT variant
# table — which enum declares this bare name, program-wide, first — so with two enums that
# both declare a `Red` it said "`Red` is a variant of `Color`, not of `Fruit`" about a `Fruit`
# that has one, and the second enum's variant was unreachable. The shared name is in this case
# on purpose: it is what the rule used to answer from, and the finding is about `Apple`.
reject qualify-with-the-wrong-enum-of-two-that-share-a-name E4031 '`Apple` is a variant of `Fruit`, not of `Color`' <<'EOF'
enum Color {
	Red
	Green
}

enum Fruit {
	Red
	Apple
}

fn main() {
	c := Color.Apple
	print(f"{int(c)}")
}
EOF

# --- a place is a place all the way down, and a conversion takes a value ---------
#
# Four shapes reached cc as "cannot take the address of an rvalue": `c_is_place` asked
# about the LAST step of a path, and the map-index lowering never bound a non-place at all.
# `list[int]()` was worse — the parser indexed an empty argument list, so the COMPILER
# aborted with its own IndexError, no place and no form named.

reject convert-nothing-to-a-list E2028 <<'EOF'
fn main() {
	xs := list[int]()
	print(f"{xs.len()}")
}
EOF

reject convert-nothing-to-an-int E2026 <<'EOF'
fn main() {
	print(f"{int()}")
}
EOF

reject convert-two-values E2027 <<'EOF'
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

reject parse-a-str-as-a-bool E3065 <<'EOF'
fn main() {
	print(f"{bool("1")}")
}
EOF

reject parse-a-str-as-a-byte E3065 <<'EOF'
fn main() {
	print(f"{byte("65")}")
}
EOF

reject convert-a-list-to-an-int E4029 <<'EOF'
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
reject convert-a-struct-to-an-int E4029 <<'EOF'
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

reject index-a-str E3019 <<'EOF'
fn main() {
	s := "hello"
	print(f"{s[0]}")
}
EOF

reject slice-a-str E3019 <<'EOF'
fn main() {
	s := "hello"
	print(s[1..3])
}
EOF

# THE SAME RULE, SEEN THROUGH `assert` — and the reason the two below are here is not E3019
# at all. `assert` hoists each operand of its condition into a temporary of its own so that
# the failure message can report the value without running the condition twice, and a
# temporary is a binding in the ordinary environment: a rejected operand left it there with
# no type, every read of it came back _E3069 undefined name `zga_l3c9`_, and the reader was
# told about a name that appears nowhere in their file. One per conjunct, so an `and` with
# two bad operands answered with three messages for two mistakes.
#
# `one-finding` is the whole claim, and the assertion above about minted names is the other
# half of it: the count catches the cascade and the vocabulary catches what it said.
#
# The SEED refuses both for a reason of its own — `assert` is not a word it knows
# (src/bootstrap/README.md) — so its half of this gate is honest about the program and says
# nothing about the rule. Only `zerg`'s answer is normative here, which is what that split
# is for.
reject assert-on-a-str-index E3019 one-finding <<'EOF'
fn main() {
	s := "hello"
	assert s[0] == 104
}
EOF

reject assert-and-on-two-str-indexes E3019 one-finding <<'EOF'
fn main() {
	s := "hello"
	t := "world"
	assert s[0] == 104 and t[1] == 111
}
EOF

# A TEMPLATE SHARES THE ONE FLAT FUNCTION NAMESPACE, and it used to skip the rules that say
# so — it is removed from the program before the pass holding them runs. Every collision was
# therefore silent AND the template won, including against a module: `strconv.to_string` and a
# local `to_string[T]` are one name here, and the local one answered.

reject generic-shadows-a-plain-function E3061 <<'EOF'
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

reject generic-declared-twice E3060 <<'EOF'
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

reject int-literal-past-i64 E3018 <<'EOF'
fn main() {
	print(f"{9223372036854775808}")
}
EOF

reject int-literal-far-past-i64 E3018 <<'EOF'
fn main() {
	print(f"{99999999999999999999999}")
}
EOF

reject hex-literal-past-i64 E3018 <<'EOF'
fn main() {
	print(f"{0xFFFFFFFFFFFFFFFFF}")
}
EOF

# A RUNE's bound is not a width, so a literal meets a predicate rather than a range: `rune`
# is "a single valid Unicode code point" (docs/core/types.md), and U+D800..U+DFFF are UTF-16
# surrogates, which are not characters. The second case is the one a width test cannot see —
# 55296 fits an i32 comfortably, and no rune holds it.

reject rune-literal-past-the-last-code-point E3029 'is not a value a rune holds' <<'EOF'
fn main() {
	r: rune = 1114112
	print(f"{int(r)}")
}
EOF

reject rune-literal-inside-the-surrogates E3029 'is not a value a rune holds' <<'EOF'
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

reject impl-misses-a-required-member E3017 'does not implement `show`' seed-gap <<'EOF'
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

reject impl-misses-one-of-two E3017 'does not implement `tag`' seed-gap <<'EOF'
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

reject impl-returns-the-wrong-type E3016 'it returns str, and the spec declares int' seed-gap <<'EOF'
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

reject impl-takes-the-wrong-count E3016 'it takes 0 arguments, and the spec declares 1' seed-gap <<'EOF'
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

reject impl-renames-a-parameter E3016 'a named argument selects by that name' seed-gap <<'EOF'
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

reject impl-drops-the-by-ref E3016 'is not `mut &` and the spec' seed-gap <<'EOF'
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

reject impl-adds-a-default E3016 'has a default and the spec declares none' seed-gap <<'EOF'
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

reject impl-drops-the-mut-fn E3016 'it is not a `mut fn` and the spec' seed-gap <<'EOF'
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

reject impl-adds-a-mut-fn E3016 'it is a `mut fn` and the spec' <<'EOF'
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

reject impl-breaks-the-self-type E3016 'parameter `other` is int, and the spec declares A' seed-gap <<'EOF'
spec Same {
	fn same(other: This) -> bool
}

struct A {
	pub v: int
}

impl Same for A {
	fn same(other: int) -> bool {
		return this.v == other
	}
}

fn main() {
	print(A(1).same(1))
}
EOF

reject impl-breaks-a-spec-parameter E3016 'parameter `k` is str, and the spec declares int' <<'EOF'
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

reject super-spec-is-not-satisfied E3017 'does not implement `same`, which `Same` requires' <<'EOF'
spec Same {
	fn same() -> bool
}

spec Ranked: Same {
	fn lt() -> bool
}

struct A {
	pub v: int
}

impl Ranked for A {
	fn lt() -> bool {
		return false
	}
}

fn main() {
	print(A(1).lt())
}
EOF

reject impl-of-a-spec-that-does-not-exist E3013 <<'EOF'
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

reject a-type-declares-a-method-twice E4025 <<'EOF'
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

reject impl-does-not-bind-a-spec-parameter E3014 <<'EOF'
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

# The construction below reports E3067 as well, because this rule RECORDS now rather than
# ending the run: `A(7)` is read against the second declaration, whose `w` it leaves unset.
# The follow-on is deliberate and c_dup_say's comment says why, so this case does not pin a
# count — a later change that suppressed it would be a decision, and one this file should be
# made to state rather than absorb.
reject a-struct-declared-twice E3078 <<'EOF'
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

reject an-enum-declared-twice E3078 seed-gap <<'EOF'
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

reject a-spec-declared-twice E3078 seed-gap <<'EOF'
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

reject a-struct-and-a-spec-share-a-name E3077 'once as a struct, once as a spec' seed-gap <<'EOF'
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

reject a-struct-and-a-function-share-a-name E3077 'once as a struct, once as a function' at=5:1 <<'EOF'
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

reject a-function-and-a-struct-share-a-name E3077 'once as a struct, once as a function' at=1:1 <<'EOF'
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

reject an-enum-and-a-function-share-a-name E3077 'once as an enum, once as a function' at=5:1 <<'EOF'
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

reject a-typedef-and-a-function-share-a-name E3077 'once as a type declaration, once as a function' at=3:1 <<'EOF'
type Celsius = int

fn Celsius(n: int) -> int {
	return n
}

fn main() {
	print "ok"
}
EOF

reject a-spec-and-a-function-share-a-name E3077 'once as a spec, once as a function' at=5:1 <<'EOF'
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
reject a-generic-function-and-a-struct-share-a-name E3077 'once as a struct, once as a function' at=5:1 <<'EOF'
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
reject a-struct-and-a-module-constant-share-a-name E3077 'once as a struct, once as a module constant' at=1:1 <<'EOF'
const A := 1

struct A {
	pub v: int
}

fn main() {
	print "ok"
}
EOF

reject const-taking-a-function-name E3077 'once as a function, once as a module constant' at=1:1 <<'EOF'
const f := 1

fn f() {
	print "x"
}

fn main() {
	print "ok"
}
EOF

reject function-taking-a-const-name E3077 'once as a function, once as a module constant' at=5:1 <<'EOF'
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

reject a-field-declared-twice E4027 'declares a field named `v` twice' seed-gap <<'EOF'
struct A {
	pub v: int
	pub v: str
}

fn main() {
	print("x")
}
EOF

reject a-variant-declared-twice E4027 'declares a variant named `X` twice' seed-gap <<'EOF'
enum E {
	X
	X
}

fn main() {
	print(f"{int(E.X)}")
}
EOF

reject a-parameter-declared-twice E3063 <<'EOF'
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

reject a-one-element-tuple-type E2014 <<'EOF'
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

reject reserved-word-as-a-binding E2025 <<'EOF'
fn main() {
	this := 1
	print(f"{this}")
}
EOF

reject reserved-word-as-a-loop-binding E2013 'cannot name a loop binding' <<'EOF'
fn main() {
	xs: list[int] = [1, 2]
	for this in xs {
		print(f"{this}")
	}
}
EOF

reject reserved-word-as-an-if-let-binding E2013 'cannot name an `if let` binding' <<'EOF'
fn main() {
	o: int? = 5
	if this := o {
		print(f"{this}")
	}
	print("x")
}
EOF

reject statement-keyword-as-a-binding E2025 <<'EOF'
fn main() {
	print := 1
	print(f"{print}")
}
EOF

# --- the SELECTOR half of the same rule -------------------------------------------------
#
# `GRAMMAR#keyword` says outright that none of them can be an identifier, and `GRAMMAR#postfix`
# derives `'.' identifier` and `'.' dec-int`. Every DECLARING position above already answers
# that way; the postfix did not — it read whatever token followed the `.` as a field name and
# handed it downstream, so `x.if` was answered by the CHECKER as ``E3072 no field `if` on int``:
# a sentence that treats a reserved word as a field somebody might plausibly have declared.
#
# The four sites that read a selector — `.` and `?.`, in `parse_postfix` and in
# `parse_chain_tail` — now share one function, because a rule written at one of four slots is
# the shape of bug this repo keeps finding.
reject a-reserved-word-as-a-field E2048 '`if` is a reserved word' <<'EOF'
fn main() {
	x := 1
	print x.if
}
EOF

# THE SWALLOWED LINE, and it is why this belongs at the postfix rather than at the checker.
# A `.` suppresses ASI, so the `print` on the NEXT line was read as the field name of `1.` and
# the line vanished from the program — what got reported was `2`, on a line that is correct,
# under E2005. Two statements in, three lines down, about the wrong one.
reject a-dot-that-eats-the-next-line E2048 '`print` is a reserved word' <<'EOF'
fn main() {
	print 1.
	print 2
}
EOF

# NOT A NAME AT ALL is the rest of the production, and it went the same way: an operator or a
# literal after the `.` became a "field" whose name is `+` or `"a"`, and E3072 said no int has
# one. The seed has asked for "a field name or tuple index" since it was written and is the
# oracle for all four of these.
reject a-field-name-that-is-not-a-name E2049 'found `+`' <<'EOF'
fn main() {
	x := 1
	print x.+
}
EOF

# `?.` derives ONLY an identifier — a tuple index is on `.` alone (GRAMMAR#postfix) — and this
# read the `0` as a field, reaching the checker as ``no field `0` on (int, int)``, with no code
# and no place on it at all.
reject a-tuple-index-through-an-optional-chain E2049 'after `?.`' <<'EOF'
fn main() {
	t := (1, 2)
	p: (int, int)? = t
	print p?.0
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

reject bind-a-byte-list-to-an-int-list E3033 'cannot bind list[byte] to a list[int] binding' <<'EOF'
fn main() {
	ys: list[int] = list[byte]("Hi")
	print(f"{ys[0]}")
}
EOF

# `take(1000)` on a `byte` parameter is the CONSTANT layer of Into: `int -> byte` is a real
# conversion, so it type-checks, and then the compiler evaluates it and reports the value
# rather than emitting the truncation cc used to complain about.
reject narrow-an-int-to-a-byte E3029 '`1000` is not a value a byte holds' <<'EOF'
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

# AND THE SENTENCE MUST NOT DROP THE `?`. The payload is where the declared type lives, so
# the comparison is right to be against the `int`; what was wrong is that the label went on
# saying "the binding `x`", so the message read `the binding `x` is int` about a binding the
# reader had declared `int?` two words earlier. The slot the value is entering is the
# carrier's payload, and the label now says which carrier.
reject str-into-an-optional-binding E3036 'the int? payload of the binding `x`' <<'EOF'
fn main() {
	x: int? = "s"
	print x ?? 0
}
EOF

reject str-into-an-optional-list-element E3036 'element 1 of this list literal is int' <<'EOF'
fn main() {
	xs: list[int?] = ["a"]
	print xs.len()
}
EOF

reject str-into-an-optional-struct-field E3036 'the field `v` of `Box`' <<'EOF'
struct Box {
	pub v: int?
}

fn main() {
	b := Box("hi")
	print "ok"
}
EOF

reject str-assigned-into-an-optional E3036 '`x` is int, and this gives str' <<'EOF'
fn main() {
	mut x: int? = 1
	x = "hi"
	print x!
}
EOF

reject str-into-a-result-left E3036 "this function's answer is int, and this gives str" <<'EOF'
fn f() -> Result[int] {
	return Either.Left("hi")
}

fn main() {
	print f()!
}
EOF

reject int-into-an-either-right E3036 "this function's answer is str, and this gives int" <<'EOF'
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

reject a-channel-of-optionals-constructed E3107 <<'EOF'
fn main() {
	ch := chan[int?](1)
	print 1
}
EOF

reject a-channel-of-optionals-as-a-parameter E3107 <<'EOF'
fn f(ch: <-chan[int?]) {
	print 1
}

fn main() {
	print 2
}
EOF

reject a-channel-of-optionals-as-a-result E3107 <<'EOF'
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
reject a-channel-of-optionals-as-a-field E3107 at=2:10 <<'EOF'
struct Box {
	pub ch: chan[int?]
}

fn main() {
	print 1
}
EOF

reject a-channel-of-optionals-in-a-typed-binding E3107 <<'EOF'
fn main() {
	ch: chan[int?] = chan[int](1)
	print 1
}
EOF

reject a-channel-of-optionals-under-a-typedef E3107 <<'EOF'
type C = chan[int?]

fn main() {
	print 1
}
EOF

reject a-channel-of-optionals-inside-a-list E3107 <<'EOF'
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
reject a-channel-of-optionals-by-substitution E3107 no-place <<'EOF'
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
reject a-channel-of-optionals-by-spec-substitution E3107 no-place <<'EOF'
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

reject a-channel-of-another-element E3033 'cannot bind chan[int] to a chan[str] binding' <<'EOF'
fn main() {
	ch: chan[str] = chan[int](1)
	print 1
}
EOF

# it compiled to `zrt_chan *zg_ch = 7;`, which cc reported as an integer-to-pointer
# conversion against a line nobody wrote
reject an-int-into-a-channel E3033 'cannot bind int to a chan[int] binding' <<'EOF'
fn main() {
	ch: chan[int] = 7
	print 1
}
EOF

# --- a channel's DIRECTION, which is the other half of its type -------------------------
#
# Four rules, and until now none of them carried a code or a place — so a reader was told a
# direction was wrong and left to find which of their sends it was, and no gate could pin
# any of the four without pinning its prose. The SEED reports all four with a place, and
# numbers a call's arguments from 1 the way `E3038` does; these numbered from 0, so a reader
# following one of them looked at the wrong parameter.
#
# Two rules and not one: what a NAME MAY DO with an end (E3109-E3111) is a different question
# from where an end MAY GO (E3112), which is why the narrowing rule has a code of its own.

reject receive-on-a-send-only-channel E3109 <<'EOF'
fn take(ch: chan[int]<-) {
	print <-ch!
}

fn main() {
	c := chan[int]()
	take(c)
}
EOF

reject send-on-a-receive-only-channel E3110 <<'EOF'
fn take(ch: <-chan[int]) {
	ch <- 1
}

fn main() {
	c := chan[int]()
	take(c)
}
EOF

reject close-a-receive-only-channel E3111 <<'EOF'
fn take(ch: <-chan[int]) {
	close(ch)
}

fn main() {
	c := chan[int]()
	take(c)
}
EOF

# THE ARGUMENT IS NUMBERED FROM 1, which is what the sentence is asserted for: `take` has
# one parameter, and this used to call it argument 0.
reject a-direction-that-does-not-narrow-at-an-argument E3112 'argument 1 of `take`' <<'EOF'
fn take(ch: chan[int]<-) {
	ch <- 1
}

fn main() {
	c := chan[int]()
	r: <-chan[int] = c
	take(r)
}
EOF

reject a-direction-that-does-not-narrow-at-a-binding E3112 'binding `s`' <<'EOF'
fn main() {
	c := chan[int]()
	r: <-chan[int] = c
	s: chan[int]<- = r
	print 1
}
EOF

reject a-direction-that-does-not-narrow-at-a-return E3112 "this function's answer" <<'EOF'
fn f(c: <-chan[int]) -> chan[int]<- {
	return c
}

fn main() {
	print 1
}
EOF

reject an-int-into-a-map E3033 'cannot bind int to a map[str, int] binding' <<'EOF'
fn main() {
	m: map[str, int] = 7
	print 1
}
EOF

# a map against a map is the pairing the literal escape does NOT cover: both sides are
# maps and neither is a literal, so nothing here is taking its shape from the declaration
reject a-map-of-another-value-type E3033 'cannot bind map[str, int] to a map[str, str] binding' <<'EOF'
fn main() {
	a: map[str, int] = {"a": 1}
	b: map[str, str] = a
	print b.len()
}
EOF

# A CARRIER PASSES THROUGH OR IS INJECTED, and a carrier of another shape does neither: an
# `int?` cannot pass through into a `str?` — they are different C types — and cannot be the
# `str` payload either. It reached cc as an incompatible-pointer argument to `zrt_str_retain`.
reject an-optional-of-another-element E3033 'cannot bind int? to a str? binding' <<'EOF'
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
# ? ``, `E9053 … over a ?`, sentences about a type nothing in the source had written. Where
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

reject a-receive-of-a-receive E4043 "chan[int]? is not one" at=6:2 <<'EOF'
fn main() {
	cc := chan[chan[int]](1)
	inner := chan[int](1)
	inner <- 7
	cc <- inner
	x := <-(<-cc)
	print x ?? -1
}
EOF

reject a-receive-on-an-int E4043 "and int is not one" <<'EOF'
fn main() {
	n := 3
	x := <-n
	print x ?? -1
}
EOF

reject a-send-on-an-int E4043 "and int is not one" <<'EOF'
fn main() {
	mut n := 3
	n <- 7
	print n
}
EOF

reject a-close-on-an-int E4043 "and int is not one" <<'EOF'
fn main() {
	n := 3
	close(n)
	print n
}
EOF

reject a-select-receive-arm-on-an-int E4043 "and int is not one" <<'EOF'
fn main() {
	n := 3
	select {
		v := <-n => print v
	}
}
EOF

reject a-select-send-arm-on-an-int E4043 "and int is not one" <<'EOF'
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

reject program-without-fn-main E5001 at=1:1 <<'EOF'
x := 1
EOF

# The SAME source under `--emit lib` must stay accepted — a module is exactly a source
# with no entry point. Nothing else in the repo builds a main-less object, so the
# acceptance half of the build rule is asserted here, beside its rejection half: the
# regression this catches is the entry-point check drifting somewhere every emit stage
# passes through.
printf 'x := 1\n' >"$tmp/module-without-main.zg"
if "$ZERG" build --emit lib -o "$tmp/module-without-main.o" "$tmp/module-without-main.zg" >/dev/null 2>&1 &&
	[ -f "$tmp/module-without-main.o" ]; then
	pass=$((pass + 1))
else
	echo "LIB       module-without-main — a main-less module no longer builds with --emit lib"
	fail=$((fail + 1))
fi

# --- `-o` NAMES THE FILE WRITTEN, AT EVERY STAGE -----------------------------------
#
# It is asserted here for the reason the case above is: this file already owns the repo's
# only `-o` assertion, and no other gate runs the DRIVER rather than the language. What it
# catches is the flag meaning a different thing per stage — which is what it meant, in both
# directions. `--emit lib` appended `.o` to a path the user had spelled in full, so `-o
# out.o` wrote `out.o.o` and `out.o.o.c`; and `--emit c`, `--emit tokens` and `--emit ast`
# discarded the flag and wrote stdout, so a build that asked for a file got none, said
# nothing, and exited 0.
printf 'fn main() {\n\tprint 1\n}\n' >"$tmp/oflag.zg"

o_case() {
	local name=$1
	shift
	if "$@" >/dev/null 2>&1 && [ -s "$tmp/$name" ]; then
		pass=$((pass + 1))
	else
		echo "OUTPUT    $name — \`-o\` did not write the file it was given"
		fail=$((fail + 1))
	fi
}

o_case o-lib.o "$ZERG" build --emit lib -o "$tmp/o-lib.o" "$tmp/oflag.zg"
o_case o-emit.c "$ZERG" build --emit c -o "$tmp/o-emit.c" "$tmp/oflag.zg"
o_case o-tokens.txt "$ZERG" build --emit tokens -o "$tmp/o-tokens.txt" "$tmp/oflag.zg"
o_case o-ast.txt "$ZERG" build --emit ast -o "$tmp/o-ast.txt" "$tmp/oflag.zg"
o_case o-bin "$ZERG" build --emit bin -o "$tmp/o-bin" "$tmp/oflag.zg"

# and the suffix is NOT appended twice: `out.o.o` is what the old rule left behind.
if [ ! -e "$tmp/o-lib.o.o" ]; then
	pass=$((pass + 1))
else
	echo "OUTPUT    o-lib-double-suffix — \`--emit lib -o out.o\` wrote out.o.o"
	fail=$((fail + 1))
fi

# WITH NO `-o` the three reading stages stay on stdout, which is the pipe every other gate
# here uses: only the DEFAULT differs per stage, never what an explicit `-o` means.
if [ -n "$("$ZERG" build --emit c "$tmp/oflag.zg" 2>/dev/null)" ]; then
	pass=$((pass + 1))
else
	echo "OUTPUT    c-without-o — \`--emit c\` with no \`-o\` wrote nothing to stdout"
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

reject display-override-with-arguments E3057 seed-gap <<'EOF'
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

reject display-override-wrong-answer E3059 seed-gap <<'EOF'
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
		reject "deep-$shape-in-a-body" E2012 seed-gap

	printf 'fn deep[T](v: T) -> int {\n\treturn %s\n}\n\nfn main() {\n\tprint deep(1)\n}\n' "$e" |
		reject "deep-$shape-in-an-instantiated-generic" E2012 seed-gap

	printf 'K := %s\n\nfn main() {\n\tprint K\n}\n' "$e" |
		reject "deep-$shape-in-a-module-constant" E2012 seed-gap

	printf 'fn f(a: int = %s) -> int {\n\treturn a\n}\n\nfn main() {\n\tprint f()\n}\n' "$e" |
		reject "deep-$shape-in-a-default-parameter" E2012 seed-gap
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
	reject deep-composed-by-a-default-splice E4028 seed-gap

# --- report ------------------------------------------------------------------------

# --- the bad paths that reached cc ------------------------------------------------------
#
# Each of these compiled to C and was reported by the C compiler, against a file under
# .zerg-cache that nobody wrote — the standing rule ("lowered correctly, or refused by name",
# docs/conformance.md) breached six times in one sweep. They are written here BEFORE the rules
# that turn them away, so the sentence each one is owed is decided by what a reader needs and
# not by whatever the fix happened to produce.

reject add-two-lists E3043 'operator `+` takes numeric operands' <<'EOF'
fn main() {
	xs := [1]
	ys := xs + [2]
	print ys.len()
}
EOF

reject subtract-two-lists E3043 'operator `-` takes numeric operands' <<'EOF'
fn main() {
	xs := [1]
	ys := xs - [2]
	print ys.len()
}
EOF

reject add-two-maps E3043 'operator `+` takes numeric operands' <<'EOF'
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

reject bitwise-on-two-lists E3042 <<'EOF'
fn main() {
	xs := [1]
	ys := xs & [2]
	print ys.len()
}
EOF

reject logical-on-two-lists E3041 <<'EOF'
fn main() {
	xs := [1]
	print str(xs and [2])
}
EOF

reject negate-a-list E3049 <<'EOF'
fn main() {
	xs := [1]
	ys := -xs
	print ys.len()
}
EOF

reject complement-a-list E3050 <<'EOF'
fn main() {
	xs := [1]
	ys := ~xs
	print ys.len()
}
EOF

reject index-a-map-with-the-wrong-key E3036 'a key of this map[str, int] is str, and this gives int' <<'EOF'
fn main() {
	m := {"a": 1}
	print m[1]
}
EOF

reject call-a-binding-that-shadows-a-function E3066 <<'EOF'
fn f() -> int {
	return 1
}

fn main() {
	f := 2
	print f()
}
EOF

reject field-on-a-non-struct E3072 <<'EOF'
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
reject field-the-struct-does-not-have E3072 <<'EOF'
struct P {
	pub n: int
}

fn main() {
	p := P(1)
	print p.z
}
EOF

# --- bad paths, sweep two: tuples, slices, iteration ------------------------------------

reject tuple-index-past-its-arity E3074 <<'EOF'
fn main() {
	t := (1, 2)
	print t.5
}
EOF

reject tuple-index-on-a-non-tuple E3073 <<'EOF'
fn main() {
	n := 5
	print n.0
}
EOF

# SILENT: a str bound on a slice was accepted and lowered, so the range walked from a pointer
reject slice-with-a-str-bound E3070 <<'EOF'
fn main() {
	xs := [1, 2]
	ys := xs["a"..1]
	print ys.len()
}
EOF

# The message named the LOOP VARIABLE as undefined, which is the consequence: the loop gave it
# no type because the thing being walked is not walkable, and `x` was blamed for it.
reject for-over-a-non-iterable E3075 <<'EOF'
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
reject struct-holding-itself-by-value E4026 <<'EOF'
struct P {
	pub p: P
}

fn main() {
	print 1
}
EOF

# SILENT: the import resolved to nothing and the program ran. Using the module then reported
# "the method `thing` on a ?", which names neither the module nor the import.
#
# It carried no place until the resolver was handed the `ImportDecl` rather than the path
# string — the last of the three import rules to be told which line wrote the import. The
# other half of that old sentence, a method call whose RECEIVER is the ill-formed part, is
# now the receiver's own finding (a-method-on-a-namespace-member-that-does-not-exist below).
reject import-a-module-that-does-not-exist E5002 <<'EOF'
import "nope"

fn main() {
	print 1
}
EOF

# BOTH SHAPES AT ONE NAME IS NO MODULE, and it is refused rather than ranked. `zerg` used to
# read the file and never the directory, the seed reads the directory and never the file, and
# `docs/runtime/package.md` wrote the first down as if it were a decision. Any silent
# precedence is a question a reader eventually has to ask; there should be nothing to ask.
#
# It lives here rather than beside the other import-path cases because it is the one of them
# that is not about the STRING: the path is well formed and the disk is ambiguous, so the case
# needs two files on disk to be one.
reject a-module-that-is-both-a-file-and-a-directory E5013 'is both a file and a directory' <<'EOF'
import "./two"

fn main() {
	print two.hi()
}
--- two.zg
pub fn hi() -> int {
	return 1
}
--- two/two.zg
pub fn hi() -> int {
	return 2
}
EOF

# --- bad paths, sweep four: what `raise` takes ------------------------------------------
#
# `raise e` carries an `Err`, and `raise "…"` is the shorthand that builds one from a message
# (docs/code/errors.md). Anything else was handed to the runtime's unwind as though it were an
# Err — `raise 5` reached cc as an incompatible-type argument, and a struct the same way.

reject raise-an-int E3076 <<'EOF'
fn main() {
	raise 5
}
EOF

reject raise-a-struct E3076 <<'EOF'
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

reject written-byte-conversion-out-of-range E3029 'is not a value a byte holds' <<'EOF'
fn main() {
	print int(byte(300))
}
EOF

reject written-rune-conversion-out-of-range E3029 'is not a value a rune holds' <<'EOF'
fn main() {
	print int(rune(1114112))
}
EOF

reject written-uint-conversion-negative E3029 'is not a value a uint holds' <<'EOF'
fn main() {
	print int(uint(-1))
}
EOF

# --- bad paths, sweep six: what an assignment may be written to -------------------------
#
# An assignment needs a PLACE — a name, a field, an index. A call's result and a literal are
# values with nowhere to live, and both were rendered to the left of a C `=`.

reject assign-to-a-call E3002 <<'EOF'
fn f() -> int {
	return 1
}

fn main() {
	f() = 2
	print 1
}
EOF

reject assign-to-a-literal E3002 <<'EOF'
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

reject index-a-list-with-a-str E3071 <<'EOF'
fn main() {
	xs := [1, 2]
	print xs["a"]
}
EOF

reject index-a-list-with-a-float E3071 <<'EOF'
fn main() {
	xs := [1, 2]
	print xs[1.5]
}
EOF

reject index-a-list-with-a-bool E3071 <<'EOF'
fn main() {
	xs := [1, 2]
	print xs[true]
}
EOF

reject index-assign-a-list-with-a-str E3071 <<'EOF'
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
reject local-annotation-names-no-type E4056 '(the binding `x`)' <<'EOF'
fn main() {
	x: Nope = 5
	print 1
}
EOF

# AND IT IS THE WHOLE TYPE THAT IS ASKED. The rule above was written against the bare name
# and read the annotation with `c_named_of`, which answers "" for every type that WRAPS one —
# so `Nope` was refused and `Nope?` was emitted as `zg_Nope` for cc to report against a file
# under .zerg-cache. These are the two wrappers a local is likeliest to spell; the walk that
# answers them answers `chan[…]`, `map[…]`, a tuple and a fn type by the same recursion.
reject local-optional-annotation-names-no-type E4056 '(the binding `h`)' <<'EOF'
fn main() {
	mut h: Nope? = nil
	print 1
}
EOF

reject local-list-annotation-names-no-type E4056 '(the binding `xs`)' <<'EOF'
fn main() {
	mut xs: list[Nope] = []
	print xs.len()
}
EOF

# `handle` IS THE FFI CHAPTER'S, and docs/runtime/ffi.md is a design rather than a description
# — nothing in this compiler declares the type. It earned its own line here because it is the
# spelling that found the wrapper hole above, and because a reader who follows that chapter
# should be told the name resolves to nothing rather than be handed a cc diagnostic.
reject an-ffi-handle-annotation E4056 '(the binding `h`)' <<'EOF'
fn main() {
	mut h: handle? = nil
	print 1
}
EOF

# --- one step past each edge ------------------------------------------------------------
#
# The refusals above use comfortable numbers — 300 for a byte, 1000 for a narrowing — and a
# comfortable number cannot tell `<=` from `<`. These are the FIRST value each rule turns away,
# paired one for one with the last it admits in test-data/codegen/type_boundaries.zg. A range
# rule's defects live entirely at its ends.

reject byte-one-past-the-last E3029 'is not a value a byte holds' <<'EOF'
fn main() {
	x: byte = 256
	print int(x)
}
EOF

reject rune-one-past-the-last E3029 'is not a value a rune holds' <<'EOF'
fn main() {
	r: rune = 1114112
	print int(r)
}
EOF

reject uint-one-past-the-last E3018 <<'EOF'
fn main() {
	u: uint = 18446744073709551616
	print u
}
EOF

# AND THE FOLD'S OWN EDGE, which has two: `200 + 55` is `255` and adopts, `200 + 56` is `256`
# and does not — the answer measured after the operands, both of which fit either way.
reject fold-one-past-the-last E3029 'is not a value a byte holds' <<'EOF'
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

reject a-list-conversion-of-two-values E2029 <<'EOF'
fn main() {
	b := list[byte]("a", "b")
	print b.len()
}
EOF

reject a-module-private-name E3001 <<'EOF'
import "strings"

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
reject a-module-private-name-spawned E3001 <<'EOF'
import "./util/text"

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

reject a-module-private-name-deferred E3001 <<'EOF'
import "./util/text"

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

# THE SAME NAME, AND THE ENTRY NAMED WITH NO DIRECTORY. Every case above hands the compiler a
# path that has one, and a module is a directory — so the rule asks a file's path which
# module it is in, and for `zerg build m.zg` the answer was the empty string, which is also
# the value that means "generated, no module a reader could have written". The entry module
# collapsed into the sentinel and every visibility question went quiet: this exact program,
# spelled `./m.zg`, was refused, and spelled `m.zg`, printed 42.
reject a-module-private-name-from-a-bare-entry-path E3001 bare-entry <<'EOF'
import "./text"

fn main() {
	print text.helper()
}
--- text/text.zg
fn helper() -> int {
	return 42
}
EOF

# --- module-level `unsafe { … }`, on its `mut` binding ------------------------------------
#
# A module-level `unsafe { … }` group holds two kinds of item, and only one of them had a rule
# about who may reach it. Its `fn` is an unsafe fn and a safe caller is refused (E3083); its
# `mut` binding — the language's ONLY mutable global (GRAMMAR group 12) — could be written
# `pub`, and was then genuinely exported: another module's SAFE `main` both read `glob.shared`
# and assigned it, with no `unsafe` anywhere in that file.
#
# The grammar makes the binding module-PRIVATE, which is what these cases pin, and it is the
# whole of the crossing: without the `pub` a reader outside is refused by the ordinary
# visibility rule, so there is no second rule owed. A SAME-module safe read or write stays
# legal, deliberately — GRAMMAR says "callable only from unsafe" of the `fn` alone, and with
# `unsafe { … }` as an expression still E9011 no function body can open an unsafe context, so
# a rule there would make the group unreachable from anything a program can write.

reject a-pub-mutable-global E3108 at=2:2 <<'EOF'
unsafe {
	pub mut shared := 5
}

fn main() {
	print 1
}
EOF

# AND WITHOUT THE `pub` THE EXPORT IS GONE, which is the other half of the same claim: the
# binding is module-private, so a reader outside is refused by the ordinary visibility rule
# and there is no second rule owed for a mutable global in particular. Both directions, since
# a write is what the hole was really about.
reject a-mutable-global-read-from-another-module E3001 'module `glob`' <<'EOF'
import "./glob"

fn main() {
	print glob.shared
}
--- glob/glob.zg
unsafe {
	mut shared := 5
}

pub fn read() -> int {
	return shared
}
EOF

reject a-mutable-global-assigned-from-another-module E3001 'module `glob`' <<'EOF'
import "./glob"

fn main() {
	glob.shared = 11
	print glob.read()
}
--- glob/glob.zg
unsafe {
	mut shared := 5
}

pub fn read() -> int {
	return shared
}
EOF

# ACROSS THE MODULE BOUNDARY, which is the shape the hole was found in: the group was in
# another module and a safe `main` both read `glob.shared` and assigned it, with no `unsafe`
# anywhere in the file. The finding lands on the DECLARATION and not on either use — the
# export is what made the uses possible, and the module that wrote `pub` is the one with a
# line to change. It is here because the rule walks the unit being emitted, so an imported
# module has to be spoken about while ITS unit is compiled, not the entry's.
reject a-pub-mutable-global-in-an-imported-module E3108 'may not be `pub`' <<'EOF'
import "./glob"

fn main() {
	print glob.shared
	glob.shared = 11
}
--- glob/glob.zg
unsafe {
	pub mut shared := 5
}
EOF

# --- visibility past functions ------------------------------------------------------------
#
# `pub` was enforced on functions and module constants and on NOTHING ELSE, which left the
# three holes below. Each of them is the same sentence from docs/runtime/package.md read at a
# different declaration, and each was measured before it was closed:
#
#   a module-private TYPE was nameable from outside its module (E5008)
#   a `pub` declaration could name a type that is not `pub` (E5009)
#   a module-private FIELD was readable from outside (E5010)
#   a namespace ANY module of the build imported was nameable from EVERY module (E5007)
#
# THE SEED IS THE ORACLE FOR THE FIRST and for nothing else here — it has refused a private
# type through a qualified name since it was written (resolveTypeRef), so that one is a rule
# `zerg` LOST rather than a rule invented. The other three are seed gaps, recorded by name in
# src/bootstrap/README.md.

reject a-module-private-type-named-outside-its-module E5008 <<'EOF'
import "./lib"

fn main() {
	s: Secret = Secret("t")
	print s.tag
}
--- lib/lib.zg
struct Secret {
	pub tag: str
}

pub fn tag_of(s: str) -> str {
	return Secret(s).tag
}
EOF

# THE SAME TYPE, SPELLED THROUGH THE NAMESPACE. `lib.Secret` is rewritten to its last segment
# before anything is lowered (c_ns_unqualify), so a rule written only on the bare form would
# have left the qualified one — which is the ONLY form the seed refuses — accepted by the
# compiler the seed builds.
reject a-module-private-type-named-through-its-namespace E5008 <<'EOF'
import "./lib"

fn main() {
	s: lib.Secret = lib.Secret("t")
	print s.tag
}
--- lib/lib.zg
struct Secret {
	pub tag: str
}

pub fn tag_of(s: str) -> str {
	return Secret(s).tag
}
EOF

# A `pub` DECLARATION MAY NOT NAME A PRIVATE TYPE — "a declaration can never be more visible
# than the types it names". It is a finding in the DECLARING module, on a program that has no
# second module at all, because the mistake is the export and not any use of it.
#
# seed-gap: the seed lets a `pub fn` return a module-private struct, and a dependent then
# obtains a value of a type it could never have named.
reject a-pub-fn-that-returns-a-module-private-type E5009 'the result of `make`' seed-gap <<'EOF'
import "./lib"

fn main() {
	print lib.make().tag
}
--- lib/lib.zg
struct Secret {
	pub tag: str
}

pub fn make() -> Secret {
	return Secret("s")
}
EOF

# THE PARAMETER SIDE OF THE SAME RULE, which is not the return side read backwards: a
# dependent cannot CALL the function either, having no way to spell an argument for it.
reject a-pub-fn-that-takes-a-module-private-type E5009 'parameter `s` of `use`' seed-gap <<'EOF'
import "./lib"

fn main() {
	print lib.tag()
}
--- lib/lib.zg
struct Secret {
	pub tag: str
}

pub fn use(s: Secret) -> str {
	return s.tag
}

pub fn tag() -> str {
	return use(Secret("s"))
}
EOF

# AND THE FIELD OF A `pub` STRUCT, where the struct is on the surface and the field's type is
# not — the same leak one level in, and the case that says `decl_pub` is a fact about the pair
# rather than about the struct.
reject a-pub-field-whose-type-is-module-private E5009 'field `Box.it`' seed-gap <<'EOF'
import "./lib"

fn main() {
	print lib.tag()
}
--- lib/lib.zg
struct Secret {
	pub tag: str
}

pub struct Box {
	pub it: Secret
}

pub fn tag() -> str {
	return Box(Secret("s")).it.tag
}
EOF

# A MODULE-PRIVATE FIELD IS NOT READABLE FROM OUTSIDE, which is the other half of the rule
# E4045 already enforces: a non-`pub` field must carry a DEFAULT so external code can
# construct the type without naming a value it may not read. The default was required and the
# value was readable anyway, so the requirement protected nothing.
#
# seed-gap: the seed prints the private field's value.
reject a-module-private-field-read-outside-its-module E5010 at=5:2 seed-gap <<'EOF'
import "./lib"

fn main() {
	s := lib.make()
	print s.hidden
}
--- lib/lib.zg
pub struct Secret {
	pub tag: str
	hidden: int = 42
}

pub fn make() -> Secret {
	return Secret("s")
}
EOF

# AND IT IS NOT WRITABLE EITHER. A write reaches the field through the same lowering as a
# read (c_lvalue goes through c_expr), so one rule covers both — and a rule written on the
# assignment path alone would have covered the half nobody tries first.
reject a-module-private-field-written-outside-its-module E5010 at=5:2 seed-gap <<'EOF'
import "./lib"

fn main() {
	mut s := lib.make()
	s.hidden = 7
	print s.tag
}
--- lib/lib.zg
pub struct Secret {
	pub tag: str
	hidden: int = 42
}

pub fn make() -> Secret {
	return Secret("s")
}
EOF

# THE SAME LEAK UNDER `--emit lib`, which is a DIFFERENT WALK and not a second spelling of
# the case above. `--emit bin` emits every unit, so a dependency's declaration is reached
# while its own unit is compiled; `--emit lib` emits one unit, and a rule that reads only the
# unit being emitted goes silent about every module the entry imported. Both halves of this
# branch's declaration rules — the leak below and the private type beside it — were
# unenforced that way for a while, and a `pub fn make() -> Nonexistent` in a dependency
# reached cc as `unknown type name 'zg_Nonexistent'`: an escape to cc, which is the class
# this file exists to keep empty.
reject a-pub-fn-leaking-a-private-type-under-emit-lib E5009 'the result of `make`' emit-lib seed-gap <<'EOF'
import "./lib"

fn main() {
	print lib.make().tag
}
--- lib/lib.zg
struct Secret {
	pub tag: str
}

pub fn make() -> Secret {
	return Secret("s")
}
EOF

# A `pub` METHOD ON A MODULE-PRIVATE TYPE is the same sentence about a receiver, and the
# receiver is what makes it worth its own case: `this` is SYNTHESIZED, so a message naming it
# as a parameter names a word the author never wrote — and one this compiler refuses as a
# parameter name elsewhere (E2013). The sentence is pinned here because that is the whole
# claim.
#
# It is a real narrowing of the language: docs/runtime/package.md says a type's `pub` methods
# travel WITH it, so a `pub` method on a type that never reaches a surface promises something
# to nobody. Both the seed and this compiler accepted it before.
reject a-pub-method-on-a-module-private-type E5009 'the receiver of `shout`' seed-gap <<'EOF'
struct Secret {
	pub tag: str
}

impl Secret {
	pub fn shout() -> str {
		return this.tag
	}
}

fn main() {
	print Secret("s").shout()
}
EOF

# --- an import is not transitive ----------------------------------------------------------
#
# "importing a package gives you its public surface only, never the packages it imports in
# turn" (docs/runtime/package.md). Every module flattened into one namespace here, so the
# binding an `import` introduced belonged to the BUILD: `main` importing only `mid` could
# still write `lib.make()`, a module it never named.
#
# seed-gap: the seed binds namespaces program-wide too, and builds this.
reject a-namespace-this-module-did-not-import E5007 at=5:2 seed-gap <<'EOF'
import "./mid"

fn main() {
	print mid.relay()
	print lib.make().tag
}
--- mid/mid.zg
import "./lib"

pub fn relay() -> str {
	return lib.make().tag
}
--- lib/lib.zg
pub struct Open {
	pub tag: str
}

pub fn make() -> Open {
	return Open("s")
}
EOF

# THE SAME NAME, ONE KEYWORD EARLIER — `spawn` and `defer` resolve their callee down a path
# of their own, which is where every other rule about a namespaced call has had to be asked
# twice (see the E3001 pair above).
#
# NO `seed-gap` MARKER, AND THE SEED DOES NOT ENFORCE THIS EITHER: it refuses `spawn` outright
# ("concurrency belongs to the self-hosting compiler"), so it says no for a reason that is not
# this rule. The marker asserts its own opposite — it fails the day the seed starts refusing —
# so carrying one here would report a gap closed the first time anybody read the line, and
# what is actually pinned is the shipping compiler's second callee path.
# AND AT A TYPE POSITION, which is where the rule was first built and did NOT reach. The
# three shapes above are expressions; `c: lib.Counter` is not, and parse_base_type resolves
# `lib.Counter` to `Counter` before anything is emitted — correctly, since every module
# flattens into one scope — so the qualifier the rule asks about was already gone. Four
# positions went silent that way while three were loud, for one rule; the parser now writes
# down that a qualifier was typed (File.ty_quals) and the checker asks over the whole program.
#
# Four cases because the four are four different paths to the same qualifier: an annotation
# and a signature reach it as a TYPE, and a construction and a variant read reach it as an
# EXPRESSION that names a type.
reject a-namespace-this-module-did-not-import-in-an-annotation E5007 at=4:2 seed-gap <<'EOF'
import "./mid"

fn main() {
	c: lib.Counter = lib.Counter("a")
	print c.tag
	print mid.relay()
}
--- mid/mid.zg
import "./lib"

pub fn relay() -> str {
	return "r"
}
--- lib/lib.zg
pub struct Counter {
	pub tag: str
}
EOF

reject a-namespace-this-module-did-not-import-in-a-signature E5007 seed-gap <<'EOF'
import "./mid"

fn take(c: lib.Counter) -> str {
	return c.tag
}

fn main() {
	print take(lib.Counter("a"))
	print mid.relay()
}
--- mid/mid.zg
import "./lib"

pub fn relay() -> str {
	return "r"
}
--- lib/lib.zg
pub struct Counter {
	pub tag: str
}
EOF

reject a-namespace-this-module-did-not-import-constructing E5007 at=4:2 seed-gap <<'EOF'
import "./mid"

fn main() {
	c := lib.Counter("a")
	print c.tag
	print mid.relay()
}
--- mid/mid.zg
import "./lib"

pub fn relay() -> str {
	return "r"
}
--- lib/lib.zg
pub struct Counter {
	pub tag: str
}
EOF

# `seed-gap`, and it EARNED the marker rather than always having carried it: the seed used to
# refuse `lib.Colour.Red` with `type "Colour" used as a value` — a no about a form it did not
# read, and one it gave identically when `lib` WAS imported — so a marker here would have
# asserted its own opposite. It reads the form now (the compiler's own diagnostic registry is
# an enum one module over), and reading it is what leaves the rule about WHO IMPORTED WHAT
# unenforced, exactly as it is for every other member a namespace reaches.
reject a-namespace-this-module-did-not-import-naming-a-variant E5007 at=4:2 seed-gap <<'EOF'
import "./mid"

fn main() {
	c := lib.Colour.Red
	print int(c)
	print mid.relay()
}
--- mid/mid.zg
import "./lib"

pub fn relay() -> str {
	return "r"
}
--- lib/lib.zg
pub enum Colour {
	Red
	Green
}
EOF

reject a-namespace-this-module-did-not-import-spawned E5007 <<'EOF'
import "./mid"

fn main() {
	spawn lib.shout("a")
	print mid.relay()
}
--- mid/mid.zg
import "./lib"

pub fn relay() -> str {
	return "r"
}
--- lib/lib.zg
pub fn shout(s: str) {
	print s
}
EOF

# --- import cycles ------------------------------------------------------------------------
#
# "Import cycles between modules are rejected" (docs/runtime/package.md). Nothing detected
# one, at either layer: `ca` importing `cb` importing `ca` compiled and ran, on an
# initialization order no chapter defines.
reject an-import-cycle-between-two-modules E5014 no-place <<'EOF'
import "./ca"

fn main() {
	print ca.a_one()
}
--- ca/ca.zg
import "./cb"

pub fn a_one() -> int {
	return cb.b_one() + 1
}
--- cb/cb.zg
import "./ca"

pub fn b_one() -> int {
	return 10
}
EOF

# A MODULE THAT IMPORTS ITSELF is the one-node cycle, and it is worth its own case because a
# detector written as "have I seen this on the way down" answers it by a different branch
# than the two-node one — the `seen` list a loader already keeps for deduplication makes the
# self-edge look exactly like a module two files import.
reject a-module-that-imports-itself E5014 no-place <<'EOF'
import "./solo"

fn main() {
	print solo.one()
}
--- solo/solo.zg
import "./solo"

pub fn one() -> int {
	return 1
}
EOF

reject unsafe-group-fn-spawned E3083 'this `spawn` is in safe code' <<'EOF'
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

reject assign-to-the-receiver E3005 <<'EOF'
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

reject a-spec-extending-no-spec E3015 <<'EOF'
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

reject an-if-expression-with-two-types E3020 <<'EOF'
fn main() {
	x := if true { 1 } else { "s" }
	print x
}
EOF

# the borrow reaches `this` THROUGH a field, so the method mutates its receiver without
# saying `mut fn` — the half of the rule that is not about the argument at all.
reject a-borrow-of-a-field-of-an-immutable-receiver E3023 <<'EOF'
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
reject divide-by-a-constant-zero E3030 seed-gap <<'EOF'
fn main() {
	x := 1 / 0
	print x
}
EOF

reject a-fold-past-int-measured-against-a-byte E3031 <<'EOF'
fn main() {
	x: byte = 9223372036854775807 * 2
	print int(x)
}
EOF

reject a-binding-of-nil E3034 <<'EOF'
fn main() {
	x := nil
	print 1
}
EOF

# --- nil is not a value ------------------------------------------------------------
#
# Two forms ANSWER nil and neither says so on its face: a `fn` with no `-> type`
# (GRAMMAR#fn-decl) and a block whose last statement is not an expression (GRAMMAR#block).
# A TYPED position already judged both; the inference path had no rule at all, so all of
# these reached cc — as `variable has incomplete type 'void'`, as `initializing 'int64_t'
# with an expression of incompatible type 'void'`, and as a `void` list element and struct
# field. The sentence tells the pair apart from the container pair.

reject a-binding-inferred-from-a-void-call E3086 'and this one is nil' <<'EOF'
fn f() {
	print "f"
}

fn main() {
	x := f()
	print 1
}
EOF

reject a-binding-inferred-from-a-valueless-block E3086 'and this one is nil' <<'EOF'
fn main() {
	z := {
		nop
	}
	print 1
}
EOF

reject printing-a-void-call E3086 '`print` needs a value' <<'EOF'
fn f() {
	print "f"
}

fn main() {
	print f()
}
EOF

reject an-f-string-hole-holding-nil E3086 'this rendering needs a value' <<'EOF'
fn f() {
	print "f"
}

fn main() {
	print(f"{f()}")
}
EOF

reject a-list-of-a-void-call E3086 'a part of this one is nil' <<'EOF'
fn f() {
	print "f"
}

fn main() {
	xs := [f()]
	print xs.len()
}
EOF

reject a-tuple-holding-a-void-call E3086 'a part of this one is nil' <<'EOF'
fn f() {
	print "f"
}

fn main() {
	t := (f(), 1)
	print t.1
}
EOF

# --- the top level runs nothing ----------------------------------------------------
#
# `program ::= stmt-list` (GRAMMAR#program) is script mode, and a compiled program has no
# moment at which to run a statement — outside `main` lives only immutable state readied
# before it (docs/runtime/package.md). All three used to be parsed and then DROPPED: the
# program built, and printed nothing.
#
# The seed drops them too, which is the narrower compiler being narrower; its README says so.

reject a-print-at-the-top-level E3087 '`print` opens a statement' at=1:1 seed-gap <<'EOF'
print 999

fn main() {
	print 1
}
EOF

reject an-if-at-the-top-level E3087 '`if` opens a statement' at=1:1 seed-gap <<'EOF'
if true {
	print 1
}

fn main() {
	print 2
}
EOF

reject a-loop-at-the-top-level E3087 '`for` opens a statement' at=1:1 seed-gap <<'EOF'
for {
	break
}

fn main() {
	print 1
}
EOF

reject an-if-on-an-optional E3052 <<'EOF'
fn main() {
	x: int? = 1
	if x {
		print 1
	}
}
EOF

reject a-display-that-mutates E3058 <<'EOF'
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

reject a-struct-literal-missing-a-field E3067 <<'EOF'
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
reject a-required-field-before-a-defaulted-one E3067 'the field `w` of `Box`' <<'EOF'
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
reject a-private-field-with-no-default E4045 '`m` of `Q`' <<'EOF'
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
reject a-private-field-beside-an-optional E4045 '`m` of `R`' <<'EOF'
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

reject an-undefined-name E3069 <<'EOF'
fn main() {
	print nosuchname
}
EOF

# THE STR BRIDGES UNDER THEIR OWN NAMES. `bytearray` is `list[byte]` and `runearray` is
# `list[rune]`, so each converts exactly one value — a name that IS a type is a conversion,
# not a constructor, and `[]` is what builds an empty list.
reject a-bytearray-of-nothing E2033 <<'EOF'
fn main() {
	b := bytearray()
	print b.len()
}
EOF

reject a-bytearray-of-two-values E2034 <<'EOF'
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
# It is E4032 rather than a rule of its own, and that is the point: this is refused for what
# the arms DO and not for how the first letter is typed. The rule that used to stand here
# read the capital instead, so `n := 3; match n { 1 => …  Zzz => … }` was refused with
# "`Zzz` is a variant of some enum" in a program that declared no enum at all.
reject a-bare-name-pattern-covers-the-arms-below E4032 <<'EOF'
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
reject a-bare-variant-value E3079 seed-gap <<'EOF'
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
reject too-many-arguments-through-a-fn-value E3082 <<'EOF'
fn apply(f: fn (int) -> int, v: int) -> int {
	return f(v, 1)
}

fn main() {
	print apply(fn (x: int) -> int { return x }, 1)
}
EOF

reject too-few-arguments-through-a-fn-value E3082 <<'EOF'
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
# was a parser NotImplemented while the carve-out was unbuilt; now the form is read,
# and this is what is left of it: a rule, with a place, at the closure.
reject a-closure-with-no-position-to-type-it E3081 <<'EOF'
fn main() {
	f := fn (x) { return x + 1 }
	print f(1)
}
EOF

reject a-bare-either-side E3080 <<'EOF'
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
reject equality-on-a-carrier E4038 <<'EOF'
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
	reject "a-coalesce-${kw}-with-a-guard" E2044 "a \`??\` right-hand \`${kw}\` takes no trailing \`if\`" <<EOF
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

reject a-coalesce-raise-with-a-guard E2044 'a `??` right-hand `raise` takes no trailing `if`' <<'EOF'
fn get() -> int? {
	return nil
}

fn main() {
	p := get()
	q := p ?? raise ValueError("x") if false
	print q
}
EOF

reject a-coalesce-return-with-a-guard E2044 'a `??` right-hand `return` takes no trailing `if`' <<'EOF'
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
reject a-coalesce-bare-return-with-a-guard E2044 'a `??` right-hand `return` takes no trailing `if`' <<'EOF'
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

reject a-discriminant-that-names-a-non-constant E4039 'the discriminant of `E.A`' <<'EOF'
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

reject a-discriminant-that-calls E4039 'the discriminant of `E.B`' <<'EOF'
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

reject a-fill-count-read-at-run-time E4040 <<'EOF'
fn size() -> int {
	return 3
}

fn main() {
	n := size()
	xs := [0; n]
	print xs.len()
}
EOF

reject a-fill-count-that-calls E4040 <<'EOF'
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
reject a-negative-fill-count E4041 <<'EOF'
fn main() {
	xs := [0; -1]
	print xs.len()
}
EOF

reject a-range-bound-read-at-run-time E4042 '`lo`' <<'EOF'
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
reject a-one-tuple E2045 <<'EOF'
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
reject a-trailing-comma-in-a-tuple E2046 'the closing `)` of a tuple literal' <<'EOF'
fn main() {
	t := (1, 2,)
	print t.0
}
EOF

reject a-trailing-comma-in-a-list E2046 'the closing `]` of a list literal' <<'EOF'
fn main() {
	xs := [1, 2, ]
	print xs.len()
}
EOF

reject a-trailing-comma-in-a-map E2046 'the closing `}` of a map literal' <<'EOF'
fn main() {
	m := {"a": 1,}
	print m.len()
}
EOF

reject a-trailing-comma-in-an-argument-list E2046 'the closing `)` of an argument list' <<'EOF'
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
reject a-spec-type-parameter-list-without-a-comma E2004 'expected `,`' <<'EOF'
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

reject a-trailing-comma-in-a-spec-type-parameter-list E2046 "the closing \`]\` of a spec's type parameter list" <<'EOF'
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

reject a-type-parameter-list-without-a-comma E2004 'expected `,`' <<'EOF'
fn f[T U](a: T, b: U) -> int {
	return 1
}

fn main() {
	print(f(1, 2))
}
EOF

reject a-trailing-comma-in-a-type-parameter-list E2046 'the closing `]` of a type parameter list' <<'EOF'
fn f[T,](a: T) -> int {
	return 1
}

fn main() {
	print(f(1))
}
EOF

# The type ARGUMENT list, which is reached only through a built-in type's own arguments:
# a program's own `f[int, str](…)` is refused as a form before the list is read (E2035), so
# `map[K, V]` is where this loop still runs.
reject a-trailing-comma-in-a-type-argument-list E2046 'the closing `]` of a type argument list' <<'EOF'
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
reject a-brace-opening-if-head E2047 'the start of an `if` head' <<'EOF'
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
reject a-range-bound-shadowed-by-a-local E4042 '`lo`' <<'EOF'
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
reject a-range-bound-that-calls E2004 'found `..`' <<'EOF'
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

reject a-brace-opening-for-head E2047 'the start of a `for` head' <<'EOF'
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
reject a-map-entry-with-no-colon E2065 'a map entry is `key: value`' at=2:30 <<'EOF'
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

reject an-if-let-bind-in-the-else E3069 'undefined name `x`' at=9:3 <<'EOF'
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
reject an-if-let-bind-in-an-else-if-head E3069 'undefined name `x`' at=8:9 <<'EOF'
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
reject an-if-let-bind-in-the-final-else-of-a-chain E3069 'undefined name `x`' at=15:3 <<'EOF'
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
reject an-if-let-bind-shadowed-in-a-nested-else E3069 'undefined name `x`' at=16:4 <<'EOF'
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

reject a-select-arm-bind-after-the-select E3069 'undefined name `v`' at=12:2 <<'EOF'
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

reject a-select-arm-bind-in-a-later-arm E3069 'undefined name `v`' at=13:15 <<'EOF'
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

# --- `del` revokes a NAME, and it has to have one to revoke ------------------------------
#
# GRAMMAR#del-stmt is `'del' identifier`, and docs/core/memory.md says what the statement
# means: it revokes THAT NAME's access to its storage. Both halves of that sentence are a
# rule, and neither was checked.
#
# The name half: `del` looked the name up, and where nothing answered it revoked nothing and
# said nothing — so `del totally_undefined` compiled and ran. The same spelling one line
# further down, in a `print`, is `E3069`; the difference was that the read path asks and the
# `del` path did not. A function name is the same finding with a different reason: `g` names
# something, and what it names has no storage a name can be revoked from.
#
# The storage half is the `mut &` parameter, which is the one binding whose reads never went
# past the liveness check — a mutable reference is a pointer and is dereferenced on use, and
# that dereference returned BEFORE the check every other read funnels through. So `del x` on
# one revoked nothing observable: the parameter carried on reading and, worse, carried on
# WRITING THROUGH to the caller's variable.
#
# The two `used after del` findings are here for a second reason as well. Both are rules the
# compiler genuinely CHECKS, and both reported with neither a code nor a place — two of the
# code-less sites docs/conformance.md counts — so nothing could pin either, and a case
# asserting the sentence alone would have gone green against a message that says where
# nothing.

reject a-del-of-an-undefined-name E3103 <<'EOF'
fn main() {
	del totally_undefined
	print "unreached"
}
EOF

reject a-del-of-a-function-name E3104 <<'EOF'
fn g() {
	print "g"
}

fn main() {
	del g
}
EOF

reject a-read-after-del E3105 '`x` is used after del' <<'EOF'
fn main() {
	x := 1
	del x
	print x
}
EOF

reject a-write-after-del E3105 '`x` is used after del' <<'EOF'
fn main() {
	mut x := 1
	del x
	x = 9
	print "unreached"
}
EOF

reject a-read-after-del-on-a-mutable-reference E3105 '`x` is used after del' <<'EOF'
fn f(mut &x: int) -> int {
	del x
	return x
}

fn main() {
	mut a := 1
	print f(a)
}
EOF

reject a-write-after-del-on-a-mutable-reference E3105 '`x` is used after del' <<'EOF'
fn f(mut &x: int) {
	del x
	x = 9
}

fn main() {
	mut a := 1
	f(a)
	print a
}
EOF

reject a-read-after-del-on-some-paths E3106 'used after del on some paths' <<'EOF'
fn main() {
	x := 1
	if x == 1 {
		del x
	}
	print x
}
EOF

# --- the parser's channel: the rules that used to raise with no code ------------------
#
# Every one of these was refused before this list existed, and every one said only a
# sentence: no code, so no case here could pin it, and no place, so the reader was told what
# and never where. They are the rules that fell out of the split docs/conformance.md names —
# a refusal carried a code only where its author had written one into the string — and they
# are cases now because the parser reports through one channel that carries both.

reject a-function-name-that-is-not-a-name E2054 'a function needs a name' <<'EOF'
fn 1() {
	print "a"
}
EOF

reject a-binding-name-that-is-not-a-name E2054 'a binding needs a name' <<'EOF'
fn main() {
	5 := 1
	print 5
}
EOF

reject a-receive-arrow-on-something-that-is-not-a-channel E2055 <<'EOF'
fn f(c: <-int) {
	print "a"
}

fn main() {
	f(1)
}
EOF

reject mut-before-something-that-is-not-a-method E2056 <<'EOF'
struct S {
	pub a: int
}

impl S {
	mut a := 1
}

fn main() {
	print S(1).a
}
EOF

reject is-against-something-that-is-not-a-type E2057 <<'EOF'
fn main() {
	e := Err("x")
	if e is 3 {
		print "y"
	}
}
EOF

reject an-f-string-whose-literal-text-is-malformed E2058 <<'EOF'
fn main() {
	print f"\q"
}
EOF

reject an-f-string-hole-holding-two-expressions E2059 <<'EOF'
fn main() {
	print f"{1 2}"
}
EOF

# A DECLARED TYPE'S NAME BEGINS WITH AN UPPER-CASE LETTER (GRAMMAR#type-ident). The case of
# that letter is how the language separates its two namespaces, and two of the three readers
# are in the PARSER — `cli.Opt` the module qualifier against `It.Item` the associated-type
# projection, and a bound naming a spec against one naming a concrete type — where nothing
# is resolved and there is no table to consult. So a lower-case type name cannot be made to
# work by teaching the constructor alone: it would be legal in one position and misread in
# three.
#
# It used to be legal in NONE and said so wrongly. `_Box(1)` on a declared `struct _Box`
# answered `E4016 undefined function`, which is false about a program that declares the type
# eight lines up — the constructor dispatch is `name_is_type`, and `_` is not upper-case.
# The leading underscore needs no rule of its own: `_` has no case, so a name that starts
# with one is in neither namespace, which is exactly what this asks about.
#
# The seed resolves the name against its symbol table and builds all three (`seed-gap`).
reject a-struct-named-with-a-leading-underscore E2060 '`_Box` cannot name a struct' seed-gap <<'EOF'
struct _Box {
	pub v: int
}

fn main() {
	b := _Box(1)
	print b.v
}
EOF

reject a-struct-named-in-lower-case E2060 '`lower` cannot name a struct' seed-gap <<'EOF'
struct lower {
	pub v: int
}

fn main() {
	b := lower(1)
	print b.v
}
EOF

reject an-enum-named-with-a-leading-underscore E2060 '`_E` cannot name an enum' seed-gap <<'EOF'
enum _E {
	A
}

fn main() {
	print int(_E.A)
}
EOF


# --- the emitter's own rules, one case per code -------------------------------------------
#
# Every refusal emit.zg makes reports through c_diag now, which takes the code as an argument
# and reads the place off the walk, so each of these asserts the rule's IDENTITY rather than
# its wording. Half of them had no code at all before that change and were pinned, where they
# were pinned at all, by a sentence.

reject a-bare-value-that-is-neither-side-of-an-either E4053 <<'EOF'
fn g(r: Either[int, str]) -> int {
	return 1
}

fn main() {
	print g(true)
}
EOF

reject an-optional-chain-reading-a-field-that-is-not-there E4054 <<'EOF'
struct P {
	pub v: int
}

fn main() {
	p: P? = P(1)
	print p?.zz
}
EOF

reject unwrap-a-value-that-carries-nothing E9080 <<'EOF'
fn f() -> int? {
	x := 1
	return x?
}

fn main() {
	print f() ?? 0
}
EOF

reject propagate-a-right-the-enclosing-function-does-not-answer E4055 <<'EOF'
fn g() -> int? {
	return nil
}

fn f() -> Result[int] {
	v := g()?
	return Left(v)
}

fn main() {
	print 1
}
EOF

reject two-modules-defining-one-public-function E9081 seed-gap <<'EOF'
import (
	"./ma"
	"./mb"
)

fn main() {
	print ma.work() + mb.work()
}
--- ma/ma.zg
pub fn work() -> int {
	return 1
}
--- mb/mb.zg
pub fn work() -> int {
	return 2
}
EOF

# TWO FILES of one module declaring a name. Two DIFFERENT modules each declaring a private one
# is legal — they take a module tag in C — so what this pins is the collision no tag can
# separate. The sentence is pinned because the case below it shares the shape and not the rule:
# what E9082 has to say here is that there are two FILES, which is the thing this compiler
# cannot tell from two modules and used to assert it could.
reject two-files-of-one-module-declaring-a-function E9082 'both define `work`' <<'EOF'
import "./one"

fn main() {
	print one.first()
}
--- one/a.zg
fn work() -> int {
	return 1
}

pub fn first() -> int {
	return work()
}
--- one/b.zg
fn work() -> int {
	return 2
}
EOF

# ONE FILE declaring a name twice, which is the other half of the same collision and is a
# different RULE: E9082 is a `NotImplemented` the package layer retires, and this is refused by
# every compiler there will ever be. It was reported as E9082 — "two modules both define
# `test_same`" about two `#[test]` functions in one file — and a reader following that sentence
# goes looking for a second module.
#
# The sentence is pinned on the half no place carries: the marker points at the SECOND
# declaration, so the first one's line is the thing the reader cannot see from the caret.
reject one-file-declaring-a-function-twice E4073 'the first is at line 1' <<'EOF'
fn work() -> int {
	return 1
}

fn work() -> int {
	return 2
}

fn main() {
	print work()
}
EOF

# AND THE CONSTANT, which is the rule's other walk over its other table. The two are one rule
# and two tables (emit.zg says so where the wording lives), so a case on the function alone
# pins half of it — and the constant half was the half that used to LINK rather than refuse.
reject one-file-declaring-a-constant-twice E4073 'the first is at line 1' seed-gap <<'EOF'
const N := 1
const N := 2

fn main() {
	print N
}
EOF

reject a-parameter-typed-by-a-name-no-declaration-carries E4056 <<'EOF'
fn f(x: Zork) -> int {
	return 1
}

fn main() {
	print f(1)
}
EOF

reject force-a-value-that-carries-nothing E9083 <<'EOF'
fn main() {
	x := 1
	print x!
}
EOF

reject coalesce-a-value-that-carries-nothing E9084 <<'EOF'
fn main() {
	x := 1
	print x ?? 2
}
EOF

reject an-is-test-on-something-that-is-not-an-err E4057 <<'EOF'
fn main() {
	x := 1
	b := x is IOError
	print b
}
EOF

reject an-in-test-on-something-that-is-not-an-err E4058 <<'EOF'
fn main() {
	x := 1
	b := x in IOError
	print b
}
EOF

reject convert-a-list-into-a-list E4059 <<'EOF'
fn main() {
	xs := [1, 2]
	print list[byte](xs).len()
}
EOF

reject bridge-a-str-to-a-list-of-something-that-is-not-a-byte E4060 <<'EOF'
fn main() {
	s := "ab"
	print list[int](s).len()
}
EOF

reject render-an-enum-as-text E9085 <<'EOF'
enum Color {
	Red
	Green
}

fn main() {
	c := Color.Red
	print str(c)
}
EOF

reject index-something-that-is-neither-a-list-nor-a-map E4061 <<'EOF'
fn main() {
	x := 1
	print x[0]
}
EOF

reject an-err-method-given-an-argument E9086 <<'EOF'
fn main() {
	r := guard {
		raise "boom"
	}
	match r {
		Right(e) => { print e.message(1) }
		_ => { print 0 }
	}
}
EOF

reject a-method-the-error-interface-does-not-declare E9087 <<'EOF'
fn main() {
	r := guard {
		raise "boom"
	}
	match r {
		Right(e) => { print e.reason() }
		_ => { print 0 }
	}
}
EOF

reject ok-or-with-no-error-to-answer-with E9088 <<'EOF'
fn g() -> int? {
	return nil
}

fn main() {
	r := g().ok_or()
	print 1
}
EOF

reject ok-or-with-two-errors-to-answer-with E9089 <<'EOF'
fn g() -> int? {
	return nil
}

fn main() {
	r := g().ok_or(IOError("a"), IOError("b"))
	print 1
}
EOF

reject ok-or-answering-an-absence-with-something-that-is-not-an-err E9090 <<'EOF'
fn g() -> int? {
	return nil
}

fn main() {
	r := g().ok_or(1)
	print 1
}
EOF

reject ok-given-an-argument E9091 <<'EOF'
fn g() -> Result[int] {
	return Left(1)
}

fn main() {
	r := g().ok(1)
	print 1
}
EOF

reject a-carrier-method-neither-carrier-answers E9092 <<'EOF'
fn g() -> int? {
	return nil
}

fn main() {
	print g().unwrap_or(1)
}
EOF

reject an-enum-type-method-that-is-not-of E9093 <<'EOF'
enum Color {
	Red
	Green
}

fn main() {
	c := Color.pick(1)
	print 1
}
EOF

reject reverse-a-discriminant-with-two-arguments E4062 <<'EOF'
enum Color {
	Red
	Green
}

fn main() {
	c := Color.of(1, 2)
	print 1
}
EOF

reject a-method-on-a-value-whose-type-declares-none E9094 <<'EOF'
fn main() {
	x := 1
	print x.wobble()
}
EOF

reject construct-the-end-of-stream-sentinel E4063 <<'EOF'
fn main() {
	e := StopIteration("x")
	print 1
}
EOF

reject construct-a-name-no-declaration-carries E4064 <<'EOF'
fn main() {
	print Zork(1)
}
EOF

reject a-constructor-pattern-naming-no-variant-of-the-subject E4065 <<'EOF'
enum Color {
	Red
	Green
}

fn main() {
	c := Color.Red
	match c {
		Nope(v) => { print 1 }
		_ => { print 2 }
	}
}
EOF

reject a-match-over-an-either-naming-one-side E4066 <<'EOF'
fn g() -> Result[int] {
	return Left(1)
}

fn main() {
	match g() {
		Left(v) => { print v }
	}
}
EOF

reject a-match-over-a-bool-naming-one-value E4067 <<'EOF'
fn main() {
	b := true
	match b {
		true => { print 1 }
	}
}
EOF

reject a-constructor-pattern-on-an-either-that-is-neither-side E9095 <<'EOF'
fn g() -> Result[int] {
	return Left(1)
}

fn main() {
	match g() {
		Nope(v) => { print 1 }
		_ => { print 0 }
	}
}
EOF

reject two-constants-that-name-each-other E4068 <<'EOF'
const A := B + 1
const B := A + 1

fn main() {
	print A
}
EOF

reject an-entry-answering-something-that-is-neither-int-nor-result E9096 <<'EOF'
fn main() -> str {
	return "a"
}
EOF

reject main-with-args-in-a-program-that-uses-concurrency E9097 <<'EOF'
fn main(args: list[str]) {
	ch := chan[int](1)
	ch <- 1
	print <-ch ?? 0
}
EOF

reject a-closure-capturing-a-name-with-no-type E4069 <<'EOF'
fn main() {
	f := fn () -> int {
		return zz
	}
	print f()
}
EOF

reject spawn-of-something-that-is-not-a-call E9098 <<'EOF'
fn main() {
	x := 1
	spawn x
}
EOF

reject spawn-of-a-method-the-receiver-does-not-declare E9099 <<'EOF'
struct P {
	pub v: int
}

fn main() {
	p := P(1)
	spawn p.nope()
}
EOF

# A `spawn` AND A `defer` NEVER ASKED WHETHER THE CALLEE IS A FUNCTION. Visibility and the
# `unsafe` rule are read off a signature ROW and a missing row is deliberately quiet, so a
# name nothing declares asked two rules with nothing to say and went straight on to spell
# `zg_<name>()` inside the thunk — reported by cc, against a file under .zerg-cache. The
# ordinary call has answered by name since `E4016` existed; these are the two keywords that
# reached the same emitter down a path of their own.
reject spawn-of-a-function-nothing-declares E4016 <<'EOF'
fn main() {
	spawn nosuchfn()
}
EOF

reject defer-of-a-function-nothing-declares E4016 <<'EOF'
fn main() {
	defer nosuchfn()
}
EOF

# and the same path missed the SHADOWING half of the question, which the ordinary call
# answers with `E3066`: the innermost binding wins, so a `defer x()` under an `x := 1` is a
# call that cannot happen rather than a call to the function of that name.
reject defer-of-a-binding-that-holds-an-int E3066 'is not callable' <<'EOF'
fn main() {
	x := 1
	defer x()
}
EOF

reject map-len-given-an-argument E4070 <<'EOF'
fn main() {
	m := {1: 2}
	print m.len(1)
}
EOF

reject map-has-given-no-key E4071 <<'EOF'
fn main() {
	m := {1: 2}
	print m.has()
}
EOF

reject a-map-method-this-compiler-does-not-have E9100 <<'EOF'
fn main() {
	m := {1: 2}
	print m.drop(1)
}
EOF

reject a-field-an-err-does-not-carry E9101 <<'EOF'
fn main() {
	r := guard {
		raise "boom"
	}
	match r {
		Right(e) => { print e.reason }
		_ => { print 0 }
	}
}
EOF

reject a-list-method-this-compiler-does-not-have E9056 <<'EOF'
fn main() {
	xs := [1, 2]
	xs.pop()
	print xs.len()
}
EOF


# --- the shapes a declaration's TYPE is written in -----------------------------------------
#
# Each of the four rules below already had a case, and every one of them stood in a position
# whose place the emitter reads from a FnDecl: a parameter, a result, an impl receiver. The
# two positions with no function behind them — a struct field and an enum payload — reported
# at `--> 0:0`, and no case here was in a shape that could see it. The rule is one rule; the
# case list is what decides which of its positions is exercised.

reject a-generic-whose-written-type-arguments-outnumber-its-parameters E4072 seed-gap <<'EOF'
fn set[T](a: T) -> int {
	return 1
}

fn main() {
	print set[int, str](1)
}
EOF

reject an-inclusive-range-arm-whose-upper-bound-is-nil E9102 seed-gap <<'EOF'
fn main() {
	x := 3
	match x {
		1..=nil => { print 1 }
		_ => { print 0 }
	}
}
EOF

reject a-struct-field-typed-by-a-name-no-declaration-carries E4056 '(field `A.v`)' <<'EOF'
struct A {
	pub v: Zork
}

fn main() {
	print 1
}
EOF

reject an-enum-payload-typed-by-a-name-no-declaration-carries E4056 '(payload 0 of `E.A`)' <<'EOF'
enum E {
	A(Zork)
	B
}

fn main() {
	print 1
}
EOF

reject a-spec-used-as-a-struct-field-type E9048 '(field `A.v`)' seed-gap <<'EOF'
spec Tag {
	fn tag() -> int
}

struct A {
	pub v: Tag
}

fn main() {
	print 1
}
EOF

reject the-self-type-as-a-struct-field E3062 'field `A.v` is outside an `impl`' <<'EOF'
struct A {
	pub v: This
}

fn main() {
	print 1
}
EOF

# --- the prelude's names, and what a test file is on the surface of ------------------
#
# A PRELUDE NAME IS TAKEN BEFORE THE PROGRAM IS READ, so a declaration cannot have one
# (docs/runtime/package.md). Each case here is `at=1:8` or `at=1:4`, because the whole claim
# is that the refusal lands on the DECLARATION: `struct list` used to be accepted and the
# complaint arrived at the first `list(1)` after it, as `E4016 undefined function` — a
# sentence that is false about a program that does declare one.
#
# The three kinds are here rather than one because the message names the slot, and a slot
# that stopped asking would leave the other two green.
#
# A TYPE DECLARATION'S NAME IS ASKED TWICE, and the struct case is written with an UPPER-CASE
# prelude name so that this half of it is what answers. The case below is the other half.
reject a-prelude-name-names-a-struct E2061 'cannot name a struct' at=1:8 <<'EOF'
struct Either {
	pub n: int
}

fn main() {
	print 1
}
EOF

# AND `struct list` IS BOTH — a prelude name and a lower-case one — so it is the case that
# pins WHICH of the two rules answers. The case rule does, and that is a decision rather than
# an accident: PascalCasing a lower-case prelude name leaves the prelude alone (`List` is a
# name the reserved set does not hold, and so is every other one of them), so `E2060`'s advice
# clears both rules in one edit where `E2061`'s — pick another name — says nothing about the
# letter and lets the second attempt be refused again for a reason the first never mentioned.
# Swap the two and this case reports `E2061`.
reject a-lower-case-prelude-name-at-a-type-declaration E2060 'begins with an UPPER-CASE LETTER' at=1:8 <<'EOF'
struct list {
	pub n: int
}

fn main() {
	print 1
}
EOF

reject a-prelude-name-names-a-spec E2061 'cannot name a spec' at=1:6 <<'EOF'
spec Eq {
	fn eq(o: This) -> bool
}

fn main() {
	print 1
}
EOF

reject a-prelude-name-names-a-function E2061 'cannot name a function' at=1:4 <<'EOF'
fn int() -> int {
	return 1
}

fn main() {
	print 1
}
EOF

# THE FUNCTION SLOT TAKES A NARROWER SET THAN THE TYPE SLOTS, and `map` is the whole of the
# difference. A type declaration's name lands in the namespace every prelude name is bound
# in; a function's lands where only the ones a CALL can spell are, and `map[…](…)` as a
# constructor is built by neither compiler — so `fn map(xs, f)` takes nothing.
#
# It is pinned here, in the reject list, because the claim is about WHICH rule answers: this
# program is ill-formed for a reason six lines below the declaration, and if `map` were
# reserved at this slot the answer would be `E2061` at `1:4` and this case would never reach
# `nope`. Swap `map` for `list` — the other container, and one a call CAN spell through
# `list[byte](s)` — and that is exactly what happens.
reject a-prelude-name-with-no-call-form-may-name-a-function E4016 'undefined function' at=6:2 <<'EOF'
fn map(zz: int) -> int {
	return zz + 1
}

fn main() {
	print nope(1)
}
EOF

# THE CARRIER'S CONSTRUCTORS ARE IN THE SET for the same reason its type name is: the emitter
# reads `Left` and `Right` BY NAME — an arity rule, a tag, and the match-exhaustiveness rule
# that says which side an arm covers — so a declaration taking one leaves those rules reading
# a name the program means something else by.
reject a-prelude-name-names-an-enum E2061 'cannot name an enum' at=1:6 <<'EOF'
enum Left {
	A
}

fn main() {
	print 1
}
EOF

# A TEST FILE IS ON NO MODULE'S SURFACE. `*_test.zg` is the build tool's convention and a
# normal build compiles none of them (docs/runtime/package.md), so this pair is what the
# exclusion looks like from a program: the name is not there, and naming the FILE is refused
# where it is written rather than resolved to an empty module.
#
# The positive half — that a module with a test file beside it still builds, and builds
# without it — is examples/1g/testfile, because a program that must BUILD cannot be written
# in this script.
reject a-member-a-test-file-declares E3084 'has no `only_in_test`' <<'EOF'
import "./lib"

fn main() {
	print lib.only_in_test()
}
--- lib/lib.zg
pub fn hello() -> int {
	return 1
}
--- lib/lib_test.zg
pub fn only_in_test() -> int {
	return 42
}
EOF

reject an-import-that-names-a-test-file E5011 at=1:8 <<'EOF'
import "./lib/lib_test"

fn main() {
	print lib_test.only_in_test()
}
--- lib/lib_test.zg
pub fn only_in_test() -> int {
	return 42
}
EOF

if [ $fail -ne 0 ]; then
	echo "reject-check: $fail case(s) the compiler did not reject by itself"
	exit 1
fi
echo "reject-check: $pass ill-formed programs rejected by the compiler, none left to cc"
