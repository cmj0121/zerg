#!/usr/bin/env bash
#
# mem-peak-check — the compiler's OWN footprint, pinned.
#
# WHY THIS EXISTS. Building the compiler with itself once peaked at 2.4 GB to produce 3.6 MB of
# C, and nothing on the board could see it: `mem-check` is about a value outliving the scope
# that made it, which is a different question entirely. The peak was rediscovered by hand, twice,
# months apart. #10.
#
# WHAT IS MEASURED IS `--emit c`, and the choice is the whole design of this gate:
#
#   * ONE PROCESS. `--emit bin` spawns `cc`, whose resident size lands in the same rusage and
#     moves with whichever C compiler the machine has. What is being pinned is the EMITTER.
#   * NO `-j`. A job count makes the number a function of the runner's CPUs, so the same tree
#     would pass on one machine and fail on the next.
#   * THE WHOLE PROGRAM AS ONE UNIT, which is the largest thing this compiler is ever asked to
#     assemble in memory — 4.65 MB of C from ~1500 functions.
#
# THE CEILING IS A REGRESSION ALARM AND NOT A TARGET. It sits above what a healthy build costs
# and below what the defect costs: the quadratic accumulation this was filed against measured
# 355 MB on this same command, and the healthy number today is 89 MB. A change that puts the
# accumulation back turns this red; a change that costs a few megabytes does not, and should
# not — a gate that fails on noise is a gate people learn to re-run.
#
# BOTH `time` FORMATS ARE READ, because CI runs Linux and macOS: BSD `time -l` reports
# "maximum resident set size" in BYTES, GNU `time -v` reports "Maximum resident set size" in
# KILOBYTES. Reading one and assuming the other is a factor of 1024 in whichever direction
# nobody tested.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

ZERG=${ZERG:-./bin/zerg}
ENTRY=${ENTRY:-src/compiler/zergc.zg}
MEM_PEAK_MAX_MB=${MEM_PEAK_MAX_MB:-320}

[ -x "$ZERG" ] || {
	printf 'mem-peak: %s is not there — run `make build` first\n' "$ZERG" >&2
	exit 2
}

tmp=$(mktemp -d) || exit 2
trap 'rm -rf "$tmp"' EXIT

# `-l` is BSD and `-v` is GNU, and neither accepts the other's flag — so the shape of the
# available `time` is discovered rather than assumed from the platform name.
if /usr/bin/time -l true >/dev/null 2>&1; then
	flag=-l
	unit=bytes
elif /usr/bin/time -v true >/dev/null 2>&1; then
	flag=-v
	unit=kb
else
	printf 'mem-peak: /usr/bin/time understands neither -l nor -v — nothing here can measure a peak\n' >&2
	exit 2
fi

/usr/bin/time $flag "$ZERG" build --emit c "$ENTRY" >"$tmp/out.c" 2>"$tmp/time.txt"
status=$?
if [ $status -ne 0 ]; then
	printf 'mem-peak: the build this gate measures did not succeed (exit %s)\n' "$status" >&2
	sed 's/^/    /' "$tmp/time.txt" >&2
	exit 1
fi

raw=$(grep -iE 'maximum resident set size' "$tmp/time.txt" | grep -oE '[0-9]+' | head -1)
[ -n "$raw" ] || {
	printf 'mem-peak: /usr/bin/time %s printed no "maximum resident set size" — the measurement found nothing\n' "$flag" >&2
	sed 's/^/    /' "$tmp/time.txt" >&2
	exit 1
}

if [ "$unit" = bytes ]; then
	mb=$((raw / 1024 / 1024))
else
	mb=$((raw / 1024))
fi

# A FLOOR UNDER THE MEASUREMENT ITSELF. A `time` that printed a zero, or an entry that stopped
# resolving into a real program, would report a peak of nothing and pass — which is the failure
# every gate here is written against, one level up. The emitter cannot lower this compiler in
# under 16 MB.
if [ "$mb" -lt 16 ]; then
	printf 'mem-peak: a peak of %s MB is not a measurement — the command cannot have built %s\n' "$mb" "$ENTRY" >&2
	exit 1
fi

artifact=$(wc -c <"$tmp/out.c" | tr -d ' ')
if [ "$mb" -gt "$MEM_PEAK_MAX_MB" ]; then
	printf 'mem-peak: %s peaked at %s MB emitting %s bytes of C, and the ceiling is %s MB\n' \
		"$ENTRY" "$mb" "$artifact" "$MEM_PEAK_MAX_MB" >&2
	printf '          the accumulation this gate exists for measured 355 MB on this command; healthy is under 100\n' >&2
	exit 1
fi

# --- and the REFUSAL that stands where the OOM killer used to -------------------------------
#
# The ceiling above is a gate's opinion about a healthy build. This is the compiler's own, and
# it is the half that matters to somebody who is not us: a program too large to assemble was
# SIGKILLed — no code, no place, nothing to read — and E5016 is what stands there now.
#
# IT IS ASSERTED HERE rather than in `make reject`, because that harness runs a case as a
# program and this refusal is about a program's SIZE: the only way to write a case for it is to
# lower the ceiling, and the ceiling is an environment variable. The two questions — what a
# healthy build costs, and what happens past the ceiling — are one subject, measured by one
# command.
if ! ZERG_EMIT_MAX=100000 "$ZERG" build --emit c "$ENTRY" >/dev/null 2>"$tmp/over.txt"; then
	grep -q 'E5016' "$tmp/over.txt" || {
		printf 'mem-peak: a build past $ZERG_EMIT_MAX was refused, and not by E5016:\n' >&2
		sed 's/^/    /' "$tmp/over.txt" >&2
		exit 1
	}
else
	printf 'mem-peak: $ZERG_EMIT_MAX=100000 built %s bytes of C — the budget is not enforced\n' "$artifact" >&2
	exit 1
fi

# AND THE ESCAPE WORKS. A budget nobody can turn off is a ceiling on the language rather than a
# guard on the compiler, so `0` has to mean what it says.
ZERG_EMIT_MAX=0 "$ZERG" build --emit c "$ENTRY" >/dev/null 2>"$tmp/off.txt" || {
	printf 'mem-peak: $ZERG_EMIT_MAX=0 did not turn the budget off:\n' >&2
	sed 's/^/    /' "$tmp/off.txt" >&2
	exit 1
}

printf 'mem-peak: %s MB to emit %s bytes of C, under the %s MB ceiling, and E5016 past $ZERG_EMIT_MAX\n' "$mb" "$artifact" "$MEM_PEAK_MAX_MB"
