#!/usr/bin/env bash
#
# formula-check — the Homebrew formula names a version this project has released.
#
# IT WAS THREE RELEASES BEHIND and nothing said so. `Formula/zerg.rb` carries a source URL and
# its sha256, and its own comment says updating those two lines is part of cutting a release —
# which is exactly the kind of instruction that is followed the first time and then is not.
# 0.2.0, 0.3.0 and 0.4.0 all shipped while the formula still pointed at v0.1.0, so
# `brew install` built a compiler from before generics, specs, or half the error codes existed.
# It is the same shape as #167: a release-path artifact that speaks once a year, with nobody in
# the room when it does.
#
# WHY THIS IS NOT `formula == VERSION`. The sha256 of a source tarball does not exist until its
# tag does, so between the commit that bumps VERSION and the commit that gets tagged there is
# no sha the formula could carry for the new number — it is REQUIRED to be one behind there.
# Holding it to VERSION would paint that window red for a rule the window cannot obey.
#
# So what is asserted is the weakest thing that would have caught this: the formula names one of
# the two newest versions the changelog has a section for. That permits exactly one release of
# lag — the lag the missing sha forces — and refuses two. The remaining window closes the day
# the release job updates the formula itself, which is a change to what CI is allowed to push
# and not to what this can see.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

formula=Formula/zerg.rb
changelog=CHANGELOG.md

# THE VERSION IS THE URL'S, because the URL is what a person downloads. A `version` field would
# be a second answer to the same question, and the formula does not carry one.
url=$(grep -m1 '^  url "' "$formula" | sed 's/.*"\(.*\)".*/\1/')
have=$(printf '%s' "$url" | sed -n 's|.*/tags/v\([0-9][0-9.]*\)\.tar\.gz.*|\1|p')

if [ -z "$have" ]; then
	echo "formula-check: $formula has no tag version in its url — found '$url'" >&2
	exit 1
fi

sha=$(grep -m1 '^  sha256 "' "$formula" | sed 's/.*"\(.*\)".*/\1/')
if ! printf '%s' "$sha" | grep -qE '^[0-9a-f]{64}$'; then
	echo "formula-check: $formula has no sha256 beside that url — found '$sha'" >&2
	exit 1
fi

# The changelog is newest first, and `release-notes` already holds it to having a section for
# the version being built, so the two newest headings are this release and the one before it.
# A FLOOR UNDER THE HEADINGS, for the reason every negative check here has one: this compares
# against what a `grep` found, and a grep that stopped matching finds nothing — which is the
# same shape as a formula that is up to date. The two versions have to be THERE before their
# being right means anything.
sections=$(grep -c '^## [0-9]' "$changelog")
if [ "$sections" -lt 2 ]; then
	echo "formula-check: $changelog has $sections versioned sections — there is nothing to compare against" >&2
	exit 1
fi

newest=$(grep -m1 '^## [0-9]' "$changelog" | sed 's/^## *//')
prev=$(grep '^## [0-9]' "$changelog" | sed -n '2s/^## *//p')

if [ "$have" != "$newest" ] && [ "$have" != "$prev" ]; then
	echo "formula-check: $formula installs v$have, and this project's last two releases are $newest and $prev" >&2
	cat >&2 <<'WHY'
    `brew install` builds what that url names, so a stale formula is a user running a compiler
    nobody has shipped for that long. Point the url and its sha256 at the newest RELEASED tag —
    the sha is what GitHub's source tarball for that tag hashes to, which is why this is done
    after the tag and not with the version bump.
WHY
	exit 1
fi

echo "formula-check: $formula installs v$have, and the last two releases are $newest and $prev"
