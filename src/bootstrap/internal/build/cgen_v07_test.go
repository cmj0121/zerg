// v0.7 Unit 7 codegen tests — pthread-backed concurrency runtime emission
// plus per-element chan helpers, spawn / defer / select / wait_group
// lowering, and the defer × `?` interaction.
//
// What's covered:
//
//   * chan[int] emits a per-element struct + send / recv / close helpers and
//     dedupes across multiple uses.
//   * chan[int] + chan[str] emit two distinct sets of helpers.
//   * spawn fn() { ... }() emits a top-level thread fn + env alloc + a
//     zerg_spawn call.
//   * Capturing a list[int] under spawn calls the list's _copy helper.
//   * defer LIFO: a fn with two defers emits two zerg_defer_push calls plus
//     the per-frame epilogue drain.
//   * defer + `?` combined: the `?` lowering inside a HasDefers fn drains
//     defers ahead of the early return.
//   * select with multiple arms emits a stack-array of zerg_select_case
//     descriptors, the runtime call, and a switch dispatch.
//   * wait_group: emits the constructor + add / done / wait method calls.
//   * Codegen size guard: a representative v0.7 program stays under 50× C-
//     to-source ratio.

package build

import (
	"strings"
	"testing"
)

// --- chan emission --------------------------------------------------------

func TestV07CgenChanIntEmitsHelpers(t *testing.T) {
	src := `let ch := chan[int]()
close(ch)
`
	out := mustEmit(t, src)
	for _, want := range []string{
		"struct zerg_chan_int64_t {",
		"static zerg_chan_int64_t *zerg_chan_int64_t_make(",
		"static void zerg_chan_int64_t_send(",
		"static void zerg_chan_int64_t_close(",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing chan helper %q in:\n%s", want, out)
		}
	}
}

func TestV07CgenChanIntDedupes(t *testing.T) {
	src := `let a := chan[int]()
let b := chan[int]()
close(a)
close(b)
`
	out := mustEmit(t, src)
	count := strings.Count(out, "struct zerg_chan_int64_t {")
	if count != 1 {
		t.Errorf("expected 1 chan struct definition, got %d in:\n%s", count, out)
	}
}

func TestV07CgenChanIntAndStrAreDistinct(t *testing.T) {
	src := `let ai := chan[int]()
let bs := chan[str]()
close(ai)
close(bs)
`
	out := mustEmit(t, src)
	if !strings.Contains(out, "struct zerg_chan_int64_t {") {
		t.Errorf("missing zerg_chan_int64_t in:\n%s", out)
	}
	if !strings.Contains(out, "struct zerg_chan_zerg_str {") {
		t.Errorf("missing zerg_chan_zerg_str in:\n%s", out)
	}
}

// --- recv emits Option-shaped enum ----------------------------------------

func TestV07CgenRecvEmitsOptionEnum(t *testing.T) {
	src := `fn run(ch: chan[int]) -> int {
let v := <- ch
return 0
}
print 0
`
	out := mustEmit(t, src)
	if !strings.Contains(out, "zerg_chan_int64_t_recv") {
		t.Errorf("missing recv helper call in:\n%s", out)
	}
}

// --- spawn emission -------------------------------------------------------

func TestV07CgenSpawnAnonEmitsThreadFn(t *testing.T) {
	src := `fn run() { spawn fn() { print 1 }() }
run()
`
	out := mustEmit(t, src)
	for _, want := range []string{
		"static void *zerg_anonfn_",
		"zerg_spawn(zerg_anonfn_",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing spawn-anon piece %q in:\n%s", want, out)
		}
	}
}

func TestV07CgenSpawnCapturesListClonesIt(t *testing.T) {
	src := `fn run() {
let xs: list[int] = [1, 2, 3]
spawn fn() { print len(xs) }()
}
run()
`
	out := mustEmit(t, src)
	if !strings.Contains(out, "zerg_list_int64_t_copy(z_xs)") {
		t.Errorf("spawn capture should clone the list via _copy; got:\n%s", out)
	}
	if !strings.Contains(out, "zerg_spawn(") {
		t.Errorf("missing zerg_spawn call in:\n%s", out)
	}
}

// --- anon-fn value / IIFE in non-spawn position ---------------------------

func TestV07CgenIIFEReturningValueEmitsBodyFn(t *testing.T) {
	src := `let x := fn() -> int { return 42 }()
print x
`
	out := mustEmit(t, src)
	for _, want := range []string{
		"static int64_t zerg_anonfn_v_",
		"zerg_anonfn_v_",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing IIFE piece %q in:\n%s", want, out)
		}
	}
}

func TestV07CgenIIFEWithArgsPassesThemThrough(t *testing.T) {
	src := `let r := fn(x: int) -> int { return x * 2 }(21)
print r
`
	out := mustEmit(t, src)
	if !strings.Contains(out, "static int64_t zerg_anonfn_v_") {
		t.Errorf("missing IIFE body fn in:\n%s", out)
	}
	if !strings.Contains(out, "int64_t z_x") {
		t.Errorf("IIFE body fn should take typed param; got:\n%s", out)
	}
}

func TestV07CgenLetFnValueEmitsFnValuePair(t *testing.T) {
	src := `let n := 7
let f := fn() -> int { return n + 1 }
print f()
`
	out := mustEmit(t, src)
	for _, want := range []string{
		"zerg_fn_value z_f",
		"(zerg_fn_value){.fn = (void *)zerg_anonfn_v_",
		"((int64_t (*)(void *))(z_f).fn)((z_f).env",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing fn-value piece %q in:\n%s", want, out)
		}
	}
}

func TestV07CgenFnValueCaptureClonedAtBind(t *testing.T) {
	src := `let xs: list[int] = [1, 2, 3]
let f := fn() -> int { return len(xs) }
print f()
`
	out := mustEmit(t, src)
	if !strings.Contains(out, "zerg_list_int64_t_copy(z_xs)") {
		t.Errorf("fn-value capture should clone the list via _copy; got:\n%s", out)
	}
}

// --- defer emission -------------------------------------------------------

func TestV07CgenDeferLIFOEmitsTwoPushes(t *testing.T) {
	src := `fn run() {
defer { print 1 }
defer { print 2 }
}
run()
`
	out := mustEmit(t, src)
	// Count call sites only — the runtime prelude declares the helper too.
	count := strings.Count(out, "zerg_defer_push(zerg_defer_")
	if count != 2 {
		t.Errorf("expected 2 zerg_defer_push call sites, got %d in:\n%s", count, out)
	}
	if !strings.Contains(out, "zerg_defer_drain(__zerg_defer_marker)") {
		t.Errorf("missing defer-drain epilogue in:\n%s", out)
	}
}

// --- defer × ? interaction ------------------------------------------------

func TestV07CgenDeferAndPropagateDrainsBeforeReturn(t *testing.T) {
	src := `fn fetch() -> Result[int, str] { return Result.Ok(7) }
fn run() -> Result[int, str] {
defer { print 1 }
let v := fetch()?
return Result.Ok(v)
}
run()
`
	out := mustEmit(t, src)
	// The propagate lowering inside a HasDefers fn must contain a drain
	// call ahead of the early-return.
	if !strings.Contains(out, "zerg_defer_drain(__zerg_defer_marker); return") {
		t.Errorf("? in HasDefers fn should drain defers before returning; got:\n%s", out)
	}
}

// --- select emission ------------------------------------------------------

func TestV07CgenSelectEmitsCaseArray(t *testing.T) {
	src := `fn run(a: chan[int], b: chan[int]) {
select {
v := <- a -> { print v }
v := <- b -> { print v }
}
}
print 0
`
	out := mustEmit(t, src)
	for _, want := range []string{
		"zerg_select_case __cases[2];",
		"int __chosen = zerg_select(__cases, 2,",
		"switch (__chosen) {",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing select piece %q in:\n%s", want, out)
		}
	}
}

func TestV07CgenSelectWithDefault(t *testing.T) {
	src := `fn run(a: chan[int]) {
select {
v := <- a -> { print v }
_ -> { print 0 }
}
}
print 0
`
	out := mustEmit(t, src)
	if !strings.Contains(out, "zerg_select(__cases, 2, 1,") {
		t.Errorf("default arm should set has_default to 1; got:\n%s", out)
	}
}

// --- wait_group -----------------------------------------------------------

func TestV07CgenWaitGroupEmitsHandle(t *testing.T) {
	src := `fn run() {
let wg := wait_group()
wg.add(2)
wg.done()
wg.wait()
}
run()
`
	out := mustEmit(t, src)
	for _, want := range []string{
		"zerg_wait_group_make()",
		"zerg_wait_group_add(z_wg,",
		"zerg_wait_group_done(z_wg)",
		"zerg_wait_group_wait(z_wg)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing wait_group call %q in:\n%s", want, out)
		}
	}
}

// --- runtime presence -----------------------------------------------------

func TestV07CgenRuntimePresentWhenChanUsed(t *testing.T) {
	src := `let ch := chan[int]()
close(ch)
`
	out := mustEmit(t, src)
	for _, want := range []string{
		"#include <pthread.h>",
		"static void zerg_defer_push(",
		"typedef struct zerg_defer_rec",
		"static int zerg_select(",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing v0.7 runtime piece %q in:\n%s", want, out)
		}
	}
}

func TestV07CgenRuntimeAbsentWithoutV07Use(t *testing.T) {
	out := mustEmit(t, "print 1\n")
	if strings.Contains(out, "#include <pthread.h>") {
		t.Errorf("v0.7 runtime should not appear in plain v0.0 program; got:\n%s", out)
	}
}

// --- size guard -----------------------------------------------------------

// TestV07CgenSizeGuard mirrors the v0.6 size guard: a representative v0.7
// program (channels, spawn, defer, wait_group, select) stays within 50× the
// source size after codegen. The pthread runtime adds ~3 KB to every v0.7
// program; the source must therefore be at least ~300 bytes for the ratio
// to hold. The fixture below is the canonical "fan-in over channels" idiom
// the v0.7 corpus exercises.
func TestV07CgenSizeGuard(t *testing.T) {
	src := `fn producer(ch: chan[int], wg: WaitGroup, base: int) {
defer { wg.done() }
for i in 0..5 {
ch <- base + i
}
}
fn collector(ch: chan[int], total: chan[int]) {
mut sum := 0
for v in ch {
sum += v
}
total <- sum
}
fn run() {
let ch := chan[int](4)
let total := chan[int](1)
let wg := wait_group()
wg.add(2)
spawn producer(ch, wg, 0)
spawn producer(ch, wg, 100)
spawn collector(ch, total)
wg.wait()
close(ch)
let r := <- total
match r {
Option.Some(s) => { print s }
Option.None => { print 0 }
}
}
run()
`
	out := mustEmit(t, src)
	if len(out) > len(src)*50 {
		t.Errorf("emitted size %d exceeds 50× source size %d", len(out), len(src))
	}
}
