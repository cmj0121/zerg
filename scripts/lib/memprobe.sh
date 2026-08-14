#!/usr/bin/env bash
# memprobe.sh <compiler> <file.zg> — emit, link against the counting allocator, run at
# 5 and 200 rounds, print both counter lines. A hand tool for locating which allocation
# a mem-check failure is about; not a gate and not on the board.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 2
RT=src/runtime/csrc
ZC=$1
SRC=$2
W="$(mktemp -d "${TMPDIR:-/tmp}/zerg-memprobe.XXXXXX")"
rt() {
	ls "$RT"/*.c | grep -Ev 'thread_win32|thread_none|ctx_ucontext|zrt_test|alloc\.c'
	case "$(uname -m)" in
	arm64 | aarch64) printf '%s/ctx_arm64.S\n' "$RT" ;;
	*) printf '%s/ctx_x86_64.S\n' "$RT" ;;
	esac
	printf 'scripts/lib/memcount.c\n'
}
"$ZC" build --emit c "$SRC" >"$W/a.c" || {
	echo "emit failed"
	exit 1
}
# shellcheck disable=SC2046
cc -std=c11 -g -I "$RT" -o "$W/a.bin" "$W/a.c" $(rt) || {
	echo "cc failed"
	exit 1
}
for n in 5 200; do
	printf '%4s: ' "$n"
	ZMEM_ROUNDS=$n ZRT_WORKERS=1 ZRT_SEED=1 "$W/a.bin" >/dev/null 2>"$W/e"
	tr '\n' ' ' <"$W/e"
	echo
done
echo "C: $W/a.c"
