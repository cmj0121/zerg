#!/usr/bin/env bash
#
# release-sums-check — the release's own assembly step, run on a staging tree this script
# builds, because the real one exists for about four seconds a year.
#
# WHAT SHIPPED THREE TIMES. Three runners each write a `SHA256SUMS` beside their own tarball,
# the publish job collected them with `merge-multiple: true`, and three files of one NAME
# flattened into one directory overwrote each other. The published file named a single
# platform — a different one each release, whichever runner finished last — and
# `sha256sum -c SHA256SUMS` PASSED beside it, because it verifies the tarball the file names.
# A true check over a population of one.
#
# Nothing could see it. `make release` builds and smoke-tests ONE tarball on ONE machine, so
# the step where three become one had no gate at all: the collision needs three artifacts to
# exist at once, which happens only on a tag. This makes those three out of `dd` and a tar, so
# the assembly is measured on every push instead of on release day.
#
# BOTH DIRECTIONS, and the second is the one worth having. A gate that only watches the fixed
# layout pass would stay green if the floor inside `release-sums.sh` were deleted tomorrow, so
# the collided layout is staged here too and the gate fails if it is ACCEPTED.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

# shellcheck source=scripts/lib/sha256.sh
. "$ROOT/scripts/lib/sha256.sh"

tmp=$(mktemp -d) || exit 2
trap 'rm -rf "$tmp"' EXIT

fail=0
cases=0

# THE PLATFORMS THE MATRIX NAMES, spelled the way `release-tarball.sh` spells them: one
# tarball per runner, in a directory named after the artifact it was uploaded as.
slugs="linux-amd64 linux-arm64 darwin-arm64"

# stage_runner <dir> <slug> — one runner's dist/, built the way release-tarball.sh builds it:
# a tarball, and a SHA256SUMS holding the one line that runner knows about.
stage_runner() {
	local dir=$1 slug=$2
	local name="zerg-9.9.9-$slug.tar.gz"
	mkdir -p "$dir"
	# the CONTENT differs per platform, so a checksum from one cannot verify another's file
	printf 'a tarball that is only %s\n' "$slug" >"$dir/$name"
	sha256_sum_file "$dir" "$name" >>"$dir/SHA256SUMS" || return 1
}

check() {
	local what=$1 want=$2 staging=$3
	local out rc
	cases=$((cases + 1))
	out=$(./scripts/release-sums.sh "$staging" "$tmp/dist-$cases" 2>&1)
	rc=$?
	# WRITTEN BEFORE THE VERDICT, so a case that fails on its exit status still leaves the
	# output for the `says` beside it to read — otherwise one wrong answer prints as three.
	printf '%s\n' "$out" >"$tmp/out-$cases"
	if [ "$rc" -ne "$want" ]; then
		echo "release-sums-check: $what — wanted exit $want, got $rc" >&2
		printf '%s\n' "$out" | sed 's/^/    /' >&2
		fail=1
	fi
}

says() {
	local n=$1 needle=$2
	grep -q "$needle" "$tmp/out-$n" && return 0
	echo "release-sums-check: case $n did not say '$needle'" >&2
	sed 's/^/    /' "$tmp/out-$n" >&2
	fail=1
}

# --- 1. the layout the workflow produces now ------------------------------------------
#
# `download-artifact` without `merge-multiple` puts each artifact in a directory of its own
# name, so the three SHA256SUMS never meet.
for s in $slugs; do stage_runner "$tmp/ok/dist-$s" "$s" || exit 2; done
check "the per-runner layout" 0 "$tmp/ok"
says 1 "3 tarballs, 3 checksums"

# --- 2. the layout that shipped -------------------------------------------------------
#
# Flattened: three tarballs side by side and ONE SHA256SUMS, the last writer's. This is the
# published state of v0.4.0 and it must be refused.
mkdir -p "$tmp/collided/all"
for s in $slugs; do
	cp "$tmp/ok/dist-$s/zerg-9.9.9-$s.tar.gz" "$tmp/collided/all/"
	cp "$tmp/ok/dist-$s/SHA256SUMS" "$tmp/collided/all/SHA256SUMS" # each overwrites the last
done
check "the flattened layout that shipped" 1 "$tmp/collided"
says 2 "covers 1 of the 3 tarballs"

# --- 3. nothing arrived ---------------------------------------------------------------
#
# An empty staging is not a release, and it is what an artifact step that failed quietly
# would leave behind.
mkdir -p "$tmp/empty"
check "an empty staging" 1 "$tmp/empty"
says 3 "the artifacts did not arrive"

# --- 4. a line for something that is not there ------------------------------------------
#
# The other direction, and it is a different accident: `release-tarball.sh` APPENDS, so a
# second `make release` on one machine left two lines for one tarball — the older naming a
# file that no longer hashes to it. Nothing cleans `dist/`, so the stale line is the one a
# downloader would have checked against.
cp -R "$tmp/ok" "$tmp/extra"
sed 's/ zerg-/ zerg-was-/' "$tmp/ok/dist-linux-amd64/SHA256SUMS" >>"$tmp/extra/dist-linux-amd64/SHA256SUMS"
check "a line naming a tarball that is not there" 1 "$tmp/extra"
says 4 "names something this release does not publish"

# --- 4. the verification is still real -------------------------------------------------
#
# The count is the new floor, not a replacement for the check under it: a tarball that does
# not match its line has to fail even when every tarball has a line.
cp -R "$tmp/ok" "$tmp/corrupt"
printf 'not what was hashed\n' >"$tmp/corrupt/dist-linux-amd64/zerg-9.9.9-linux-amd64.tar.gz"
check "a tarball that does not match its line" 1 "$tmp/corrupt"

# A FLOOR under the cases, because a loop that stopped running reports nothing and nothing is
# what passing looks like.
if [ "$cases" -lt 5 ]; then
	echo "release-sums-check: only $cases cases ran" >&2
	exit 1
fi

if [ "$fail" -ne 0 ]; then
	echo "release-sums-check: the release's assembly step does not hold" >&2
	exit 1
fi

echo "release-sums-check: $cases stagings — the per-runner layout verifies 3 of 3, and the collided one is refused"
