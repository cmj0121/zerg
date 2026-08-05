#!/usr/bin/env bash
#
# gen-version — carry the root VERSION down into the Zerg half of the toolchain.
#
# The shipping compiler is written in Zerg and has no way to read a file at build time, so
# the version it prints has to BE a source file. This writes that file. It is generated on
# every `make build`, before the seed compiles anything, which is what makes the root
# VERSION the only place the number is ever edited: the alternative — a string in zergc.zg
# kept in step with a tag by hand — is the arrangement that gives a release three different
# answers to `--version`.
#
# The output is COMMITTED, not ignored. `make build` is not the only way this tree gets
# compiled: the fixpoint script builds a stage straight from source, a fresh clone may be
# read before anything is run, and `zerg build src/compiler/zergc.zg` is a thing a person
# does. A generated file missing from the tree breaks all three, and the cost of keeping it
# is one line of diff on the days the version changes.
#
# Byte-stability is a requirement, not a nicety. `make fixpoint` proves the compiler emits
# identical C for its own source twice over, so a generator that varied — a timestamp in the
# header, a differently quoted string — would break that gate every run for a reason that
# has nothing to do with the emitter. The same VERSION must produce the same bytes forever.
set -eu

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

VERSION_FILE="${VERSION_FILE:-VERSION}"
OUT="${OUT:-src/compiler/zerg/version.zg}"

# The version is read from the file rather than taken as an argument, so that a caller
# cannot generate a version.zg that disagrees with what `make version-check` will compare
# it against. There is one source of truth and this script does not offer a way around it.
[ -f "$VERSION_FILE" ] || {
	printf 'gen-version: %s does not exist — it is the toolchain version and nothing can be built without it\n' "$VERSION_FILE" >&2
	exit 1
}
version="$(tr -d ' \t\n\r' <"$VERSION_FILE")"
[ -n "$version" ] || {
	printf 'gen-version: %s is empty — it must hold one version, e.g. 0.1.0\n' "$VERSION_FILE" >&2
	exit 1
}

# Written to a temp file and compared, rather than written straight over the target. The
# common case by far is that nothing changed, and leaving the file untouched keeps a build
# from looking like it edited the tree.
tmp="$(mktemp "${TMPDIR:-/tmp}/zerg-version.XXXXXX")" || exit 2
trap 'rm -f "$tmp"' EXIT

cat >"$tmp" <<EOF
# version — the toolchain's version, in the one form a Zerg program can read.
#
# GENERATED from the root VERSION file by \`make\`. An edit here is overwritten by the next
# build and takes nothing with it, so the number is changed in VERSION and the build carries
# it down — to this file, to the seed's -ldflags, and to whatever a release is tagged. One
# number in one file is the only arrangement in which \`zerg --version\` and \`zerg0 --version\`
# cannot come to disagree, and \`make version-check\` is what holds them to it.
#
# It is a \`pub fn\` and not a \`const\` for a reason that is about the SEED. The Go seed cannot
# parse a module-level \`const\` inside an imported module, and this file is only ever reached
# through an import — so a constant here would compile under \`zerg\` and stop the bootstrap
# dead at the one step that has to keep working.

# zerg_version is the version alone — "$version" — with no product name around it, because
# the two compilers word their banners differently and only the number is shared.
#
# It cannot be called \`version\`: a program and the modules it imports flatten into one
# scope, and \`cli.version\` is already a name in it.
pub fn zerg_version() -> str {
	return "$version"
}
EOF

if [ -f "$OUT" ] && cmp -s "$tmp" "$OUT"; then
	exit 0
fi
cat "$tmp" >"$OUT"
printf 'gen-version: %s now reads %s\n' "$OUT" "$version"
