#!/usr/bin/env bash
#
# release-tarball — the artifact a person downloads, built from the layout `make install`
# already defines.
#
# THE CONTENTS ARE NOT A LIST. `make install` decides what an installed toolchain is — which
# runtime sources, which stdlib modules, and which of them are filtered out (`zrt_test.*` and
# every `*_test.zg`, each for a reason written where it happens). Spelling that out again here
# would be a second answer to one question, and the next file added to either tree would land
# in one copy and not the other. So this stages an install and tars the staging directory.
#
# THE NAME IS THE COMPILER'S OWN ANSWER. `__zrt_platform()` and `__zrt_arch()` are what the
# binary in the tarball reports about the machine it runs on, so asking IT removes the
# os/arch table this script would otherwise have to keep in step with the runtime's own
# per-platform slots. A tarball is named by the compiler inside it.
#
# CLOC_CONFIG POINTS AWAY FROM $HOME. `make install`'s last line registers the language
# definition with cloc, which lives in the user's config directory — outside `$(PREFIX)`, and
# not something a release build has any business editing. The same throwaway `install-check`
# uses, for the same reason.
#
# WHAT IS ADDED BEYOND THE PREFIX: `LICENSE`, `LICENSES/` and `README.md`. A binary somebody
# downloads should carry the terms it is under and one page saying what it is.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

# `DIST` AND NOT `OUT`, and that is not taste. `scripts/gen-version.sh` reads $OUT for the
# path it writes `version.zg` to, and this script runs `make install`, which runs `make build`,
# which runs that script — so an exported OUT=dist made the generator write the compiler's own
# version source into a file called `dist`, and the tar that followed had nowhere to go. A
# one-word environment name is an implicit contract with every script downstream of you.
DIST=${DIST:-dist}
VERSION=$(cat VERSION)

work=$(mktemp -d) || exit 2
trap 'rm -rf "$work"' EXIT

stage="$work/stage"
mkdir -p "$stage" "$work/cloc"

# The install itself. `make install` builds first, so this needs no separate build step, and a
# failure here is a failure of the thing a user would run.
if ! make install PREFIX="$stage" CLOC_CONFIG="$work/cloc" >"$work/install.log" 2>&1; then
	echo "release-tarball: staging the install failed" >&2
	sed 's/^/    /' "$work/install.log" >&2
	exit 1
fi

[ -x "$stage/bin/zerg" ] || {
	echo "release-tarball: no bin/zerg in the staged prefix — the install did not produce a compiler" >&2
	exit 1
}

# and the name, from the compiler that is about to be shipped
probe="$work/probe.zg"
cat >"$probe" <<'ZG'
fn main() {
	print __zrt_platform() + "-" + __zrt_arch()
}
ZG
slug=$(cd "$work" && "$stage/bin/zerg" build probe.zg -o probe 2>/dev/null && ./probe 2>/dev/null)
case $slug in
[a-z0-9_]*-[a-z0-9_]*) ;;
*)
	echo "release-tarball: the staged compiler did not answer with an os-arch pair (got '$slug')" >&2
	exit 1
	;;
esac

name="zerg-$VERSION-$slug"
mkdir -p "$DIST"

# A TOP-LEVEL DIRECTORY INSIDE THE ARCHIVE, so unpacking in a home directory does not scatter
# `bin/` and `lib/` into it. The directory is the tarball's own name, which is what a reader
# expects and what `tar tzf` shows them before they commit to it.
mv "$stage" "$work/$name"
cp LICENSE README.md "$work/$name/"
cp -R LICENSES "$work/$name/"

tar czf "$DIST/$name.tar.gz" -C "$work" "$name" || {
	echo "release-tarball: tar failed" >&2
	exit 1
}

# AND ITS CHECKSUM, in the file every platform's tooling already reads. One line per artifact,
# appended rather than written whole: the three platforms are built on three runners and each
# knows only its own, so the release job collects them and `sha256sum -c SHA256SUMS` verifies
# whichever ones a person downloaded.
#
# TWO SPELLINGS OF THE SAME TOOL. GNU coreutils has `sha256sum`; macOS ships `shasum -a 256`.
# Both print `<hash>  <name>`, which is the format `-c` reads on either.
if command -v sha256sum >/dev/null 2>&1; then
	sum=$(cd "$DIST" && sha256sum "$name.tar.gz")
elif command -v shasum >/dev/null 2>&1; then
	sum=$(cd "$DIST" && shasum -a 256 "$name.tar.gz")
else
	echo "release-tarball: neither sha256sum nor shasum is on this machine — the artifact has no checksum" >&2
	exit 1
fi
printf '%s\n' "$sum" >>"$DIST/SHA256SUMS"

size=$(wc -c <"$DIST/$name.tar.gz" | tr -d ' ')
files=$(tar tzf "$DIST/$name.tar.gz" | grep -cv '/$')
echo "release-tarball: $DIST/$name.tar.gz — $files files, $size bytes"
echo "release-tarball: ${sum%% *}"
