package sema

import "testing"

// TestDynValueGenericRejected rejects a '#[dyn]' that would erase a value (const)
// generic, which has no witness-table slot (DESIGN-1c §6.3).
func TestDynValueGenericRejected(t *testing.T) {
	wantErr(t, "#[dyn]\nfn f[N: int](x: int) -> int {\n  return x\n}", "cannot erase value generic")
}

// TestDynTypeGenericAccepted accepts a '#[dyn]' over an ordinary type parameter —
// only value erasure is forbidden.
func TestDynTypeGenericAccepted(t *testing.T) {
	wantOK(t, "spec Show {\n  fn value() -> int\n}\n"+
		"#[dyn]\nfn f[T: Show](x: T) -> int {\n  return x.value()\n}")
}

// TestDynFlagSet records that the '#[dyn]' decorator sets the signature's Dyn flag,
// the switch the backend reads to emit a shared witness body.
func TestDynFlagSet(t *testing.T) {
	info, msgs := checkInfo(t, "spec Show {\n  fn value() -> int\n}\n"+
		"#[dyn]\nfn f[T: Show](x: T) -> int {\n  return x.value()\n}")
	if len(msgs) != 0 {
		t.Fatalf("unexpected errors: %v", msgs)
	}
	if sig := info.Funcs["f"]; sig == nil || !sig.Dyn {
		t.Fatalf("f should carry Dyn; got %+v", sig)
	}
}
