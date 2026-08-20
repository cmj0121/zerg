#!/usr/bin/env bash
#
# release-smoke — the ARTIFACT, on a machine that has nothing else.
#
# NOTHING HAS EVER TESTED THE THING THAT GETS DOWNLOADED. `install-check` is a real gate and it
# asks a different question: it runs `make install` FROM THE REPOSITORY, so a checkout, a Go
# toolchain and every source tree are all standing behind it. What a person downloads is a
# tarball, unpacked wherever they happen to be, with none of that.
#
# SO THIS EXPORTS NOTHING, and that is the point rather than a detail: `zerg_root()` probes the
# executable's own path and `root_layout()` decides which of the two layouts it found. Setting
# `ZERG_ROOT` would answer the question this is asking. Every ZERG_* variable is unset for the
# run, in case the environment that invokes it has opinions.
#
# IT RUNS FROM SOMEWHERE ELSE. The unpacked tree is not the working directory — the old default
# was "the current directory", and a test that stood inside the tree would pass on a compiler
# that still believed it.
#
# A TARBALL THAT FAILS THIS IS NOT PUBLISHED. That is the whole reason it is a script and not a
# paragraph in a checklist.
#
#   usage: release-smoke.sh <tarball>
set -uo pipefail

TARBALL=${1:-}
[ -n "$TARBALL" ] || {
	echo "usage: release-smoke.sh <tarball>" >&2
	exit 2
}
[ -f "$TARBALL" ] || {
	echo "release-smoke: $TARBALL is not there" >&2
	exit 2
}
TARBALL=$(cd "$(dirname "$TARBALL")" && pwd)/$(basename "$TARBALL")

tmp=$(mktemp -d) || exit 2
trap 'rm -rf "$tmp"' EXIT

fail=0
say() {
	if [ "$2" -eq 0 ]; then
		printf '  ok    %s\n' "$1"
	else
		printf '  FAIL  %s\n' "$1" >&2
		fail=$((fail + 1))
	fi
}

mkdir -p "$tmp/unpack" "$tmp/work"
tar xzf "$TARBALL" -C "$tmp/unpack" || {
	echo "release-smoke: the tarball does not unpack" >&2
	exit 1
}

# ONE TOP-LEVEL DIRECTORY, checked rather than assumed: a tarball that unpacks its `bin/` and
# `lib/` straight into the current directory is one that scatters over whatever was there.
top=$(ls "$tmp/unpack")
n=$(printf '%s\n' "$top" | grep -c .)
[ "$n" -eq 1 ]
say "the archive holds exactly one top-level directory (got $n)" $?
[ "$n" -eq 1 ] || exit 1

ZERG="$tmp/unpack/$top/bin/zerg"
[ -x "$ZERG" ]
say "bin/zerg is there and executable" $?
[ -x "$ZERG" ] || exit 1

run() { env -u ZERG_ROOT -u ZERG_RUNTIME -u ZERG_STDLIB -u ZERG_CACHE -u ZERG_CSTD "$@"; }

# 1. it knows what it is
v=$(run "$ZERG" --version 2>&1)
printf '%s' "$v" | grep -q "$(cat "$(dirname "$0")/../VERSION" 2>/dev/null || echo 0)" 2>/dev/null ||
	printf '%s' "$v" | grep -qE '[0-9]+\.[0-9]+\.[0-9]+'
say "--version answers ($v)" $?

# 2. a program, from a directory that is not the unpacked tree
cd "$tmp/work" || exit 2
printf 'fn main() {\n\tprint "hello, world"\n}\n' >hello.zg
out=$(run "$ZERG" build hello.zg -o hello 2>&1)
st=$?
[ $st -eq 0 ] || printf '%s\n' "$out" | sed 's/^/        /' >&2
say "zerg build hello.zg, with nothing exported" $st
if [ -x ./hello ]; then
	got=$(./hello 2>&1)
	[ "$got" = "hello, world" ]
	say "the program runs and prints what it should (got '$got')" $?
else
	say "the build produced no binary" 1
fi

# 3. and the STANDARD LIBRARY, which is the half `zerg_root()` is really being asked about: the
#    runtime C sources are found by the same probe, so a program that imports nothing already
#    proves one of them — this proves the other.
printf 'import "strings"\n\nfn main() {\n\tprint strings.repeat("ab", 3)\n}\n' >lib.zg
out=$(run "$ZERG" build lib.zg -o lib 2>&1)
st=$?
[ $st -eq 0 ] || printf '%s\n' "$out" | sed 's/^/        /' >&2
say "zerg build over an import of the standard library" $st
if [ -x ./lib ]; then
	got=$(./lib 2>&1)
	[ "$got" = "ababab" ]
	say "the stdlib program prints what it should (got '$got')" $?
else
	say "the stdlib build produced no binary" 1
fi

# 4. a REFUSAL still reads properly out of a tarball — the diagnostics carry a place, and a
#    compiler that lost its sources would report about a file the reader never wrote instead.
printf 'fn main() {\n\tprint nosuch()\n}\n' >bad.zg
out=$(run "$ZERG" build bad.zg -o bad 2>&1)
# BOTH SPELLINGS. A refusal reaches the terminal as `E4016 …` or as `error: E4016 …`
# depending on which renderer answered, and pinning one of them here would make this a test of
# a prefix rather than of the contract: a code, and a place.
printf '%s' "$out" | grep -qE '^(error: )?E[0-9]{4} ' && printf '%s' "$out" | grep -q -- '-->'
say "an ill-formed program is refused with a code and a place" $?

if [ "$fail" -ne 0 ]; then
	echo "release-smoke: $fail check(s) failed — this tarball is not publishable" >&2
	exit 1
fi
echo "release-smoke: $(basename "$TARBALL") builds and runs on a machine with nothing else on it"
