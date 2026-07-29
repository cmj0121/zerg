SUBDIR := editors src/bootstrap src/runtime

# The self-hosting compiler is `zerg`; the Go seed that builds it is `zerg0`. Both
# resolve `import` themselves, so either builds the compiler from the entry file alone.
#
# The `zerg` that ships is compiled BY zerg, not by the seed: the seed only has to produce
# a good-enough intermediate, which then builds the real one. That keeps the seed off the
# delivery path, and it means every build exercises the self-host path rather than leaving
# it to a separate command nobody runs.
ZERG_ENTRY := src/compiler/zergc.zg
ZERG_STAGE1 := ./bin/.zerg-stage1

# The test-data corpus belongs to the self-hosting compiler: it describes the LANGUAGE,
# which is what `zerg` is growing toward, while the seed is covered by its own unit
# tests. CORPUS_PASS is the set `zerg` compiles and runs correctly today — the gate. The
# rest of test-data/codegen/ is reported but not enforced: each case that starts passing
# is a feature landing, and moves into this list.
# JOBS is how many units the self-hosted compiler builds at once.
JOBS ?= 4

CORPUS_PASS := arithmetic bitwise booleans conc_actor conc_break_release conc_chan_buffer conc_chan_dir conc_close conc_close_kind conc_crash \
	conc_defer_close conc_fanin conc_forin conc_payload_copy conc_select conc_spawn countdown default_params enum_basic enum_guard factorial \
	fib fizzbuzz floats gcd fn_value hello list_basic list_literal list_str method_chain power raise_kind \
	rec_expr rec_tree str_bytes struct_basic struct_nested sumto value_semantics

# A `conc_` case is run more than once. Every other case is a function of its source, so
# one run answers the question; a concurrent one is a function of its source AND of an
# interleaving the scheduler picks fresh each time, and a race that shows up one run in
# twenty would sail through a single attempt. Repetition is what makes it a gate rather
# than a coin toss. They are milliseconds each, so the whole corpus stays quick.
CORPUS_CONC_REPS ?= 10

.PHONY: all clean test run build install uninstall upgrade examples corpus fmt-corpus fixpoint sanitize-conc docs-links lint fmt help $(SUBDIR)

all: build                      # default action
	@[ -f .git/hooks/pre-commit ] || pre-commit install --install-hooks
	@git config commit.template .git-commit-template

clean: $(SUBDIR)                # clean-up environment
	@find . -name '*.sw[po]' -delete
	@rm -rf bin/examples
	@rm -f bin/zerg bin/zerg.c bin/.zerg-stage1 bin/.zerg-stage1.c
	@rm -rf .zerg-cache

test: $(SUBDIR) examples        # run test (unit suites + the examples/ corpus)

run: $(SUBDIR)                  # run in the local environment

build: $(SUBDIR)                # build the toolchain: zerg0, an intermediate, then zerg
	./bin/zerg0 build $(ZERG_ENTRY) -o $(ZERG_STAGE1)
	$(ZERG_STAGE1) build --emit bin -j $(JOBS) -o ./bin/zerg $(ZERG_ENTRY)
	@rm -f $(ZERG_STAGE1) $(ZERG_STAGE1).c

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

# The examples are the corpus a reader meets first, so they are built by the compiler that
# SHIPS — `zerg` — and they are RUN, not merely emitted. Until now the seed compiled them
# to C and nothing ever executed the result, so an example could abort on its first line
# and still pass; and the seed is deliberately narrower than the language, which kept the
# corpus inside a subset a reader is not writing in. Note `--emit bin`: `zerg build` alone
# stops at an object file, so a target that omits it links nothing and tests nothing.
#
# `$(MAKE) build` is the dependency, as in `corpus`, `lint` and `fmt-corpus` — the toolchain
# it produces is bin/zerg AND bin/zerg0, so the two seed-built demos below need no second one.
#
# Two demos stay on the seed, because `zerg` cannot build them yet: examples/modules needs a
# generic function definition ("NotImplemented"), and examples/1g/init emits a module constant
# its init never declares (the cc fails on it). Both still build and run here, by zerg0 — the
# exception is which compiler proves them, not whether they are proven — and they move up to
# `zerg` with the numbered corpus as soon as those two gaps close.
examples:                       # build every example with zerg itself, and run it
	$(MAKE) build
	@fail=0; n=0; mkdir -p bin/examples; \
	for src in examples/[0-9][0-9]_*.zg examples/1g/reexport/main.zg; do \
		out=bin/examples/$$(echo $$src | sed 's|^examples/||; s|/|_|g; s|\.zg$$||'); \
		./bin/zerg build $$src --emit bin -o $$out >/dev/null 2>&1 || { echo "BUILD  $$src"; fail=1; continue; }; \
		$$out >/dev/null 2>&1 || { echo "RUN    $$src"; fail=1; continue; }; \
		n=$$((n+1)); \
	done; \
	for src in examples/modules/main.zg examples/1g/init/main.zg; do \
		out=bin/examples/$$(echo $$src | sed 's|^examples/||; s|/|_|g; s|\.zg$$||'); \
		./bin/zerg0 build $$src -o $$out >/dev/null 2>&1 || { echo "BUILD  $$src (seed)"; fail=1; continue; }; \
		$$out >/dev/null 2>&1 || { echo "RUN    $$src (seed)"; fail=1; continue; }; \
		n=$$((n+1)); \
	done; \
	[ $$fail -eq 0 ] || { echo "examples: an example no longer builds, or no longer runs"; exit 1; }; \
	echo "examples: $$n examples built and run"

fmt-corpus:                     # every test-data/fmt case must already be canonical
	$(MAKE) build
	@[ -d test-data/fmt ] || { echo "test-data submodule not initialized (git submodule update --init)"; exit 1; }
	@fail=0; tmp=$$(mktemp -d); \
	for src in test-data/fmt/*.zg; do \
		cp $$src $$tmp/case.zg; \
		./bin/zerg fmt $$tmp/case.zg >/dev/null; \
		cmp -s $$src $$tmp/case.zg || { echo "FMT    $$(basename $$src)"; fail=1; }; \
	done; \
	rm -rf $$tmp; \
	[ $$fail -eq 0 ] || { echo "fmt-corpus: a case is not in canonical form — the formatter changed it"; exit 1; }; \
	echo "fmt-corpus: $$(ls test-data/fmt/*.zg | wc -l | tr -d ' ') cases are fmt's fixpoint"

corpus:                         # run zerg against the test-data corpus it now owns
	$(MAKE) build
	@[ -d test-data/codegen ] || { echo "test-data submodule not initialized (git submodule update --init)"; exit 1; }
	@fail=0; \
	for name in $(CORPUS_PASS); do \
		src=test-data/codegen/$$name.zg; \
		./bin/zerg build --emit bin -o ./bin/corpus-case $$src >/dev/null 2>&1 || { echo "BUILD  $$name"; fail=1; continue; }; \
		want=$$(cat test-data/codegen/$$name.out); \
		reps=1; case $$name in conc_*) reps=$(CORPUS_CONC_REPS);; esac; \
		n=0; \
		while [ $$n -lt $$reps ]; do \
			got=$$(./bin/corpus-case 2>/dev/null); \
			[ "$$got" = "$$want" ] || { echo "OUTPUT $$name (run $$n)"; fail=1; break; }; \
			n=$$((n+1)); \
		done; \
	done; \
	rm -f ./bin/corpus-case ./bin/corpus-case.c; \
	[ $$fail -eq 0 ] || { echo "corpus: a case that used to pass regressed"; exit 1; }; \
	echo "corpus: $(words $(CORPUS_PASS))/$$(ls test-data/codegen/*.zg | wc -l | tr -d ' ') cases pass (the rest await features zerg does not have yet)"

# The compiler compiles itself, so the one program big enough to find a rare emitter path
# is the compiler — and until now nothing compared the two stages `build` already makes.
#
# This is NOT in `make test`, for the same reason `corpus` is not. `test` is the fan-out
# over the subdirectories, and each of those suites answers for the code beside it: it
# stays runnable while the whole-program build is in pieces, which is exactly when a unit
# suite is the thing worth having. This target answers for the toolchain as a whole and
# builds the compiler twice from a bare tree, so a compiler mid-rewrite fails it for
# reasons the unit suites have already said more precisely. It takes about five seconds on
# a warm tree and it runs on every pull request, which is where a whole-toolchain gate
# belongs — the argument for it is cheapness plus reach, not habit.
fixpoint:                       # prove the compiler still emits the same C for itself
	./scripts/selfhost-fixpoint.sh

# `corpus` checks what these cases PRINT, which is blind to a coroutine stack freed under a
# live fiber or a channel the scheduler forgot on the way out — the program answers
# correctly and leaves the damage behind. Deliberate rather than in `test`, again: it
# rebuilds every case against the sanitizers, and on Linux it is the only leak gate the
# concurrency path has (LeakSanitizer does not exist on macOS).
sanitize-conc:                  # run the concurrency corpus under address/UB/leak sanitizers
	$(MAKE) build
	./scripts/sanitize-conc.sh

# Every other gate here asks what the toolchain BUILDS. This one asks what it turns away,
# and the property it pins is not that a bad program fails — it always did — but WHO says
# so. A program the compiler emits anyway reaches cc, which reports a real error against
# generated C in .zerg-cache, at a line the programmer cannot open. That regresses silently,
# because the case still "fails". Not in `test` for the same reason `corpus` is not: it
# needs the whole toolchain, both compilers, built.
refuse:                         # every program that must be turned away, is — by the compiler
	$(MAKE) build
	./scripts/refuse-check.sh

docs-links:                     # every docs path the repo cites must resolve
	@fail=0; \
	for p in $$(git grep -hoE 'docs/[A-Za-z0-9_./-]+\.md' -- . ':!docs' | sort -u); do \
		[ -f "$$p" ] || { echo "CITED  $$p"; fail=1; }; \
	done; \
	for f in $$(git ls-files 'docs/**/*.md' 'docs/*.md'); do \
		d=$$(dirname $$f); \
		for l in $$(sed -E 's/\]\(/\n\]\(/g' $$f \
			| sed -nE 's/^\]\((\.\.\/[^)#:]*|[^)#:]*\.md)(#[^)]*)?\).*/\1/p'); do \
			[ -e "$$d/$$l" ] || { echo "LINK   $$f -> $$l"; fail=1; }; \
		done; \
	done; \
	[ $$fail -eq 0 ] || { echo "docs-links: a cited path does not exist"; exit 1; }; \
	echo "docs-links: every cited docs path resolves"

lint:                           # lint the compiler and stdlib with zerg itself
	$(MAKE) build
	./bin/zerg lint $(ZERG_ENTRY)

fmt:                            # rewrite the compiler and stdlib in canonical style
	$(MAKE) build
	@for f in $(ZERG_ENTRY) src/compiler/zerg/*.zg src/stdlib/*.zg; do \
		./bin/zerg fmt $$f || { echo "fmt: failed on $$f"; exit 1; }; \
	done
