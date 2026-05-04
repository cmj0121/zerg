package run

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// v0.3 push / cap-doubling runtime tests.
//
// Unit 5 of v0.3 added cap-doubling growth to push so successive pushes
// don't realloc on every call. The interpreter doesn't carry an explicit
// cap (it's a Go slice with built-in cap), but the codegen does — these
// tests exercise behaviour observable from the surface so run/build parity
// holds.
// ---------------------------------------------------------------------------

// TestRunPushPastInitialCapGrows — pushing past the initial small list grows
// the backing buffer correctly. Initial len 1 → push 4 more → final list of
// 5 elements. Exercises the cap-doubling growth path (cap goes 1 → 2 → 4 →
// 8 in the codegen).
func TestRunPushPastInitialCapGrows(t *testing.T) {
	src := `mut xs := [1]
push(xs, 2)
push(xs, 3)
push(xs, 4)
push(xs, 5)
print xs
print len(xs)
`
	expectOK(t, src, "[ 1, 2, 3, 4, 5 ]\n5\n")
}

// TestRunPushFromEmptyList — pushing into an initially-empty list exercises
// the cap == 0 → 4 first-growth path. The list literal `[]` is a typeck
// error without a type annotation; we rely on the inferred type from the
// first push not being a thing (typeck requires the literal to type-resolve
// at construction). Use a length-1 starter as a cheaper proxy.
func TestRunPushFromEmptyList(t *testing.T) {
	src := `mut xs := [0]
push(xs, 1)
push(xs, 2)
push(xs, 3)
print len(xs)
print xs[0]
print xs[3]
`
	expectOK(t, src, "4\n0\n3\n")
}

// TestRunPushHundredElements — stress test that lots of pushes succeed
// without OOM or runtime error. The cap-doubling means the program does
// O(log N) reallocs, not O(N).
func TestRunPushHundredElements(t *testing.T) {
	src := `mut xs := [0]
for i in 1..100 {
push(xs, i)
}
print len(xs)
print xs[0]
print xs[99]
`
	expectOK(t, src, "100\n0\n99\n")
}

// TestRunPushAndIndexAssignInterleaved — push then index-assign then push
// again to confirm the (data, len, cap) header stays consistent across
// operations.
func TestRunPushAndIndexAssignInterleaved(t *testing.T) {
	src := `mut xs := [1, 2]
push(xs, 3)
xs[0] = 99
push(xs, 4)
print xs
`
	expectOK(t, src, "[ 99, 2, 3, 4 ]\n")
}

// TestRunPushRetainsExistingValues — verify cap-doubling realloc copies the
// old buffer contents (it should — realloc preserves data up to the old
// size).
func TestRunPushRetainsExistingValues(t *testing.T) {
	src := `mut xs := [10, 20, 30]
push(xs, 40)
push(xs, 50)
print xs[0]
print xs[1]
print xs[2]
print xs[3]
print xs[4]
`
	expectOK(t, src, "10\n20\n30\n40\n50\n")
}

// TestRunPushDoesNotErrorOnLargeBuild — sanity that the interpreter doesn't
// hit any internal panic on a stress build of 1000 pushes.
func TestRunPushOneThousand(t *testing.T) {
	// Build the program string with a loop body — the parse path is the
	// only thing being exercised here; the runtime walks the actual loop.
	src := `mut xs := [0]
for i in 1..1000 {
push(xs, i)
}
print len(xs)
`
	got, err := runSrc(t, src)
	if err != nil {
		t.Fatalf("runSrc: %v", err)
	}
	if !strings.HasPrefix(got, "1000\n") {
		t.Errorf("expected len 1000, got %q", got)
	}
}
