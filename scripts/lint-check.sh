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
#   2. a NON-ZERO exit, so `zerg lint` stays usable as a gate
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

# lint <code> <wanted-substring> — the program arrives on stdin.
lint() {
	local code=$1 want=$2
	local src="$tmp/$code.zg"
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

	if [ $status -eq 0 ]; then
		echo "EXIT 0    $code — the finding was printed and the linter reported success"
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

lint L401 'by declaration order alone' <<'EOF'
enum Fruit {
	Red
	Green
}

enum Color {
	Red
	Blue
}

fn main() {
	c := Red
	print int(c)
}
EOF

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

if [ $fail -ne 0 ]; then
	echo "lint-check: $fail rule(s) the linter no longer reports"
	exit 1
fi
echo "lint-check: $pass rules seen firing, every documented code covered"
