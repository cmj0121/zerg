# The Zerg toolchain driver (Go host).
#
# Builds the `zerg` driver into the repo-root bin/. Invoked directly
# (`make -C src/bootstrap build`) or through the root Makefile's SUBDIR fan-out.
BIN     := $(CURDIR)/../../bin
BINARY  := $(BIN)/zerg
GO      := go
PKG     := ./cmd/zerg

.PHONY: all build test run clean install uninstall help

all: build			# default action: build the toolchain driver

build:				# build the zerg toolchain driver into bin/
	@mkdir -p "$(BIN)"
	$(GO) build -o "$(BINARY)" $(PKG)

test:				# run the Go test suite
	$(GO) test ./...

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
