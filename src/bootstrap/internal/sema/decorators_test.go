package sema

import "testing"

// Decorators are a fixed, compiler-owned set (GRAMMAR group 7): an implemented one is
// accepted, an unknown one is a clean error rather than a silent no-op, and a
// recognized-but-unimplemented one is rejected with a distinct "not yet supported".

// TestKnownDecoratorsAccepted checks the implemented decorators (derive/test)
// still pass the validation pass untouched.
func TestKnownDecoratorsAccepted(t *testing.T) {
	wantOK(t, "#[derive(Eq)]\nstruct P {\n\tx: int\n}")
	wantOK(t, "#[test]\nfn t() {\n}")
}

// TestUnknownDecoratorRejected covers the silent-no-op fix: a typo or invented
// attribute is rejected loudly instead of compiling clean with no effect.
func TestUnknownDecoratorRejected(t *testing.T) {
	wantErr(t, "#[bogus]\nstruct P {\n\tx: int\n}", `unknown decorator "bogus"`)
	wantErr(t, "#[deriv(Eq)]\nstruct P {\n\tx: int\n}", `unknown decorator "deriv"`)
}

// TestSealedNotYetSupported covers Fix 3: `#[sealed]` no longer silently no-ops; it
// is a clean "not yet supported" diagnostic.
func TestSealedNotYetSupported(t *testing.T) {
	wantErr(t, "#[sealed]\nstruct P {\n\tx: int\n}", "#[sealed] is not yet supported")
}

// TestReservedLayoutDecoratorsNotYetSupported covers the reserved layout family:
// recognized (so a typo is distinguishable), but rejected until they are built.
func TestReservedLayoutDecoratorsNotYetSupported(t *testing.T) {
	wantErr(t, "#[align(16)]\nstruct P {\n\tx: int\n}", "#[align] is not yet supported")
	wantErr(t, "#[packed]\nstruct P {\n\tx: int\n}", "#[packed] is not yet supported")
	wantErr(t, "#[repr]\nstruct P {\n\tx: int\n}", "#[repr] is not yet supported")
}

// TestDynNotYetSupported holds the seed's boundary: witness-table dispatch left with
// the rest of the erased-generic machinery, so '#[dyn]' is reserved rather than
// silently monomorphized — the author asked for one erased body, not N.
func TestDynNotYetSupported(t *testing.T) {
	wantErr(t, "#[dyn]\nfn id[T](x: T) -> T {\n\treturn x\n}", "#[dyn] is not yet supported")
}
