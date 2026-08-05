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

# VERSION is the toolchain's version and the repo's VERSION file is the only place it is
# written. Everything downstream is derived: the seed takes it through -ldflags, the shipping
# compiler takes it through a generated src/compiler/zerg/version.zg, and `make version-check`
# holds all three to each other.
#
# It is a file rather than a variable here because a version is also read by things that are
# not make — a release script, a packager, a person — and `cat VERSION` is an interface all of
# them already have. That is also why it holds a bare number and no comments.
#
# `tr`, not `cat`, and it is the same `tr` gen-version.sh and version-check.sh read the file
# with. One tolerance shared by three readers, because two tolerances is a file that passes
# the gate and breaks the build: `cat` keeps a leading space, which reaches the fan-out below
# as `VERSION=  0.1.0` — an EMPTY version for the sub-make, silently falling the seed back to
# 0.0.0-dev, and `0.1.0` left over as a goal make has no rule for. A leading space is exactly
# what pre-commit's trailing-whitespace hook does not look at.
VERSION := $(shell tr -d ' \t\n\r' <VERSION)

# The test-data corpus belongs to the self-hosting compiler: it describes the LANGUAGE,
# which is what `zerg` is growing toward, while the seed is covered by its own unit tests.
#
# The gate is EVERY case except the ones named below, and it is that way round on purpose.
# An allowlist makes "not gated" the default for a new case: adding one and forgetting to
# register it leaves it silently unenforced, which is the one failure a test corpus must
# not have. Naming what CANNOT pass instead makes a new case gated the moment it exists,
# and gives the list an end state — it shrinks toward empty as the features land, rather
# than growing forever.
#
# JOBS is how many units the self-hosted compiler builds at once.
JOBS ?= 4

# Cases awaiting a feature `zerg` does not have. Delete a name when its feature lands;
# that deletion IS the gate for the feature.
CORPUS_SKIP := \
	derive_enum derive_ord \
	dyn_witness spec_bound \
	gen_enum gen_enum2 gen_identity gen_struct

CORPUS_PASS := $(filter-out $(CORPUS_SKIP),$(basename $(notdir $(wildcard test-data/codegen/*.zg))))

# A FLOOR under how many cases the gate has to have run, of the kind fmt-tokens and
# reject-fuzz carry. The `[ -d test-data/codegen ]` guard in the recipe catches an ABSENT
# submodule and nothing else; a shallow, partial or wrong-commit checkout leaves the
# directory THERE with a handful of cases in it, the wildcard above shrinks to match, and the
# gate reports `1/1 cases pass` and exits 0 — success for having measured almost nothing,
# which is the one failure that still looks like a corpus.
#
# 60 against the 80 that pass today. The gap is room for cases to move into CORPUS_SKIP while
# they wait for a feature, so that adding a case for something `zerg` cannot build yet is not
# also a chore here; it is nowhere near the two or three a broken checkout leaves behind.
CORPUS_MIN ?= 60

# A `conc_` case is run more than once. Every other case is a function of its source, so
# one run answers the question; a concurrent one is a function of its source AND of an
# interleaving the scheduler picks fresh each time, and a race that shows up one run in
# twenty would sail through a single attempt. Repetition is what makes it a gate rather
# than a coin toss. They are milliseconds each, so the whole corpus stays quick.
CORPUS_CONC_REPS ?= 10

.PHONY: all clean test run build install uninstall upgrade examples corpus fmt-corpus fmt-self fixpoint sanitize-conc refuse reject reject-fuzz fmt-tokens linux-ci docs-links lint lint-check version-check fmt help $(SUBDIR)

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
	@# BEFORE the seed reads a line of the compiler: version.zg is one of the compiler's own
	@# sources, so regenerating it afterwards would put the new number into the NEXT build and
	@# leave this one quietly claiming the old one.
	@./scripts/gen-version.sh
	./bin/zerg0 build $(ZERG_ENTRY) -o $(ZERG_STAGE1)
	$(ZERG_STAGE1) build --emit bin -j $(JOBS) -o ./bin/zerg $(ZERG_ENTRY)
	@rm -f $(ZERG_STAGE1) $(ZERG_STAGE1).c

# PREFIX is where a release lands. The layout is the one `zerg` looks for when it is not
# running from its own source tree: the binary at <prefix>/bin, and the two source trees a
# build needs — the runtime C and the standard library — at <prefix>/lib/zerg. The compiler
# finds them from its OWN path (zrt_exe_path), so nothing has to be exported and no
# directory has to be the current one.
PREFIX ?= /usr/local

# `build` runs as a recipe line, not a prerequisite: as a prerequisite it would race the
# submodule work under `make -j`, and what is installed must be the binary this run built.
install: $(SUBDIR)              # install the toolchain into $(PREFIX), and the editor integrations
	$(MAKE) build
	@install -d "$(PREFIX)/bin" "$(PREFIX)/lib/zerg/csrc" "$(PREFIX)/lib/zerg/stdlib"
	install -m 0755 bin/zerg "$(PREFIX)/bin/zerg"
	@# zrt_test.* is the C suite's harness and belongs to no program; the others are the
	@# per-platform slots the driver picks between, and it needs all of them present.
	@cp $(filter-out %/zrt_test.c,$(wildcard src/runtime/csrc/*.c)) $(filter-out %/zrt_test.h,$(wildcard src/runtime/csrc/*.h)) src/runtime/csrc/*.S "$(PREFIX)/lib/zerg/csrc/"
	@cp src/stdlib/*.zg "$(PREFIX)/lib/zerg/stdlib/"
	@echo "installed: $(PREFIX)/bin/zerg with its runtime and stdlib under $(PREFIX)/lib/zerg"

uninstall: $(SUBDIR)            # remove what `make install` put in $(PREFIX)
	rm -f "$(PREFIX)/bin/zerg"
	rm -rf "$(PREFIX)/lib/zerg"

upgrade:			            # upgrade all the necessary packages
	pre-commit autoupdate

help:				            # show this message
	@printf "Usage: make [OPTION]\n"
	@printf "\n"
	@perl -nle 'print $$& if m{^[\w-]+:.*?#.*$$}' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?#"} {printf "    %-18s %s\n", $$1, $$2}'

# VERSION rides along on the fan-out because the seed's Makefile stamps it into the binary,
# and a sub-make inherits nothing that is not handed to it. It is passed to every
# subdirectory rather than only to src/bootstrap: the ones with no use for it ignore it, and
# a list of which subdirectory cares is a thing that goes stale the day another one starts to.
$(SUBDIR):
	$(MAKE) -C $@ $(MAKECMDGOALS) VERSION=$(VERSION)

# The examples are the corpus a reader meets first, so they are built by the compiler that
# SHIPS — `zerg` — and they are RUN, not merely emitted. Until now the seed compiled them
# to C and nothing ever executed the result, so an example could abort on its first line
# and still pass; and the seed is deliberately narrower than the language, which kept the
# corpus inside a subset a reader is not writing in. Note `--emit bin`: `zerg build` alone
# stops at an object file, so a target that omits it links nothing and tests nothing.
#
# `$(MAKE) build` is the dependency, as in `corpus`, `lint` and `fmt-corpus`.
#
# ONE compiler. Two demos used to be built by `zerg0` here, because `zerg` could not build
# them — which made the gate answer a narrower question than its name: two shipped examples
# were provable only by the compiler the README says a reader never meets, and the
# `zerg build …` line in each one's own header answered `NotImplemented` when typed. The
# forms they needed were a named argument and a module-level inferred binding; both examples
# are now written in the language this compiler has, and the seed is off this target.
#
# The list is a GLOB, not names. `examples/1g/private` was absent from the hand-written list
# and so was built by nothing at all — a negative example asserting a refusal this compiler
# does not make, sitting in the tree with no gate to notice. A glob makes a new example
# directory gated the moment it exists, which is the same reason `corpus` names what CANNOT
# pass instead of what can.
#
# EXAMPLE_MIN is the floor a shrinking glob runs into. The guard against a mistyped pattern
# is not that the loop fails, it is that the loop has nothing to iterate and says so happily.
EXAMPLE_MIN ?= 20

# The examples that must be REFUSED, and the sentence they must be refused with. An example
# is a claim about the language, and a NEGATIVE one — this program is not Zerg — is a claim
# a build-and-run loop cannot check: it can only report that the build failed, which is what
# a typo does too. So the refusal is held to what it SAYS and to carrying a place, which is
# the same standard `make reject` holds its own cases to. That gate takes single files from a
# heredoc and this example is a module and an entry, which is why it is checked here.
EXAMPLE_REFUSED ?= examples/1g/private/main.zg
EXAMPLE_REFUSED_SAYS ?= is not a public member of module

examples:                       # build every example with zerg itself, and run it
	$(MAKE) build
	@fail=0; n=0; mkdir -p bin/examples; \
	for src in examples/[0-9][0-9]_*.zg examples/*/main.zg examples/1g/*/main.zg; do \
		case " $(EXAMPLE_REFUSED) " in *" $$src "*) continue;; esac; \
		out=bin/examples/$$(echo $$src | sed 's|^examples/||; s|/|_|g; s|\.zg$$||'); \
		./bin/zerg build $$src --emit bin -o $$out >/dev/null 2>&1 || { echo "BUILD  $$src"; fail=1; continue; }; \
		$$out >/dev/null 2>&1 || { echo "RUN    $$src"; fail=1; continue; }; \
		n=$$((n+1)); \
	done; \
	for src in $(EXAMPLE_REFUSED); do \
		say=$$(./bin/zerg build $$src --emit bin -o bin/examples/refused 2>&1); \
		if [ $$? -eq 0 ]; then echo "BUILT  $$src (it must be refused)"; fail=1; continue; fi; \
		echo "$$say" | grep -q "$(EXAMPLE_REFUSED_SAYS)" || { echo "SAID   $$src: $$say"; fail=1; continue; }; \
		echo "$$say" | grep -q "$$(basename $$src):" || { echo "PLACE  $$src said no file:line:col"; fail=1; continue; }; \
		n=$$((n+1)); \
	done; \
	[ $$fail -eq 0 ] || { echo "examples: an example no longer builds, or no longer runs"; exit 1; }; \
	[ $$n -ge $(EXAMPLE_MIN) ] || { echo "examples: only $$n were built, and the floor is $(EXAMPLE_MIN)"; exit 1; }; \
	echo "examples: $$n examples built and run"

# Where the fmt cases live, and a FLOOR under how many of them were checked. Same shape as
# `corpus`: the directory guard below catches an absent submodule, and a checkout that has
# test-data/fmt with two cases in it satisfies every assertion here — `zerg fmt --check` is
# happy with an argument list of two, and the gate says `2 cases are fmt's fixpoint` and
# exits 0.
#
# 24 against the 31 there are today, the same judgement fmt-tokens made with 100 against 137:
# far enough below that retiring or renaming a case is not a chore, far enough above that a
# corpus which lost most of itself cannot pass.
#
# The directory is a variable for the reason reject-fuzz's $CORPUS is one — a floor nobody
# has watched fire is a floor nobody knows works, and watching this one fire means pointing
# the gate at a directory holding fewer cases than test-data/fmt does.
FMT_CORPUS ?= test-data/fmt
FMT_CORPUS_MIN ?= 24

# `zerg fmt --check` is the tool answering its own question. This target used to copy each
# case to a temp file, format the copy and `cmp` — the Makefile reimplementing in shell
# something the formatter knew and had no way to say.
fmt-corpus:                     # every test-data/fmt case must already be canonical
	$(MAKE) build
	@[ -d $(FMT_CORPUS) ] || { echo "test-data submodule not initialized (git submodule update --init)"; exit 1; }
	@./bin/zerg fmt --check $(FMT_CORPUS)/*.zg || { echo "fmt-corpus: a case is not in canonical form"; exit 1; }
	@n=$$(ls $(FMT_CORPUS)/*.zg | wc -l | tr -d ' '); \
	[ $$n -ge $(FMT_CORPUS_MIN) ] || { echo "fmt-corpus: only $$n cases were checked, and the floor is $(FMT_CORPUS_MIN)"; exit 1; }; \
	echo "fmt-corpus: $$n cases are fmt's fixpoint"

# The compiler's OWN sources, which no gate covered. That is how three fresh deviations
# landed in one branch: `zerg fmt` would have silently reverted four lines of it. This
# needs no submodule, so it runs everywhere.
fmt-self:                       # the compiler and the stdlib are canonical too
	$(MAKE) build
	@./bin/zerg fmt --check src/compiler/*.zg src/compiler/zerg/*.zg src/stdlib/*.zg \
		|| { echo "fmt-self: a compiler or stdlib source is not in canonical form"; exit 1; }
	@echo "fmt-self: $$(ls src/compiler/*.zg src/compiler/zerg/*.zg src/stdlib/*.zg | wc -l | tr -d ' ') sources are fmt's fixpoint"

# A target of its own, because CI does not run fmt-corpus — hanging this off it meant the
# gate written to catch `fn main( {` -> `fn main({` ran only from a hand-typed make.
fmt-tokens:                     # formatting changes spacing, never the token stream
	$(MAKE) build
	@[ -d test-data/fmt ] || { echo "test-data submodule not initialized (git submodule update --init)"; exit 1; }
	@./scripts/fmt-tokens.sh

# A case's EXIT STATUS is checked as well as its stdout, against `<name>.rc` where there is
# one and against 0 where there is not. Only stdout was compared before, so a program that
# stopped aborting — or started — passed unchanged: the abort contract's third step (exit 1)
# was a thing the corpus could not see, which is a poor property for a corpus that holds
# cases whose whole subject is aborting. Its FIRST step, the message on stderr, still is:
# that needs a `<name>.err` beside the other two and is not built.
corpus:                         # run zerg against the test-data corpus it now owns
	$(MAKE) build
	@[ -d test-data/codegen ] || { echo "test-data submodule not initialized (git submodule update --init)"; exit 1; }
	@fail=0; ran=0; \
	for name in $(CORPUS_PASS); do \
		src=test-data/codegen/$$name.zg; \
		./bin/zerg build --emit bin -o ./bin/corpus-case $$src >/dev/null 2>&1 || { echo "BUILD  $$name"; fail=1; continue; }; \
		want=$$(cat test-data/codegen/$$name.out); \
		want_rc=0; [ -f test-data/codegen/$$name.rc ] && want_rc=$$(cat test-data/codegen/$$name.rc); \
		reps=1; case $$name in conc_*) reps=$(CORPUS_CONC_REPS);; esac; \
		n=0; \
		while [ $$n -lt $$reps ]; do \
			got=$$(./bin/corpus-case 2>/dev/null); rc=$$?; \
			[ "$$got" = "$$want" ] || { echo "OUTPUT $$name (run $$n)"; fail=1; break; }; \
			[ "$$rc" = "$$want_rc" ] || { echo "STATUS $$name (run $$n): want $$want_rc, got $$rc"; fail=1; break; }; \
			n=$$((n+1)); \
		done; \
		if [ $$n -eq $$reps ]; then ran=$$((ran+1)); fi; \
	done; \
	rm -f ./bin/corpus-case ./bin/corpus-case.c; \
	[ $$fail -eq 0 ] || { echo "corpus: a case that used to pass regressed"; exit 1; }; \
	[ $$ran -ge $(CORPUS_MIN) ] || { echo "corpus: only $$ran cases were run, and the floor is $(CORPUS_MIN)"; exit 1; }; \
	echo "corpus: $$ran/$$(ls test-data/codegen/*.zg | wc -l | tr -d ' ') cases pass (the rest await features zerg does not have yet)"

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

# `refuse` asks about forms this compiler has not built; this one asks about programs that
# are not Zerg, and the difference is a lifetime. A refusal disappears when the feature
# lands. A rejection is the LANGUAGE's answer and never does — so the two lists stay
# apart, or neither reads as a contract.
#
# It exists because every other gate here only ever compiles WELL-FORMED programs, and the
# whole class of ill-formed ones was therefore invisible: the self-hosting compiler shipped
# without the seed's semantic analysis, and nothing in ten targets could see it.
reject:                         # every program that is not Zerg is rejected — by the compiler
	$(MAKE) build
	./scripts/reject-check.sh

# `reject` makes the seed the oracle for programs that must be turned away. This asks the
# same question about programs that must be ACCEPTED: two compilers of one language may not
# disagree about what a valid program prints.
#
# Nothing else could see that class. `build` needs only that the compiler compile itself,
# and its own source does not write every form; `examples` gates on the exit status, which
# a wrong number leaves at 0; the corpus is compiled by `zerg` alone. `int("42")` printed a
# POINTER for a whole release under a full board of green gates.
oracle:                         # the seed and the shipping compiler agree about a valid program
	$(MAKE) build
	@./scripts/oracle-check.sh examples/[0-9][0-9]_*.zg $$(ls test-data/codegen/*.zg 2>/dev/null)

# Two Linux-only defects reached main this month — a preprocessor `#if` no GCC before 14
# can parse, and a `MAP_ANONYMOUS` glibc hides under `-std=c11` — and neither was visible
# from macOS. The docker flow that found them was driven by hand every time; this is it,
# written down. It is not part of any other target: it needs docker, and a developer
# without it should still be able to run everything else.
linux-ci:                       # run the Linux gates in a container, as CI does
	@docker info >/dev/null 2>&1 || { echo "linux-ci: docker is not running"; exit 1; }
	docker run --rm -v "$(PWD):/src:ro" $(LINUX_IMAGE) bash -c '\
		mkdir -p /w && cp -a /src/. /w/ && cd /w && rm -rf bin .zerg-cache && \
		for t in $(LINUX_GATES); do printf "linux %-14s " $$t; \
			make $$t >/dev/null 2>&1 && echo OK || { echo FAIL; exit 1; }; \
		done'

LINUX_IMAGE ?= golang:1.26-bookworm
# `version-check` sits straight after `build` because it reads bin/ rather than filling it.
LINUX_GATES ?= build version-check test examples corpus refuse reject oracle reject-fuzz fmt-corpus fmt-tokens fmt-self lint lint-check fixpoint docs-links sanitize-conc

# `reject` holds the mistakes somebody thought of; this holds the ones nobody did. It takes
# the corpus's WELL-FORMED programs, breaks each in a way the language has a rule about,
# and holds the result to the standing contract — refused by the compiler, never by cc. It
# found two things on its first run: an enum payload whose type was not a type name, and
# how many refusals still carry no position.
reject-fuzz:                    # break the corpus's working programs and hold the contract
	$(MAKE) build
	@[ -d test-data/codegen ] || { echo "test-data submodule not initialized (git submodule update --init)"; exit 1; }
	./scripts/reject-fuzz.sh

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

# No `$(MAKE) build` above the recipe, unlike almost everything else here, and that is the
# one thing about this target worth knowing from the Makefile: it reads bin/ instead of
# filling it, so it must be run after a build rather than instead of one. What it compares
# and why nothing else can see it are set out in the script.
version-check:                  # VERSION, the generated source, and both compilers agree
	./scripts/version-check.sh

lint:                           # lint the compiler and stdlib with zerg itself
	$(MAKE) build
	./bin/zerg lint $(ZERG_ENTRY)

# `lint` asks whether the compiler is clean, and it is — which is exactly why it cannot tell a
# rule that finds nothing from a rule that is gone. This one makes every rule fire.
lint-check:                     # every linter rule has a program that makes it fire
	$(MAKE) build
	./scripts/lint-check.sh

fmt:                            # rewrite the compiler and stdlib in canonical style
	$(MAKE) build
	@for f in $(ZERG_ENTRY) src/compiler/zerg/*.zg src/stdlib/*.zg; do \
		./bin/zerg fmt $$f || { echo "fmt: failed on $$f"; exit 1; }; \
	done
