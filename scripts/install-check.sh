#!/usr/bin/env bash
#
# install-check — `make install` produces a toolchain that works, and `make uninstall` takes
# it away again.
#
# This exists because `make install` was BROKEN and no gate could see it. On any macOS where
# `/usr/local/bin` belongs to root — the default — it failed at the first line:
#
#     install: chmod 755 /usr/local/bin: Operation not permitted
#
# BSD install(1) chmods a directory even when it already exists, to the mode it already has,
# on a path the target did not create. Every other gate in this repository runs the compiler
# out of `./bin`, so the one command a user runs first was the one command nothing ran.
#
# The check is the whole round trip into a temporary prefix: install, then compile and RUN a
# program with the installed binary and nothing exported — because finding the runtime and
# the stdlib from its own path is the thing an install has to get right and the thing a file
# listing cannot tell you — then uninstall, and look at what is left.

set -u

PREFIX=${PREFIX:-}
if [ -z "$PREFIX" ]; then
	PREFIX=$(mktemp -d)/prefix
	trap 'rm -rf "$(dirname "$PREFIX")"' EXIT
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

fail=0

if ! out=$(make install PREFIX="$PREFIX" 2>&1); then
	echo "INSTALL   make install failed"
	echo "$out" | tail -5 | sed 's/^/  /'
	exit 1
fi

for f in "$PREFIX/bin/zerg" "$PREFIX/lib/zerg/csrc/zergrt.h" "$PREFIX/lib/zerg/stdlib/io.zg"; do
	if [ ! -f "$f" ]; then
		echo "MISSING   $f — the install did not put it there"
		fail=$((fail + 1))
	fi
done

# The runtime needs its per-platform slots present, not merely most of them: the driver
# picks between them by target, and a missing one is a link error a user cannot act on.
for f in "$PREFIX"/lib/zerg/csrc/*.S; do
	[ -e "$f" ] && break
	echo "MISSING   no .S sources under $PREFIX/lib/zerg/csrc — the context-switch slots"
	fail=$((fail + 1))
done

# --- the part a file listing cannot answer ---------------------------------------------
#
# NOTHING is exported. `zerg_root` derives the prefix from the binary's own path, and the
# whole reason it does is that a user runs `zerg` from wherever they are standing. Setting
# ZERG_ROOT here would test the environment variable and leave the default untested — which
# is what an earlier version of this did, and it reported a failure that was its own.
printf 'fn main() {\n\tprint 6 * 7\n}\n' >"$work/t.zg"
if ! (cd "$work" && "$PREFIX/bin/zerg" build --emit bin -o "$work/t" "$work/t.zg" >"$work/build.log" 2>&1); then
	echo "BUILD     the installed compiler cannot build a program from outside the source tree"
	tail -5 "$work/build.log" | sed 's/^/  /'
	fail=$((fail + 1))
else
	got=$("$work/t" 2>/dev/null)
	if [ "$got" != "42" ]; then
		echo "RUN       the installed compiler built a program that prints '$got', not 42"
		fail=$((fail + 1))
	fi
fi

if ! out=$(make uninstall PREFIX="$PREFIX" 2>&1); then
	echo "UNINSTALL make uninstall failed"
	echo "$out" | tail -5 | sed 's/^/  /'
	fail=$((fail + 1))
fi

for f in "$PREFIX/bin/zerg" "$PREFIX/lib/zerg"; do
	if [ -e "$f" ]; then
		echo "LEFTOVER  $f survived make uninstall"
		fail=$((fail + 1))
	fi
done

if [ $fail -ne 0 ]; then
	echo "install-check: $fail problem(s) with the install round trip"
	exit 1
fi
echo "install-check: installs, compiles and runs from its own path, and uninstalls clean"
