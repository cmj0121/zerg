#!/usr/bin/env bash
#
# nesting-check — every shape a program can nest, at the deepest depth the parser accepts,
# builds and runs on an 8 MB stack; one level more is refused E2012 by name.
#
# ZG_MAX_NESTING (parser.zg) is a translation limit, and a limit is a promise in both
# directions: past it the program is refused by name, and up to it the program is BUILT. The
# second half is the one nothing held. reject-check sends programs past the bound, and the
# parser refuses them before any later walk runs, so a walk that needed more stack than the
# bound allows was never reached by a gate: nested calls overflowed the emitter on an 8 MB stack
# well inside the depth the parser accepted, and the reader was told StackOverflowError at a
# line of the compiler.
#
# THE STACK IS SET HERE, not inherited. An 8 MB stack is what a Linux shell and a macOS
# terminal hand a process, but macOS make 3.81 raises RLIMIT_STACK to 64 MB for its recipes,
# so `make` alone hid every overflow this gate is for. The script lowers its own soft limit
# to the 8 MB a reader's shell gives, and the compiler and each built program inherit it.
#
# THE DEPTH IS DERIVED, not written down. For each shape the deepest accepted depth is found
# by asking the parser (`--emit ast` stops there, E2012 or not), so the gate follows the
# bound rather than a number copied from a probe. Its FLOOR is held per shape: a shape spends
# a known number of levels a step (`levels` below), so its deepest depth is the bound divided
# by that, less the few levels `main` and its statement take. A generator that stopped
# nesting, or a shape that started spending a level more a step, fails there instead of
# passing on a shallower depth than the bound promises.
#
# Each case is built and RUN, and its output compared, because a build that finished with
# a wrong program is the other failure a deep walk can have.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

ZERG=${ZERG:-./bin/zerg}
STACK_KB=${STACK_KB:-8176}

[ -x "$ZERG" ] || {
	printf 'nesting-check: %s is not built — run `make build` first\n' "$ZERG" >&2
	exit 2
}

bound=$(sed -n -E 's/^pub const ZG_MAX_NESTING := ([0-9]+)$/\1/p' src/compiler/zerg/parser.zg)
[ -n "$bound" ] || {
	printf 'nesting-check: no `pub const ZG_MAX_NESTING := <n>` in src/compiler/zerg/parser.zg\n' >&2
	exit 2
}

ulimit -s "$STACK_KB" 2>/dev/null || {
	printf 'nesting-check: cannot lower the stack to %s KB (ulimit -s)\n' "$STACK_KB" >&2
	exit 2
}

tmp=$(mktemp -d) || exit 2
trap 'rm -rf "$tmp"' EXIT

# gen <shape> <n> — one program nesting <shape> <n> deep, printing what `want` says.
gen() {
	awk -v shape="$1" -v n="$2" '
	function tabs(k,   s, i) { s = ""; for (i = 0; i < k; i++) s = s "\t"; return s }
	function rep(t, k,   s, i) { s = ""; for (i = 0; i < k; i++) s = s t; return s }
	BEGIN {
		if (shape == "call") {
			print "fn id(x: int) -> int {\n\treturn x\n}\n\nfn main() {"
			print "\tprint " rep("id(", n) "1" rep(")", n) "\n}"
		} else if (shape == "generic-call") {
			print "fn id[T](x: T) -> T {\n\treturn x\n}\n\nfn main() {"
			print "\tprint " rep("id(", n) "1" rep(")", n) "\n}"
		} else if (shape == "call-in-a-generic") {
			print "fn id(x: int) -> int {\n\treturn x\n}\n\nfn g[T](x: T) -> int {"
			print "\treturn " rep("id(", n) "1" rep(")", n) "\n}\n\nfn main() {\n\tprint g(1)\n}"
		} else if (shape == "method") {
			print "struct C {\n\tpub v: int\n}\n\nimpl C {\n\tfn inc() -> C {\n\t\treturn C(this.v + 1)\n\t}\n}\n"
			print "fn main() {\n\tprint C(0)" rep(".inc()", n) ".v\n}"
		} else if (shape == "match") {
			print "fn main() {\n\tprint " rep("match 1 { _ => ", n) "1" rep(" }", n) "\n}"
		} else if (shape == "if-expression") {
			print "fn main() {\n\tprint " rep("if true { ", n) "1" rep(" } else { 0 }", n) "\n}"
		} else if (shape == "list") {
			print "fn main() {\n\tx := " rep("[", n) "1" rep("]", n) "\n\tprint 1\n}"
		} else if (shape == "tuple") {
			print "fn main() {\n\tx := " rep("(", n) "1" rep(", 2)", n) "\n\tprint 1\n}"
		} else if (shape == "variant") {
			print "enum T {\n\tLeaf\n\tNode(T)\n}\n\nfn main() {"
			print "\tx := " rep("T.Node(", n) "T.Leaf" rep(")", n) "\n\tprint 1\n}"
		} else if (shape == "index") {
			print "fn main() {\n\txs := [0]\n\tprint " rep("xs[", n) "0" rep("]", n) "\n}"
		} else if (shape == "block" || shape == "if" || shape == "for") {
			print "fn main() {"
			for (i = 1; i <= n; i++) {
				if (shape == "block") print tabs(i) "{"
				else if (shape == "if") print tabs(i) "if true {"
				else print tabs(i) "for i" i " in 0..1 {"
			}
			print tabs(n + 1) "print 1"
			for (i = n; i >= 1; i--) print tabs(i) "}"
			print "}"
		} else if (shape == "block-in-a-generic") {
			print "fn g[T](x: T) -> T {"
			for (i = 1; i <= n; i++) print tabs(i) "{"
			print tabs(n + 1) "print 1"
			for (i = n; i >= 1; i--) print tabs(i) "}"
			print "\treturn x\n}\n\nfn main() {\n\tg(1)\n}"
		} else if (shape == "closure") {
			print "fn main() {"
			for (i = 1; i <= n; i++) print tabs(i) "f" i " := fn () -> int {"
			print tabs(n + 1) "return 1"
			for (i = n; i >= 1; i--) {
				print tabs(i) "}"
				print tabs(i) (i > 1 ? "return f" i "()" : "print f" i "()")
			}
			print "}"
		}
	}'
}

# levels <shape> — how many nesting levels one step of the shape spends: a closure is a
# function body inside a block, two levels; every other shape is one.
levels() {
	case $1 in
	closure) printf '2\n' ;;
	*) printf '1\n' ;;
	esac
}

# want <shape> <n> — what the program at that depth prints.
want() {
	case $1 in
	method) printf '%s\n' "$2" ;;
	index) printf '0\n' ;;
	*) printf '1\n' ;;
	esac
}

# refused <file> — 0 when the parser refuses the file with E2012.
refused() {
	"$ZERG" build --emit ast -o "$tmp/ast" "$1" >"$tmp/check.out" 2>&1 && return 1
	grep -q 'E2012' "$tmp/check.out"
}

fail=0
ran=0
for shape in call generic-call call-in-a-generic method match if-expression list tuple variant index \
	block if for block-in-a-generic closure; do
	f="$tmp/$shape.zg"

	# the deepest depth in [1, bound] the parser accepts: refused at hi, accepted at lo
	lo=0
	hi=$((bound + 1))
	gen "$shape" "$hi" >"$f"
	if ! refused "$f"; then
		printf 'BOUND     %s — %s levels are not refused E2012: %s\n' "$shape" "$hi" "$(head -1 "$tmp/check.out")"
		fail=1
		continue
	fi
	while [ $((hi - lo)) -gt 1 ]; do
		mid=$(((lo + hi) / 2))
		gen "$shape" "$mid" >"$f"
		if refused "$f"; then hi=$mid; else lo=$mid; fi
	done
	per=$(levels "$shape")
	floor=$((bound / per - 3))
	if [ "$lo" -lt "$floor" ]; then
		printf 'FLOOR     %s — the deepest accepted depth is %s, under %s (ZG_MAX_NESTING %s / %s levels a step, less 3)\n' \
			"$shape" "$lo" "$floor" "$bound" "$per"
		fail=1
		continue
	fi

	# one level more: refused by name, the sentence a reader is owed
	gen "$shape" "$hi" >"$f"
	"$ZERG" build --emit bin -o "$tmp/$shape.bin" "$f" >"$tmp/past.out" 2>&1
	if ! grep -q 'E2012 this program nests more than' "$tmp/past.out"; then
		printf 'PAST      %s %s — wanted E2012 by name, got: %s\n' "$shape" "$hi" "$(head -1 "$tmp/past.out")"
		fail=1
		continue
	fi

	# the deepest: built and run on the same stack
	gen "$shape" "$lo" >"$f"
	if ! "$ZERG" build --emit bin -o "$tmp/$shape.bin" "$f" >"$tmp/build.out" 2>&1; then
		printf 'BUILD     %s %s — %s\n' "$shape" "$lo" "$(grep -v '^  --> ' "$tmp/build.out" | head -1)"
		fail=1
		continue
	fi
	want "$shape" "$lo" >"$tmp/want"
	"$tmp/$shape.bin" >"$tmp/got" 2>"$tmp/got.err"
	rc=$?
	if [ "$rc" -ne 0 ] || ! cmp -s "$tmp/want" "$tmp/got"; then
		printf 'RUN       %s %s — exit %s, printed %s %s\n' "$shape" "$lo" "$rc" "$(head -1 "$tmp/got")" \
			"$(head -1 "$tmp/got.err")"
		fail=1
		continue
	fi
	printf 'ok        %-18s %s deep builds and runs, %s is E2012\n' "$shape" "$lo" "$hi"
	ran=$((ran + 1))
done

[ "$fail" -eq 0 ] || {
	printf 'nesting-check: a shape inside the nesting bound does not build on a %s KB stack\n' "$STACK_KB" >&2
	exit 1
}
printf 'nesting-check: %s shapes build and run at the deepest depth ZG_MAX_NESTING %s accepts, on a %s KB stack\n' \
	"$ran" "$bound" "$STACK_KB"
