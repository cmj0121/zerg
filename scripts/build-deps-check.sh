#!/usr/bin/env bash
#
# build-deps-check — `bin/zerg` is rebuilt for every file that can change it.
#
# Thirty-one gates used to begin by recompiling a tree that had not changed, and a no-op
# rebuild is nineteen seconds (#84). `bin/zerg` is a real target now, and the whole of that
# change is its prerequisite list: a gate that runs against a stale binary is a gate that
# reports green for a tree it did not test, which is strictly worse than the slow board it
# replaced.
#
# SO THE LIST IS ASSERTED RATHER THAN TRUSTED. `make build-deps` prints it as data — one
# source of truth, no second copy here — and this walks it by ROOT: touch one file under each,
# ask whether `bin/zerg` would rebuild, put the timestamp back. Nothing is compiled; `make -q`
# answers the question and runs nothing.
#
# `make -q bin/zerg`, NOT `make -q build`. `build` carries a phony prerequisite, and a phony is
# newer than everything forever — so the question would answer "yes, rebuild" whatever the list
# said, and this gate would pass over a rule with no prerequisites at all. (In GNU Make 3.81
# `-q` also runs a phony prerequisite's recipe, which is how that was noticed.)
#
# THE ONE-SECOND WINDOW. Apple ships GNU Make 3.81, whose timestamp comparison is whole
# seconds, so a file written in the same second as `bin/zerg` reads as up to date. That is not
# something this change introduced — it is how make has always compared here — but it was
# unreachable while every gate rebuilt unconditionally. The `sleep 1` below is what keeps this
# gate from measuring it by accident; a human edit is never that close to a build, and a script
# that rewrites a source immediately after one can be.
set -u

cd "$(dirname "$0")/.." || exit 2

fail=0
deps=$(make --no-print-directory build-deps 2>/dev/null)
n=$(printf '%s\n' "$deps" | grep -c .)

# A FLOOR, because every assertion below is per-root: an empty list makes all of them vacuous
# and the summary would report success over a rule nobody checked.
if [ "$n" -lt 50 ]; then
	echo "build-deps-check: \`make build-deps\` printed $n files — the list is not being read, and nothing below was checked"
	exit 1
fi

roots=$(printf '%s\n' "$deps" | awk -F/ '{ print (NF > 1 ? $1 "/" $2 : $1) }' | sort -u)
nroots=$(printf '%s\n' "$roots" | grep -c .)
if [ "$nroots" -lt 4 ]; then
	echo "build-deps-check: $nroots roots in the prerequisite list — a build that reads the compiler, the standard library, the runtime and the seed has more than that"
	exit 1
fi

make build >/dev/null 2>&1 || { echo "build-deps-check: the tree does not build, so nothing here means anything"; exit 1; }

if ! make -q bin/zerg >/dev/null 2>&1; then
	echo "UNCONDITIONAL  \`bin/zerg\` is out of date immediately after a build — the rule rebuilds whatever the tree does"
	echo "               (a phony prerequisite does this, and it makes every assertion below vacuous)"
	exit 1
fi

# one representative per root, and the timestamp goes back afterwards so the next gate on the
# board does not pay for a rebuild this one caused
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
sleep 1

checked=0
for root in $roots; do
	f=$(printf '%s\n' "$deps" | awk -F/ -v r="$root" '{ k = (NF > 1 ? $1 "/" $2 : $1) } k == r { print; exit }')
	[ -f "$f" ] || continue

	touch -r "$f" "$tmp/ref"
	touch "$f"
	if make -q bin/zerg >/dev/null 2>&1; then
		echo "STALE     $root — touching $f leaves \`bin/zerg\` up to date, so a gate would run against a binary that predates it"
		fail=$((fail + 1))
	fi
	touch -r "$tmp/ref" "$f"
	checked=$((checked + 1))
done

if ! make -q bin/zerg >/dev/null 2>&1; then
	echo "build-deps-check: a timestamp was not put back — the next gate will rebuild for nothing"
	fail=$((fail + 1))
fi

if [ "$fail" -ne 0 ]; then
	echo "build-deps-check: $fail root(s) can change the binary without rebuilding it"
	exit 1
fi
echo "build-deps-check: $n files over $checked roots, and touching any one of them rebuilds \`bin/zerg\`"
