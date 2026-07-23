package build

import (
	"strings"
	"testing"
)

// RUN-based tests for the built-in map[K, V] container (docs/collections.md), one per
// implemented slice M1-M5. Every program is compiled to C, linked against the
// materialized runtime under ASan+UBSan, and executed, so a passing test asserts a
// clean exit + exact stdout with no memory error. The non-POD teardown paths
// additionally run under the counting allocator (runProgramRTBalanced) to prove a zero
// alloc/free balance, since macOS ships no LeakSanitizer. The abort path is exercised
// separately because it exits non-zero by design.

// TestMapBindAndDrop (M1): a map literal binds, indexes by key, and drops its storage
// at scope exit (the runtime-linked compile under ASan proves the drop-env runs clean).
func TestMapBindAndDrop(t *testing.T) {
	got := runProgramRTBalanced(t, "fn main() {\n\tm := {\"a\": 1, \"b\": 2}\n\tprint m[\"a\"]\n\tprint m[\"b\"]\n}\n")
	if got != "1\n2\n" {
		t.Fatalf("map bind/index = %q, want %q", got, "1\n2\n")
	}
}

// TestMapIntKeys (M1): an int-keyed map builds and indexes with the built-in int Hash.
func TestMapIntKeys(t *testing.T) {
	got := runProgramRTBalanced(t, "fn main() {\n\tm := {1: 10, 2: 20, 3: 30}\n\tprint m[2]\n\tprint m[3]\n}\n")
	if got != "20\n30\n" {
		t.Fatalf("int-keyed map = %q, want %q", got, "20\n30\n")
	}
}

// TestMapEmptyAnnotated (M1): an empty literal `{:}` builds against a type annotation.
// An empty map allocates no storage, so there is no heap to balance — runProgramRT's
// clean ASan exit is the assertion.
func TestMapEmptyAnnotated(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n\tm: map[int, int] = {:}\n\tprint m.len()\n}\n")
	if got != "0\n" {
		t.Fatalf("empty map len = %q, want %q", got, "0\n")
	}
}

// TestMapKeyGate (M1): a map key of a non-Hash type (only int/str this phase) is
// rejected with a clean diagnostic, not a backend failure.
func TestMapKeyGate(t *testing.T) {
	_, _, diags := Compile("fn main() {\n\tm: map[bool, int] = {true: 1}\n}\n")
	if len(diags) == 0 || !strings.Contains(diags[0].Msg, "needs Hash") {
		t.Fatalf("expected a map-key Hash gate, got %v", diags)
	}
}

// TestMapEmptyInferGate (M1): a bare empty literal cannot infer its type.
func TestMapEmptyInferGate(t *testing.T) {
	_, _, diags := Compile("fn main() {\n\tm := {:}\n}\n")
	if len(diags) == 0 || !strings.Contains(diags[0].Msg, "empty map") {
		t.Fatalf("expected an empty-map inference gate, got %v", diags)
	}
}

// TestMapIndexAndSet (M2): `m[k]` reads, `m[k] = v` inserts a new key and updates an
// existing one in place; a mut map reflects both.
func TestMapIndexAndSet(t *testing.T) {
	got := runProgramRTBalanced(t, "fn main() {\n\tmut m := {\"a\": 1, \"b\": 2}\n\tm[\"c\"] = 3\n\tm[\"a\"] = 100\n"+
		"\tprint m[\"a\"]\n\tprint m[\"c\"]\n\tprint m.len()\n}\n")
	if got != "100\n3\n3\n" {
		t.Fatalf("map index/set = %q, want %q", got, "100\n3\n3\n")
	}
}

// TestMapIndexMissAbort (M2): `m[k]` on a missing key aborts with KeyError ("key not
// found") and a non-zero exit, not undefined behaviour.
func TestMapIndexMissAbort(t *testing.T) {
	got := runProgramRTAbort(t, "fn main() {\n\tm := {\"a\": 1}\n\tprint m[\"zzz\"]\n}\n")
	if !strings.Contains(got, "key not found") {
		t.Fatalf("expected a key-not-found abort, got %q", got)
	}
}

// TestMapSetImmutableGate (M2): `m[k] = v` on a plain (non-mut) map is rejected.
func TestMapSetImmutableGate(t *testing.T) {
	_, _, diags := Compile("fn main() {\n\tm := {1: 2}\n\tm[3] = 4\n}\n")
	if len(diags) == 0 || !strings.Contains(diags[0].Msg, "immutable") {
		t.Fatalf("expected an immutable-map set gate, got %v", diags)
	}
}

// TestMapGet (M2): `.get(k)` yields V? — the value for a present key, the empty case for
// a missing one, read here through the `??` default.
func TestMapGet(t *testing.T) {
	got := runProgramRTBalanced(t, "fn main() {\n\tm := {\"a\": 5, \"b\": 6}\n"+
		"\tprint m.get(\"b\") ?? -1\n\tprint m.get(\"zzz\") ?? -1\n}\n")
	if got != "6\n-1\n" {
		t.Fatalf("map get = %q, want %q", got, "6\n-1\n")
	}
}

// TestMapMembership (M3): `k in m` yields bool via the runtime membership probe.
func TestMapMembership(t *testing.T) {
	got := runProgramRTBalanced(t, "fn main() {\n\tm := {\"a\": 1, \"b\": 2}\n"+
		"\tif \"a\" in m { print 1 }\n\tif \"zzz\" in m { print 2 } else { print 3 }\n}\n")
	if got != "1\n3\n" {
		t.Fatalf("map membership = %q, want %q", got, "1\n3\n")
	}
}

// TestMapLen (M4): `.len()` yields the entry count, including growth past the load factor.
func TestMapLen(t *testing.T) {
	got := runProgramRTBalanced(t, "fn main() {\n\tmut m := {\"k0\": 0}\n"+
		"\tm[\"k1\"] = 1\n\tm[\"k2\"] = 2\n\tm[\"k3\"] = 3\n\tm[\"k4\"] = 4\n\tm[\"k5\"] = 5\n"+
		"\tm[\"k6\"] = 6\n\tm[\"k7\"] = 7\n\tm[\"k8\"] = 8\n\tm[\"k9\"] = 9\n\tprint m.len()\n}\n")
	if got != "10\n" {
		t.Fatalf("map len after grow = %q, want %q", got, "10\n")
	}
}

// TestMapForInInsertionOrder (M5): `for k in m` binds the KEY and walks in INSERTION
// order (docs/collections.md), with the value reached via `m[k]`.
func TestMapForInInsertionOrder(t *testing.T) {
	got := runProgramRTBalanced(t, "fn main() {\n\tmut m := {\"a\": 1, \"b\": 2}\n\tm[\"c\"] = 3\n"+
		"\tfor k in m { print m[k] }\n}\n")
	if got != "1\n2\n3\n" {
		t.Fatalf("map for-in order = %q, want %q", got, "1\n2\n3\n")
	}
}

// TestMapForInSum (M5): iterating and reading each value accumulates over a grown map,
// proving the entry-vector walk stays valid after a rehash.
func TestMapForInSum(t *testing.T) {
	got := runProgramRTBalanced(t, "fn main() {\n\tmut m := {\"k0\": 0}\n"+
		"\tm[\"k1\"] = 1\n\tm[\"k2\"] = 2\n\tm[\"k3\"] = 3\n\tm[\"k4\"] = 4\n\tm[\"k5\"] = 5\n"+
		"\tm[\"k6\"] = 6\n\tm[\"k7\"] = 7\n\tm[\"k8\"] = 8\n\tm[\"k9\"] = 9\n"+
		"\tmut sum := 0\n\tfor k in m { sum = sum + m[k] }\n\tprint sum\n}\n")
	if got != "45\n" {
		t.Fatalf("map for-in sum = %q, want %q", got, "45\n")
	}
}

// TestMapForInFrozenRebind (M5): rebinding a map while iterating it is rejected.
func TestMapForInFrozenRebind(t *testing.T) {
	_, _, diags := Compile("fn main() {\n\tmut m := {1: 2}\n\tfor k in m { m = {9: 9} }\n}\n")
	if len(diags) == 0 || !strings.Contains(diags[0].Msg, "iterating") {
		t.Fatalf("expected a frozen-rebind gate, got %v", diags)
	}
}

// TestMapForInFrozenInsert (M5): inserting into a map while iterating it is rejected
// (a map insert can grow the entry vector and invalidate the walk).
func TestMapForInFrozenInsert(t *testing.T) {
	_, _, diags := Compile("fn main() {\n\tmut m := {1: 2}\n\tfor k in m { m[k+1] = 9 }\n}\n")
	if len(diags) == 0 || !strings.Contains(diags[0].Msg, "iterating") {
		t.Fatalf("expected a frozen-insert gate, got %v", diags)
	}
}

// TestMapForMutGate (M5): `for mut k in m` is rejected (a map key is an immutable
// snapshot with no write-back).
func TestMapForMutGate(t *testing.T) {
	_, _, diags := Compile("fn main() {\n\tm := {1: 2}\n\tfor mut k in m { k = 9 }\n}\n")
	if len(diags) == 0 || !strings.Contains(diags[0].Msg, "immutable") {
		t.Fatalf("expected a for-mut-map gate, got %v", diags)
	}
}

// TestMapOfRefValue: a map[str, Ref[int]] value deep-copies (retain) on a map copy and
// releases each box at both holders' scope exit — a zero balance proves no double-free
// or leak through the value vtable.
func TestMapOfRefValue(t *testing.T) {
	got := runProgramRTBalanced(t, "fn main() {\n\tm := {\"a\": Ref(10), \"b\": Ref(20)}\n\tn := m\n"+
		"\tprint deref(m[\"a\"])\n\tprint deref(n[\"b\"])\n\tprint m.len()\n}\n")
	if got != "10\n20\n2\n" {
		t.Fatalf("map[str, Ref] = %q, want %q", got, "10\n20\n2\n")
	}
}

// TestMapOfListValue: a map[int, list[int]] value deep-copies each inner list on a map
// copy and drops them recursively at scope exit — a zero balance proves no leak.
func TestMapOfListValue(t *testing.T) {
	got := runProgramRTBalanced(t, "fn main() {\n\ta := [1, 2]\n\tb := [3, 4, 5]\n"+
		"\tm := {1: a, 2: b}\n\tn := m\n\tprint m[1].len()\n\tprint n[2].len()\n\tprint m[2][0]\n}\n")
	if got != "2\n3\n3\n" {
		t.Fatalf("map[int, list] = %q, want %q", got, "2\n3\n3\n")
	}
}

// TestMapNestedMapValue: a map[int, map[str,int]] deep-copies its inner maps on a value
// copy and drops them recursively — a zero balance proves the nested value vtable is
// wired both ways (copy + drop).
func TestMapNestedMapValue(t *testing.T) {
	got := runProgramRTBalanced(t, "fn main() {\n\tinner := {\"x\": 1, \"y\": 2}\n"+
		"\tm := {1: inner, 2: {\"z\": 9}}\n\tn := m\n"+
		"\tprint m[1][\"x\"]\n\tprint n[2][\"z\"]\n\tprint m[1].len()\n}\n")
	if got != "1\n9\n2\n" {
		t.Fatalf("map[int, map] = %q, want %q", got, "1\n9\n2\n")
	}
}

// TestMapEqualityGate: `a == b` on two maps is not implemented (map equality). Without a
// sema gate two same-type maps satisfy `comparable` and the backend emits `zg_a == zg_b`
// on the runtime map struct, which cc rejects. It must be a clean diagnostic.
func TestMapEqualityGate(t *testing.T) {
	_, _, diags := Compile("fn main() {\n\ta := {1: 2}\n\tb := {1: 2}\n\tif a == b { print 1 }\n}\n")
	if len(diags) == 0 || !strings.Contains(diags[0].Msg, "map equality") {
		t.Fatalf("expected a map-equality deferral gate, got %v", diags)
	}
}
