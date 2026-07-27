SUBDIR := editors src/bootstrap

# The self-hosting compiler is `zerg`; the Go seed that builds it is `zerg0`. Both
# resolve `import` themselves, so either builds the compiler from the entry file alone.
ZERG_ENTRY := src/compiler/zergc.zg

# The test-data corpus belongs to the self-hosting compiler: it describes the LANGUAGE,
# which is what `zerg` is growing toward, while the seed is covered by its own unit
# tests. CORPUS_PASS is the set `zerg` compiles and runs correctly today — the gate. The
# rest of test-data/codegen/ is reported but not enforced: each case that starts passing
# is a feature landing, and moves into this list.
CORPUS_PASS := arithmetic bitwise booleans factorial fib fizzbuzz floats gcd hello power sumto

.PHONY: all clean test run build install uninstall upgrade examples corpus selfhost help $(SUBDIR)

all: build                      # default action
	@[ -f .git/hooks/pre-commit ] || pre-commit install --install-hooks
	@git config commit.template .git-commit-template

clean: $(SUBDIR)                # clean-up environment
	@find . -name '*.sw[po]' -delete
	@rm -rf bin/examples
	@rm -f bin/zerg bin/zerg-stage2 bin/zerg.c bin/zerg-stage2.c

test: $(SUBDIR) examples        # run test (unit suites + the examples/ corpus)

run: $(SUBDIR)                  # run in the local environment

build: $(SUBDIR)                # build the toolchain: the zerg0 seed, then zerg itself
	./bin/zerg0 build $(ZERG_ENTRY) -o ./bin/zerg

install: $(SUBDIR)              # install editor integrations (nvim syntax) locally

uninstall: $(SUBDIR)            # remove editor integrations installed by `make install`

upgrade:			            # upgrade all the necessary packages
	pre-commit autoupdate

help:				            # show this message
	@printf "Usage: make [OPTION]\n"
	@printf "\n"
	@perl -nle 'print $$& if m{^[\w-]+:.*?#.*$$}' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?#"} {printf "    %-18s %s\n", $$1, $$2}'

$(SUBDIR):
	$(MAKE) -C $@ $(MAKECMDGOALS)

examples:                       # build the examples corpus with the seed
	$(MAKE) -C src/bootstrap build
	@for src in examples/[0-9][0-9]_*.zg examples/modules/main.zg examples/1g/init/main.zg examples/1g/reexport/main.zg; do \
		echo "Building $$src..."; \
		./bin/zerg0 build $$src --emit c     >/dev/null || exit 1; \
	done

corpus:                         # run zerg against the test-data corpus it now owns
	$(MAKE) build
	@[ -d test-data/codegen ] || { echo "test-data submodule not initialized (git submodule update --init)"; exit 1; }
	@fail=0; \
	for name in $(CORPUS_PASS); do \
		src=test-data/codegen/$$name.zg; \
		./bin/zerg build -o ./bin/corpus-case $$src >/dev/null 2>&1 || { echo "BUILD  $$name"; fail=1; continue; }; \
		got=$$(./bin/corpus-case 2>/dev/null); \
		[ "$$got" = "$$(cat test-data/codegen/$$name.out)" ] || { echo "OUTPUT $$name"; fail=1; }; \
	done; \
	rm -f ./bin/corpus-case ./bin/corpus-case.c; \
	[ $$fail -eq 0 ] || { echo "corpus: a case that used to pass regressed"; exit 1; }; \
	echo "corpus: $(words $(CORPUS_PASS))/$$(ls test-data/codegen/*.zg | wc -l | tr -d ' ') cases pass (the rest await features zerg does not have yet)"

selfhost:                       # have the built zerg rebuild itself, closing the chain
	$(MAKE) build
	./bin/zerg build -o ./bin/zerg-stage2 $(ZERG_ENTRY)
	@echo "self-host chain closed: bin/zerg-stage2 built by bin/zerg"
