#!/usr/bin/env bash
#
# sha256-check — the one thing in this tree that has an outside authority.
#
# `src/stdlib/sha256.zg` is pure Zerg, and `zerg build` names a unit's cached object by
# what it answers. A hash that is subtly wrong does not look wrong: it still returns
# sixty-four hex characters, it is still stable, and the cache still works — right up to
# the day two units collide and the build links an object nobody compiled from that source.
#
# THE ORACLE IS NO USE HERE, which is why this gate exists at all. `make oracle` compares
# the seed and the shipping compiler on the same program — and both would be running the
# same Zerg implementation, so a wrong rotation is wrong identically on both sides and the
# comparison stays green. The authority has to come from outside this repository.
#
# Two of them:
#
#   FIPS 180-4's own known-answer vectors, which is what "this is SHA-256" means. They are
#   written out below rather than computed, because a vector derived from the thing under
#   test is not a test.
#
#   the SYSTEM tool, over random inputs, which is what the vectors cannot give: three fixed
#   messages exercise three lengths, and the padding rule has a case per length mod 64. The
#   lengths below walk both block boundaries deliberately — 55, 56, 63, 64, 65, 119, 120 —
#   because 56 is where the length field stops fitting and a message grows a whole extra
#   block for it, which is the single most common place a hand-written SHA-256 is wrong.
set -uo pipefail

ZERG=${ZERG:-./bin/zerg}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fail=0

# The system tool is `shasum -a 256` on macOS and `sha256sum` on most Linuxes. Neither is
# guaranteed, and a checkout without one is not a failure — it is a machine that cannot run
# HALF of this gate. The vectors half still runs, and says so.
if command -v sha256sum >/dev/null 2>&1; then
	sys_hash() { sha256sum | cut -d' ' -f1; }
	have_sys=1
elif command -v shasum >/dev/null 2>&1; then
	sys_hash() { shasum -a 256 | cut -d' ' -f1; }
	have_sys=1
else
	have_sys=0
fi

# The probe reads the path it is GIVEN, so the gate can hand it a file rather than a
# literal — which is the only way to feed a message containing a NUL, a newline, or the
# 4096 random bytes below. `main`'s `args` is the arguments alone; the program's own name
# is not among them (docs/code/functions.md), so the path is `args[0]`.
cat >"$tmp/digest.zg" <<'EOF'
import (
	"io"
	"sha256"
)

fn main(args: list[str]) {
	print(sha256.hex(io.read_file(args[0])))
}
EOF

if ! "$ZERG" build --emit bin -o "$tmp/digest" "$tmp/digest.zg" >"$tmp/build.log" 2>&1; then
	echo "sha256-check: the probe does not build" >&2
	cat "$tmp/build.log" >&2
	exit 1
fi

ours() {
	"$tmp/digest" "$1"
}

# --- FIPS 180-4 known-answer vectors --------------------------------------------------
vectors=0
kat() {
	local name=$1 expect=$2
	local got
	got=$(ours "$tmp/msg")
	vectors=$((vectors + 1))
	if [ "$got" != "$expect" ]; then
		echo "VECTOR    $name" >&2
		echo "  wanted  $expect" >&2
		echo "  got     $got" >&2
		fail=1
	fi
}

printf '' >"$tmp/msg"
kat "the empty message" e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855

printf 'abc' >"$tmp/msg"
kat "abc" ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad

printf 'abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq' >"$tmp/msg"
kat "the 448-bit message" 248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1

printf 'abcdefghbcdefghicdefghijdefghijkefghijklfghijklmghijklmnhijklmnoijklmnopjklmnopqklmnopqrlmnopqrsmnopqrstnopqrstu' >"$tmp/msg"
kat "the 896-bit message" cf5b16a778af8380036ce59e7b0492370b249b11e8f07a51afac45037afee9d1

# a million 'a': the vector that is about the BLOCK LOOP rather than the padding, and the
# only one here long enough for a mistake in it to show
yes a 2>/dev/null | tr -d '\n' | head -c 1000000 >"$tmp/msg"
[ "$(wc -c <"$tmp/msg")" -eq 1000000 ] && kat "one million 'a'" cdc76e5c9914fb9281a1c7e284d73e67f1809a48a497200e046d39ccc7112cd0

# --- the differential, against whatever the system has --------------------------------
random=0
if [ "$have_sys" -eq 1 ]; then
	for n in 0 1 55 56 57 63 64 65 119 120 127 128 129 1000 4096; do
		# `head -c 0` is an error on macOS, and an empty message is the case most worth
		# keeping — it is the one that is ALL padding.
		: >"$tmp/msg"
		[ "$n" -gt 0 ] && head -c "$n" /dev/urandom >"$tmp/msg"
		want=$(sys_hash <"$tmp/msg")
		got=$(ours "$tmp/msg")
		random=$((random + 1))
		if [ "$got" != "$want" ]; then
			echo "DIFFER    at $n bytes — the system tool says $want, this says $got" >&2
			fail=1
		fi
	done
fi

if [ "$fail" -ne 0 ]; then
	echo "sha256-check: the digest is not SHA-256" >&2
	exit 1
fi

# A FLOOR under the vectors, for the same reason every negative gate here has one: a `kat`
# that stopped running reports nothing, and nothing is what passing looks like.
if [ "$vectors" -lt 5 ]; then
	echo "sha256-check: only $vectors vectors ran — the known-answer half did not happen" >&2
	exit 1
fi

if [ "$have_sys" -eq 0 ]; then
	echo "sha256-check: $vectors FIPS 180-4 vectors — no system sha256 tool, so the random differential did not run"
	exit 0
fi

echo "sha256-check: $vectors FIPS 180-4 vectors, and $random random inputs agree with the system tool"
