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

# THE RUNTIME HEADER is the third input the program's C does not carry: the C includes it,
# and its inline helpers and macros are compiled into the PROGRAM's object. Keyed on the C
# alone, a changed header read back objects built against the old one (#405: a function that
# became a macro left cached objects calling a symbol nothing defined, and the link failed).
# So the same program is built against a copy of the runtime whose header differs by one
# trailing comment, and the PROGRAM unit's key must change. A program unit is the object
# with an emitted `.c` beside it; the runtime units' keys hashed the header already, so
# comparing every key would pass without the fix.
mkdir -p "$tmp/rt"
cp src/runtime/csrc/* "$tmp/rt/"
printf '/* cache-key-check: the header changed */\n' >>"$tmp/rt/zergrt.h"

prog_keys() {
	local cache=$1
	find "$cache" -name '*.o.c' -exec basename {} .o.c \; | sort | tr '\n' ' '
}

build_into "$tmp/c3" "$PATH" >/dev/null
plain=$(prog_keys "$tmp/c3")
rm -rf "$tmp/c4"
ZERG_RUNTIME="$tmp/rt" ZERG_CACHE="$tmp/c4" "$ZERG" build "$tmp/p.zg" -o "$tmp/out" >/dev/null 2>&1 || {
	printf 'cache-key-check: the build against the copied runtime failed\n' >&2
	exit 1
}
changed=$(prog_keys "$tmp/c4")

if [[ -z "$plain" || -z "$changed" ]]; then
	printf 'cache-key-check: no program unit was cached — the `.c` beside it is how this finds one\n' >&2
	exit 1
fi

if [[ "$plain" == "$changed" ]]; then
	printf 'cache-key-check: the same program key under two runtime headers — %s\n' "$plain" >&2
	printf 'cache-key-check: zergrt.h is not in a program unit'"'"'s key, so a changed header reads back stale objects\n' >&2
	exit 1
fi

# EVERYTHING THAT BUILDS THE COMPILE ARGV IS IN THE KEY. No build can vary the dialect flags from
# outside, so this half is asked of the source: each `cc_…` helper `cc_object_argv` calls to put
# together the command is named in `unit_key`'s body. A flag added to cc_dialect then changes the
# key by construction; a helper added beside it that the key does not read fails here.
fn_body() {
	awk -v F="$2" '$0 ~ "^(pub )?fn " F "\\(" { on = 1; next } on && /^}/ { exit } on { print }' "$1"
}
argv_helpers=$(fn_body src/compiler/cmd/layout.zg cc_object_argv | grep -oE 'cc_[a-z_]+\(' | tr -d '(' | sort -u)
key_body=$(fn_body src/compiler/cmd/unit.zg unit_key)
n=0
for h in $argv_helpers; do
	n=$((n + 1))
	case $key_body in
	*"$h("*) ;;
	*)
		printf 'cache-key-check: cc_object_argv builds the command with `%s`, and unit_key does not read it\n' "$h" >&2
		printf 'cache-key-check: a change there reaches cc and not the key, so a cache filled before it reads back stale objects\n' >&2
		exit 1
		;;
	esac
done
if [[ $n -lt 3 ]]; then
	printf 'cache-key-check: found %s argv helper(s) in cc_object_argv — the extraction stopped matching\n' "$n" >&2
	exit 1
fi

printf 'cache-key-check: two compilers, two keys; two headers, two program keys; every argv helper keyed — the cache names what filled it\n'
