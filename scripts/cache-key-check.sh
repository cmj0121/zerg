#!/usr/bin/env bash
#
# cache-key-check — the build cache names the compiler that filled it.
#
# The object cache is keyed on the emitted C, which folds in the program, everything it can
# see, and the compiler that produced it. Two inputs to `cc` are NOT downstream of that C,
# so the C cannot stand in for them: the dialect, and `cc` itself. A key that omits either
# one hands back an object built by something else and reports success — the worst shape a
# build cache has, because nothing fails and the binary is simply not the one asked for.
#
# The dialect half has been in the key since the cache was written. This gate is for the
# other half, and it exists because that half was missing for as long: `cc` is a NAME, and
# the file it resolves to changes when $CC changes, when $PATH changes, and when a toolchain
# lands beside the old one. Switching compilers read back the objects the first one built.
#
# What it asserts is a property of `unit_key`, not a behaviour of a build: compile the SAME
# program twice into two fresh caches, changing only which `cc` is found, and the file names
# in those caches must differ. Same program, same dialect, different compiler, different key.
#
# It cannot assert the converse — that an unchanged compiler yields an unchanged key — from
# two separate runs, because a fresh cache directory is the point of the test. That property
# is what the cache does every day, and a regression in it shows up as a build that never
# hits rather than one that hits wrongly.
#
# The stand-in compiler is a shell script that execs the real one. That is deliberate: the
# object it produces is byte-identical to the real cc's, so a key that changed here changed
# because the compiler's IDENTITY changed and not because its output did.
set -euo pipefail

ZERG=${ZERG:-./bin/zerg}
if [[ ! -x "$ZERG" ]]; then
	printf 'cache-key-check: %s is not built — run `make build` first\n' "$ZERG" >&2
	exit 1
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# The program is the smallest one that reaches cc at all: what is being compared is the key,
# not the code.
cat >"$tmp/p.zg" <<'EOF'
fn main() {
	print 1
}
EOF

# A stand-in `cc` that execs the real one, so only the NAME resolution differs.
real_cc=$(command -v cc)
mkdir -p "$tmp/bin"
printf '#!/bin/sh\nexec %s "$@"\n' "$real_cc" >"$tmp/bin/cc"
chmod +x "$tmp/bin/cc"

build_into() {
	local cache=$1 path=$2
	rm -rf "$cache"
	PATH="$path" ZERG_CACHE="$cache" "$ZERG" build "$tmp/p.zg" -o "$tmp/out" >/dev/null 2>&1 || {
		printf 'cache-key-check: the build failed under PATH=%s\n' "$path" >&2
		exit 1
	}
	# The key is the file name; the extension is not part of it.
	find "$cache" -name '*.o' -exec basename {} .o \; | sort | tr '\n' ' '
}

first=$(build_into "$tmp/c1" "$PATH")
second=$(build_into "$tmp/c2" "$tmp/bin:$PATH")

if [[ -z "$first" || -z "$second" ]]; then
	printf 'cache-key-check: a build produced no cached object — the cache is not being written\n' >&2
	exit 1
fi

if [[ "$first" == "$second" ]]; then
	printf 'cache-key-check: the same key under two compilers — %s\n' "$first" >&2
	printf 'cache-key-check: `cc` is not in the key, so switching compilers reads back the other one'"'"'s objects\n' >&2
	exit 1
fi

printf 'cache-key-check: two compilers, two keys — the cache names what filled it\n'
