#!/usr/bin/env bash
#
# lint-check — every rule in the linter has a program here that MAKES IT FIRE.
#
# `make lint` runs the linter over the compiler and the stdlib, and they are clean. That is
# the point of running it — and it means the gate cannot tell a rule that finds nothing from a
# rule that is no longer there. Delete any check in lint.zg and `make lint` stays green; the
# rule's own corpus is the only thing that would notice.
#
# This is the same shape as refuse-check and reject-check, asking the third question those two
# do not: refuse pins what the compiler has NOT built, reject pins what the language does not
# accept, and this pins what the linter can still SEE. A program here is well formed and runs
# — that is what makes it a lint finding rather than an error.
#
# Two assertions per case:
#
#   1. the expected sentence, so the rule that fired is the one meant
#   2. THE EXIT STATUS THE SEVERITY OWES. A finding fails `zerg lint`; a warning and an info
#      print and do not. That is the severity axis, and it is asserted per case rather than
#      described anywhere, because a warning that quietly started failing the tool — or a
#      finding that quietly stopped — leaves the sentence exactly as it was.
#
# And one at the bottom: every code the linter documents must appear above. A rule added
# without a case is the hole this file exists to close, and counting is what makes that
# automatic rather than remembered.

set -u

ZERG=${ZERG:-./bin/zerg}
LINT_SRC=${LINT_SRC:-src/compiler/zerg/lint.zg}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

pass=0
fail=0
seen=""

# say <code> <wanted-substring> <wanted-status> [name] — the program arrives on stdin. The
# three `lint` / `lint_warn` / `lint_info` wrappers below are this with the status filled in,
# so a case reads as its severity and the assertion is the same one three times.
#
# `name` is the file's stem, and it defaults to the code: a rule with two cases needs two
# files, since the second `cat >` would otherwise overwrite the first.
say() {
	local code=$1 want=$2 wanted_status=$3 name=${4:-$1}
	local src="$tmp/$name.zg"
	cat >"$src"
	seen="$seen $code"

	local out status
	out=$("$ZERG" lint "$src" 2>&1)
	status=$?

	case $out in
	*"$want"*) ;;
	*)
		echo "MISSED    $code — wanted \"$want\", got: $(echo "$out" | head -1)"
		fail=$((fail + 1))
		return
		;;
	esac

	if [ "$status" -ne "$wanted_status" ]; then
		echo "STATUS    $code — wanted exit $wanted_status, got $status"
		fail=$((fail + 1))
		return
	fi
	pass=$((pass + 1))
}

# lint <code> <wanted-substring> [name] — a FINDING: printed, and it fails the tool.
lint() {
	say "$1" "$2" 1 "${3:-}"
}

# lint_warn <code> <wanted-substring> [name] — a WARNING: printed, and `zerg lint` still
# exits 0. `make lint` passes --strict and fails on it; that half is asserted at the bottom.
lint_warn() {
	say "$1" "$2" 0 "${3:-}"
}

# lint_info <code> <wanted-substring> [name] — an INFO: printed, and nothing fails for it,
# ever, --strict included.
lint_info() {
	say "$1" "$2" 0 "${3:-}"
}

# quiet <code> <name> <why> — the OTHER assertion a rule owes: a well formed program the rule
# must stay SILENT on. The program arrives on stdin.
#
# Every case above asks whether a rule still fires; none of them can see a rule firing on a
# program it has no business firing on, and that is the failure this repository has just paid
# for twice — L101 called an import unused when the only thing reaching it was a TYPE, and
# L102 called a private function dead when the only thing calling it was a module-level
# `const`. Both are green boards from every angle a positive case looks from.
#
# It does NOT touch `seen`: a rule is covered by a program that MAKES IT FIRE, and counting a
# silence as coverage would let the positive case be deleted.
quiet() {
	local code=$1 name=$2 why=$3
	local src="$tmp/$name.zg"
	cat >"$src"

	local out status
	out=$("$ZERG" lint "$src" 2>&1)
	status=$?

	case $out in
	*"$code"*)
		echo "SPOKE     $code fired on $why: $(echo "$out" | head -1)"
		fail=$((fail + 1))
		return
		;;
	esac

	if [ "$status" -ne 0 ]; then
		echo "STATUS    $name — wanted exit 0 on a clean program, got $status: $(echo "$out" | head -1)"
		fail=$((fail + 1))
		return
	fi
	pass=$((pass + 1))
}

# --- L1xx — dead code -------------------------------------------------------------

lint L101 'unused import' <<'EOF'
import "math"

fn main() {
	print "hi"
}
EOF

lint L102 'is never called' <<'EOF'
fn unused_helper() -> int {
	return 1
}

fn main() {
	print "hi"
}
EOF

lint L103 'is never read' <<'EOF'
fn main() {
	n := 41 + 1
	print "hi"
}
EOF

lint L104 '`_ :=`' <<'EOF'
fn side() -> int {
	print "side"
	return 1
}

fn main() {
	_ := side()
}
EOF

# `with e as x` where nothing reads `x`. The block already scopes the resource — that is
# what `with` IS — so the name buys nothing. It is a TOKEN rule: `with` expands in the
# parser, so by the time there is an AST there is no `with` left to lint.
lint L105 'nothing in the block reads the name' <<'EOF'
fn acquire() -> list[int] {
	return [1]
}

fn main() {
	with acquire() as unused {
		print 1
	}
	print acquire().len()
}
EOF

# --- the same two rules, on programs they must stay silent about --------------------
#
# L101 and L102 are the two rules that judge a DECLARATION by its uses, so both are only as
# good as the inventory of places a use can be written. Three places were missing at once and
# every case above stayed green through all of them; these are that inventory, asserted.

quiet L101 'l101-type-only' 'an import reached only from a TYPE position' <<'EOF'
import "testing"

pub fn takes(ctx: testing.Context) -> int {
	return 1
}

fn main() {
	print "hi"
}
EOF

# both rules at once, because a const initialiser is the one place that reaches both: `seed` is
# called from there and `math` is reached from there. The exit-0 half of the assertion is what
# covers the L101 side — no finding of any code may be printed.
# A BARE BLOCK, which is the one block form with no keyword in front of it and therefore the
# arm `walk_body` had not got. Both rules again, and for the same reason as the const case
# below: a block is the only other place this walk was blind, so the import and the call are
# put in one to prove the walk descends rather than that one arm was added.
quiet L102 'l102-from-a-bare-block' 'a private function called only from inside a bare block' <<'EOF'
import "math"

fn seed() -> int {
	return 7
}

fn main() {
	mut n := 0
	mut r := 0.0
	{
		n = seed()
		r = math.sqrt(4.0)
	}
	print n
	print r
}
EOF

quiet L101 'l101-from-a-bare-block' 'an import reached only from inside a bare block' <<'EOF'
import "math"

fn main() {
	mut r := 0.0
	{
		r = math.sqrt(9.0)
	}
	print r
}
EOF

quiet L102 'l102-from-const' 'a private function called only from a module-level const' <<'EOF'
import "math"

fn seed() -> int {
	return 7
}

const START := seed()
const ROOT := math.sqrt(4.0)

fn main() {
	print START
	print ROOT
}
EOF

quiet L102 'l102-from-field-default' 'a private function called only from a struct field default' <<'EOF'
fn seed() -> int {
	return 7
}

struct Box {
	n: int = seed()
}

fn main() {
	b := Box()
	print b.n
}
EOF

# --- the suppression, and the two things worth saying about one ---------------------
#
# A `#[allow(…)]` that WORKS is silent, so the first case here proves the mechanism by the
# only evidence there is: the same program, with and without the decorator. L102 fires above
# and must not fire here.
out=$("$ZERG" lint /dev/stdin <<'EOF' 2>&1
#[allow(L102)]
fn unused_helper() -> int {
	return 1
}

fn main() {
	print "hi"
}
EOF
)
status=$?
if [ $status -ne 0 ] || [ -n "$out" ]; then
	echo "ALLOWED   the suppression did not suppress — wanted silence and exit 0, got status $status: $(echo "$out" | head -1)"
	fail=$((fail + 1))
else
	pass=$((pass + 1))
fi

# The INFO. A stale allow silences a rule that stopped firing, and nobody learns when the
# real problem returns — the "measures nothing looks like finds nothing" shape this project
# spent a span taking out of its gates, arriving through the suppression mechanism.
lint_info L106 'has nothing to suppress' <<'EOF'
#[allow(L102)]
fn main() {
	print "hi"
}
EOF

# The WARNING, in the spelling that matters: an `E` code. `#[allow]` must never suppress a
# compiler diagnostic, so naming one is a suppression that can never apply.
lint_warn L107 'an `E` code is a COMPILER diagnostic' <<'EOF'
#[allow(E9040)]
fn main() {
	print "hi"
}
EOF

# and the other spelling — a code no rule has at all
lint_warn L107 'the `L…` codes documented in lint.zg' 'L107-unknown' <<'EOF'
fn main() {
	#[allow(L999)]
	print "hi"
}
EOF

# --- L2xx — null safety -----------------------------------------------------------

lint L201 '`?? nil`' <<'EOF'
fn find(n: int) -> int? {
	return nil if n < 0
	return n
}

fn main() {
	v := find(1) ?? nil
	print v ?? 0
}
EOF

lint L202 'hands the absence back' <<'EOF'
fn find(n: int) -> int? {
	return nil if n < 0
	return n
}

fn twice(n: int) -> int? {
	return find(n)! * 2
}

fn main() {
	print twice(1) ?? 0
}
EOF

# --- L3xx — capture ---------------------------------------------------------------

lint L301 'not the later one' <<'EOF'
fn note(n: int) {
	print n
}

fn main() {
	mut n := 1
	defer note(n)
	n = 2
	print n
}
EOF

# --- L4xx — resolution ------------------------------------------------------------
#
# `L401` stood here, over a variant name two enums declare. Its program is not ambiguous any
# more: a bare `Red` is E3079 in either enum, and a qualified `Color.Red` resolves inside the
# enum it names — so the case went with the rule, the same way `L501`'s did below.

lint L402 'never writes through `this`' <<'EOF'
struct P {
	pub x: int
}

impl P {
	mut fn peek() -> int {
		return this.x
	}
}

fn main() {
	mut p := P(1)
	print p.peek()
}
EOF

# --- L5xx — conversion ------------------------------------------------------------
#
# The family that needs TYPES, which is why the linter asks the lowering walk for it. `L501`
# stood here too, over a VALUE that converted at a position; that program is a compile error
# now, so the case went with the rule. What is left is the LITERAL that took a type the page
# does not show — and `1.5 + 1.0` is in the program on purpose, because it must NOT be
# reported, which is the whole argument for reporting `1.5 + 1`.

lint L502 'is a float here — write `1.0`' <<'EOF'
fn main() {
	print 1.5 + 1
	print 1.5 + 1.0
}
EOF

# --- L6xx — what the binary carries -------------------------------------------------
#
# A `#[test]` outside a `*_test.zg` file. The owner's decision is that it is LEGAL and SHIPS,
# so this is a warning and not an error — and the sentence has to carry the CONSEQUENCE, or
# it is style advice a reader learns to scroll past. What is asserted is that half.
lint_warn L601 'so it SHIPS' <<'EOF'
#[test]
fn checks_something() {
	print "ran"
}

fn main() {
	print "hi"
}
EOF

# and the SAME function in a file whose name says where it belongs is silent. That is the
# other half of the rule and the only thing that shows it is about the FILE rather than about
# the decorator — without it, a rule that reported every `#[test]` anywhere would pass above.
cat >"$tmp/lib_test.zg" <<'EOF'
#[test]
fn checks_something() {
	print "ran"
}

fn main() {
	print "hi"
}
EOF
out=$("$ZERG" lint "$tmp/lib_test.zg" 2>&1)
status=$?
if [ $status -ne 0 ] || [ -n "$out" ]; then
	echo "L601      fired inside a *_test.zg file, where a test BELONGS: $(echo "$out" | head -1)"
	fail=$((fail + 1))
else
	pass=$((pass + 1))
fi

# L601's sibling: an `assert` outside a `*_test.zg` file. Also legal, also a warning, and the
# consequence is the sharper one — the claim is compiled in and can abort a running program.
# What is asserted is the half of the sentence that names the REPLACEMENT, since a warning
# that only says "don't" is the style advice L601's comment is about.
lint_warn L602 'raise ValueError("xs must be non-empty") if xs.len() == 0' <<'EOF'
fn head(xs: list[int]) -> int {
	assert xs.len() > 0
	return xs[0]
}

fn main() {
	print head([1])
}
EOF

# and the same `assert` in a file whose name says where it belongs is silent — the half that
# shows the rule is about the FILE and not about the word
cat >"$tmp/claim_test.zg" <<'EOF'
fn head(xs: list[int]) -> int {
	assert xs.len() > 0
	return xs[0]
}

fn main() {
	print head([1])
}
EOF
out=$("$ZERG" lint "$tmp/claim_test.zg" 2>&1)
status=$?
if [ $status -ne 0 ] || [ -n "$out" ]; then
	echo "L602      fired inside a *_test.zg file, where a claim BELONGS: $(echo "$out" | head -1)"
	fail=$((fail + 1))
else
	pass=$((pass + 1))
fi

# the suppression, on the two scopes a reader reaches for: a whole function, and one
# statement. Both must be silent AND must not leave an L106 behind saying they suppressed
# nothing — a stale-allow report here would mean the rule and the allow disagree about where
# the finding is.
cat >"$tmp/allowed.zg" <<'EOF'
#[allow(L602)]
fn head(xs: list[int]) -> int {
	assert xs.len() > 0
	return xs[0]
}

fn main() {
	print head([1])

	#[allow(L602)]
	assert head([2]) == 2
}
EOF
out=$("$ZERG" lint --strict "$tmp/allowed.zg" 2>&1)
status=$?
if [ $status -ne 0 ] || [ -n "$out" ]; then
	echo "L602      \`#[allow(L602)]\` did not suppress: $(echo "$out" | head -1)"
	fail=$((fail + 1))
else
	pass=$((pass + 1))
fi

# --- --strict, which is what `make lint` runs ---------------------------------------
#
# The two exit codes are one rule each and neither can be read off the other: a warning that
# does not fail the tool has to fail the board, or `make lint` is asserting less than it says.
cat >"$tmp/strict.zg" <<'EOF'
#[test]
fn checks_something() {
	print "ran"
}

fn main() {
	print "hi"
}
EOF
if "$ZERG" lint --strict "$tmp/strict.zg" >/dev/null 2>&1; then
	echo "STRICT    --strict exited 0 on a warning, so \`make lint\` would pass a test that ships"
	fail=$((fail + 1))
else
	pass=$((pass + 1))
fi

# an INFO must NOT fail even under --strict — the severity below warning is the one that
# never changes an exit status, and without this the two would be one severity with two names
cat >"$tmp/strict-info.zg" <<'EOF'
#[allow(L102)]
fn main() {
	print "hi"
}
EOF
if "$ZERG" lint --strict "$tmp/strict-info.zg" >/dev/null 2>&1; then
	pass=$((pass + 1))
else
	echo "STRICT    --strict failed on an info, which never changes an exit status"
	fail=$((fail + 1))
fi

# --- every documented rule has a case ----------------------------------------------

for code in $(sed -n 's/^#   \(L[0-9][0-9][0-9]\).*/\1/p' "$LINT_SRC" | sort -u); do
	case " $seen " in
	*" $code "*) ;;
	*)
		echo "NO CASE   $code is documented in $LINT_SRC and no program here makes it fire"
		fail=$((fail + 1))
		;;
	esac
done

# --- the catalogue `#[allow]` is checked against says the same set --------------------
#
# `lint_codes()` is what a suppression is validated against and the doc block above it is
# what a reader consults, and they are two lists in one file. A code in one and not the other
# is either a rule that can never be allowed or an allow for a rule nobody documented — and
# both look exactly like nothing at all from the outside.
documented=$(sed -n 's/^#   \(L[0-9][0-9][0-9]\).*/\1/p' "$LINT_SRC" | sort -u)
catalogued=$(sed -nE '/^(pub )?fn lint_codes\(\)/,/^\}/p' "$LINT_SRC" | grep -oE 'L[0-9]{3}' | sort -u)
if [ "$documented" != "$catalogued" ]; then
	echo "CATALOGUE lint_codes() and the doc block in $LINT_SRC name different sets:"
	diff <(echo "$documented") <(echo "$catalogued") | sed 's/^/          /'
	fail=$((fail + 1))
else
	pass=$((pass + 1))
fi

if [ $fail -ne 0 ]; then
	echo "lint-check: $fail rule(s) the linter no longer reports"
	exit 1
fi
echo "lint-check: $pass rules seen firing, every documented code covered"
