package main

// version is the toolchain version, stamped in at link time from the root VERSION file:
// `go build -ldflags "-X main.version=$(VERSION)"`, which src/bootstrap/Makefile passes
// whenever the root Makefile hands it a VERSION. It cannot be a constant — the linker can
// only rewrite a variable — and it cannot be read from a file at run time either, since the
// seed is copied around on its own and would then answer differently depending on where it
// was standing.
//
// The fallback is `0.0.0-dev` rather than the real number, and that asymmetry is the point.
// A plain `go build ./...` — a developer's edit-compile loop, `go test`, an IDE — produces a
// binary nobody released, and it should say so instead of claiming to be whatever VERSION
// happened to hold. It also means `make version-check` fails on a seed that was built
// outside the build system, which is exactly the drift that gate exists to catch.
//
// It lives in its own file so the linker's -X target is a whole file's worth of context
// rather than one line in the middle of the command line's declarations, where the next
// reader would have no reason to suspect it is written from outside.
var version = "0.0.0-dev"
