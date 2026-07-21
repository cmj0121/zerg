# The bootstrap Zerg compiler (Go host).
#
# Builds the `zergc` driver into the repo-root bin/. Invoked directly
# (`make -C src/bootstrap build`) or through the root Makefile's SUBDIR fan-out.
BIN     := $(CURDIR)/../../bin
BINARY  := $(BIN)/zergc
GO      := go
PKG     := ./cmd/zergc

.PHONY: all build test run clean install uninstall help

all: build			# default action: build the compiler

build:				# build the zergc bootstrap compiler into bin/
	@mkdir -p "$(BIN)"
	$(GO) build -o "$(BINARY)" $(PKG)

test:				# run the Go test suite
	$(GO) test ./...

run: build			# build then run zergc (pass args with ARGS="…")
	@"$(BINARY)" $(ARGS)

clean:				# remove build artifacts
	@rm -f "$(BINARY)"
	@$(GO) clean

install:			# (no-op) zergc is run from bin/, not installed

uninstall:			# (no-op)

help:				# show this message
	@printf "Usage: make [OPTION]\n\n"
	@perl -nle 'print $$& if m{^[\w-]+:.*?#.*$$}' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?#"} {printf "    %-18s %s\n", $$1, $$2}'
