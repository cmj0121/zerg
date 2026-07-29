# The Zerg runtime (C sources, embedded into the compilers by embed.go).
#
# There is nothing to build here — the C in csrc/ is compiled by whichever program
# emits it, not ahead of time. What this Makefile exists for is `test`: the suite
# in runtime_test.go compiles those sources with the host cc and runs them, which
# is the only place the runtime is exercised as C rather than as an implementation
# detail of a Zerg program.
#
# Until this file existed the root Makefile's SUBDIR fan-out could not reach the
# directory, so `make test` never ran a single one of those tests. A map bug and a
# scheduler race both lived here undisturbed because of it.
GO      := go
PKG     := ./...

# A racing scheduler fails by hanging, not by returning a wrong answer, so the suite
# needs a deadline shorter than go's ten-minute default — a stuck run should say so
# while someone is still watching, and it takes well under a minute when it is healthy.
TIMEOUT ?= 120s

.PHONY: all build test run clean install uninstall help

all: build			# default action

build:				# (no-op) the runtime C is compiled by its consumers, not here
	@$(GO) build $(PKG)

test:				# run the runtime suite (compiles csrc/ with the host cc)
	$(GO) test -timeout $(TIMEOUT) $(PKG)

run:				# (no-op) the runtime is a library, it has no entry point

clean:				# remove build artifacts
	@$(GO) clean

install:			# (no-op) the runtime ships inside the compiler binary

uninstall:			# (no-op)

help:				# show this message
	@printf "Usage: make [OPTION]\n\n"
	@perl -nle 'print $$& if m{^[\w-]+:.*?#.*$$}' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?#"} {printf "    %-18s %s\n", $$1, $$2}'
