#!/usr/bin/env bash
#
# mem-check.sh — a value that outlives its scope is a leak, and this is what counts them.
#
# WHAT IT PROVES: for each case below, the number of allocations still LIVE at exit does
# not depend on how many rounds the program ran. The same program is run twice, at 5
# rounds and at 200, and the two live counts must be equal. A value that is allocated per
# round and never freed makes that difference grow linearly, which is the shape every one
# of these cases was written to have.
#
# WHAT IT CANNOT SEE: a BOUNDED leak. One allocation per program, or one per closure SITE
# rather than one per construction, is identical at 5 rounds and at 200 and passes here
# untouched. This gate is "no per-round leak", never "no leak" — do not read it as the
# stronger sentence.
#
# WHY NOT `live == 0`: the runtime keeps deliberate live allocations at exit (a heap
# cause on zrt_err_chain, a channel's crash message) and reaches the platform around the
# counter in several places (unwind.c's bare realloc, sched.c's mmap, sys.c's argv). Every
# one of those is a CONSTANT, so a difference is immune to it and an absolute zero is not
# reachable. The floor below is what stops the difference form from passing by measuring
# nothing: total(200) - total(5) must be positive, so a case that allocates nothing at all
# fails instead of looking clean.
#
# WHY NOT LeakSanitizer: it does not exist on macOS, and `make sanitize-conc` — the only
# gate that has it — reads the PRIVATE corpus, which does not exist on a fork's CI. A fix
# defended only where the submodule and LSan both happen to be present is a fix with no
# gate. The programs here are therefore written in this file, the way refuse-check.sh and
# reject-check.sh write theirs, and for the same stated reason: they are not programs that
# must run correctly, they are contracts.
#
# HOW IT COUNTS: scripts/lib/memcount.c replaces the runtime's alloc.c, exactly as
# src/runtime/runtime_test.go already does for its map suite. The C is emitted with
# `zerg build --emit c` and linked here because `zerg build` decides the whole cc
# invocation itself and has nowhere to put a flag — the same reason sanitize-conc.sh
# drives cc by hand.
#
# BOTH COMPILERS: a case the Go seed can also build is built by BOTH, and both must
# balance. Two of the three leaks this gate was written for are rules the self-hosting
# compiler LOST on the way out of the seed — the seed frees a recursive chain and a
# named carrier correctly today — so the seed is the oracle here, and running it holds
# that in place rather than trusting a paragraph to remember it.
#
# A CONCURRENT CASE PINS ITS SCHEDULE: ZRT_WORKERS=1 and a fixed ZRT_SEED. With several
# workers the count at exit depends on who finished last, and a gate whose number drifts
# is a gate whose failures get ignored.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

ZERG="${ZERG:-$ROOT/bin/zerg}"
ZERG0="${ZERG0:-$ROOT/bin/zerg0}"
CC="${CC:-cc}"
RT="src/runtime/csrc"

# The two round counts. They are far apart on purpose: a per-round leak of one allocation
# shows up as a difference of 195, which no amount of allocator noise imitates.
FEW="${FEW:-5}"
MANY="${MANY:-200}"

# How many cases must actually be measured. Every assertion here is of the form "these two
# numbers agree", which an empty list satisfies — so a typo in the case list would leave a
# green gate that built nothing.
MIN_CASES="${MIN_CASES:-9}"

[ -x "$ZERG" ] || {
	printf 'mem-check: %s is not built — run `make build` first\n' "$ZERG" >&2
	exit 2
}
[ -x "$ZERG0" ] || {
	printf 'mem-check: %s is not built — run `make build` first\n' "$ZERG0" >&2
	exit 2
}

# The runtime units to link, derived by REMOVING the alternates rather than by listing the
# keepers — the same subtraction sanitize-conc.sh makes, and for the reason it gives: a
# list of keepers goes stale the day the runtime grows a unit. alloc.c comes out here
# because memcount.c takes its place.
rt_sources() {
	ls "$RT"/*.c | grep -Ev 'thread_win32|thread_none|ctx_ucontext|zrt_test|alloc\.c'
	case "$(uname -m)" in
	arm64 | aarch64) printf '%s/ctx_arm64.S\n' "$RT" ;;
	x86_64 | amd64) printf '%s/ctx_x86_64.S\n' "$RT" ;;
	*) printf '%s/ctx_ucontext.c\n' "$RT" ;;
	esac
	printf 'scripts/lib/memcount.c\n'
}

WORK="$(mktemp -d "${TMPDIR:-/tmp}/zerg-memcheck.XXXXXX")" || exit 2

fail=0
cases=0

# Every case reads its round count from the environment, so ONE binary answers both
# questions and the two runs differ in nothing else. A `rounds()` written per case would
# be one copy of the same six lines per case; this is the preamble each case is built with.
preamble='import "os"

fn rounds() -> int {
	v := os.env("ZMEM_ROUNDS")
	if s := v {
		return int(s)
	}
	return 5
}
'

# emit_case <name> — writes $WORK/<name>.zg from the preamble plus the body on stdin.
emit_case() {
	{
		printf '%s\n' "$preamble"
		cat
	} >"$WORK/$1.zg"
}

# run_at <bin> <rounds> [env...] — runs the binary and echoes "total live".
# The program's own answer goes to stdout and the counter line to stderr, so the two
# never have to be told apart by shape.
run_at() {
	local bin=$1 n=$2 conc=$3 line total live
	if [ "$conc" = yes ]; then
		ZMEM_ROUNDS="$n" ZRT_WORKERS=1 ZRT_SEED=1 "$bin" >/dev/null 2>"$WORK/err"
	else
		ZMEM_ROUNDS="$n" "$bin" >/dev/null 2>"$WORK/err"
	fi
	local rc=$?
	if [ "$rc" -ne 0 ]; then
		printf 'RUN    %s at %s rounds exited %s\n' "$bin" "$n" "$rc" >&2
		head -5 "$WORK/err" >&2
		return 1
	fi
	line="$(grep '^zrt-mem: ' "$WORK/err" | tail -1)"
	[ -n "$line" ] || {
		printf 'RUN    %s at %s rounds printed no counter line\n' "$bin" "$n" >&2
		return 1
	}
	total="${line#*total=}"
	total="${total%% *}"
	live="${line#*live=}"
	printf '%s %s\n' "$total" "$live"
}

# measure <label> <compiler> <name> <conc> — build the case with one compiler and hold
# its live count at MANY rounds to its live count at FEW.
measure() {
	local label=$1 zc=$2 name=$3 conc=$4
	local c="$WORK/$name.$label.c" bin="$WORK/$name.$label.bin"

	if ! "$zc" build --emit c "$WORK/$name.zg" >"$c" 2>"$WORK/$name.$label.emit.log"; then
		printf 'EMIT   %-14s (%s)\n' "$name" "$label"
		head -5 "$WORK/$name.$label.emit.log"
		fail=1
		return
	fi
	# shellcheck disable=SC2046  # one path per line, no spaces
	if ! $CC -std=c11 -g -I "$RT" -o "$bin" "$c" $(rt_sources) 2>"$WORK/$name.$label.cc.log"; then
		printf 'CC     %-14s (%s)\n' "$name" "$label"
		head -10 "$WORK/$name.$label.cc.log"
		fail=1
		return
	fi

	local few many
	few="$(run_at "$bin" "$FEW" "$conc")" || {
		fail=1
		return
	}
	many="$(run_at "$bin" "$MANY" "$conc")" || {
		fail=1
		return
	}

	local tf=${few% *} lf=${few#* } tm=${many% *} lm=${many#* }
	local dlive=$((lm - lf)) dtotal=$((tm - tf))

	if [ "$dtotal" -le 0 ]; then
		printf 'FLOOR  %-14s (%s) allocated nothing per round — total %s at %s rounds, %s at %s\n' \
			"$name" "$label" "$tf" "$FEW" "$tm" "$MANY"
		fail=1
		return
	fi
	if [ "$dlive" -ne 0 ]; then
		printf 'LEAK   %-14s (%s) live %s at %s rounds, %s at %s — %s per round unfreed (total +%s)\n' \
			"$name" "$label" "$lf" "$FEW" "$lm" "$MANY" "$dlive" "$dtotal"
		fail=1
		return
	fi
	printf 'ok     %-14s (%-5s) live %s = %s, total +%s over %s rounds\n' \
		"$name" "$label" "$lf" "$lm" "$dtotal" "$((MANY - FEW))"
}

# case_run <name> <seed?> <conc?> — the body arrives on stdin.
case_run() {
	local name=$1 seed=$2 conc=$3
	emit_case "$name"
	cases=$((cases + 1))
	measure zerg "$ZERG" "$name" "$conc"
	[ "$seed" = yes ] && measure seed "$ZERG0" "$name" "$conc"
	return 0
}

printf 'mem-check: %s rounds against %s, live counts must be equal\n\n' "$FEW" "$MANY"

# --- the recursive enum ------------------------------------------------------------
# A chain of 2000 boxed cells built and dropped per round. The auto-boxed slot is the
# one place this language allocates without the programmer writing an allocation, and
# `acc = L.Cons(i, acc)` is the reassignment whose new value READS the old one — a drop
# emitted before the right-hand side is materialised is a use-after-free, not a leak.
case_run rec_chain yes no <<'ZG'
enum L {
	Nil
	Cons(int, L)
}

fn build(n: int) -> L {
	mut acc := L.Nil
	mut i := 0
	for i < n {
		acc = L.Cons(i, acc)
		i = i + 1
	}
	return acc
}

fn head(l: L) -> int {
	return match l {
		L.Nil => -1
		L.Cons(v, _) => v
	}
}

fn main() {
	mut total := 0
	mut r := 0
	n := rounds()
	for r < n {
		c := build(2000)
		total = total + head(c)
		r = r + 1
	}
	print total
}
ZG

# --- an enum payload that is not the recursive slot -----------------------------------
# `rec_chain` above walks ONE branch of the payload walk: an `int` and the boxed slot. The
# other branch — a payload that owns something WITHOUT being the enum itself — was
# unmeasured, and it is a different two lines in the copy and the drop helper. The
# constructor retains the payload whatever the enum does with it, which is why the enum
# owing nothing back showed up here as a leak before it was anything else.
case_run enum_payload yes no <<'ZG'
enum Tag {
	None
	Name(str)
	Names(list[str])
}

fn size(t: Tag) -> int {
	return match t {
		Tag.None => 0
		Tag.Name(s) => bytearray(s).len()
		Tag.Names(xs) => xs.len()
	}
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		a := str(i) + "!"
		t := Tag.Name(a)
		dup := t
		mut ys: list[str] = []
		ys.append(a)
		v := Tag.Names(ys)
		n = n + size(t) + size(dup) + size(v)
		i = i + 1
	}
	print n
}
ZG

# --- a carrier whose payload is a recursive enum --------------------------------------
# The two units meet here. An `L?` is a carrier whose copy and drop are the ENUM's, reached
# through the carrier's own pair, and a `list[L?]` reaches both through an element vtable —
# so this is the one case where a regression in either dispatch shows up as the other one's
# fault. The chain is short because the subject is the plumbing, not the depth.
case_run carrier_rec yes no <<'ZG'
enum L {
	Nil
	Cons(int, L)
}

fn build(n: int) -> L {
	mut acc := L.Nil
	mut i := 0
	for i < n {
		acc = L.Cons(i, acc)
		i = i + 1
	}
	return acc
}

fn head(l: L) -> int {
	return match l {
		L.Nil => -1
		L.Cons(v, _) => v
	}
}

fn pick(i: int) -> L? {
	return nil if i % 7 == 0
	return build(50)
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		got := pick(i)
		mut xs: list[L?] = []
		xs.append(got)
		e := xs[0]
		if c := e {
			n = n + head(c)
		}
		i = i + 1
	}
	print n
}
ZG

# --- a carrier that is given a name -------------------------------------------------
# THE PAYLOAD IS COMPUTED, and that is not decoration. A string LITERAL compiles to a
# static cell marked ZRT_RC_IMMORTAL and is never allocated at all, so the same case
# written `return "x"` measures nothing and reads as "the seed does not free it either".
case_run carrier_opt yes no <<'ZG'
fn pick(i: int) -> str? {
	return nil if i % 7 == 0
	return str(i) + "!"
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		got := pick(i)
		if s := got {
			n = n + bytearray(s).len()
		}
		i = i + 1
	}
	print n
}
ZG

# --- a named carrier passed as an argument ------------------------------------------
# The callee registers a drop for every by-value parameter. If the call site handed the
# carrier over as a bare bit-copy, one payload would be given back twice.
case_run carrier_arg yes no <<'ZG'
fn pick(i: int) -> str? {
	return nil if i % 7 == 0
	return str(i) + "!"
}

fn take(v: str?) -> int {
	if s := v {
		return bytearray(s).len()
	}
	return 0
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		got := pick(i)
		n = n + take(got)
		i = i + 1
	}
	print n
}
ZG

# --- a named carrier returned --------------------------------------------------------
# The return copies the carrier out and then unwinds the scope that held it. Copy and
# unwind have to agree, or the caller reads a payload that has already been given back.
case_run carrier_ret yes no <<'ZG'
fn pick(i: int) -> str? {
	return nil if i % 7 == 0
	return str(i) + "!"
}

fn relay(i: int) -> str? {
	got := pick(i)
	return got
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		v := relay(i)
		if s := v {
			n = n + bytearray(s).len()
		}
		i = i + 1
	}
	print n
}
ZG

# --- a carrier inside a composite ----------------------------------------------------
# A struct field and a list element reach the carrier through the per-type copy and drop
# helpers rather than through a binding, which is a different pair of call sites.
case_run carrier_field yes no <<'ZG'
struct Box {
	pub tag: int
	pub val: str?
}

fn pick(i: int) -> str? {
	return nil if i % 7 == 0
	return str(i) + "!"
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		p := pick(i)
		b := Box(i, p)
		dup := b
		q := pick(i + 1)
		mut xs: list[str?] = []
		xs.append(q)
		xs.append(dup.val)
		e := xs[0]
		if s := e {
			n = n + bytearray(s).len()
		}
		f := b.val
		if s := f {
			n = n + bytearray(s).len()
		}
		i = i + 1
	}
	print n
}
ZG

# --- a carrier out of a channel -------------------------------------------------------
# `got := <-c` is the form the deviation names. The seed has no concurrency at all — it
# turns `ch <- v` away by name — so this one case has no second compiler to agree with.
case_run carrier_chan no yes <<'ZG'
fn produce(n: int, out: chan[str]<-) {
	mut i := 0
	for i < n {
		v := str(i) + "!"
		out <- v
		i = i + 1
	}
	close(out)
}

fn main() {
	mut n := 0
	r := rounds()
	c := chan[str](4)
	spawn produce(r, c)
	for {
		got := <-c
		if s := got {
			n = n + bytearray(s).len()
		} else {
			break
		}
	}
	print n
}
ZG

# --- a closure environment -------------------------------------------------------------
# One environment per construction, and the closure may outlive the scope that made it —
# which is what closing over a value is for, and why the scope cannot be what frees it.
# The seed refuses a closure used as a value by name, so again there is no second opinion.
case_run closure_env no no <<'ZG'
fn make_adder(base: str) -> fn(int) -> int {
	return fn(k: int) -> int {
		return bytearray(base).len() + k
	}
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		b := str(i) + "!"
		f := make_adder(b)
		n = n + f(1)
		i = i + 1
	}
	print n
}
ZG

if [ "$fail" -ne 0 ]; then
	printf '\nmem-check: a value outlives the scope that made it\n' >&2
	printf 'mem-check: the sources, the C and the binaries are kept in %s\n' "$WORK" >&2
	exit 1
fi
rm -rf "$WORK"
if [ "$cases" -lt "$MIN_CASES" ]; then
	printf '\nmem-check: only %s cases were measured, and the floor is %s\n' "$cases" "$MIN_CASES" >&2
	exit 1
fi
printf '\nmem-check: %s cases, no per-round leak\n' "$cases"
