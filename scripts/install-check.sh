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

# `make install` also writes cloc's config, which is the ONE thing it puts outside $(PREFIX):
# a real path, in the developer's home, shared with whatever else they have told cloc. A gate
# that edited it would change the machine every time CI ran, so it is pointed at the throwaway
# here — and pointing it somewhere is also what lets the round trip below look at the file.
#
# It is a whole fake HOME rather than a bare directory because cloc builds its config path as
# $ENV{HOME}/.config/cloc/options.txt and reads no variable that would redirect it. Handing
# cloc this HOME is therefore the only way to ask the question the install actually makes a
# claim about — `cloc` with NO arguments knows what Zerg is — instead of the weaker one
# `--config <path>` would ask, which is that the file parses.
CLOC_HOME="$work/home"
CLOC_CONFIG="$CLOC_HOME/.config/cloc"

fail=0

if ! out=$(make install PREFIX="$PREFIX" CLOC_CONFIG="$CLOC_CONFIG" 2>&1); then
	echo "INSTALL   make install failed"
	echo "$out" | tail -5 | sed 's/^/  /'
	exit 1
fi

for f in "$PREFIX/bin/zerg" "$PREFIX/lib/zerg/csrc/zergrt.h" "$PREFIX/lib/zerg/stdlib/io.zg" "$PREFIX/lib/zerg/cloc.def"; do
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

# --- and the part only cloc can answer --------------------------------------------------
#
# The claim the cloc half of the install makes is not that a file was written, it is that
# `cloc` counts Zerg with nothing typed. So it is run with nothing typed, against the
# examples, under the HOME the config was installed into.
#
# SKIPPED, LOUDLY, when cloc is absent: it is not a dependency of this toolchain and a
# developer without it should still be able to run every gate. A skipped step that prints
# nothing is the failure gates-check.sh has its own header about, one level up.
if [ ! -f "$CLOC_CONFIG/options.txt" ]; then
	echo "MISSING   $CLOC_CONFIG/options.txt — the install did not tell cloc about Zerg"
	fail=$((fail + 1))
elif ! command -v cloc >/dev/null 2>&1; then
	echo "install-check: cloc is not installed — the config was written, but nothing read it"
elif ! HOME="$CLOC_HOME" cloc --quiet examples 2>/dev/null | grep -q '^Zerg '; then
	echo "CLOC      cloc with no arguments still does not count Zerg under examples/"
	HOME="$CLOC_HOME" cloc --quiet examples 2>&1 | tail -5 | sed 's/^/  /'
	fail=$((fail + 1))
fi

if ! out=$(make uninstall PREFIX="$PREFIX" CLOC_CONFIG="$CLOC_CONFIG" 2>&1); then
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

# The config line is the leftover that MATTERS. Every other one sits in a prefix nobody looks
# in again; this one names a path that has just been deleted, and cloc meeting it answers
# `Unable to read` and counts nothing — in every project on the machine, not only this one.
if [ -e "$CLOC_CONFIG/options.txt" ]; then
	echo "LEFTOVER  $CLOC_CONFIG/options.txt survived make uninstall — cloc now points at a deleted file"
	sed 's/^/  /' "$CLOC_CONFIG/options.txt"
	fail=$((fail + 1))
fi

if [ $fail -ne 0 ]; then
	echo "install-check: $fail problem(s) with the install round trip"
	exit 1
fi
echo "install-check: installs, compiles and runs from its own path, and uninstalls clean"
