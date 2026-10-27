#!/usr/bin/env bash
#
# ffi-export-check — a `--emit lib` build writes a header a C caller can include and link
# against, and says which `pub` declarations it left out (docs/runtime/ffi.md, "Exporting a
# package").
#
# Two halves, because the chapter makes two promises:
#
#   the ROUND TRIP  a library is built, a C file that includes its header TWICE (the guard)
#                   calls what it exports — a free function, a struct by value, a method by
#                   value and by `mut &`, an associated fn — is compiled by cc, linked against
#                   the object and the runtime, run, and its output compared. A header whose
#                   prototype disagreed with the object's definition fails here, at cc or at
#                   the comparison, and nowhere else.
#   the LEFT OUT    a `pub` declaration that does not cross is REPORTED and left out, and the
#                   library still builds. Each `expect` case asserts the build exits 0, the
#                   report opens `warning: <code>` with a place, a header was still written,
#                   and the declaration's symbol is not in it — so a report that went quiet, a
#                   declaration that crept into the header anyway, or a report that started
#                   stopping the build are each a failure by name.
#
# The seed has no `--emit lib` at all (src/bootstrap/README.md), so no case here asks it.

set -u

# shellcheck source=scripts/lib/diag.sh
. "$(dirname "$0")/lib/diag.sh"
# shellcheck source=scripts/lib/case.sh
. "$(dirname "$0")/lib/case.sh"

ZERG=${ZERG:-./bin/zerg}
CC=${CC:-cc}
RT=${RT:-src/runtime/csrc}
LAYOUT=${LAYOUT:-src/compiler/cmd/layout.zg}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

pass=0
fail=0
cases=0

# THE RUNTIME A C CALLER LINKS, read from the list the driver links a program with
# (layout.zg's rt_units) rather than restated here — a unit added there and missing here would
# be an undefined symbol this gate blamed on the header. The context switch and the thread
# backend are chosen by target there, and the same way here.
rt_units() {
	sed -nE 's/^[[:space:]]*units\.append\("([a-z_]+\.c)"\)$/\1/p' "$LAYOUT"
	case $(uname -m) in
	arm64 | aarch64) echo ctx_arm64.S ;;
	x86_64 | amd64) echo ctx_x86_64.S ;;
	*) echo ctx_ucontext.c ;;
	esac
	echo thread_pthread.c
}

units=""
n_units=0
for u in $(rt_units); do
	units="$units $RT/$u"
	n_units=$((n_units + 1))
done

# A FLOOR under the derivation: a layout.zg that stopped matching the sed would hand cc no
# runtime, and the link failure would read as the header's.
if [ "$n_units" -lt 8 ]; then
	echo "ffi-export-check: read $n_units runtime units from $LAYOUT, below the floor of 8" >&2
	exit 1
fi

# --- the round trip ---------------------------------------------------------------------

rt="$tmp/roundtrip"
mkdir -p "$rt"
cat >"$rt/calc.zg" <<'EOF'
pub struct Pair {
	pub a: int
	pub b: int
}

impl Pair {
	pub fn origin() -> Pair {
		return Pair(a: 0, b: 0)
	}

	pub fn total() -> int {
		return this.a + this.b
	}

	pub mut fn bump(n: int) {
		this.a = this.a + n
	}
}

pub fn add(x: int, y: int) -> int {
	return x + y
}

pub fn swap(p: Pair) -> Pair {
	return Pair(a: p.b, b: p.a)
}

pub fn half(x: float) -> float {
	return x / 2.0
}

pub fn is_even(n: int) -> bool {
	return n % 2 == 0
}

pub fn shout() {
	print "from zerg"
}
EOF
cat >"$rt/main.c" <<'EOF'
#include <stdio.h>
#include "calc.h"
#include "calc.h"

int main(void) {
	printf("%lld\n", (long long)zg_add(40, 2));
	zg_Pair p = zg_swap((zg_Pair){.zg_a = 1, .zg_b = 9});
	printf("%lld %lld\n", (long long)p.zg_a, (long long)p.zg_b);
	zg_Pair_bump(&p, 5);
	printf("%lld\n", (long long)zg_Pair_total(p));
	zg_Pair o = zg_Pair_origin();
	printf("%lld\n", (long long)zg_Pair_total(o));
	printf("%g\n", zg_half(3.0));
	printf("%d %d\n", zg_is_even(4) ? 1 : 0, zg_is_even(7) ? 1 : 0);
	fflush(stdout);
	zg_shout();
	return 0;
}
EOF
want='42
9 1
15
0
1.5
1 0
from zerg'

roundtrip() {
	local out got
	if ! out=$("$ZERG" build --emit lib -o "$rt/calc.o" "$rt/calc.zg" 2>&1); then
		echo "BUILD     roundtrip — the library did not build: ${out%%$'\n'*}"
		return 1
	fi
	if [ -n "$out" ]; then
		echo "REPORTED  roundtrip — every declaration here crosses, and the build said: ${out%%$'\n'*}"
		return 1
	fi
	if [ ! -f "$rt/calc.h" ]; then
		echo "HEADER    roundtrip — \`-o calc.o\` wrote no calc.h beside it"
		return 1
	fi
	# shellcheck disable=SC2086
	if ! out=$("$CC" -std=c11 -I "$RT" -o "$rt/main" "$rt/main.c" "$rt/calc.o" $units 2>&1); then
		echo "LINK      roundtrip — a C caller of the header did not compile and link: ${out%%$'\n'*}"
		return 1
	fi
	if ! got=$("$rt/main" 2>&1); then
		echo "RUN       roundtrip — the C caller exited non-zero: ${got%%$'\n'*}"
		return 1
	fi
	if [ "$got" != "$want" ]; then
		echo "OUTPUT    roundtrip — the C caller printed something else:"
		printf '%s\n' "$got" | sed 's/^/          /'
		return 1
	fi
	return 0
}

if roundtrip; then
	pass=$((pass + 1))
else
	fail=$((fail + 1))
fi

# --- what is left out -------------------------------------------------------------------

# expect <name> <code> <symbol> [sentence] — the case on stdin is a library whose `pub`
# declaration with C symbol <symbol> does not cross. A sentence, where given, tells two cases
# of one code apart.
expect() {
	local name=$1 code=$2 sym=$3 says=${4:-}
	cases=$((cases + 1))
	local dir="$tmp/$name.d"
	local src="$dir/$name.zg"
	write_case "$dir" "$src"

	local out status first
	out=$("$ZERG" build --emit lib -o "$dir/$name.o" "$src" 2>&1 >/dev/null)
	status=$?
	first=${out%%$'\n'*}
	if [ $status -ne 0 ]; then
		echo "STOPPED   $name — a left-out declaration stopped the build (exit $status): $first"
		fail=$((fail + 1))
		return
	fi
	if ! warns_with_code "$out" "$code"; then
		echo "CODE      $name — wanted \`warning: $code\` to open the report, got: $first"
		fail=$((fail + 1))
		return
	fi
	if [ -n "$says" ]; then
		case $out in
		*"$says"*) ;;
		*)
			echo "MESSAGE   $name — wanted \"$says\", got: $first"
			fail=$((fail + 1))
			return
			;;
		esac
	fi
	if ! has_place "$out"; then
		echo "NO PLACE  $name — the report does not say where: $first"
		fail=$((fail + 1))
		return
	fi
	if [ ! -f "$dir/$name.h" ]; then
		echo "HEADER    $name — the build wrote no header"
		fail=$((fail + 1))
		return
	fi
	if grep -qw "$sym" "$dir/$name.h"; then
		echo "IN HEADER $name — \`$sym\` was reported and is in the header anyway"
		fail=$((fail + 1))
		return
	fi
	pass=$((pass + 1))
}

# What the chapter calls not FFI-safe: a generic, a `spec` used as a type, `Result[T]`, a
# channel, a type that reaches itself — and a map, which its definition leaves out.

expect generic-fn E4120 zg_first 'a generic `fn`' <<'EOF'
pub fn first[T](xs: list[T]) -> T {
	return xs[0]
}
EOF

expect spec-as-type E4120 zg_render 'a `spec` used as a type' <<'EOF'
pub spec Shape {
	fn area() -> int
}

pub struct Sq {
	pub n: int
}

impl Shape for Sq {
	fn area() -> int {
		return this.n * this.n
	}
}

pub fn render(s: Shape) -> int {
	return s.area()
}
EOF

expect result-return E4120 zg_parse '`Result[int]` is not FFI-safe' <<'EOF'
pub fn parse(n: int) -> Result[int] {
	return n
}
EOF

expect chan-param E4120 zg_feed '`chan[int]` is not FFI-safe' <<'EOF'
pub fn feed(c: chan[int]) -> int {
	return 0
}
EOF

expect recursive-struct E4120 zg_depth '`Node` reaches itself' <<'EOF'
pub struct Node {
	pub v: int
	pub next: Node?
}

pub fn depth(n: Node) -> int {
	return n.v
}
EOF

expect map-param E4120 zg_size '`map[str, int]` is not FFI-safe' <<'EOF'
pub fn size(m: map[str, int]) -> int {
	return m.len()
}
EOF

# What crosses by the chapter and has no C form here yet — and the two builds whose symbols
# would be wrong to hand over.

expect str-param E9120 zg_greet 'no form for `str`' <<'EOF'
pub fn greet(s: str) -> int {
	return 0
}
EOF

expect list-return E9120 zg_three 'no form for `list[int]`' <<'EOF'
pub fn three() -> list[int] {
	return [1, 2, 3]
}
EOF

expect enum-param E9120 zg_pick 'no form for `Colour`' <<'EOF'
pub enum Colour {
	Red
	Green
}

pub fn pick(c: Colour) -> int {
	return 1
}
EOF

expect module-state E9120 zg_limit 'module-level state' <<'EOF'
const LIMIT: int = 3

pub fn limit() -> int {
	return LIMIT
}
EOF

expect module-tag E9120 zg_add 'carries a module tag' <<'EOF'
import "./other"

pub fn add(a: int, b: int) -> int {
	return a + b + other.add(0, 0)
}
--- other.zg
pub fn add(a: int, b: int) -> int {
	return a - b
}
EOF

# A FLOOR under the cases, so a harness that stopped reaching them cannot pass on none.
if [ "$cases" -lt 11 ]; then
	echo "ffi-export-check: ran $cases left-out case(s), below the floor of 11" >&2
	exit 1
fi

if [ $fail -ne 0 ]; then
	echo "ffi-export-check: $fail of $((pass + fail)) check(s) failed"
	exit 1
fi
echo "ffi-export-check: a C caller links the header's library, and $cases left-out declarations are reported"
