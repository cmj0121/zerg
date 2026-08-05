# The Zerg bootstrap seed (Go host).
#
# Builds the `zerg0` seed into the repo-root bin/. Invoked directly
# (`make -C src/bootstrap build`) or through the root Makefile's SUBDIR fan-out.
# The name `zerg` belongs to the self-hosting compiler this seed builds.
BIN     := $(CURDIR)/../../bin
BINARY  := $(BIN)/zerg0
GO      := go
PKG     := ./cmd/zerg

# The suite BUILDS PROGRAMS — it drives the seed over the examples and the corpus — so it
# is minutes rather than seconds, and Go's default is a 10-minute timeout with no way to
# tell the two apart in the output: a hit reads `FAIL <pkg> 600.061s`, the same shape an
# assertion failure has. It took a CI run on an emulated platform, where everything is
# several times slower, to say so. The runtime's Makefile has carried its own for the same
# reason.
TIMEOUT ?= 30m

# VERSION is stamped into the binary at link time, and it arrives from OUTSIDE — the root
# Makefile reads it from the repo's VERSION file and hands it down, so the number lives in
# one place rather than being restated here.
#
# It is deliberately allowed to be unset. `go build ./...`, `go test`, and an editor running
# the compiler for its own reasons all produce a working seed without going through the root
# Makefile, and none of them should have to. Those binaries keep main.go's `0.0.0-dev`, which
# says what they are; passing `-X main.version=` with nothing after it would instead give
# them an EMPTY version, which is the one answer that looks like a bug in the flag rather
# than like a development build.
LDFLAGS := $(if $(VERSION),-ldflags "-X main.version=$(VERSION)")

.PHONY: all build test run clean install uninstall help

all: build			# default action: build the toolchain driver

build:				# build the zerg toolchain driver into bin/ (VERSION=x.y.z stamps --version)
	@mkdir -p "$(BIN)"
	$(GO) build $(LDFLAGS) -o "$(BINARY)" $(PKG)

test:				# run the Go test suite
	$(GO) test -timeout $(TIMEOUT) ./...

run: build			# build then run zerg (pass args with ARGS="…", e.g. ARGS="build hello.zg")
	@"$(BINARY)" $(ARGS)

clean:				# remove build artifacts
	@rm -f "$(BINARY)"
	@$(GO) clean

install:			# (no-op) zerg is run from bin/, not installed

uninstall:			# (no-op)

help:				# show this message
	@printf "Usage: make [OPTION]\n\n"
	@perl -nle 'print $$& if m{^[\w-]+:.*?#.*$$}' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?#"} {printf "    %-18s %s\n", $$1, $$2}'
