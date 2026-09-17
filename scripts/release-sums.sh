#!/usr/bin/env bash
#
# release-sums — ONE SHA256SUMS covering every tarball a release publishes, and a floor that
# refuses to publish one covering a subset.
#
# THE MERGE WAS A NAME COLLISION. Each platform builds on its own runner, and
# `release-tarball.sh` appends its line to a `SHA256SUMS` beside its own artifact — so three
# runners produce three FILES OF THAT NAME. The release job downloaded them with
# `merge-multiple: true`, which flattens three directories into one: the three tarballs landed
# side by side and the three `SHA256SUMS` overwrote each other. Whichever artifact was written
# last is the one that survived, which is why it was a different platform every release.
#
# `sha256sum -c SHA256SUMS` then PASSED, because it verifies the tarball the file names. The
# check was real and its population was one — a gate measuring a fraction of what it names,
# which is the failure this repository has a whole doctrine about, sitting in the release path.
#
# So the files are concatenated rather than merged, and the count is asserted: a downloader is
# told to run `sha256sum -c SHA256SUMS`, and that instruction is only true if the file covers
# what they were offered.
set -eu

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/sha256.sh
. "$ROOT/scripts/lib/sha256.sh"

STAGING=${1:?usage: release-sums.sh <staging-dir> <dist-dir>}
DIST=${2:?usage: release-sums.sh <staging-dir> <dist-dir>}

mkdir -p "$DIST"

# every tarball, from whichever per-runner directory it landed in
find "$STAGING" -name '*.tar.gz' -exec cp {} "$DIST/" \;

# and every runner's LINES. `find … -exec cat {} +` is the whole of the fix: three files of one
# name concatenate here, where flattening three directories made them collide.
find "$STAGING" -name 'SHA256SUMS' -exec cat {} + | sort -k2 >"$DIST/SHA256SUMS"

n_sums=$(grep -c . "$DIST/SHA256SUMS" || true)
n_tars=$(find "$DIST" -name '*.tar.gz' | grep -c . || true)

# THE FLOOR, and it is the point of the script rather than a guard on it. A SHA256SUMS naming
# fewer tarballs than the release publishes is exactly the state that shipped three times, and
# it published each time because the verification that ran beside it passed.
#
# BOTH DIRECTIONS, and they are different accidents. FEWER lines than tarballs is the collision
# this script exists for: a downloader checks a file that names a subset of what they were
# offered. MORE is a dist/ that was never cleaned — `release-tarball.sh` appends, so a second
# run on one machine used to leave two lines for one tarball, the older of them naming a file
# that no longer hashes to it.
if [ "$n_sums" -lt "$n_tars" ]; then
	printf 'release-sums: SHA256SUMS covers %s of the %s tarballs this release publishes — a downloader is told to check a file that names a subset\n' \
		"$n_sums" "$n_tars" >&2
	exit 1
fi

if [ "$n_sums" -gt "$n_tars" ]; then
	printf 'release-sums: SHA256SUMS has %s lines for %s tarballs — it names something this release does not publish\n' \
		"$n_sums" "$n_tars" >&2
	exit 1
fi

# and nothing at all is not a release
if [ "$n_tars" -eq 0 ]; then
	printf 'release-sums: no tarball was collected from %s — the artifacts did not arrive\n' "$STAGING" >&2
	exit 1
fi

# and the check a downloader is told to run, through the file that knows both spellings of
# the tool — `release-tarball.sh` WROTE these lines through it too.
sha256_verify "$DIST" SHA256SUMS

printf 'release-sums: %s tarballs, %s checksums, each verified\n' "$n_tars" "$n_sums"
