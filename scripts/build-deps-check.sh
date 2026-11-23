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
# ONE INPUT IS NOT A FILE AND IS NOT CHECKED HERE. The seed's `--version` comes from `-X
# main.version=`, a make VARIABLE, so a `bin/zerg0` built by another route carries `0.0.0-dev`
# and no timestamp says so. `bin/zerg`'s recipe asks the binary before it compiles the answer
# in, and `version-check` is the gate that owns the question — it is the one that caught this,
# on CI, after this rule stopped re-stamping the seed by accident.


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

# stale_after_touch <target> <file> succeeds when touching <file> leaves <target> up to date,
# and puts the file's timestamp back either way
stale_after_touch() {
	touch -r "$2" "$tmp/ref"
	touch "$2"
	make -q "$1" >/dev/null 2>&1
	local rc=$?
	touch -r "$tmp/ref" "$2"
	return $rc
}

checked=0
for root in $roots; do
	f=$(printf '%s\n' "$deps" | awk -F/ -v r="$root" '{ k = (NF > 1 ? $1 "/" $2 : $1) } k == r { print; exit }')
	[ -f "$f" ] || continue

	if stale_after_touch bin/zerg "$f"; then
		echo "STALE     $root — touching $f leaves \`bin/zerg\` up to date, so a gate would run against a binary that predates it"
		fail=$((fail + 1))
	fi
	checked=$((checked + 1))
done

if ! make -q bin/zerg >/dev/null 2>&1; then
	echo "build-deps-check: a timestamp was not put back — the next gate will rebuild for nothing"
	fail=$((fail + 1))
fi

# THE SEED'S INPUTS ARE ASKED OF GO, NOT OF THE MAKEFILE. `bin/zerg0` embeds sources from
# sibling modules (`go:embed` in src/runtime and src/stdlib), and a rule that missed them kept
# a seed carrying the OLD runtime, which `oracle` then held the compiler to. `build-deps` cannot
# find that: it prints the same variables the rule reads. `go list -deps` is the build's own
# answer — every Go file, embedded file and go.mod in the seed's import graph — and `go env
# GOWORK` names the go.work that resolves the sibling modules. Every one of them inside this
# repo is touched in turn and must make `bin/zerg0` out of date. `go list`'s stderr is kept so
# a floor that trips says why.
repo=$(pwd)
seed=$(cd src/bootstrap && { go list -deps -f '{{if not .Standard}}{{$d := .Dir}}{{range .GoFiles}}{{$d}}/{{.}}
{{end}}{{range .EmbedFiles}}{{$d}}/{{.}}
{{end}}{{with .Module}}{{.GoMod}}
{{end}}{{end}}' ./cmd/zerg 2>"$tmp/golist.err"; go env GOWORK; } | sed -n "s|^$repo/||p" | sort -u)
nseed=$(printf '%s\n' "$seed" | grep -c .)
nseeddirs=$(printf '%s\n' "$seed" | sed -E 's|/[^/]*$||' | sort -u | grep -c .)
if [ "$nseed" -lt 50 ] || [ "$nseeddirs" -lt 3 ]; then
	echo "build-deps-check: \`go list\` named $nseed seed inputs in $nseeddirs directories — the seed's graph is not being read, and nothing below was checked"
	cat "$tmp/golist.err"
	exit 1
fi
if ! make -q bin/zerg0 >/dev/null 2>&1; then
	echo "UNCONDITIONAL  \`bin/zerg0\` is out of date immediately after a build"
	exit 1
fi

seedstale=0
for f in $seed; do
	[ -f "$f" ] || continue
	if stale_after_touch bin/zerg0 "$f"; then
		echo "STALE     $f — the seed is built from it, and touching it leaves \`bin/zerg0\` up to date"
		seedstale=$((seedstale + 1))
	fi
done
fail=$((fail + seedstale))

if ! make -q bin/zerg >/dev/null 2>&1; then
	echo "build-deps-check: a seed timestamp was not put back — the next gate will rebuild for nothing"
	fail=$((fail + 1))
fi

if [ "$fail" -ne 0 ]; then
	echo "build-deps-check: $fail input(s) can change a binary without rebuilding it"
	exit 1
fi
echo "build-deps-check: $n files over $checked roots rebuild \`bin/zerg\`, and $nseed seed inputs over $nseeddirs directories rebuild \`bin/zerg0\`"
