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
MIN_CASES="${MIN_CASES:-18}"

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

# measure_free <label> <compiler> <name> — the OPPOSITE claim to `measure`'s, for the one
# property in this repository that is about an allocation NOT happening: a disabled log entry.
#
# `lg.at_level(log.Level.DEBUG)` at a level that is off answers a dead builder, and `log`'s whole
# design rests on that costing NOTHING — no field formatted, no list grown, no `[]` made.
# "Costs nothing" is not something a test can assert and not something a benchmark can prove;
# it is a COUNT, and this file already counts. So the two runs are made and the totals must be
# EQUAL: whatever a round did, it did not allocate.
#
# ITS FLOOR IS THE SAME PROGRAM WITH THE LEVEL ON. `measure` floors itself on `dtotal > 0`,
# because a case that allocates nothing is a case that measured nothing; this one cannot,
# since zero is the answer it wants. So the binary is run a second pair of times with
# ZLOG_ON=1, which turns the level up and nothing else, and THAT pair must allocate. A loop
# that stopped running, a counter that stopped counting, or a builder the compiler folded away
# entirely all fail there rather than passing quietly here.
#
# THE ON PAIR IS NOT HELD TO `live`. It allocates and it does not free everything: a `str`
# built and returned by a call leaks in this compiler, which `strings.join` does too and which
# is not this module's to fix. The claim here is exactly the one that is about `log`.
measure_free() {
	local label=$1 zc=$2 name=$3
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

	local off_few off_many on_few on_many
	off_few="$(run_at "$bin" "$FEW" no)" || {
		fail=1
		return
	}
	off_many="$(run_at "$bin" "$MANY" no)" || {
		fail=1
		return
	}
	on_few="$(ZLOG_ON=1 run_at "$bin" "$FEW" no)" || {
		fail=1
		return
	}
	on_many="$(ZLOG_ON=1 run_at "$bin" "$MANY" no)" || {
		fail=1
		return
	}

	local d_off=$(( ${off_many% *} - ${off_few% *} ))
	local d_on=$(( ${on_many% *} - ${on_few% *} ))

	if [ "$d_on" -le 0 ]; then
		printf 'FLOOR  %-14s (%s) allocated nothing with the level ON — the case measured nothing\n' \
			"$name" "$label"
		fail=1
		return
	fi
	if [ "$d_off" -ne 0 ]; then
		printf 'ALLOC  %-14s (%s) allocated %s more over %s extra rounds with the level OFF — a disabled line must allocate NOTHING\n' \
			"$name" "$label" "$d_off" "$((MANY - FEW))"
		fail=1
		return
	fi
	printf 'ok     %-14s (%-5s) off +0, on +%s over %s rounds — a disabled line allocates nothing\n' \
		"$name" "$label" "$d_on" "$((MANY - FEW))"
}

# case_free <name> — the body arrives on stdin. `zerg` only: the seed cannot parse the
# module-level `unsafe { }` group `log` holds its global logger in.
case_free() {
	local name=$1
	emit_case "$name"
	cases=$((cases + 1))
	measure_free zerg "$ZERG" "$name"
	return 0
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

# --- a tuple that owns something ------------------------------------------------------
# THE THIRD COMPOSITE, and the one that had a copy helper and no drop at all: `t := (i, s)`
# retained the str and nothing ever gave it back. It is one allocation a round, which is
# exactly the shape this gate reads, and it went unmeasured because every carrier case
# above holds a str DIRECTLY.
#
# The same missing half is why `(int, str)?` did not compile: the carrier decided whether to
# emit its copy/drop pair from the drop question and its callers named the pair from
# c_needs_copy, and a tuple was the type the two disagreed about. So the carrier of a tuple
# is here beside the bare one — the leak and the cc error were one defect.
#
# The list of tuples reaches the same pair through an element vtable, and `(int, list[int])`
# is the other kind of heap element, so the three call sites are all in one round. NO `!`:
# force-unwrap discards the payload it copies out, for every carrier and not just this one,
# and a case measuring that belongs with that defect rather than hiding inside this one.
case_run tuple_heap no no <<'ZG'
fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		a := str(i) + "!"
		t := (i, a)
		dup := t

		mut ys: list[int] = []
		ys.append(i)
		u := (i, ys)
		v := u

		mut rows: list[(int, str)] = []
		rows.append(t)
		more := rows

		p: (int, str)? = t
		q := p
		if g := q {
			n = n + g.0
		}

		n = n + bytearray(dup.1).len() + v.1[0] + more[0].0
		i = i + 1
	}
	print n
}
ZG

# --- a spawn the scheduler never gets to ----------------------------------------------
# THE ONE LEAK CLASS THIS FILE COULD NOT SEE, and the reason is in its own header: every
# concurrent case here runs its coroutines to completion. A `spawn`'s environment is filled
# at the spawn site — a reference taken per captured value — and released by the BODY, so a
# coroutine the scheduler never reaches gave none of it back. `main` returning ends the
# program and whatever is queued stays where it is, which is the language's rule and not the
# defect; the defect was that what stayed was never freed.
#
# `main` never touches the channel, so with one worker it runs to its `print` without ever
# yielding and not one of the spawned coroutines starts. That is the case, exactly.
#
# It captures a `str` AND a channel, because the two are given back by different halves of
# the same teardown, and a sweep that ran only one of them would still balance the count on
# a case that captured only the other.
case_run spawn_unstarted no yes <<'ZG'
fn sink(s: str, ch: chan[int]) {
	ch <- bytearray(s).len()
}

fn main() {
	ch := chan[int](1)
	mut i := 0
	r := rounds()
	for i < r {
		s := str(i) + "-never-scheduled"
		spawn sink(s, ch)
		i = i + 1
	}
	print 1
}
ZG

# --- an Either whose RIGHT owns something ---------------------------------------------
# THE OTHER SIDE, and every carrier case above holds its payload on the LEFT. Whether a
# carrier owned anything at all used to be asked of the Left's type alone, so an
# `Either[int, str]` was judged to own nothing and got no copy helper and NO DROP AT ALL —
# while the wrap that builds a Right hands it a reference. One allocation leaked per Right
# constructed, which is exactly the shape this gate reads, and no case here could see it
# because none of them put anything on the right-hand side.
#
# A `Result[T]` would not have found it either: its Right is an `Err`, whose storage is the
# runtime's, so the widened question answers the same for it as the narrow one did.
#
# The list element is here for the same reason it is in the tuple case: a
# `list[Either[int, str]]` reaches the pair through an element vtable rather than by name,
# and the ownership question is asked a second time to build that vtable. Both asks were
# wrong, so both are measured.
#
# The seed does not build it — it has no `Either.Right` — so this one is `zerg` alone.
case_run either_right no no <<'ZG'
fn mk(i: int) -> Either[int, str] {
	return Either.Right(str(i) + "!") if i % 2 == 0
	return Either.Left(i)
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		e := mk(i)
		f := e
		if g := f {
			n = n + g
		}

		mut rows: list[Either[int, str]] = []
		rows.append(e)
		more := rows
		if h := more[0] {
			n = n + h
		}
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

# --- a channel held by something else --------------------------------------------------
# A channel end is a counted handle. Storing one in a holder — a list, a map, a struct, a
# returned value — takes a count of its own, and that holder gives it back. A call argument
# does not: the callee borrows the caller's handle for the call. The seed has no concurrency,
# so this case has no second compiler to agree with.
case_run chan_holder no yes <<'ZG'
fn mk(n: int) -> chan[int] {
	c := chan[int](1)
	c <- n
	return c
}

fn expect(c: chan[int]) {
	v := <-c ?? 0
	raise "channel held the wrong value" if v != 7
}

fn keep(c: chan[int]) {
}

fn one(n: int) -> list[chan[int]] {
	c := chan[int](1)
	c <- n
	return [c]
}

fn twice() -> fn () -> int {
	c := chan[int](2)
	c <- 7
	c <- 7
	return fn () -> int {
		return <-c ?? 0
	}
}

fn some(n: int) -> chan[int]? {
	c := mk(n)
	return c
}

fn put(mut &x: chan[int], y: chan[int]) {
	x = y
}

fn pass(c: chan[int]) -> int {
	return <-c ?? 0
}

fn give(n: int) -> chan[int] {
	return mk(n)
}

fn src() -> <-chan[int] {
	return mk(7)
}

fn worker(c: chan[int]<-) {
	c <- 7
}

struct W {
	pub c: chan[int]
}

enum E {
	Has(chan[int])
	None
}

fn round() {
	xs := one(7)
	c0 := xs[0]
	expect(c0)

	f := twice()
	raise "closure" if f() != 7
	raise "closure again" if f() != 7

	if c1 := some(7) {
		expect(c1)
	} else {
		raise "optional return"
	}

	mut a := mk(1)
	v := mk(7)
	a = v
	expect(a)

	mut b := mk(1)
	w := mk(7)
	put(b, w)
	expect(b)

	missing: chan[int]? = nil
	u := mk(7)
	d0 := missing ?? u
	expect(d0)

	present: chan[int]? = mk(7)
	d1 := present ?? mk(1)
	expect(d1)

	held := mk(7)
	from_block := {
		k := 1
		held
	}
	expect(from_block)

	k := 1
	arm := mk(7)
	from_match := match k {
		1 => arm
		_ => mk(2)
	}
	expect(from_match)

	named := mk(7)
	box := Ref(named)
	from_box := deref(box)
	expect(from_box)

	fresh := Ref(mk(7))
	left := deref(fresh)
	right := deref(fresh)
	expect(left)
	keep(right)

	listed := [mk(7)]
	expect(listed[0])

	mapped := {"a": mk(7)}
	expect(mapped["a"])

	tup := (mk(7), 1)
	expect(tup.0)

	arr: [chan[int]; 1] = [mk(7)]
	expect(arr[0])

	st := W(mk(7))
	expect(st.c)

	taken := W(mk(7)).c
	expect(taken)

	en := E.Has(mk(7))
	from_enum := match en {
		E.Has(ch) => ch
		E.None    => mk(0)
	}
	expect(from_enum)

	opt: chan[int]? = mk(7)
	if c2 := opt {
		expect(c2)
	} else {
		raise "optional binding"
	}

	cell := Ref(mk(7))
	expect(deref(cell))

	own := mk(7)
	alias := own
	expect(alias)

	borrowed := mk(7)
	raise "borrowed argument" if pass(borrowed) != 7

	fv := pass
	called := mk(7)
	raise "function value" if fv(called) != 7

	gave := give(7)
	expect(gave)

	both := mk(7)
	bag := [both]
	expect(bag[0])

	rx := src()
	raise "narrowed return" if (<-rx ?? 0) != 7

	spawned := chan[int](1)
	spawn worker(spawned)
	expect(spawned)

	indexed := one(7)[0]
	expect(indexed)

	# a receive takes the copy the send moved in, and gives it back. The sender's own
	# name is a different count: draining the copy must leave that name intact.
	inbox := chan[E](1)
	carried := mk(7)
	inbox <- E.Has(carried)
	close(inbox)

	for m in inbox {
		got := match m {
			E.Has(ch) => ch
			E.None    => mk(0)
		}
		expect(got)
	}

	pipe := chan[chan[int]](1)
	reply := mk(7)
	pipe <- reply
	close(pipe)

	for once in pipe {
		expect(once)
	}

	nest := chan[chan[int]](1)
	inner := mk(7)
	nest <- inner
	raise "nested receive" if (<-((<-nest)!) ?? 0) != 7

	sel := chan[chan[int]](1)
	held := mk(7)
	sel <- held

	select {
		got := <-sel => expect(got)
	}
}

fn main() {
	mut i := 0
	r := rounds()
	for i < r {
		round()
		i = i + 1
	}
}
ZG

# --- a value received out of a channel -------------------------------------------------
# A send moves an owned copy into the channel, and whoever takes it out owns it. A `for … in`
# over a channel and a `select` receive arm each wrote that value into a slot and never gave
# it back, so every element received was one allocation kept (#325, #336). Both an element
# that is one allocation (a `str`) and one that owns another (a `list[int]`) are walked and
# bound; the walk is over a channel a call answered, the arm over one a name holds, inside a
# `for select` as well as a single `select`. A wrong element aborts.
case_run chan_received no yes <<'ZG'
fn word(n: int) -> str {
	return str(n) + "!"
}

fn pair(n: int) -> list[int] {
	return [n, n + 1]
}

fn lists(n: int) -> chan[list[int]] {
	c := chan[list[int]](2)
	c <- pair(n)
	c <- pair(n + 1)
	close(c)
	return c
}

fn words(n: int) -> chan[str] {
	c := chan[str](2)
	c <- word(n)
	c <- word(n + 1)
	close(c)
	return c
}

fn round(n: int) {
	mut t := 0
	for xs in lists(n) {
		t = t + xs[1]
	}
	raise "a walked channel of lists gave the wrong elements" if t != n + n + 3

	mut u := ""
	for s in words(n) {
		u = u + s
	}
	raise "a walked channel of strs gave the wrong elements" if u != word(n) + word(n + 1)

	c := chan[str](1)
	c <- word(n)
	select {
		v := <-c => {
			raise "a select arm bound the wrong str" if v != word(n)
		}
	}

	d := chan[list[int]](1)
	d <- pair(n)
	select {
		v := <-d => {
			raise "a select arm bound the wrong list" if v[1] != n + 1
		}
	}

	e := words(n)
	mut seen := ""
	for select {
		v := <-e => {
			seen = seen + v
		}
	}
	raise "a looped select arm bound the wrong strs" if seen != word(n) + word(n + 1)
}

fn main() {
	mut i := 0
	r := rounds()
	for i < r {
		round(i)
		i = i + 1
	}
}
ZG

# --- a Ref of a type whose name is not a C identifier ----------------------------------
# The helpers a `Ref` copies and drops its payload with are named after the payload's type,
# and a type's name is spelled with more than a C identifier allows: a function type has
# `(`, `)`, `-` and `>`, a tuple its parentheses, an optional `?`, an array `;`. Each went
# into the helper's name as written and cc reported the C (#339). A function, a closure, a
# list of functions, a tuple that owns a str and a list, an optional and an array are each
# boxed and read back; a wrong answer aborts. The seed has no closure value, so it is zerg
# only.
case_run ref_payloads no no <<'ZG'
fn word(n: int) -> str {
	return str(n) + "!"
}

fn inc(n: int) -> int {
	return n + 1
}

fn round(n: int) {
	rf := Ref(inc)
	f := deref(rf)
	raise "a Ref of a function called the wrong one" if f(n) != n + 1

	rc := Ref(
		fn (k: int) -> int {
			return k + n
		}
	)
	g := deref(rc)
	raise "a Ref of a closure lost what it captured" if g(1) != n + 1

	rl := Ref([inc])
	fs := deref(rl)
	raise "a Ref of a list of functions called the wrong one" if fs[0](n) != n + 1

	rt := Ref((word(n), [n, n + 1]))
	t := deref(rt)

	raise "a Ref of a tuple read back the wrong str" if t.0 != word(n)
	raise "a Ref of a tuple read back the wrong list" if t.1[1] != n + 1

	o: str? = word(n)
	ro := Ref(o)
	raise "a Ref of an optional read back the wrong value" if (deref(ro) ?? "") != word(n)

	a: [int; 2] = [n, n + 1]
	ra := Ref(a)
	b := deref(ra)
	raise "a Ref of an array read back the wrong elements" if b[0] + b[1] != n + n + 1
}

fn main() {
	mut i := 0
	r := rounds()
	for i < r {
		round(i)
		i = i + 1
	}
}
ZG

# --- the checked index ------------------------------------------------------------------
# `xs.get(i)` and `m.get(k)` answer an optional that OWNS a copy of the element, so every
# consumer gives one back: `??`, a binding, a discarded statement, `if v := …`, a return. The
# receiver is a name, a call's result and a literal; the map's key is a name and a str the
# expression built, which the lookup only reads. An absent index or key answers nothing and
# allocates nothing. A wrong answer aborts, and a list read with `get` must not share its
# buffer with the list it came from. The seed copies no map, so it is zerg only.
case_run checked_get no no <<'ZG'
fn word(n: int) -> str {
	return str(n) + "!"
}

fn words(n: int) -> list[str] {
	return [word(n), word(n + 1)]
}

struct P {
	pub n: int
	pub s: str
}

fn first(xs: list[str]) -> str? {
	return xs.get(0)
}

fn round(n: int) {
	xs := words(n)

	raise "a list `get` answered the wrong element" if (xs.get(1) ?? "") != word(n + 1)
	raise "a list `get` past the end answered an element" if (xs.get(2) ?? "none") != "none"
	raise "a list `get` at a negative index answered an element" if (xs.get(-1) ?? "none") != "none"

	a := xs.get(0)
	raise "a bound list `get` answered the wrong element" if (a ?? "") != word(n)
	xs.get(1)
	if v := xs.get(0) {
		raise "an unwrapped list `get` answered the wrong element" if v != word(n)
	}

	raise "a `get` on a list a call answered was wrong" if (words(n).get(0) ?? "") != word(n)
	raise "a `get` on a list a call answered found an element past the end" if (words(n).get(9) ?? "none") != "none"
	raise "a returned `get` was wrong" if (first(xs) ?? "") != word(n)

	mut ls := [[n], [n + 1]]
	mut got := ls.get(0) ?? []
	got.append(7)
	raise "a list read with `get` shared its buffer with the list it came from" if ls[0].len() != 1

	ps := [P(n, word(n))]
	if p := ps.get(0) {
		raise "a struct read with `get` was wrong" if p.s != word(n)
	}

	m := {word(n): word(n + 1), word(n + 2): word(n + 3)}

	raise "a map `get` answered the wrong value" if (m.get(word(n)) ?? "") != word(n + 1)
	raise "a map `get` of an absent key answered a value" if (m.get(word(n + 9)) ?? "anon") != "anon"

	k := word(n + 2)
	b := m.get(k)

	raise "a bound map `get` answered the wrong value" if (b ?? "") != word(n + 3)
	raise "a map `get` took the key it was asked with" if k != word(n + 2)

	raise "a `get` on a map literal was wrong" if ({word(n): [n, n]}.get(word(n)) ?? []).len() != 2
}

fn main() {
	mut i := 0
	r := rounds()
	for i < r {
		round(i)
		i = i + 1
	}
}
ZG

# --- two unnamed forms compared -----------------------------------------------------------
# `==` and `!=` over a list, a map, a set, a tuple and an array READ both sides and keep
# neither. An operand somebody else holds is looked at where it stands; one the expression
# made — a call's result, a literal — is given back once the answer is in hand, whichever
# way the answer went and however early the walk stopped. A part with an `Eq` is handed two
# copies, which its `eq` owns. A wrong answer aborts. The seed compares no container, so it
# is zerg only.
case_run container_equality no no <<'ZG'
#[derive(Eq)]
struct P {
	pub s: str
	pub xs: list[int]
}

fn word(n: int) -> str {
	return str(n) + "!"
}

fn words(n: int) -> list[str] {
	return [word(n), word(n + 1)]
}

fn same[T: Eq](a: T, b: T) -> bool {
	return a == b
}

fn round(n: int) {
	xs := words(n)
	ys := words(n)

	raise "two equal lists compared unequal" if xs != ys
	raise "two lists a call answered compared unequal" if words(n) != words(n)
	raise "two different lists compared equal" if words(n) == words(n + 1)
	raise "a list and a literal compared unequal" if xs != [word(n), word(n + 1)]
	raise "a list and a shorter one compared equal" if xs == [word(n)]
	raise "a held list was taken by the comparison" if xs[1] != word(n + 1)

	raise "two equal nested lists compared unequal" if [words(n), words(n)] != [xs, ys]
	raise "two different nested lists compared equal" if [words(n), words(n)] == [xs, words(n + 2)]

	m := {word(n): words(n), word(n + 1): words(n + 1)}
	k := {word(n + 1): words(n + 1), word(n): words(n)}
	j := {word(n + 1): words(n + 1), word(n): words(n + 5)}

	raise "two equal maps compared unequal" if m != k
	raise "two different maps compared equal" if m == j
	raise "a map and a map a literal made compared unequal" if m != {word(n): words(n), word(n + 1): words(n + 1)}

	raise "two equal sets compared unequal" if set(xs) != set([word(n + 1), word(n)])
	raise "two different sets compared equal" if set(xs) == set([word(n)])

	t := (n, word(n), words(n))

	raise "two equal tuples compared unequal" if t != (n, word(n), words(n))
	raise "two different tuples compared equal" if t == (n, word(n), words(n + 1))
	raise "a tuple differing in its first part compared equal" if t == (n + 1, word(n), words(n))

	a: [str; 2] = [word(n), word(n + 1)]
	b: [str; 2] = [word(n), word(n + 2)]

	raise "two different arrays compared equal" if a == b
	raise "an array compared unequal to itself" if a != a

	ps := [P(word(n), [n]), P(word(n + 1), [n, n])]

	raise "two equal lists of a declared type compared unequal" if ps != [P(word(n), [n]), P(word(n + 1), [n, n])]
	raise "two different lists of a declared type compared equal" if ps == [P(word(n), [n]), P(word(n + 1), [n])]

	raise "the bound answered wrong" if not same(xs, words(n))
	raise "the bound answered wrong for two different lists" if same(m, j)
}

fn main() {
	mut i := 0
	r := rounds()
	for i < r {
		round(i)
		i = i + 1
	}
}
ZG

# --- `in` over a list whose elements compare by their parts -------------------------------
# `x in xs` asks each element the `==` a program would write, so the value looked for and a
# list a call answered are this expression's to give back, whichever way the answer went and
# wherever the walk stopped; a list somebody else holds is read where it stands. An element
# with an `Eq` is handed two copies, which its `eq` owns. A wrong answer aborts. The seed
# reads no `in` over a list, so it is zerg only.
case_run in_over_parts no no <<'ZG'
#[derive(Eq)]
struct P {
	pub s: str
	pub xs: list[int]
}

fn word(n: int) -> str {
	return str(n) + "!"
}

fn words(n: int) -> list[str] {
	return [word(n), word(n + 1)]
}

fn rows(n: int) -> list[list[str]] {
	return [words(n), words(n + 2), words(n + 4)]
}

fn round(n: int) {
	xss := rows(n)
	w := words(n + 2)

	raise "a held list was not found" if not (w in xss)
	raise "a list a call answered was not found" if not (words(n + 4) in xss)
	raise "a list that is not there was found" if words(n + 1) in xss
	raise "a literal was not found" if not ([word(n), word(n + 1)] in xss)
	raise "a list was not found in one a call answered" if not (w in rows(n))
	raise "a list was found in one a call answered" if words(n + 9) in rows(n)
	raise "a held list was taken by the test" if xss[1] != w

	ts := [(n, word(n)), (n + 1, word(n + 1))]

	raise "a tuple was not found" if not ((n + 1, word(n + 1)) in ts)
	raise "a tuple that is not there was found" if (n, word(n + 1)) in ts

	ms := [{word(n): words(n)}, {word(n + 1): words(n + 1)}]

	raise "a map was not found" if not ({word(n + 1): words(n + 1)} in ms)
	raise "a map that is not there was found" if {word(n + 1): words(n)} in ms

	ps := [P(word(n), [n]), P(word(n + 1), [n, n])]
	p := P(word(n + 1), [n, n])

	raise "a held value of a declared type was not found" if not (p in ps)
	raise "a made value of a declared type was not found" if not (P(word(n), [n]) in ps)
	raise "a value of a declared type that is not there was found" if P(word(n), [n, n]) in ps
}

fn main() {
	mut i := 0
	r := rounds()
	for i < r {
		round(i)
		i = i + 1
	}
}
ZG

# --- a list rendered through `str(…)` and a hole -----------------------------------------
# `str(xs)` and `f"{xs}"` reach the renderer `print xs` uses, so a list a call answered is this
# expression's to give back once the text is built, and the text is the caller's: each is
# compared, concatenated or measured and then dropped. A held list is rendered in place and
# read afterwards. An `assert` over two lists binds both sides whether or not it holds; one
# that fails is the corpus case's, where the text is read. The seed renders no list, so it is
# zerg only.
case_run list_in_a_hole no no <<'ZG'
fn word(n: int) -> str {
	return str(n) + "!"
}

fn words(n: int) -> list[str] {
	return [word(n), word(n + 1)]
}

fn claim(a: list[str], b: list[str]) -> int {
	assert a == b
	return 1
}

fn round(n: int) {
	xs := words(n)

	raise "a held list rendered wrong" if str(xs) != f"[\"{word(n)}\", \"{word(n + 1)}\"]"
	raise "a list a call answered rendered wrong" if str(words(n)) != str(xs)
	raise "a hole rendered wrong" if f"<{words(n)}>" != "<" + str(xs) + ">"
	raise "a held list was taken by the rendering" if xs[1] != word(n + 1)
	raise "a nested list rendered wrong" if f"{[words(n), xs]}" != f"[{xs}, {xs}]"
	raise "a list of ints rendered wrong" if str([n, n + 1]) != f"[{n}, {n + 1}]"

	raise "two equal lists were claimed unequal" if claim(xs, words(n)) != 1
	assert words(n) == xs
}

fn main() {
	mut i := 0
	r := rounds()
	for i < r {
		round(i)
		i = i + 1
	}
}
ZG

# --- two tuples or arrays ordered by their parts ------------------------------------------
# `<` over a tuple binds each operand once and walks the parts until one differs, so a tuple
# a call answered is this expression's to give back whichever way it ended, and a held one is
# read where it stands. A part with an `Ord` is handed copies, which its `less` owns. A wrong
# answer aborts. The seed orders no tuple, so it is zerg only.
case_run tuple_order no no <<'ZG'
#[derive(Eq, Ord)]
struct P {
	pub s: str
}

# the same width at every n, so the order of two words is the order of their numbers
fn word(n: int) -> str {
	return str(n + 1000) + "!"
}

fn pair(n: int, m: int) -> (str, str) {
	return (word(n), word(m))
}

fn round(n: int) {
	t := pair(n, n + 1)

	raise "a held tuple ordered wrong" if not (t < pair(n, n + 2))
	raise "two tuples a call answered ordered wrong" if pair(n, n + 2) <= pair(n, n + 1)
	raise "an equal tuple was less" if t < pair(n, n + 1)
	raise "an equal tuple was not at least" if not (t >= pair(n, n + 1))
	raise "a held tuple was taken by the test" if t.1 != word(n + 1)

	ps := (P(word(n)), word(n))
	raise "a part with an Ord ordered wrong" if not ((P(word(n)), word(n)) < (P(word(n + 1)), word(n)))
	raise "a held tuple of a declared type ordered wrong" if ps > (P(word(n)), word(n + 1))

	a: [str; 2] = [word(n), word(n + 1)]
	b: [str; 2] = [word(n), word(n + 2)]
	raise "two arrays ordered wrong" if not (a < b)
	raise "an array was less than itself" if a < a
}

fn main() {
	mut i := 0
	r := rounds()
	for i < r {
		round(i)
		i = i + 1
	}
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

# --- a closure called where it arrives ----------------------------------------------------
# The same environment with no binding to give it back: `make_adder(b)(1)` hands the call a
# closure nobody else holds, and the callee was a temp nothing released (#305). One
# environment per round, so the difference grows with the rounds. The borrowed callee is
# beside it — an element of a list the loop still holds, called twice — because a release
# that took the list's count would free it under the second call.
case_run closure_in_place no no <<'ZG'
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
		n = n + make_adder(b)(1)
		fs := [make_adder(b)]
		n = n + fs[0](2) + fs[0](3)
		i = i + 1
	}
	print n
}
ZG

# --- a disabled log entry allocates nothing --------------------------------------------
#
# The claim `log`'s whole design rests on. The builder is chained through four field methods
# and a terminal, so every one of them takes the dead path, and the two totals must be equal.
#
# THE LITERALS ARE IN THE LOOP ON PURPOSE. Hoisting them would measure a shape nobody writes;
# a call site is arguments spelled at the call, and the module documents that evaluating those
# is the one cost a disabled line still pays. This asserts that the cost is ONLY that.
#
# ZLOG_ON=1 turns the level up and changes nothing else, which is what makes the same binary
# its own floor — see `measure_free`.
case_free log_dead <<'ZG'
import "log"

fn level() -> log.Level {
	v := os.env("ZLOG_ON")
	if s := v {
		return log.Level.TRACE if s == "1"
	}
	return log.Level.OFF
}

fn main() {
	lg := log.new().level(level())
	mut i := 0
	n := rounds()
	for i < n {
		lg.at_level(log.Level.DEBUG).str("k", "v").int("n", i).bool("b", true).dur("t", 5).msg("never")
		i = i + 1
	}
	print n
}
ZG

# --- assigning over a value that owns something -------------------------------------
# `docs/core/memory.md` named this as the shape with no case here, which is what a gate not
# finding something looks like. Three spellings of one rule, because the lowering had two
# paths and only one of them is the general one.
#
# A LIST LITERAL had its own path: re-init the destination, then push. That abandoned the
# old buffer AND evaluated the elements after the destination was emptied, so a literal
# reading what it replaces read the emptied list. `strings.split` resets its accumulator
# exactly this way once per separator, which is what made every caller of it look like a
# leak of its own (#11).
case_run assign_list_literal yes no <<'ZG'
fn main() {
	mut cur: list[int] = []
	mut i := 0
	n := rounds()
	for i < n {
		cur.append(i)
		cur = [i, i + 1, i + 2]
		i = i + 1
	}
	print cur.len()
}
ZG

# AND EVERY OTHER OWNING TYPE, which went through the general path and was told not to drop:
# `c_assign_drops` answered yes for a carrier and a recursive enum and no for the rest.
case_run assign_str yes no <<'ZG'
fn mk(n: int) -> str {
	return "abcdefgh" + "ijklmnop"
}

fn main() {
	mut s := "x"
	mut i := 0
	n := rounds()
	for i < n {
		s = mk(i)
		i = i + 1
	}
	print s
}
ZG

case_run assign_list_value yes no <<'ZG'
fn mk() -> list[int] {
	return [1, 2, 3, 4, 5, 6, 7, 8]
}

fn main() {
	mut xs := [0]
	mut i := 0
	n := rounds()
	for i < n {
		xs = mk()
		i = i + 1
	}
	print xs.len()
}
ZG

# and a FIELD, which is the half issue #11 measured as its own shape and is the same rule
# reached through a different lvalue
case_run assign_field yes no <<'ZG'
struct B {
	pub xs: list[int]
}

fn main() {
	mut b := B([0])
	mut i := 0
	n := rounds()
	for i < n {
		b.xs = [i, i + 1, i + 2, i + 3, i + 4, i + 5, i + 6, i + 7]
		i = i + 1
	}
	print b.xs.len()
}
ZG

# --- the collection a `for … in` over a map copies to walk ---------------------------
# The list loop marks the scope, registers the pending drop and unwinds to the mark, so its
# copy is released on every way out — `break` and abort included. The map loop made the same
# copy and dropped none of them: one per EXECUTION of the loop, so `for k in m` inside a
# 1,000,000-round loop peaked at 108.8 MB against a 1.4 MB floor. Two loops, one rule.
case_run forin_map_copy no no <<'ZG'
fn main() {
	mut m: map[str, int] = {"alpha": 1, "beta": 2, "gamma": 3}
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		for k in m {
			n = n + 1
		}
		i = i + 1
	}
	print n
}
ZG

# --- the two shapes the chapter also named, which measure clean -----------------------
# `docs/core/memory.md` listed a held function reassigned and a force-unwrap's copied payload
# beside the assignments above. Both pass here. A case that is green from the day it is
# written is worth the same as one held red first only if it can still FAIL, which these can:
# each allocates per round, so the floor below refuses them if they ever stop.
case_run assign_fn_value no no <<'ZG'
fn main() {
	s := "abcdefghijklmnop"
	mut cur := fn() -> int { return bytearray(s).len() }
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		# a CLOSURE, not a bare function name: the name allocates nothing, so a case built
		# from one measures nothing and the floor says so. What the chapter named is the
		# environment cell and the value it captured, and only a capture makes those.
		cur = fn() -> int { return bytearray(s).len() + 1 }
		n = n + cur()
		i = i + 1
	}
	print n
}
ZG

case_run force_unwrap_copy no no <<'ZG'
fn main() {
	s := "abcdefghijklmnop"
	p: str? = s
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		q := p!
		n = n + bytearray(q).len()
		i = i + 1
	}
	print n
}
ZG

# --- the operand of a folded `is` -----------------------------------------------------
# `x is T` on a known concrete type is a compile-time constant (docs/core/specs.md), and the
# constant is the only part that is decided early: the operand is still a call the program
# made, and a value that call BUILT has nobody else to give it back. Nothing else can see
# this. The answer is right whether or not the drop is there, `make corpus` compares output,
# and macOS has no LeakSanitizer — so a fold that dropped the operand on the floor would have
# been green everywhere.
#
# Three shapes, because c_drop_fn answers for them in two calling conventions: a `str` by its
# own value, a `list` and a `map` by address.
case_run is_folded_operand no no <<'ZG'
struct P {
	pub a: int
}

fn mkstr(n: int) -> str {
	return f"{n}abcdefghijklmnop"
}

fn mklist(n: int) -> list[int] {
	return [n, n + 1, n + 2]
}

fn mkmap(n: int) -> map[str, int] {
	return {"a": n, "b": n + 1}
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		if mkstr(i) is P {
			n = n + 1
		}
		if mklist(i) is P {
			n = n + 2
		}
		if mkmap(i) is P {
			n = n + 4
		}
		i = i + 1
	}
	print n
}
ZG

# --- the receiver of an array's `len` -------------------------------------------------
# `a.len()` is a compile-time constant, and the receiver is still an expression the program
# wrote. It used not to be rendered at all — the constant WAS the whole expression — so a
# receiver that built something had its call deleted rather than leaked, which is the one
# shape a leak check cannot see. Rendering it makes the give-back this measures necessary:
# a `[str; N]` handed back by a call is nobody else's, and nothing else would release it.
#
# It is the array beside `is_folded_operand` above, and they share one helper for one rule.
case_run arr_len_receiver no no <<'ZG'
fn mk(n: int) -> [str; 2] {
	return [f"{n}abcdefghijklmnop", "b"]
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		n = n + mk(i).len()
		i = i + 1
	}
	print n
}
ZG

# --- the counted box ------------------------------------------------------------------
# A `Ref[T]` is the one value shared BY REFERENCE: a copy retains and the last holder releases,
# so what this measures is that the two BALANCE. Nothing else can: the answers are right
# whether or not a retain has a release, `corpus` compares output, and a box that is never
# freed is a leak no assertion about a value would notice.
#
# The payload OWNS something, which is the half that has two chances to go wrong — the cell and
# the string inside it — and the box is copied so the retain is exercised rather than assumed.
# AND THE USER-WRITTEN DROP, which replaces the payload's own: what has to balance here is the
# CELL, and the action is what says whether it ran. A leak of the cell shows as a live count
# that grows; an action that ran twice would be a double free, which the sanitizers see.
case_run ref_box_drop no no <<'ZG'
fn closed(n: int) {
	nop
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		a := Ref(i, closed)
		b := a
		n = n + deref(b)
		i = i + 1
	}
	print n
}
ZG

case_run ref_box no no <<'ZG'
fn mk(n: int) -> Ref[str] {
	return Ref(f"{n}abcdefghijklmnop")
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		a := mk(i)
		b := a
		s := deref(b)
		n = n + bytearray(s).len()
		i = i + 1
	}
	print n
}
ZG

# --- the bridge out of a str ----------------------------------------------------------
# `bytearray(s)` and `runearray(s)` WALK the string and build a fresh list; neither takes it,
# which is the opposite of `zrt_str_from_bytes` beside them. So a source the expression owns —
# a call's result, a concatenation — has nobody to give it back, and every one of these leaked
# a string.
#
# THE SOURCE IS A CALL AND NOT A BINDING, because a named str is somebody else's and is read
# in place: written with a binding the case measures the path that was always right. Both
# bridges are here, since they are two functions under one rule.
case_run str_bridge_source no no <<'ZG'
fn mk(n: int) -> str {
	return f"{n}abcdefghijklmnop"
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		n = n + bytearray(mk(i)).len()
		n = n + runearray(mk(i)).len()
		i = i + 1
	}
	print n
}
ZG

# --- a value read out of a value nobody holds ------------------------------------------
# The shapes a language server paid for on every check (#23), because the compiler is
# written in them: `cur(p).lexeme`, `match c_unmark(t) { … }` and `c_ident_name(e) != ""`. In
# each the expression is the only owner of a value it made, and each read the answer out and
# walked away from it.
#
# A FIELD OR AN ELEMENT OF AN OWNED RVALUE is taken out of a temporary that is then dropped, so
# it is already the reader's own; the reader copied it again as though it were somebody else's.
case_run rvalue_projection no no <<'ZG'
struct T {
	pub s: str
	pub n: int
}

fn mk(n: int) -> T {
	return T(f"{n}abcdefghijklmnop", n)
}

fn mks(n: int) -> list[str] {
	return [f"{n}abcdefghijklmnop", "b"]
}

fn size(s: str) -> int {
	return bytearray(s).len()
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		f := mk(i).s
		n = n + size(f)
		n = n + size(mks(i)[0])
		i = i + 1
	}
	print n
}
ZG

# A SCRUTINEE THE MATCH MADE was bound to a temporary and never given back, whatever the arm.
case_run match_rvalue no no <<'ZG'
enum E {
	A(str)
	B
}

fn mk(n: int) -> E {
	return E.A(f"{n}abcdefghijklmnop")
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		n = n + match mk(i) {
			E.A(s) => bytearray(s).len()
			E.B    => 0
		}
		i = i + 1
	}
	print n
}
ZG

# A STR COMPARED is compared and not kept, and an operand the comparison owned was not released.
case_run str_cmp_owned no no <<'ZG'
fn mk(n: int) -> str {
	return f"{n}abcdefghijklmnop"
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		if mk(i) != "" {
			n = n + 1
		}
		if "zzz" > mk(i) {
			n = n + 1
		}
		i = i + 1
	}
	print n
}
ZG

# AN INTRINSIC BORROWS whatever it is handed — no runtime leaf gives an argument back — so one
# the call owned was never given back, whether the leaf answers something or nothing. The
# write goes to standard output, which this gate discards; the counter is on standard error.
case_run intrinsic_owned_arg no no <<'ZG'
fn mk(n: int) -> str {
	return f"/nonexistent/{n}abcdefghijklmnop"
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		if __zrt_exists(mk(i)) {
			n = n + 1
		}
		__zrt_write(1, mk(i))
		i = i + 1
	}
	print n
}
ZG

# AN ELEMENT OF A LIST A NAMED MAP HOLDS is borrowed, whole: `m["k"][0]` reads storage the map
# still owns, so the consumer takes its own copy and the read takes none. The read used to copy
# as well, and the consumer's copy was the only one ever given back.
case_run map_elem_borrow no no <<'ZG'
fn hs(n: int) -> str {
	return f"{n}abcdefghijklmnop"
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		m: map[str, list[str]] = {"k": [hs(i)]}
		s := m["k"][0]
		n = n + bytearray(s).len()
		print m["k"][0]
		i = i + 1
	}
	print n
}
ZG

# A VALUE A STATEMENT OWNS AND NOBODY READS is given back where it is dropped: `r?` written
# for its early return alone, on a carrier that names storage, copies the Left out — and a call
# answering a `str`, written as a statement, hands one over too.
case_run discarded_owned no no <<'ZG'
fn hs(n: int) -> str {
	return f"{n}abcdefghijklmnop"
}

fn rs(n: int) -> Result[str] {
	return Either.Left(hs(n))
}

fn stmt_try(r: Result[str]) -> Result[int] {
	r?
	return Either.Left(1)
}

fn main() {
	mut n := 0
	mut i := 0
	rr := rounds()
	for i < rr {
		r := rs(i)
		n = n + (stmt_try(r) ?? 0)
		hs(i)
		i = i + 1
	}
	print n
}
ZG

# --- a composite rendered where it arrives ------------------------------------------------
# A renderer reads its operand and keeps none of it, so an operand nobody else holds — a
# call's result, a literal — was shown and then had no one to give it back (#307). `print` is
# the spelling the issue names; `.debug()` and a converted hole reach the same renderer and
# are held with it. The seed prints no composite at all, so this is `zerg` alone.
#
# The borrowed operand is beside them: a named list printed twice and read afterwards, which
# a release that took the binding's storage would free under the second line.
case_run printed_rvalue no no <<'ZG'
struct Row {
	pub tag: str
	pub xs: list[int]
}

fn mk(n: int) -> list[int] {
	return [n, n + 1]
}

fn row(n: int) -> Row {
	return Row(f"{n}abcdefghijklmnop", mk(n))
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		print mk(i)
		print [i, i + 1]
		print row(i)
		n = n + bytearray(mk(i).debug()).len() + bytearray(f"{row(i)!r}").len()
		xs := mk(i)
		print xs
		print xs
		n = n + xs.len()
		i = i + 1
	}
	print n
}
ZG

# --- a spec method called on a box ----------------------------------------------------
# The call hands the member a copy of the cell, and the member drops the `this` it was given.
# A dispatcher that then released the cell's payload as well dropped it twice per call (#313),
# and the second drop writes into storage already given back. Nothing promises what that does
# without a sanitizer: here it ended the run on a signal, which is `RUN` below. The use after
# free is NAMED by `sanitize-corpus`; this is the half a checkout without the corpus has.
#
# One round makes the call every way a box is reached: through a name, twice and then read;
# on a receiver nobody named; on an element by index and by a loop's binding; on a field; and
# through members that answer an owned `str` and the payload's own list. The payload holds a
# list built from the round number, so there is something counted to drop. The seed has no
# spec as a type, so this is `zerg` alone.
case_run spec_box_calls no no <<'ZG'
spec Shape {
	fn area() -> int
	fn name() -> str
	fn names() -> list[str]
}

struct Lab {
	pub parts: list[str]
}

struct Holder {
	pub sh: Shape
	pub n: int
}

impl Shape for Lab {
	fn area() -> int {
		return this.parts.len()
	}

	fn name() -> str {
		return this.parts[0] + "!"
	}

	fn names() -> list[str] {
		return this.parts
	}
}

fn ml(n: int) -> Shape {
	return Lab([str(n) + "l", str(n) + "m"])
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		y := ml(i)
		n = n + y.area()
		n = n + y.area()
		print y
		n = n + ml(i).area()
		xs := [ml(i), ml(i + 1)]
		n = n + xs[0].area() + xs[0].area()
		for x in xs {
			n = n + x.area()
		}
		h := Holder(ml(i), i)
		n = n + h.sh.area() + h.sh.area() + h.n
		n = n + bytearray(y.name()).len() + bytearray(y.name()).len()
		n = n + y.names().len() + y.names().len()
		i = i + 1
	}
	print n
}
ZG

# --- a composite of spec boxes, rendered ----------------------------------------------
# A composite's renderer shows a box through the spec's render dispatcher, and that dispatcher
# was declared only where a box was rendered on its own — so a program whose one rendering of
# the spec was a list of them called a function nothing had declared, and `cc` said so (#314).
# No box is printed on its own here, on purpose: one `print` of a bare box would declare the
# dispatcher and this would build without the fix.
#
# `Wrap` is the shape with no composite printed at all. It implements the spec and holds a box
# of it, so its own renderer — written for its witness table whether or not anyone asks for
# it — reaches the dispatcher. The seed prints no composite and has no spec as a type.
case_run printed_spec_boxes no no <<'ZG'
spec Shape {
	fn area() -> int
}

struct Sq {
	pub s: int
	pub tags: list[str]
}

struct Lab {
	pub parts: list[str]
}

struct Wrap {
	pub inner: Shape
	pub tag: str
}

impl Shape for Sq {
	fn area() -> int {
		return this.s * this.s
	}
}

impl Shape for Lab {
	fn area() -> int {
		return this.parts.len()
	}
}

impl Shape for Wrap {
	fn area() -> int {
		return this.inner.area() + bytearray(this.tag).len()
	}
}

impl Lab {
	fn display() -> str {
		return f"<{this.parts[0]}>"
	}
}

fn mk(n: int) -> Shape {
	return Sq(n, [str(n) + "t"])
}

fn ml(n: int) -> Shape {
	return Lab([str(n) + "l"])
}

fn mw(n: int) -> Shape {
	return Wrap(ml(n), str(n) + "w")
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		print [ml(i), mk(i)]
		xs := [mk(i), mw(i)]
		print xs
		print (ml(i), i, mk(i))
		n = n + xs.len() + mw(i).area()
		i = i + 1
	}
	print n
}
ZG

# --- what an expression owns when it aborts ---------------------------------------------
# A release written on the line after a use is a release an abort jumps over. The cases below
# hold the places that was true, each in the shape the others above cannot see: every value
# is built at run time and is owned by nothing but the expression that is still being
# evaluated when a LATER part of it raises, under a `guard` that lets the loop go round again.
#
# EVERY SHAPE IS RUN TWICE A ROUND through `both`: once where the later part raises and once
# where it does not. The twin is what a fix that registers a temporary can break — a value
# given back by the unwind AND by the line after it is freed twice — and it is also what makes
# the raising half mean something: `gone` ends the run when a part that had to abort answered,
# and `kept` when a twin did not, so a shape the compiler quietly stopped aborting is a `RUN`
# failure here rather than a round that allocated and freed nothing of interest.
#
# What is NOT here is as deliberate. An earlier argument of a call or a method, held while a
# later one raises, is the same class and still leaks unless it is a channel end, and so do a
# `spawn`'s environment and a `defer`'s (#326); a shape that leaks on the path where nothing
# raises is a different defect with an issue of its own. Neither is written into a case,
# because a case that has to tolerate a leak counts nothing.
abort_prelude='fn gone(v: int) -> int {
	raise "a part that had to abort answered instead" if v >= 0
	return 1
}

fn kept(v: int) -> int {
	raise "a twin that had to answer aborted instead" if v < 0
	return v
}

fn both(f: fn (int, int) -> int, i: int) -> int {
	return gone(guard { f(i, 1) } ?? -1) + kept(guard { f(i, 0) } ?? -1)
}

fn word(n: int) -> str {
	return str(n) + "abcdefghijklmnop"
}

fn boom(k: int) -> str {
	raise "boom" if k > 0
	return "ok"
}

fn bad(k: int) -> int {
	raise "bad" if k > 0
	return 0
}

fn mk(n: int) -> list[int] {
	return [n, n + 1]
}
'

# case_abort <name> <conc?> — a case built with the prelude above in front of the body on
# stdin, for `zerg` alone. The seed has no `in`, no slice, no `set`, no `Either` and no spec
# as a type, and every case here uses one of them, so it is not the oracle for these.
case_abort() {
	local name=$1 conc=$2
	{
		printf '%s\n' "$abort_prelude"
		cat
	} | emit_case "$name"
	cases=$((cases + 1))
	measure zerg "$ZERG" "$name" "$conc"
	return 0
}

# A TEMPORARY HELD ACROSS A LATER OPERAND (#309). The left side of a `+`, a comparison or an
# `in`, the list under an index or a slice, a `match`'s scrutinee, a map nobody named under a
# method, a channel end handed to a call that raises or whose next argument does, the value a
# send could not deliver, an assignment's key, and the left side of an operator its own type
# declares: each sits in a C temporary while something after it runs, and each was given back
# only on the line that something had to reach. The index and the slice abort in the runtime's
# own bounds check as well as in a part that raises, and the send aborts because the channel
# is closed — an abort is not always a `raise` the program wrote. The channels are buffered
# and nothing is spawned; the schedule is pinned all the same, as it is for every case that
# holds one.
case_abort abort_operand yes <<'ZG'
struct P {
	pub name: str
	pub xs: list[int]
}

impl Eq for P {
	fn eq(o: P) -> bool {
		return this.name == o.name
	}

	fn ne(o: P) -> bool {
		return not this.eq(o)
	}
}

impl Ord for P {
	fn less(o: P) -> bool {
		return this.xs.len() < o.xs.len()
	}
}

fn mkp(n: int) -> P {
	return P(word(n), mk(n))
}

fn mkq(n: int, k: int) -> P {
	raise "q" if k > 0
	return P(word(n), mk(n))
}

fn mkm(n: int) -> map[str, int] {
	return {word(n): n}
}

fn source(n: int) -> chan[int] {
	c := chan[int](2)
	c <- n
	return c
}

fn drain(c: <-chan[int], k: int) -> int {
	return bad(k) + 1
}

fn concat(i: int, k: int) -> int {
	return bytearray(word(i) + boom(k)).len()
}

fn concat_chain(i: int, k: int) -> int {
	return bytearray(word(i) + word(i + 1) + boom(k) + word(i + 2)).len()
}

fn hole(i: int, k: int) -> int {
	return bytearray(f"{word(i)}:{boom(k)}").len()
}

fn str_eq(i: int, k: int) -> int {
	return 1 if word(i) == boom(k)
	return 2
}

fn str_lt(i: int, k: int) -> int {
	return 1 if word(i) < boom(k)
	return 2
}

fn in_list(i: int, k: int) -> int {
	return 1 if bad(k) in mk(i)
	return 2
}

fn in_map(i: int, k: int) -> int {
	return 1 if boom(k) in mkm(i)
	return 2
}

fn index_part(i: int, k: int) -> int {
	return mk(i)[bad(k)] - i
}

fn index_range(i: int, k: int) -> int {
	return mk(i)[k * 5] - i
}

fn slice_part(i: int, k: int) -> int {
	return mk(i)[0..bad(k) + 1].len()
}

fn slice_range(i: int, k: int) -> int {
	return mk(i)[0..k * 5 + 1].len()
}

fn match_list(i: int, k: int) -> int {
	return match mk(i) {
		_ => bad(k)
	}
}

fn match_str(i: int, k: int) -> int {
	return match word(i) {
		"x" => 1
		_   => bad(k)
	}
}

fn map_method(i: int, k: int) -> int {
	return 1 if mkm(i).has(boom(k))
	return 2
}

fn chan_arg(i: int, k: int) -> int {
	return drain(source(i), k)
}

fn pass(c: <-chan[int], k: int) -> int {
	return k + 1
}

fn chan_arg_later(i: int, k: int) -> int {
	return pass(source(i), bad(k))
}

fn send_closed(i: int, k: int) -> int {
	c := chan[list[int]](1)
	if k > 0 {
		close(c)
	}
	c <- mk(i)
	xs := <-c ?? [0]
	return xs.len()
}

fn map_key(i: int, k: int) -> int {
	mut m := {word(i): mk(i)}
	m[word(i + 1)] = mk(bad(k))
	return m.len()
}

fn own_eq(i: int, k: int) -> int {
	return 1 if mkp(i) == mkq(i, k)
	return 2
}

fn own_lt(i: int, k: int) -> int {
	return 1 if mkp(i) < mkq(i, k)
	return 2
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		n = n + both(concat, i) + both(concat_chain, i) + both(hole, i)
		n = n + both(str_eq, i) + both(str_lt, i)
		n = n + both(in_list, i) + both(in_map, i)
		n = n + both(index_part, i) + both(index_range, i)
		n = n + both(slice_part, i) + both(slice_range, i)
		n = n + both(match_list, i) + both(match_str, i)
		n = n + both(map_method, i)
		n = n + both(chan_arg, i) + both(chan_arg_later, i) + both(send_closed, i)
		n = n + both(map_key, i)
		n = n + both(own_eq, i) + both(own_lt, i)
		i = i + 1
	}
	print n
}
ZG

# A VALUE UNDER CONSTRUCTION (#312). A literal's earlier parts are owned by nothing until the
# whole value exists: a list or a map written as an expression, a map literal a `:=` binds (the
# binding is registered after the literal is whole, so it does not help), a set built from a
# literal, a tuple, an array, a struct written by position, by name and with a trailing default
# that raises, and a variant — whose payload, when the enum holds itself, is a cell as well as
# a value. A spec box is the same cell one type over, as a list's element and a struct's field.
case_abort abort_literal no <<'ZG'
struct P {
	pub name: str
	pub xs: list[int]
}

struct D {
	pub name: str
	pub xs: list[int]
	pub n: int = bad(1)
}

enum E {
	None
	Two(str, list[int])
}

enum T {
	Leaf(list[int])
	Node(T, T)
}

spec Shape {
	fn area() -> int
}

struct Sq {
	pub s: int
	pub tags: list[str]
}

impl Shape for Sq {
	fn area() -> int {
		return this.s * this.s
	}
}

struct H {
	pub a: Shape
	pub n: int
}

fn total(xs: list[Shape]) -> int {
	mut n := 0
	for x in xs {
		n = n + x.area()
	}
	return n
}

fn depth(t: T) -> int {
	return match t {
		T.Leaf(xs)   => xs.len()
		T.Node(a, b) => depth(a) + depth(b)
	}
}

fn list_of_lists(i: int, k: int) -> int {
	return [mk(i), mk(i + 1 + bad(k)), mk(i + 2)].len()
}

fn list_of_strs(i: int, k: int) -> int {
	return [word(i), boom(k)].len()
}

fn map_key(i: int, k: int) -> int {
	return {word(i): 1, boom(k): 2}.len()
}

fn map_value(i: int, k: int) -> int {
	return {word(i): mk(i), word(i + 1): mk(bad(k))}.len()
}

fn map_bound(i: int, k: int) -> int {
	m := {word(i): mk(i), word(i + 1): mk(bad(k))}
	return m.len()
}

fn set_of(i: int, k: int) -> int {
	return set([i, i + 1, i + 2 + bad(k)]).len()
}

fn tuple_of(i: int, k: int) -> int {
	t := (word(i), mk(i), boom(k))
	return t.1.len()
}

fn array_of(i: int, k: int) -> int {
	a: [str; 2] = [word(i), boom(k)]
	return a.len()
}

fn struct_by_place(i: int, k: int) -> int {
	p := P(word(i), mk(bad(k)))
	return p.xs.len()
}

fn struct_by_name(i: int, k: int) -> int {
	p := P(name: word(i), xs: mk(bad(k)))
	return p.xs.len()
}

fn struct_default(i: int, k: int) -> int {
	return D(word(i), mk(i), 0).xs.len() if k == 0
	d := D(word(i), mk(i))
	return d.xs.len()
}

fn variant(i: int, k: int) -> int {
	e := E.Two(word(i), mk(bad(k)))
	return match e {
		E.None       => 0
		E.Two(_, xs) => xs.len()
	}
}

fn variant_boxed(i: int, k: int) -> int {
	t := T.Node(T.Node(T.Leaf(mk(i)), T.Leaf(mk(i + 1))), T.Leaf(mk(bad(k))))
	return depth(t)
}

fn box_list(i: int, k: int) -> int {
	return total([Sq(1, [word(i)]), Sq(2, [word(i + 1)]), Sq(bad(k), [word(i + 2)])])
}

fn box_field(i: int, k: int) -> int {
	h := H(Sq(2, [word(i)]), bad(k))
	return h.a.area() + h.n
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		n = n + both(list_of_lists, i) + both(list_of_strs, i)
		n = n + both(map_key, i) + both(map_value, i) + both(map_bound, i)
		n = n + both(set_of, i)
		n = n + both(tuple_of, i) + both(array_of, i)
		n = n + both(struct_by_place, i) + both(struct_by_name, i) + both(struct_default, i)
		n = n + both(variant, i) + both(variant_boxed, i)
		n = n + both(box_list, i) + both(box_field, i)
		i = i + 1
	}
	print n
}
ZG

# A RENDERER'S TEXT (#311). A generated renderer joins its parts into one accumulator, and a
# part whose own `display` raises leaves by a jump that the release at the renderer's end is
# not on. Every part before the one that raises is rendered first, so there is text to lose:
# a list, an array, a map's value, a tuple, an Either arm, a variant's payload and a struct's
# field, reached through `.debug()` and — for one of them each — `print`, `str()` and an
# f-string hole with and without its conversion.
case_abort abort_render no <<'ZG'
struct Q {
	pub k: int
	pub tags: list[str]
}

impl Q {
	fn display() -> str {
		raise "no show" if this.k > 0
		return f"Q{this.tags.len()}{this.tags[0]}"
	}
}

struct W {
	pub name: str
	pub q: Q
}

enum E {
	None
	Two(str, Q)
}

fn q(n: int, k: int) -> Q {
	return Q(k, [word(n)])
}

fn res(n: int, k: int) -> Result[Q] {
	return Either.Left(q(n, k))
}

fn size(s: str) -> int {
	return bytearray(s).len()
}

fn list_debug(i: int, k: int) -> int {
	ys := [q(i, 0), q(i, k)]
	return size(ys.debug())
}

fn list_print(i: int, k: int) -> int {
	ys := [q(i, 0), q(i, k)]
	print ys
	return ys.len()
}

fn list_rvalue(i: int, k: int) -> int {
	print [q(i, 0), q(i, k)]
	return i
}

fn array_debug(i: int, k: int) -> int {
	ys: [Q; 2] = [q(i, 0), q(i, k)]
	return size(ys.debug())
}

fn map_debug(i: int, k: int) -> int {
	ys := {word(i): q(i, 0), word(i + 1): q(i, k)}
	return size(ys.debug())
}

fn tuple_debug(i: int, k: int) -> int {
	ys := (word(i), q(i, 0), q(i, k))
	return size(ys.debug())
}

fn tuple_str(i: int, k: int) -> int {
	ys := (word(i), q(i, 0), q(i, k))
	return size(str(ys) + "!")
}

fn either_debug(i: int, k: int) -> int {
	ys := res(i, k)
	return size(ys.debug())
}

fn variant_debug(i: int, k: int) -> int {
	ys := E.Two(word(i), q(i, k))
	return size(ys.debug())
}

fn struct_debug(i: int, k: int) -> int {
	ys := W(word(i), q(i, k))
	return size(ys.debug())
}

fn struct_hole(i: int, k: int) -> int {
	ys := W(word(i), q(i, k))
	return size(f"<{i}|{ys}|{ys!r}>")
}

fn nested(i: int, k: int) -> int {
	ys := [[q(i, 0), q(i, 0)], [q(i, 0), q(i, k)]]
	return size(ys.debug())
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		n = n + both(list_debug, i) + both(list_print, i) + both(list_rvalue, i)
		n = n + both(array_debug, i) + both(map_debug, i)
		n = n + both(tuple_debug, i) + both(tuple_str, i)
		n = n + both(either_debug, i) + both(variant_debug, i)
		n = n + both(struct_debug, i) + both(struct_hole, i)
		n = n + both(nested, i)
		i = i + 1
	}
	print n
}
ZG

# A SPEC METHOD'S CELL (#318). A by-value spec method is called on a copy of the box, and the
# member that raises gives back the payload it was handed and not the cell the copy was made
# in. The box is reached through a name, as a receiver nobody named and as a parameter; the
# member takes owned arguments of its own; and the method is the spec's default, which has no
# member of the type's to enter.
case_abort abort_spec no <<'ZG'
spec Shape {
	fn size() -> int
	fn area(k: int) -> int
	fn fit(k: int, tag: str, xs: list[str]) -> int
	fn twice(k: int) -> int {
		return this.size() * 2 + bad(k)
	}
}

struct Lab {
	pub parts: list[str]
}

impl Shape for Lab {
	fn size() -> int {
		return this.parts.len()
	}

	fn area(k: int) -> int {
		return this.parts.len() + bad(k)
	}

	fn fit(k: int, tag: str, xs: list[str]) -> int {
		return this.parts.len() + xs.len() + bytearray(tag).len() + bad(k)
	}
}

fn ml(n: int) -> Shape {
	return Lab([word(n), word(n + 1)])
}

fn through(s: Shape, k: int) -> int {
	return s.area(k)
}

fn named(i: int, k: int) -> int {
	y := ml(i)
	return y.area(0) + y.area(k)
}

fn rvalue(i: int, k: int) -> int {
	return ml(i).area(k)
}

fn param(i: int, k: int) -> int {
	return through(ml(i), k)
}

fn named_args(i: int, k: int) -> int {
	y := ml(i)
	return y.fit(0, word(i), [word(i + 2)]) + y.fit(k, word(i), [word(i + 3)])
}

fn rvalue_args(i: int, k: int) -> int {
	return ml(i).fit(k, word(i), [word(i + 2)])
}

fn named_default(i: int, k: int) -> int {
	y := ml(i)
	return y.twice(0) + y.twice(k)
}

fn rvalue_default(i: int, k: int) -> int {
	return ml(i).twice(k)
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		n = n + both(named, i) + both(rvalue, i) + both(param, i)
		n = n + both(named_args, i) + both(rvalue_args, i)
		n = n + both(named_default, i) + both(rvalue_default, i)
		i = i + 1
	}
	print n
}
ZG

# A KEY COPIED OUT OF A NAME (#340). A map literal copies a key it reads from something that
# owns it — a name, a parameter, a field, an element — and the copy sat in a C temporary
# nothing had registered while the entry's value was evaluated. The key is in the entry whose
# value raises and in an earlier one, alone and beside another named key; the value is a
# plain one, a text and a list; and the literal is bound with and without its type, assigned,
# returned, passed, written as an element and as another map's value, and used as a receiver.
# No shape here asks `in` anything, so this case and the one below it fail apart.
case_abort abort_map_key no <<'ZG'
struct H {
	pub key: str
	pub n: int
}

fn sbad(n: int, k: int) -> str {
	raise "s" if k > 0
	return word(n)
}

fn size(m: map[str, int]) -> int {
	return m.len()
}

fn made(key: str, k: int) -> map[str, int] {
	return {key: bad(k)}
}

fn bound(i: int, k: int) -> int {
	key := word(i)
	m := {key: bad(k)}
	return m.len()
}

fn bound_typed(i: int, k: int) -> int {
	key := word(i)
	m: map[str, int] = {key: bad(k)}
	return m.len()
}

fn of_param(key: str, k: int) -> int {
	m := {key: bad(k)}
	return m.len()
}

fn param(i: int, k: int) -> int {
	return of_param(word(i), k)
}

fn later_entry(i: int, k: int) -> int {
	key := word(i)
	m := {"a": 0, key: bad(k)}
	return m.len() - 1
}

fn earlier_entry(i: int, k: int) -> int {
	key := word(i)
	m := {key: 1, "b": bad(k)}
	return m.len() - 1
}

fn field_key(i: int, k: int) -> int {
	h := H(word(i), 1)
	m := {h.key: bad(k)}
	return m.len()
}

fn indexed_key(i: int, k: int) -> int {
	xs := [word(i), word(i + 1)]
	m := {xs[0]: bad(k)}
	return m.len()
}

fn two_named(i: int, k: int) -> int {
	key := word(i)
	other := word(i + 1)
	m := {key: bad(0), other: bad(k)}
	return m.len() - 1
}

fn str_value(i: int, k: int) -> int {
	key := word(i)
	m := {key: sbad(i + 1, k)}
	return m.len()
}

fn list_value(i: int, k: int) -> int {
	key := word(i)
	m := {key: mk(bad(k))}
	return m.len()
}

fn assigned(i: int, k: int) -> int {
	key := word(i)
	mut m: map[str, int] = {"a": 1}
	m = {key: bad(k)}
	return m.len()
}

fn returned(i: int, k: int) -> int {
	key := word(i)
	m := made(key, k)
	return m.len()
}

fn argument(i: int, k: int) -> int {
	key := word(i)
	return size({key: bad(k)})
}

fn in_a_list(i: int, k: int) -> int {
	key := word(i)
	xs := [{key: bad(k)}]
	return xs.len()
}

fn in_a_map(i: int, k: int) -> int {
	key := word(i)
	m := {"a": {key: bad(k)}}
	return m.len()
}

fn receiver(i: int, k: int) -> int {
	key := word(i)
	return {key: bad(k)}.len()
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		n = n + both(bound, i) + both(bound_typed, i) + both(param, i)
		n = n + both(later_entry, i) + both(earlier_entry, i)
		n = n + both(field_key, i) + both(indexed_key, i) + both(two_named, i)
		n = n + both(str_value, i) + both(list_value, i)
		n = n + both(assigned, i) + both(returned, i) + both(argument, i)
		n = n + both(in_a_list, i) + both(in_a_map, i) + both(receiver, i)
		i = i + 1
	}
	print n
}
ZG

# THE VALUE `in` LOOKS FOR (#335). `v in xs` copied a text it only had to read and gave the
# copy to nobody, where nothing raises at all — so most of this case runs outside a `guard`.
# The value is a name, a text built in place, a field, an element, a concatenation, a branch
# and a loop variable; the container is a list, a map and a set, each under a name and as
# the value of a call, and a set built where it is asked. EVERY ANSWER IS READ: `yes` raises
# on a wrong one, present and absent alike, and nothing catches it, so a fix that stops
# copying and reads the wrong text is a `RUN` failure and not a count that happens to
# balance. The last part is the value that raises, and a test followed by a part that
# raises, through `both` — the twin there is the half that leaked.
#
# What is NOT here: the same text handed to `has`, which is a method's argument and still
# leaks. A case that has to tolerate that counts nothing.
case_abort in_value no <<'ZG'
struct H {
	pub key: str
}

fn mkl(n: int) -> list[str] {
	return [word(n), word(n + 1)]
}

fn mkm(n: int) -> map[str, int] {
	return {word(n): n, word(n + 1): n}
}

fn mks(n: int) -> set[str] {
	return set([word(n), word(n + 1)])
}

fn sbad(n: int, k: int) -> str {
	raise "s" if k > 0
	return word(n)
}

fn yes(got: bool, at: str) -> int {
	raise at if not got
	return 1
}

fn over_list(i: int) -> int {
	xs := mkl(i)
	k := word(i)
	far := word(i + 9)
	h := H(word(i + 1))
	ys := [word(i), word(i + 9)]
	mut n := yes(k in xs, "a name, in a list")
	n = n + yes(word(i + 1) in xs, "a text built in place, in a list")
	n = n + yes(h.key in xs, "a field, in a list")
	n = n + yes(ys[0] in xs, "an element, in a list")
	n = n + yes(str(i) + "abcdefghijklmnop" in xs, "a concatenation, in a list")
	n = n + yes((if i % 2 == 0 { k } else { word(i + 1) }) in xs, "a branch, in a list")
	n = n + yes(k in mkl(i), "a name, in a list nobody named")
	n = n + yes(word(i) in mkl(i), "a text built in place, in a list nobody named")
	n = n + yes(not (far in xs), "an absent name, in a list")
	n = n + yes(not (word(i + 9) in xs), "an absent text built in place, in a list")
	n = n + yes(not (ys[1] in mkl(i)), "an absent element, in a list nobody named")
	for w in ys {
		n = n + yes((w in xs) == (w == k), "a loop variable, in a list")
	}
	return n
}

fn over_map(i: int) -> int {
	m := mkm(i)
	k := word(i)
	far := word(i + 9)
	h := H(word(i + 1))
	ys := [word(i), word(i + 9)]
	mut n := yes(k in m, "a name, in a map")
	n = n + yes(word(i + 1) in m, "a text built in place, in a map")
	n = n + yes(h.key in m, "a field, in a map")
	n = n + yes(ys[0] in m, "an element, in a map")
	n = n + yes(str(i) + "abcdefghijklmnop" in m, "a concatenation, in a map")
	n = n + yes((if i % 2 == 0 { k } else { word(i + 1) }) in m, "a branch, in a map")
	n = n + yes(k in mkm(i), "a name, in a map nobody named")
	n = n + yes(word(i) in mkm(i), "a text built in place, in a map nobody named")
	n = n + yes(not (far in m), "an absent name, in a map")
	n = n + yes(not (word(i + 9) in m), "an absent text built in place, in a map")
	n = n + yes(not (ys[1] in mkm(i)), "an absent element, in a map nobody named")
	for w in ys {
		n = n + yes((w in m) == (w == k), "a loop variable, in a map")
	}
	return n
}

fn over_set(i: int) -> int {
	s := mks(i)
	k := word(i)
	far := word(i + 9)
	h := H(word(i + 1))
	ys := [word(i), word(i + 9)]
	mut n := yes(k in s, "a name, in a set")
	n = n + yes(word(i + 1) in s, "a text built in place, in a set")
	n = n + yes(h.key in s, "a field, in a set")
	n = n + yes(ys[0] in s, "an element, in a set")
	n = n + yes(str(i) + "abcdefghijklmnop" in s, "a concatenation, in a set")
	n = n + yes((if i % 2 == 0 { k } else { word(i + 1) }) in s, "a branch, in a set")
	n = n + yes(k in mks(i), "a name, in a set nobody named")
	n = n + yes(word(i) in mks(i), "a text built in place, in a set nobody named")
	n = n + yes(k in set([word(i), word(i + 1)]), "a name, in a set built in place")
	n = n + yes(not (far in s), "an absent name, in a set")
	n = n + yes(not (word(i + 9) in s), "an absent text built in place, in a set")
	n = n + yes(not (ys[1] in mks(i)), "an absent element, in a set nobody named")
	n = n + yes(not (far in set([word(i), word(i + 1)])), "an absent name, in a set built in place")
	for w in ys {
		n = n + yes((w in s) == (w == k), "a loop variable, in a set")
	}
	return n
}

fn raises_list(i: int, k: int) -> int {
	xs := mkl(i)
	return yes(sbad(i, k) in xs, "a value that may raise, in a list")
}

fn raises_map(i: int, k: int) -> int {
	m := mkm(i)
	return yes(sbad(i, k) in m, "a value that may raise, in a map")
}

fn raises_set(i: int, k: int) -> int {
	s := mks(i)
	return yes(sbad(i, k) in s, "a value that may raise, in a set")
}

fn raises_list_rvalue(i: int, k: int) -> int {
	return yes(sbad(i, k) in mkl(i), "a value that may raise, in a list nobody named")
}

fn raises_map_rvalue(i: int, k: int) -> int {
	return yes(sbad(i, k) in mkm(i), "a value that may raise, in a map nobody named")
}

fn raises_set_rvalue(i: int, k: int) -> int {
	return yes(sbad(i, k) in mks(i), "a value that may raise, in a set nobody named")
}

fn then_raises(i: int, k: int) -> int {
	xs := mkl(i)
	return yes((word(i) in xs) and bad(k) == 0, "a built text, then a part that may raise")
}

fn named_then_raises(i: int, k: int) -> int {
	m := mkm(i)
	key := word(i)
	return yes((key in m) and bad(k) == 0, "a name, then a part that may raise")
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		n = n + over_list(i) + over_map(i) + over_set(i)
		n = n + both(raises_list, i) + both(raises_map, i) + both(raises_set, i)
		n = n + both(raises_list_rvalue, i) + both(raises_map_rvalue, i) + both(raises_set_rvalue, i)
		n = n + both(then_raises, i) + both(named_then_raises, i)
		i = i + 1
	}
	print n
}
ZG

# --- a counted value a container takes, and an exit taken while one is being built ------
# The cases above are leaks, and a count is the whole of what this gate was written to read.
# The ones below are not: a container that takes a value it does not own gives it back a
# second time, and a release run against a frame that is gone writes where it should not.
# Only a sanitizer NAMES either, and the one gate that runs a sanitizer reads the private
# corpus (`sanitize-corpus`), which is where each of these is pinned by name.
#
# WHAT A PLAIN BINARY CAN STILL SHOW is narrower, and it is all that is claimed here. A value
# given back twice is a block the allocator hands to the next request while its first owner
# is still reading it, so every shape reads its SOURCE back after the store and after one
# more allocation of the same size, and raises when the text is no longer its own; a set is
# asked for each member it was built from. A second `free` of one block the platform's
# allocator may also refuse outright, which ends the run on a signal. Both are `RUN` failures
# and both depend on what the allocator does with a freed block — a hold, not a proof. The
# counts are the part that does not depend on it: a value nobody named is stored beside each
# named one, so a fix that counts what the container already owned is a per-round leak, and
# an exit that skips an unwind leaves what was already pushed live.
#
# These are for `zerg` alone. The seed has no map literal, no `set` and no closure value, and
# on the early-exit shapes it is not the oracle: it leaves the pushed elements live itself.
#
# What is NOT here: a channel inside any holder, which is not counted in or out at all, and
# the arguments of a call, a `spawn` or a `defer` (#326). Each is a defect of its own, and a
# case that has to tolerate one counts nothing.

# A MAP ASSIGNED THROUGH A KEY SOMETHING ELSE OWNS (#327). `m[k] = v` handed the map the
# caller's own text. A key the map did not have was then given back by both; one it already
# had was given back on the spot, under the name still reading it. The key is a name, a field,
# an element, a parameter and a loop variable, and each is stored through more than once.
case_run held_map_key no no <<'ZG'
struct K {
	pub f: str
}

fn word(n: int) -> str {
	return str(n) + "abcdefghijklmnop"
}

fn same(got: str, n: int) -> int {
	raise "a key was changed by the store it was used for" if got != word(n)
	return 1
}

fn by_name(i: int) -> int {
	mut m: map[str, int] = {:}
	key := word(i)
	m[key] = 1
	later := word(i + 1)
	return m.len() + same(key, i) + same(later, i + 1)
}

fn twice(i: int) -> int {
	mut m: map[str, int] = {:}
	key := word(i)
	m[key] = 1
	m[key] = 2
	later := word(i + 1)
	return m.len() + m[key] + same(key, i) + same(later, i + 1)
}

fn by_field(i: int) -> int {
	mut m: map[str, int] = {:}
	w := K(word(i))
	m[w.f] = 1
	m[w.f] = 2
	later := word(i + 1)
	return m.len() + same(w.f, i) + same(later, i + 1)
}

fn by_element(i: int) -> int {
	mut m: map[str, int] = {:}
	xs := [word(i), word(i + 1)]
	m[xs[0]] = 1
	m[xs[0]] = 2
	later := word(i + 2)
	return m.len() + same(xs[0], i) + same(later, i + 2)
}

fn store(key: str) -> int {
	mut m: map[str, int] = {:}
	m[key] = 1
	m[key] = 2
	return m.len()
}

fn by_parameter(i: int) -> int {
	key := word(i)
	n := store(key)
	later := word(i + 1)
	return n + same(key, i) + same(later, i + 1) + store(word(i + 2))
}

fn by_loop_variable(i: int) -> int {
	mut m: map[str, int] = {:}
	xs := [word(i), word(i + 1), word(i)]
	for k in xs {
		m[k] = 1
	}
	later := word(i + 2)
	return m.len() + same(xs[0], i) + same(xs[2], i) + same(later, i + 2)
}

fn unnamed(i: int) -> int {
	mut m: map[str, int] = {:}
	m[word(i)] = 1
	m[word(i)] = 2
	m[word(i + 1)] = 3
	return m.len()
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		n = n + by_name(i) + twice(i) + by_field(i) + by_element(i)
		n = n + by_parameter(i) + by_loop_variable(i) + unnamed(i)
		i = i + 1
	}
	print n
}
ZG

# A SET BUILT FROM A LIST (#329). Each member moved out of the list uncounted and the list
# gave its own back, so the set held text that was gone; its length still came out right. The
# list is a literal, a name, a field, a parameter and one nobody named, and a member written
# twice goes down the path that drops the surplus one — which is why each has to arrive owned.
case_run held_set_item no no <<'ZG'
struct H {
	pub xs: list[str]
}

fn word(n: int) -> str {
	return str(n) + "abcdefghijklmnop"
}

fn words(n: int) -> list[str] {
	return [word(n), word(n + 1)]
}

fn same(got: str, n: int) -> int {
	raise "a list was changed by the set built from it" if got != word(n)
	return 1
}

fn holds(s: set[str], n: int) -> int {
	mut found := 0
	for w in s {
		if w == word(n) {
			found = found + 1
		}
	}
	raise "a set lost a member it was built from" if found != 1
	return 1
}

fn from_literal(i: int) -> int {
	s := set([word(i), word(i + 1)])
	later := words(i + 2)
	return s.len() + holds(s, i) + holds(s, i + 1) + later.len()
}

fn from_named(i: int) -> int {
	xs := words(i)
	s := set(xs)
	later := words(i + 2)
	return s.len() + same(xs[0], i) + same(xs[1], i + 1) + holds(s, i + 1) + later.len()
}

fn from_unnamed(i: int) -> int {
	s := set(words(i))
	later := words(i + 2)
	return s.len() + holds(s, i) + holds(s, i + 1) + later.len()
}

fn from_field(i: int) -> int {
	h := H(words(i))
	s := set(h.xs)
	later := words(i + 2)
	return s.len() + same(h.xs[0], i) + holds(s, i) + later.len()
}

fn size(xs: list[str]) -> int {
	s := set(xs)
	return s.len()
}

fn from_parameter(i: int) -> int {
	xs := words(i)
	n := size(xs)
	later := words(i + 2)
	return n + same(xs[1], i + 1) + size(words(i + 4)) + later.len()
}

fn duplicate(i: int) -> int {
	xs := [word(i), word(i), word(i + 1)]
	s := set(xs)
	t := set([word(i + 2), word(i + 2)])
	later := words(i + 3)
	return s.len() + t.len() + same(xs[0], i) + same(xs[1], i) + holds(s, i) + holds(t, i + 2) + later.len()
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		n = n + from_literal(i) + from_named(i) + from_unnamed(i)
		n = n + from_field(i) + from_parameter(i) + duplicate(i)
		i = i + 1
	}
	print n
}
ZG

# A LIST LITERAL ASSIGNED. `xs = [v]` pushed `v` as written where `xs := [v]` copies what it
# borrows, and `xs = [xs[0], xs[0]]` read the old list into the new one uncounted and dropped
# the old one under it. The literal is assigned as written, as a fill and through a field, for
# each element type that is counted: text, a list, a map, a struct that owns, a spec box and a
# closure.
case_run held_list_elem no no <<'ZG'
struct P {
	pub name: str
	pub xs: list[int]
}

spec Shape {
	fn area() -> int
}

struct Sq {
	pub tag: str
	pub side: int
}

impl Shape for Sq {
	fn area() -> int {
		return this.side + bytearray(this.tag).len()
	}
}

struct HStr {
	pub xs: list[str]
}

struct HStruct {
	pub xs: list[P]
}

fn word(n: int) -> str {
	return str(n) + "abcdefghijklmnop"
}

fn mkl(n: int) -> list[int] {
	return [n, n + 1]
}

fn mkm(n: int) -> map[str, str] {
	return {"k": word(n)}
}

fn mkp(n: int) -> P {
	return P(word(n), mkl(n))
}

fn mkb(n: int) -> Shape {
	return Sq(word(n), n)
}

fn mkf(n: int) -> fn (int) -> int {
	xs := [n, n]
	return fn (a: int) -> int {
		return a + xs[0]
	}
}

fn same(got: str, n: int) -> int {
	raise "a value was changed by the list it was put in" if got != word(n)
	return 1
}

fn texts(i: int) -> int {
	v := word(i)
	mut xs: list[str] = [word(i + 1)]
	xs = [v]
	mut n := same(xs[0], i)
	xs = [v; 2]
	n = n + same(xs[1], i)
	mut h := HStr([word(i + 2)])
	h.xs = [v]
	n = n + same(h.xs[0], i)
	xs = [word(i + 3), v, word(i + 4)]
	later := word(i + 5)
	return n + same(xs[1], i) + same(v, i) + same(later, i + 5)
}

fn itself(i: int) -> int {
	mut xs := [word(i), word(i + 1)]
	xs = [xs[0], xs[0]]
	later := word(i + 2)
	return same(xs[0], i) + same(xs[1], i) + same(later, i + 2)
}

fn lists(i: int) -> int {
	v := mkl(i)
	mut xs: list[list[int]] = [mkl(i + 1)]
	xs = [v]
	xs = [v; 2]
	xs = [xs[0], xs[1]]
	later := mkl(i + 2)
	raise "a list was changed by the list it was put in" if xs[1][0] != i or v[1] != i + 1
	return xs.len() + later.len()
}

fn maps(i: int) -> int {
	v := mkm(i)
	mut xs: list[map[str, str]] = [mkm(i + 1)]
	xs = [v]
	xs = [v; 2]
	later := mkm(i + 2)
	return same(xs[1]["k"], i) + same(v["k"], i) + later.len()
}

fn structs(i: int) -> int {
	v := mkp(i)
	mut xs: list[P] = [mkp(i + 1)]
	xs = [v]
	mut h := HStruct([mkp(i + 2)])
	h.xs = [v; 2]
	later := mkp(i + 3)
	return same(xs[0].name, i) + same(h.xs[1].name, i) + same(v.name, i) + later.xs.len()
}

fn boxes(i: int) -> int {
	v := mkb(i)
	mut xs: list[Shape] = [mkb(i + 1)]
	xs = [v]
	xs = [v; 2]
	later := mkb(i + 2)
	raise "a box was changed by the list it was put in" if xs[1].area() != v.area()
	return later.area() - i
}

fn closures(i: int) -> int {
	v := mkf(i)
	mut xs: list[fn (int) -> int] = [mkf(i + 1)]
	xs = [v]
	xs = [v; 2]
	f := xs[1]
	later := mkf(i + 2)
	raise "a closure was changed by the list it was put in" if f(1) != v(1)
	return later(0) - i
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		n = n + texts(i) + itself(i) + lists(i) + maps(i) + structs(i) + boxes(i) + closures(i)
		i = i + 1
	}
	print n
}
ZG

# AND THE LIST IT IS BUILDING. The new list was held by nothing until the assignment was done,
# so a later element that left — a `?`, a `?? break`, a raise under a `guard` — left it and
# what it already held behind. This one is a count and nothing else.
case_run assigned_exit no no <<'ZG'
fn mkl(n: int) -> list[int] {
	return [n, n + 1]
}

fn may(k: int) -> int? {
	return nil if k > 0
	return 5
}

fn bad(k: int) -> int {
	raise "bad" if k > 0
	return 0
}

fn leaves(i: int, k: int) -> int? {
	mut xs: list[list[int]] = [mkl(i)]
	xs = [mkl(i), mkl(may(k)?), mkl(i + 2)]
	return xs.len()
}

fn breaks(i: int, k: int) -> int {
	mut n := 0
	for j in 0..2 {
		mut xs: list[list[int]] = [mkl(i + j)]
		xs = [mkl(i), mkl((may(k) ?? break)), mkl(i + 2)]
		n = n + xs.len()
	}
	return n
}

fn aborts(i: int, k: int) -> int {
	mut xs: list[list[int]] = [mkl(i)]
	xs = [mkl(i), mkl(bad(k)), mkl(i + 2)]
	return xs.len()
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		n = n + (leaves(i, 1) ?? 1) + (leaves(i, 0) ?? 0) + breaks(i, 1) + breaks(i, 0)
		n = n + (guard { aborts(i, 1) } ?? 1) + (guard { aborts(i, 0) } ?? 0)
		i = i + 1
	}
	print n
}
ZG

# AN EARLY EXIT INSIDE AN ELEMENT OF `xs := [...]` (#328). The binding was registered for
# release before its elements were rendered and counted as pending only after, so an exit
# written inside an element left without unwinding it: the registration stayed behind, naming
# a frame that was gone, and the elements already pushed were given back by nobody. `a`, `b`
# and `c` choose which element leaves, so one function is the exit in first, middle and last
# position and the path where nothing leaves; an earlier binding that owns something made a
# function exit unwind and hid the fault, and did not hide it for a loop exit.
case_run literal_exit no no <<'ZG'
fn word(n: int) -> str {
	return str(n) + "abcdefghijklmnop"
}

fn mkl(n: int) -> list[int] {
	return [n, n + 1]
}

fn may(k: int) -> int? {
	return nil if k > 0
	return 5
}

fn mayr(k: int) -> Result[int] {
	return Either.Right(ValueError("neg")) if k > 0
	return Either.Left(5)
}

fn q_opt(a: int, b: int, c: int) -> int? {
	xs := [mkl(may(a)?), mkl(may(b)?), mkl(may(c)?)]
	return xs.len()
}

fn q_opt_pre(a: int, b: int, c: int) -> int? {
	keep := mkl(9)
	xs := [mkl(may(a)?), mkl(may(b)?), mkl(may(c)?)]
	return xs.len() + keep.len()
}

fn q_res(a: int, b: int, c: int) -> Result[int] {
	xs := [mkl(mayr(a)?), mkl(mayr(b)?), mkl(mayr(c)?)]
	return Either.Left(xs.len())
}

fn ret(a: int, b: int, c: int) -> int {
	xs := [mkl((may(a) ?? return -1)), mkl((may(b) ?? return -2)), mkl((may(c) ?? return -3))]
	return xs.len()
}

fn brk(a: int, b: int, c: int) -> int {
	mut n := 0
	for i in 0..3 {
		xs := [mkl((may(a) ?? break)), mkl((may(b) ?? break)), mkl((may(c) ?? break))]
		n = n + xs.len() + i
	}
	return n
}

fn brk_pre(a: int, b: int, c: int) -> int {
	mut n := 0
	for i in 0..3 {
		keep := mkl(9)
		xs := [mkl((may(a) ?? break)), mkl((may(b) ?? break)), mkl((may(c) ?? break))]
		n = n + xs.len() + keep.len() + i
	}
	return n
}

fn cont(a: int, b: int, c: int) -> int {
	mut n := 0
	for i in 0..3 {
		n = n + i + 1
		xs := [mkl((may(a) ?? continue)), mkl((may(b) ?? continue)), mkl((may(c) ?? continue))]
		n = n + xs.len()
	}
	return n
}

fn texts(a: int, b: int, c: int) -> int? {
	xs := [word(may(a)?), word(may(b)?), word(may(c)?)]
	return xs.len()
}

fn scoped(a: int, b: int, c: int) -> int? {
	mut n := 0
	with [mkl(may(a)?), mkl(may(b)?), mkl(may(c)?)] as xs {
		n = xs.len()
	}
	return n
}

fn opt(f: fn (int, int, int) -> int?) -> int {
	return (f(1, 0, 0) ?? 1) + (f(0, 1, 0) ?? 2) + (f(0, 0, 1) ?? 3) + (f(0, 0, 0) ?? 4)
}

fn res(f: fn (int, int, int) -> Result[int]) -> int {
	return (f(1, 0, 0) ?? 1) + (f(0, 1, 0) ?? 2) + (f(0, 0, 1) ?? 3) + (f(0, 0, 0) ?? 4)
}

fn plain(f: fn (int, int, int) -> int) -> int {
	return f(1, 0, 0) + f(0, 1, 0) + f(0, 0, 1) + f(0, 0, 0)
}

fn main() {
	mut n := 0
	mut i := 0
	r := rounds()
	for i < r {
		n = n + opt(q_opt) + opt(q_opt_pre) + res(q_res) + opt(texts) + opt(scoped)
		n = n + plain(ret) + plain(brk) + plain(brk_pre) + plain(cont)
		i = i + 1
	}
	print n
}
ZG

# --- a literal with no type of its own, at a position that has one (#332) -----------------
#
# A SILENTLY WRONG ANSWER, which is the one kind of defect nothing else here was written for.
# A map literal took its key and value types from its entries, so an empty `{:}` anywhere but
# a typed binding was built as a table of integers: a `str` key was hashed and compared by
# where it lives, the same key written twice made two entries, a read through an equal key
# found neither, and the map gave back neither its keys nor its values. No sanitizer sees
# that, the seed builds none of these programs, and the gate that compares what a program
# PRINTS reads the private corpus — where each position is a line of its own.
#
# WHAT THIS GATE CAN HOLD OF IT is the answer as well as the count. Every position asks its
# map the question and the round RAISES, naming each position that answered wrong, so a wrong
# answer is a `RUN` failure here and not a number nobody read; the keys and values a wrong
# table leaves behind are the per-round count, for a fix that gets the answer and loses the
# release. The read is made only of a map holding one entry, so a wrong table is reported and
# does not end the round at the first position it meets.
#
# ONE PROGRAM, WRITTEN ONCE, FOR EACH TYPE. `typed_positions` puts a type, the literal under
# test and another value of that type into the same list of positions; the case supplies
# `count`, which asks the question, and `want`, the answer. The cases differ in what the wrong
# table gets wrong: a `str` key over plain values, the same over values that are counted, a
# literal that HAS an entry and read its value type from it (`{"a": 1}` where a float is
# wanted), and `set()`, which was refused at every position but a typed binding (E4100).
#
# What is NOT here: `{:}` where no position says what it holds — a branch of an `if` or a
# `match`, a tuple element, an argument only a type parameter describes — which is refused
# by name and held by `reject`.
typed_positions() {
	local name=$1 ty=$2 lit=$3 other=$4
	# THROUGH A FILE, not a pipe: the right-hand side of a pipe is a subshell, and a case run
	# in one keeps its verdict and its place in the count to itself.
	{
		cat
		sed -e "s|@T@|$ty|g" -e "s|@L@|$lit|g" -e "s|@N@|$other|g" <<'ZG'
struct H {
	pub m: @T@
}

struct D {
	pub n: int
	pub m: @T@ = @L@
}

struct K {
	pub n: int
}

enum E {
	No
	Has(@T@)
}

spec Taker {
	fn take(q: @T@) -> int
}

impl D {
	fn pass(q: @T@) -> int {
		return count(q) * this.n
	}
}

impl Taker for K {
	fn take(q: @T@) -> int {
		return count(q) * this.n
	}
}

fn check(mut &bad: list[str], what: str, n: int) {
	if n != want() {
		bad.append(what + " gave " + str(n))
	}
}

fn told(bad: list[str]) -> str {
	mut all := ""
	for b in bad {
		all = all + b + "; "
	}
	return all
}

fn same(q: @T@) -> @T@ {
	return q
}

fn dflt(q: @T@ = @L@) -> @T@ {
	return q
}

fn with_t[T](q: @T@, w: T) -> @T@ {
	return q
}

fn made() -> @T@ {
	return @L@
}

fn some() -> @T@? {
	return @L@
}

fn none() -> @T@? {
	return nil
}

fn first(xs: list[@T@]) -> @T@ {
	return xs[0]
}

fn at1(mm: map[int, @T@]) -> @T@ {
	return mm[1]
}

fn inside(e: E) -> int {
	return match e {
		E.Has(m) => count(m)
		_ => 0
	}
}

fn work(q: @T@, o: chan[int]<-) {
	o <- count(q)
}

fn deferred(o: chan[int]<-) {
	defer work(@L@, o)
}

fn bindings(mut &bad: list[str]) {
	typed: @T@ = @L@
	check(bad, "a typed binding", count(typed))

	mut x: @T@ = @N@
	x = @L@
	check(bad, "an assignment", count(x))

	mut h := H(@N@)
	h.m = @L@
	check(bad, "an assignment to a field", count(h.m))

	mut xs: list[@T@] = [@N@]
	xs[0] = @L@
	check(bad, "an assignment to an element", count(xs[0]))

	mut mm: map[int, @T@] = {1: @N@}
	mm[1] = @L@
	mm[2] = @L@
	check(bad, "a map write over a key", count(mm[1]))
	check(bad, "a map write of a new key", count(mm[2]))

	mut ys: list[@T@] = []
	ys.append(@L@)
	check(bad, "an appended element", count(ys[0]))
}

fn arguments(mut &bad: list[str]) {
	check(bad, "an argument", count(@L@))
	check(bad, "a named argument", count(m: @L@))
	d := D(1)
	check(bad, "a method's argument", d.pass(@L@))
	t: Taker = K(1)
	check(bad, "a spec method's argument", t.take(@L@))
	f := fn (q: @T@) -> @T@ {
		return q
	}
	check(bad, "a closure's argument", count(f(@L@)))
	g := same
	check(bad, "a function value's argument", count(g(@L@)))
	check(bad, "an argument beside a type parameter's", count(with_t(@L@, 1)))
	o := chan[int](1)
	deferred(o)
	if n := <-o {
		check(bad, "an argument a defer keeps", n)
	}
	check(bad, "a parameter's default", count(dflt()))
	check(bad, "a field's default", count(D(1).m))
}

fn constructions(mut &bad: list[str]) {
	check(bad, "a struct field", count(H(@L@).m))
	check(bad, "a named struct field", count(D(m: @L@, n: 1).m))
	check(bad, "a variant's payload", inside(E.Has(@L@)))
	hs: list[H] = [H(@L@)]
	check(bad, "a field in a list element", count(hs[0].m))
	es: list[E] = [E.Has(@L@)]
	check(bad, "a payload in a list element", inside(es[0]))

	xs: list[@T@] = [@L@]
	check(bad, "a list element", count(xs[0]))
	ys: list[@T@] = [@N@, @L@]
	check(bad, "a later list element", count(ys[1]))
	zs := [@N@, @L@]
	check(bad, "a list element typed by the first", count(zs[1]))
	fs: list[@T@] = [@L@; 2]
	check(bad, "a filled list's element", count(fs[1]))
	arr: [@T@; 1] = [@L@]
	check(bad, "an array element", count(arr[0]))
	check(bad, "an element of a list argument", count(first([@L@])))

	mm: map[int, @T@] = {1: @L@}
	check(bad, "a map value", count(mm[1]))
	nn: map[int, @T@] = {1: @N@, 2: @L@}
	check(bad, "a later map value", count(nn[2]))
	oo := {1: @N@, 2: @L@}
	check(bad, "a map value typed by the first", count(oo[2]))
	check(bad, "a value of a map argument", count(at1({1: @L@})))
}

fn answers(mut &bad: list[str]) {
	check(bad, "a return", count(made()))
	mut bound := made()
	check(bad, "a return, bound", count(bound))
	f := fn () -> @T@ {
		return @L@
	}
	check(bad, "a closure's return", count(f()))
	if v := some() {
		check(bad, "an optional return", count(v))
	}
	o: @T@? = @L@
	if v := o {
		check(bad, "an optional binding", count(v))
	}
	x := none() ?? @L@
	check(bad, "a fallback", count(x))
	y: @T@ = none() ?? @L@
	check(bad, "a fallback at a typed binding", count(y))
}

fn sends(mut &bad: list[str]) {
	ch := chan[@T@](2)
	ch <- @L@
	if v := <-ch {
		check(bad, "a send", count(v))
	}
	select {
		ch <- @L@ => {
			nop
		}
	}
	if v := <-ch {
		check(bad, "a send in a select", count(v))
	}
	o := chan[int](1)
	spawn work(@L@, o)
	if n := <-o {
		check(bad, "a spawned call's argument", n)
	}
}

fn main() {
	mut i := 0
	r := rounds()
	for i < r {
		mut bad: list[str] = []
		bindings(bad)
		arguments(bad)
		constructions(bad)
		answers(bad)
		sends(bad)
		raise told(bad) if bad.len() > 0
		i = i + 1
	}
	print i
}
ZG
	} >"$WORK/$name.body"
	case_run "$name" no yes <"$WORK/$name.body"
}

typed_positions slot_map 'map[str, int]' '{:}' '{"a": 1}' <<'ZG'
fn word(n: int) -> str {
	return str(n) + "abcdefghijklmnop"
}

fn want() -> int {
	return 12
}

fn count(m: map[str, int]) -> int {
	mut c := m
	k := word(1)
	c[word(1)] = 1
	c[word(1)] = 2
	return c.len() * 10 if c.len() != 1
	return 10 + c[k]
}

ZG

typed_positions slot_map_owned 'map[str, list[int]]' '{:}' '{"a": [1]}' <<'ZG'
fn word(n: int) -> str {
	return str(n) + "abcdefghijklmnop"
}

fn mkl(n: int) -> list[int] {
	return [n, n + 1]
}

fn want() -> int {
	return 15
}

fn count(m: map[str, list[int]]) -> int {
	mut c := m
	k := word(1)
	c[word(1)] = mkl(1)
	c[word(1)] = mkl(5)
	return c.len() * 10 if c.len() != 1
	v := c[k]
	return 10 + v[0]
}

ZG

typed_positions slot_literal 'map[str, float]' '{"a": 1}' '{"a": 2.5}' <<'ZG'
fn want() -> int {
	return 15
}

fn count(m: map[str, float]) -> int {
	half := m["a"] + 0.5
	return 15 if half == 1.5
	return 5 if half == 0.5
	return 0
}

ZG

typed_positions slot_set 'set[str]' 'set()' 'set(["a"])' <<'ZG'
fn want() -> int {
	return 0
}

fn count(m: set[str]) -> int {
	return m.len() * 10 + int("a" in m)
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
