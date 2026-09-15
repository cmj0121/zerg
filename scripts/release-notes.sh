#!/usr/bin/env bash
#
# release-notes — the body of a GitHub release, from the one file that already holds it.
#
# `body_path: CHANGELOG.md` publishes the WHOLE file, and most of that file is about the file:
# "Zerg's releases, newest first", where the number comes from, what belongs in `notes/`. None
# of it is about the release a person just clicked on — and after 0.2.0 it would carry every
# older entry along too.
#
# SO THE SECTION IS EXTRACTED, keyed by the version in VERSION, which means this needs no edit
# at the next release and cannot drift from what a reader of the repository sees: one source,
# two renderings.
#
# AND WHAT A DOWNLOADER ASKS FIRST is added below it, because the changelog has no reason to
# carry it: which file to take, how to check it arrived intact, and that `cc` is needed to
# BUILD A PROGRAM and not only to build the compiler.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

VERSION=$(cat VERSION)
CHANGELOG=${CHANGELOG:-CHANGELOG.md}

body=$(awk -v v="## $VERSION" '
	$0 == v { on = 1; next }
	on && /^## / { exit }
	on { print }
' "$CHANGELOG")

# A FLOOR, because an extraction that matched nothing would publish an empty release body and
# look like a formatting choice rather than a failure.
if [ "$(printf '%s' "$body" | grep -c .)" -lt 5 ]; then
	printf 'release-notes: no `## %s` section in %s — the release body would be empty\n' "$VERSION" "$CHANGELOG" >&2
	exit 1
fi

# THE LINK AT THE BOTTOM IS DERIVED AND THEN CHECKED. It was written out with the SERIES
# hardcoded — `notes/0.1/` — and the version interpolated beside it, so it was right for the
# release it was written during and for no other: the published 0.2.0 and 0.3.0 bodies both
# point at a file that is not there. Nothing saw it, because the only thing this gate asked was
# whether the CHANGELOG had a section, and a dead link in a release body is not a broken build.
#
# The series is the version without its patch, which is how `notes/` is laid out, and the file
# has to EXIST — deriving a path is what put the wrong one there, so the derivation is held to
# the tree rather than trusted.
NOTES="notes/${VERSION%.*}/${VERSION}_CHANGELOG.md"
if [ ! -f "$NOTES" ]; then
	printf 'release-notes: the body would link %s and it is not there\n' "$NOTES" >&2
	exit 1
fi

printf '%s\n' "$body"
cat <<EOF
## Install

Download the tarball for your platform, unpack it anywhere, and put its \`bin\` on your PATH.
Nothing has to be exported: the compiler finds its runtime and standard library from its own
path.

\`\`\`sh
tar xzf zerg-$VERSION-<platform>.tar.gz
./zerg-$VERSION-<platform>/bin/zerg --version
\`\`\`

Verify what you downloaded against \`SHA256SUMS\`:

\`\`\`sh
sha256sum -c SHA256SUMS        # macOS: shasum -a 256 -c SHA256SUMS
\`\`\`

**A C compiler is required to build a program**, not only to build Zerg: \`zerg build\` emits C17
and hands it to \`cc\`.

There is no Intel-Mac binary — Rosetta runs x86_64 on Apple Silicon and not the reverse, so an
Intel Mac builds from source, which needs Go as well as \`cc\`.

→ [the full account, with every gap named]($NOTES)
EOF
