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
#   4. nothing shaped like a cc diagnostic (`<file>.c:LINE:COL: error`)
#   5. the SEED refuses it too
#
# The fourth is not redundant with the third. A build given `-o` puts its intermediate C
# beside the output rather than in the cache, so a cc error can carry no cache path at all
# and still be a cc error — which is exactly the failure this gate exists to catch.
#
# The fifth makes zerg0 the ORACLE. The seed has had a semantic-analysis pass all along
# and diagnoses every rule here; a rule it enforces and `zerg` does not is a rule `zerg`
# LOST on the way to self-hosting, which is how this whole class went unnoticed. Only the
# sentence `zerg` prints is normative — the seed merely has to say no — because the two
# word their diagnostics differently and pinning both wordings would pin nothing useful.

set -u

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
reject() {
	local name=$1 want=$2
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
	if echo "$out" | grep -qE '\.c:[0-9]+:[0-9]+: (error|warning)'; then
		echo "VIA CC    $name — the message is a cc diagnostic about generated C"
		fail=$((fail + 1))
		return
	fi

	if "$ZERG0" build "$src" -o "$tmp/$name.seed.o" >/dev/null 2>&1; then
		echo "SEED      $name — the seed ACCEPTED a program the language rejects"
		fail=$((fail + 1))
		return
	fi

	pass=$((pass + 1))
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

# --- report ------------------------------------------------------------------------

if [ $fail -ne 0 ]; then
	echo "reject-check: $fail case(s) the compiler did not reject by itself"
	exit 1
fi
echo "reject-check: $pass ill-formed programs rejected by the compiler, none left to cc"
