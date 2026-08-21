#!/usr/bin/env bash
#
# version-check — one version, five derivations, and they agree.
#
# The root VERSION file is the source of truth, and each thing downstream of it is derived
# by a mechanism of its own: a generated Zerg source the shipping compiler compiles in, the
# linker's -X into the seed, a hand-written badge URL in the READMEs, the file itself read
# back, and — at a release — the git TAG. Five mechanisms is five chances to drift, and the
# failure mode is silent by construction — a stale `--version` is a string that prints
# happily and is simply not true.
# No build breaks, no test fails, and the number a user quotes in a bug report names the
# wrong compiler.
#
# So the derivations are not trusted; they are compared. What this asserts is that the
# NUMBER matches, plus the ONE WORD that says which compiler answered — not that the banners
# are identical, because they deliberately are not: `zerg` says self-hosted and `zerg0` says
# seed, which is how a build log tells them apart. Pinning the wording further would make
# this a test of a sentence rather than of a version.
#
# It deliberately does NOT build anything. A gate that rebuilds before it looks can only ever
# compare a tree with itself and would pass on a binary that was months out of date; this one
# asks what is in bin/ right now, which is what a person or a CI job is about to ship. The
# cost is that it has to run after `make build`, and the Makefile target and the CI job
# sequence it exactly there.
#
# That same argument is why the generated source is checked TWICE, against two different
# things. `make build` regenerates version.zg in place, so by the time this runs the file on
# disk always agrees — reading it proves only that the generator ran. What it cannot prove is
# that the regenerated file was ever COMMITTED, and an uncommitted one is not cosmetic:
# selfhost-fixpoint.sh builds its stages straight from a bare checkout, so it compiles
# whatever git has, which is the stale number. Hence the second arm, against HEAD.

set -u

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

ZERG=${ZERG:-./bin/zerg}
ZERG0=${ZERG0:-./bin/zerg0}
VERSION_FILE=${VERSION_FILE:-VERSION}
VERSION_ZG=${VERSION_ZG:-src/compiler/zerg/version.zg}

fail=0

# --- does a banner carry a version? ----------------------------------------------------
#
# The two predicates below are the whole judgement this gate makes, so they are written
# first and held to their own cases before anything is measured with them.

# digit_or_dot <char> — true when <char> would extend a version number. An EMPTY char is the
# start or the end of the text, which ends a number just as well as a space does.
digit_or_dot() {
	case $1 in
	[0-9.]) return 0 ;;
	esac
	return 1
}

# carries <text> <version> — does <text> contain <version> as a whole number?
#
# Deliberately NOT a regex, and that is a defect corrected rather than a preference. The
# version comes from a file a person edits, and SemVer allows `0.1.0-rc.1` and
# `0.1.0+20260805` — `+` is an ERE operator, so a perfectly valid VERSION compiled into a
# pattern that matched nothing and failed a tree in which all three answers agreed. The same
# hole ran the other way: `0.1.0|7.7.7` in the file made a binary saying only 7.7.7 pass, and
# `0.1.0*` matched 0.1.00000. Shell's `case` with a QUOTED needle has no such reading — every
# byte is itself, whatever byte it is.
#
# The boundaries are why this is not plain containment: `0.1` must not be satisfied by a
# binary still saying `0.1.0`. A digit or a dot on either side of a match means the number
# does not end where the wanted one does, so that occurrence is rejected and the search moves
# past it — `10.1.0` holds `0.1.0` and is not it.
carries() {
	local rest=$1 needle=$2 pre post
	while :; do
		case $rest in
		*"$needle"*) ;;
		*) return 1 ;;
		esac
		pre=${rest%%"$needle"*}
		post=${rest#*"$needle"}
		if ! digit_or_dot "${pre: -1}" && ! digit_or_dot "${post:0:1}"; then
			return 0
		fi
		rest=$post
	done
}

# self_test holds `carries` to the cases that shaped it, and it runs on every invocation
# because it costs nothing and because a predicate that has quietly become "return true" is
# indistinguishable, from the outside, from a repo that never drifts. Three of these are
# defects this check actually had; the rest are the boundary behaviour that has to survive
# whatever rewrites it next.
#
# A case is `text|version`, and the needle is everything after the FIRST `|` so that a case
# can carry one of its own — which one of them must, since a `|` in the version is the whole
# point of two of these.
self_test() {
	local bad=0 c
	local -a must_carry=(
		"zerg 0.1.0 (self-hosted)|0.1.0"
		"zerg0 0.1.0 (seed)|0.1.0"
		"zerg 0.1.0+20260805 (self-hosted)|0.1.0+20260805"
		"zerg 0.1.0-rc.1 (self-hosted)|0.1.0-rc.1"
		"0.1.0|0.1.0"
	)
	local -a must_not=(
		"0.1.00000|0.1.0*"
		"7.7.7|0.1.0|7.7.7"
		"zerg 0.1.0 (self-hosted)|0.1"
		"zerg 10.1.0 (self-hosted)|0.1.0"
		"zerg 0.1.01 (self-hosted)|0.1.0"
		"zerg 0.1.0.0 (self-hosted)|0.1.0"
	)

	for c in "${must_carry[@]}"; do
		carries "${c%%|*}" "${c#*|}" || {
			echo "SELFTEST  \"${c%%|*}\" does carry \"${c#*|}\", and the predicate says it does not"
			bad=$((bad + 1))
		}
	done
	for c in "${must_not[@]}"; do
		carries "${c%%|*}" "${c#*|}" && {
			echo "SELFTEST  \"${c%%|*}\" does not carry \"${c#*|}\", and the predicate says it does"
			bad=$((bad + 1))
		}
	done

	[ $bad -eq 0 ] && return 0
	echo "version-check: the version predicate no longer behaves as it is documented to, so every"
	echo "               comparison below would be meaningless and none of them were made"
	return 1
}

self_test || exit 1

# --- the version everything else is measured against ------------------------------------

if [ ! -f "$VERSION_FILE" ]; then
	echo "version-check: $VERSION_FILE does not exist — it is the one place the toolchain version is written"
	exit 1
fi

# The SAME rule the Makefile and gen-version.sh read it under. Readers with two tolerances is
# how a VERSION holding a stray leading space passes here and expands in the Makefile to
# `VERSION=  0.1.0`, which hands the sub-make an empty version and `0.1.0` as a goal it has
# no rule for.
want="$(tr -d ' \t\n\r' <"$VERSION_FILE")"
if [ -z "$want" ]; then
	echo "version-check: $VERSION_FILE is empty — it must hold one version, e.g. 0.1.0"
	exit 1
fi

# agree <what> <got> <word> — <got> must carry the wanted version, whole, and name the
# compiler it came from. <word> is the one part of the wording this asserts, and it is
# asserted because the binaries are told apart by it: a build accident that leaves the seed
# at bin/zerg is something this gate is in a position to see and had no reason to miss. It is
# empty for the generated source, which is a number and no banner at all.
agree() {
	local what=$1 got=$2 word=$3
	if ! carries "$got" "$want"; then
		echo "DRIFTED   $what says \"$got\", and $VERSION_FILE says $want"
		fail=$((fail + 1))
		return
	fi
	if [ -n "$word" ] && ! printf '%s' "$got" | grep -qF -- "$word"; then
		echo "WRONG     $what says \"$got\", which does not name $word — is the right binary at that path?"
		fail=$((fail + 1))
		return
	fi
	printf '  %-28s %s\n' "$what" "$got"
}

# --- 1. the generated source, read as TEXT ----------------------------------------------
#
# Read rather than executed, because what the binary says is the next two checks and this one
# is about the FILE. The extraction is anchored to the body of `zerg_version` and takes the
# first `return` in it: an unanchored one passed a hand-edited file where zerg_version
# returned 9.9.9 and a second function below it returned the wanted number — fail-open, in
# the one check that exists because somebody may have hand-edited a generated file.
if [ ! -f "$VERSION_ZG" ]; then
	echo "MISSING   $VERSION_ZG — run \`make build\` (or ./scripts/gen-version.sh) to generate it"
	fail=$((fail + 1))
else
	agree "$VERSION_ZG" \
		"$(sed -n '/^pub fn zerg_version(/,/^}/ s/^[[:space:]]*return "\([^"]*\)".*$/\1/p' "$VERSION_ZG" | head -1)" ""
fi

# --- 2. and that same file as GIT has it ------------------------------------------------
#
# `make build` regenerates version.zg before it compiles anything, so the check above can
# only ever report that the generator ran: the file is repaired in place moments before it is
# read. The state it cannot see is the one that matters — VERSION bumped, version.zg
# regenerated, and the regenerated file never committed. Everything driven by `make` stays
# green while the tree a clone gets still holds the old number.
#
# Skipped, quietly and deliberately, outside a git work tree. This has to keep working from a
# release tarball, where there is no history to disagree with and nothing has gone wrong.
if git rev-parse --is-inside-work-tree >/dev/null 2>&1 && git rev-parse --verify -q HEAD >/dev/null; then
	if ! git diff --quiet HEAD -- "$VERSION_ZG"; then
		echo "UNCOMMITTED $VERSION_ZG does not match the committed one — commit it beside $VERSION_FILE"
		echo "            (\`git add $VERSION_ZG\`), or a fresh clone builds a compiler naming the old release"
		fail=$((fail + 1))
	fi
fi

# --- 3. what the two compilers actually say ---------------------------------------------

# ask <what> <binary> <word> — a binary that is not there is a distinct failure from one that
# answers wrongly, and saying which saves the next person a rebuild to find out.
ask() {
	local what=$1 bin=$2 word=$3
	if [ ! -x "$bin" ]; then
		echo "MISSING   $bin — run \`make build\` first; this gate reads what is in bin/, it does not build it"
		fail=$((fail + 1))
		return
	fi
	agree "$what" "$("$bin" --version 2>&1 | head -1)" "$word"
}

ask "$ZERG --version" "$ZERG" "(self-hosted)"
ask "$ZERG0 --version" "$ZERG0" "(seed)"

# --- 4. the number the READMEs show a visitor -------------------------------------------
#
# The version badge is a STATIC shields.io URL, because there is nothing dynamic to point it
# at: VERSION is plain text rather than JSON, and the repo cuts no GitHub release for a tag
# badge to read. So it is a fourth derivation of the same number, written by hand, in the one
# place a person looks first — and it drifts exactly the way the others do, silently, with a
# rendered image that is simply not true.
#
# `-` is shields.io's field separator, so a prerelease VERSION such as `0.1.0-rc.1` is spelled
# `0.1.0--rc.1` in the URL. The doubling is undone before comparing; every other byte of a
# SemVer passes through the URL as itself.
for readme in README.md README.zh-TW.md; do
	if [ ! -f "$readme" ]; then
		echo "MISSING   $readme — the version badge lives at the top of it"
		fail=$((fail + 1))
		continue
	fi
	badge="$(sed -n 's|.*img\.shields\.io/badge/version-\(.*\)-[A-Za-z0-9#]*).*|\1|p' "$readme" | head -1)"
	if [ -z "$badge" ]; then
		echo "MISSING   $readme has no img.shields.io version badge to check"
		fail=$((fail + 1))
		continue
	fi
	agree "$readme badge" "${badge//--/-}" ""
done

# --- 5. the tag, when there is one -------------------------------------------------------
#
# A TAG IS THE ONE DERIVATION A HUMAN TYPES, and the only one that cannot be regenerated: the
# other four come out of $VERSION_FILE by a mechanism, and a tag comes out of somebody's
# fingers at the end of a long day. `v0.1.O` builds four perfectly consistent tarballs named
# `0.1.0` and publishes them under a tag that is not the version, and nothing downstream has
# any reason to notice.
#
# CHECKED FIRST BY THE RELEASE, and here rather than in the workflow because this file is
# where "one version, and they agree" is written down — a second answer in YAML would be a
# second place to keep in step, which is the drift this whole script exists against.
#
# ABSENT IS NOT A FAILURE. Every ordinary run of the board has no tag to compare, so `$TAG`
# unset means the derivation is not in play; what is refused is a tag that is there and does
# not match. The closing line says which of the two happened, because a check that quietly
# measured nothing is the failure every gate here is written against.
#
# `$TAG` AND NOTHING AMBIENT. This read `$GITHUB_REF_NAME` as a fallback, and on a pull request
# that variable is `49/merge` — not a tag, and not something anybody meant as one — so every PR
# turned this red on a derivation that was not in play. A tag is passed by whoever means it;
# the release workflow sets `TAG: ${{ github.ref_name }}` at the one step where that name IS a
# tag, and nothing else has to know the shape of a CI environment.
tag=${TAG:-}
tagged=no
case $tag in
refs/tags/*) tag=${tag#refs/tags/} ;;
esac
if [ -n "$tag" ]; then
	tagged=yes
	if [ "$tag" != "v$want" ]; then
		echo "TAG       $tag is not v$want — $VERSION_FILE reads $want, and a release is tagged v<version>"
		fail=$((fail + 1))
	fi
fi

if [ $fail -ne 0 ]; then
	echo "version-check: $fail finding(s) — the toolchain does not carry the version in $VERSION_FILE"
	exit 1
fi
if [ "$tagged" = yes ]; then
	echo "version-check: the tag, the generated source, both compilers and both READMEs carry $want"
else
	echo "version-check: the generated source, both compilers and both READMEs carry $want (no tag to check)"
fi
