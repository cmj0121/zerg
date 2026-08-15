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

.PHONY: all ci sha256 clean test test-runner stdlib-test run build install uninstall upgrade examples corpus fmt-corpus fmt-self fixpoint sanitize-conc refuse reject reject-fuzz fmt-tokens linux-ci docs-links grammar-cites grammar-keywords grammar-mirror layering conformance productions counterexamples error-codes-check cache-key-check gates lint lint-check version-check fmt desugar lsp editor-align treesitter install-check help $(SUBDIR)

all: build                      # default action
	@[ -f .git/hooks/pre-commit ] || pre-commit install --install-hooks
	@git config commit.template .git-commit-template

clean: $(SUBDIR)                # clean-up environment
	@find . -name '*.sw[po]' -delete
	@rm -rf bin/examples
	@rm -f bin/zerg bin/zerg.c bin/.zerg-stage1 bin/.zerg-stage1.c
	@rm -rf .zerg-cache

test: $(SUBDIR) examples        # run test (unit suites + the examples/ corpus)

# `zerg test` is the command the tests of this project will eventually be written for, and
# this is the gate on IT rather than on them: a runner that cannot detect a failing test
# reports a green board for a broken program, which is the one failure a test corpus must
# never have. The fixtures are in the script, chosen by failure mode.
test-runner:                    # the test runner can see a test that fails
	$(MAKE) build
	./scripts/test-runner-check.sh

# And the suites written FOR that runner. `test-runner` asks whether the runner can see a
# failure; this asks the standard library the questions, and it is a separate target because
# a red board should say which of the two broke.
#
# THE SUITES ARE NOT BESIDE THE MODULES THEY TEST, which is where docs/runtime/package.md puts
# a white-box test. They no longer HAVE to be out here — a test build resolves a test file's
# package the way an import is resolved, so a `strings_test.zg` beside `strings.zg` is a package
# of that pair and none of `src/stdlib`'s other fifteen modules is compiled with it — but moving
# a suite is a change of its own, and this one stays where it was written until somebody makes
# it. What it costs meanwhile is the white-box position: a suite here reaches its module the way
# a user does, through `import`, so a module-private name is out of its reach.
#
# A FLOOR, of the kind `corpus` and `test-runner` carry, and here it is not a formality: a
# `zerg test` over a tree it finds no test in prints `no tests` and EXITS 0. So a walk that
# broke, a directory that moved, or a suite somebody deleted all leave this target green for
# having asked nothing — the one failure a test gate must not have.
STDLIB_TEST_MIN ?= 60

# The modules whose comments carry runnable examples. An example nobody executes is an
# unverified claim, which is the shape this repository has spent a span removing, so the
# ` ```zerg ` / ` ```output ` pairs are COMPILED AND RUN and their stated output diffed
# against what came out. The list is a variable so that adding a module's examples is one
# name here rather than a second copy of the rule.
DOC_EXAMPLE_SRCS := src/stdlib/strings.zg

stdlib-test:                    # the standard library's own suites, and a floor under them
	$(MAKE) build
	./scripts/doc-examples-check.sh $(DOC_EXAMPLE_SRCS)
	@out=$$(./bin/zerg test tests/stdlib); status=$$?; \
	printf '%s\n' "$$out"; \
	[ $$status -eq 0 ] || exit 1; \
	n=$$(printf '%s\n' "$$out" | sed -n 's/^\([0-9][0-9]*\) passed,.*/\1/p'); \
	[ -n "$$n" ] && [ $$n -ge $(STDLIB_TEST_MIN) ] || \
		{ echo "stdlib-test: $${n:-no} tests passed, and the floor is $(STDLIB_TEST_MIN) — the gate did not run itself"; exit 1; }

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

# Where cloc looks for its own default switches, which is NOT under $(PREFIX): it is the
# user's, one per machine, and cloc reads it from wherever it is standing.
#
# `$(HOME)/.config` and NOT `$(XDG_CONFIG_HOME)`, which is where the nvim install next door
# looks and is the obvious thing to copy. cloc does not read that variable — it builds the
# path as $ENV{HOME}/.config/cloc/options.txt and has no fallback — so honouring XDG here
# would write a correct file to a path cloc never opens, on exactly the machines that set it,
# and the install would report success while `cloc .` went on not knowing what Zerg is.
#
# Overridable for the reason `install-check` needs its own PREFIX: a gate that writes the
# developer's real dotfiles is a gate nobody can afford to run.
CLOC_CONFIG ?= $(HOME)/.config/cloc

# `build` runs as a recipe line, not a prerequisite: as a prerequisite it would race the
# submodule work under `make -j`, and what is installed must be the binary this run built.
install: $(SUBDIR)              # install the toolchain into $(PREFIX), and the editor integrations
	$(MAKE) build
	@# `mkdir -p` and NOT `install -d`. BSD install(1) chmods the directory even when it
	@# already exists, so `install -d /usr/local/bin` fails with
	@#
	@#     install: chmod 755 /usr/local/bin: Operation not permitted
	@#
	@# on any macOS where that directory belongs to root — which is the default. The chmod is
	@# to the mode it already has, on a directory this target did not create and was only ever
	@# going to write one file into. `mkdir -p` asks for exactly what is wanted: the path
	@# exists, and nothing is said about a path that already did.
	@mkdir -p "$(PREFIX)/bin" "$(PREFIX)/lib/zerg/csrc" "$(PREFIX)/lib/zerg/stdlib" 2>/dev/null || true
	@# and then the question the user actually has to answer, asked before three commands fail
	@# one at a time. `install` reports one path per failure; this reports what to do about it.
	@[ -w "$(PREFIX)/bin" ] && [ -w "$(PREFIX)/lib/zerg/csrc" ] && [ -w "$(PREFIX)/lib/zerg/stdlib" ] || { \
		echo "install: $(PREFIX) is not writable by $$(id -un)."; \
		echo "    sudo make install                   install for everyone"; \
		echo "    make install PREFIX=$$HOME/.local   install for you (put $$HOME/.local/bin on PATH)"; \
		exit 1; }
	install -m 0755 bin/zerg "$(PREFIX)/bin/zerg"
	@# zrt_test.* is the C suite's harness and belongs to no program; the others are the
	@# per-platform slots the driver picks between, and it needs all of them present.
	@cp $(filter-out %/zrt_test.c,$(wildcard src/runtime/csrc/*.c)) $(filter-out %/zrt_test.h,$(wildcard src/runtime/csrc/*.h)) src/runtime/csrc/*.S "$(PREFIX)/lib/zerg/csrc/"
	@cp src/stdlib/*.zg "$(PREFIX)/lib/zerg/stdlib/"
	@# cloc ships no Zerg, so a count of any tree holding some reports a large unnamed
	@# remainder. `.cloc.def` is the missing definition and cloc reads
	@# `$(CLOC_CONFIG)/options.txt` before every run, so installing it there is what makes
	@# plain `cloc .` count Zerg — no flag, and no target here to run instead of cloc.
	@cp .cloc.def "$(PREFIX)/lib/zerg/cloc.def"
	@./scripts/cloc-config.sh install "$(PREFIX)/lib/zerg/cloc.def" "$(CLOC_CONFIG)"
	@echo "installed: $(PREFIX)/bin/zerg with its runtime and stdlib under $(PREFIX)/lib/zerg"

# `make install` is the first command a user runs and was the one command nothing ran: every
# other gate here uses the compiler out of ./bin, so a broken install was invisible until
# somebody hit it. This does the round trip into a temporary prefix — install, compile and
# RUN with nothing exported, uninstall, look at what is left.
install-check:                  # the installed toolchain works, and uninstall takes it away
	@./scripts/install-check.sh

uninstall: $(SUBDIR)            # remove what `make install` put in $(PREFIX)
	@# BEFORE the files go, and not after: the config line names a path under $(PREFIX), and
	@# a cloc pointed at a deleted definition answers `Unable to read` and counts nothing —
	@# in every project on the machine, not only this one. An uninstall that stopped halfway
	@# would leave the machine worse than not having run it.
	@./scripts/cloc-config.sh uninstall "$(CLOC_CONFIG)"
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
# The example sources, named once. `examples` inlined these globs and so did the tree-sitter
# gate, which is the `SELF_SRCS` hazard again: a scope written twice goes stale on whichever
# copy the next directory is not added to.
EXAMPLE_SRCS := examples/[0-9][0-9]_*.zg examples/*/main.zg examples/1g/*/main.zg

EXAMPLE_MIN ?= 20

# The examples that must be REFUSED, and the sentence they must be refused with. An example
# is a claim about the language, and a NEGATIVE one — this program is not Zerg — is a claim
# a build-and-run loop cannot check: it can only report that the build failed, which is what
# a typo does too. So the refusal is held to what it SAYS and to carrying a place, which is
# the same standard `make reject` holds its own cases to. That gate takes single files from a
# heredoc and this example is a module and an entry, which is why it is checked here.
EXAMPLE_REFUSED ?= examples/1g/private/main.zg examples/1g/privconst/main.zg
EXAMPLE_REFUSED_SAYS ?= is not a public member of module

examples:                       # build every example with zerg itself, and run it
	$(MAKE) build
	@fail=0; n=0; mkdir -p bin/examples; \
	for src in $(EXAMPLE_SRCS); do \
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
# Every source this repository writes IN Zerg. It is one variable because `fmt` and
# `fmt-self` have to name the same set: they were two globs, and the day the `lsp` module
# was added it landed in neither — `make fmt` did not reach it and `make fmt-self` did not
# notice, so a whole directory of the compiler was outside the rule that every other line
# of it is held to. A gate whose SCOPE is written twice is a gate with a blind spot the
# size of whatever was added last.
SELF_SRCS := src/compiler/*.zg src/compiler/cmd/*.zg src/compiler/zerg/*.zg src/compiler/lsp/*.zg src/stdlib/*.zg tests/stdlib/*/*.zg

fmt-self:                       # the compiler and the stdlib are canonical too
	$(MAKE) build
	@./bin/zerg fmt --check $(SELF_SRCS) \
		|| { echo "fmt-self: a compiler or stdlib source is not in canonical form"; exit 1; }
	@echo "fmt-self: $$(ls $(SELF_SRCS) | wc -l | tr -d ' ') sources are fmt's fixpoint"

# A target of its own, and it stays one now that CI runs both: they ask different questions
# of the same cases. `fmt-corpus` asks whether a case is already canonical — a rule that is
# stably wrong passes it — and this asks the property that rule cannot fake, that the token
# stream survives being formatted. It is the gate written to catch `fn main( {` -> `fn main({`.
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

# `sanitize-conc` is the only leak gate this repository had, and it can only run where
# LeakSanitizer exists — Linux — AND where the private corpus was fetched, which on a fork
# is nowhere. So the whole memory contract was defended on one platform, behind a secret.
#
# This asks a narrower question that needs neither: the same program is run at 5 rounds and
# at 200 against a counting allocator, and the number of allocations still live at exit must
# be the SAME. That is exactly the shape a per-round leak has, it is an exact integer rather
# than an RSS reading, and its programs are written inside the script — so it runs on every
# platform, on every fork, with nothing fetched. What it cannot see is written at the top of
# the script: a leak that is bounded per PROGRAM is invisible to a difference.
mem-check:                      # a value that outlives its scope, counted rather than estimated
	$(MAKE) build
	./scripts/mem-check.sh

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

# `oracle` compares two COMPILERS on one program. This compares two PROGRAMS on one
# compiler: a source as written, and the same source with its sugar undone.
#
# GRAMMAR defines `return x if c`, a while-`for` and a range-`for` as something else, and
# the emitter lowers each surface form directly — so the core form those definitions name
# goes down a different path, and no gate compared the two paths. `zerg desugar` writes the
# core form and this builds and runs both. The corpus is the input, so the gate grows with
# the corpus rather than when somebody remembers to extend it.
#
# The floor is what makes the run mean something: every assertion inside is "these two
# agree", which an empty list satisfies.
DESUGAR_MIN ?= 80

# The language server has no analysis of its own — every answer it gives is a call into the
# compiler's own `pub` surface — so the way it goes wrong is by growing one. This drives a
# REAL session over stdio and holds what it publishes to what `zerg build` and `zerg lint`
# say about the same file, which is `oracle`'s argument applied to the second front end.
#
# It is also the only thing in this tree that exercises the wire: Content-Length framing, a
# request/response loop over a stream that does not end, and a reply for every id.
LSP_MIN ?= 20

lsp:                            # the language server says what the compiler says
	$(MAKE) build
	@MIN_SESSIONS=$(LSP_MIN) ./scripts/lsp-check.sh examples/[0-9][0-9]_*.zg $$(ls test-data/codegen/*.zg 2>/dev/null | head -40)

# An editor file is the one place a language fact is REPEATED rather than asked for, so it
# is the one place that needs a diff rather than a call. Two facts today: the reserved-word
# list, and which character an indent is.
editor-align:                   # no editor file states a language fact the compiler denies
	$(MAKE) build
	@./scripts/editor-align.sh

# The one gate here that does NOT ask the compiler. `editors/tree-sitter-zerg` is a second
# implementation of GRAMMAR, so there is nothing to diff it against — what it is held to is a
# corpus: every `.zg` file in the tree must parse with no ERROR and no MISSING node. The
# script says at length why that is weaker than everything else on this board.
#
# It needs the tree-sitter CLI, and therefore node, which is a developer's tool and not a
# dependency of this language. A machine without it SKIPS: the toolchain does not need the
# parser to build, and a gate that goes red over a missing editor tool teaches people to stop
# reading the board.
treesitter:                     # the tree-sitter grammar reads every Zerg file in the tree
	@./scripts/treesitter-check.sh $(SELF_SRCS) $(EXAMPLE_SRCS) $$(ls test-data/codegen/*.zg test-data/fmt/*.zg 2>/dev/null)

desugar:                        # a program and the same program desugared do the same thing
	$(MAKE) build
	@MIN_COMPARED=$(DESUGAR_MIN) ./scripts/desugar-check.sh examples/[0-9][0-9]_*.zg $$(ls test-data/codegen/*.zg 2>/dev/null) $$(ls test-data/desugar/*.zg 2>/dev/null | grep -v '\.core\.zg$$')
	@./scripts/desugar-golden.sh

# The whole board, natively. `linux-ci` runs the same list in a container for the defects
# only Linux shows; this is what a developer runs before asking for that. The LIST is the
# point — `make gates` holds it to the Makefile's own targets and to what CI runs, because
# a gate that is only on one of the three is a gate somebody has to remember.
ci:                             # every gate on the board, in order
	@fail=""; for t in $(LINUX_GATES); do printf '%-20s ' $$t; \
		$(MAKE) $$t >/dev/null 2>&1 && echo OK || { echo FAIL; fail="$$fail $$t"; }; \
	done; \
	[ -z "$$fail" ] || { echo "ci: failed —$$fail (run each alone for its report)"; exit 1; }; \
	echo "ci: $(words $(LINUX_GATES)) gates green"

# Three places a gate has to appear before it protects anything: the Makefile, the board,
# and the workflow. Nine were on fewer than three when this was written.
gates:                          # every gate is on the board, and the board is run by CI
	./scripts/gates-check.sh

# Two Linux-only defects reached main this month — a preprocessor `#if` no GCC before 14
# can parse, and a `MAP_ANONYMOUS` glibc hides under `-std=c11` — and neither was visible
# from macOS. The docker flow that found them was driven by hand every time; this is it,
# written down. It is not part of any other target: it needs docker, and a developer
# without it should still be able to run everything else.
linux-ci:                       # run the Linux gates in a container, as CI does
	@docker info >/dev/null 2>&1 || { echo "linux-ci: docker is not running"; exit 1; }
	@# cloc comes first because `install-check` is on the board and needs it, the same way the
	@# workflow installs it beside that step. The image has no reason to carry it, and a
	@# container that reached the gate without it would fail on a missing tool rather than on
	@# anything about this repository.
	docker run --rm -v "$(PWD):/src:ro" $(LINUX_IMAGE) bash -c '\
		apt-get update >/dev/null && apt-get install -y cloc >/dev/null && \
		mkdir -p /w && cp -a /src/. /w/ && cd /w && rm -rf bin .zerg-cache && make ci'

LINUX_IMAGE ?= golang:1.26-bookworm
# `version-check` sits straight after `build` because it reads bin/ rather than filling it.
LINUX_GATES ?= build version-check test test-runner stdlib-test examples corpus desugar lsp editor-align treesitter install-check refuse reject oracle reject-fuzz fmt-corpus fmt-tokens fmt-self lint lint-check fixpoint docs-links grammar-cites grammar-keywords grammar-mirror layering conformance productions counterexamples error-codes-check seed-gaps cache-key-check sha256 gates mem-check sanitize-conc

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

# docs-links's sibling, for the other half of the specification. It builds nothing and reads
# no binary: a citation is text, and whether it resolves is a fact about the tree.
grammar-cites:                  # every GRAMMAR citation the repo makes must resolve
	./scripts/grammar-cites.sh

# The one thing in this tree with an OUTSIDE authority. `make oracle` is no use for a hash:
# both compilers would run the same Zerg source, so a wrong rotation is wrong identically on
# both sides and the comparison stays green. FIPS 180-4's vectors and the system tool are
# where the right answer comes from.
sha256:                         # the pure-Zerg digest is the digest everyone else computes
	$(MAKE) build
	./scripts/sha256-check.sh

# Two claims no test can reach, because both are about what the code MAY REACH rather than
# what it does: the parser builds the AST from tokens alone, and inference runs bottom-up
# over four named carve-outs. A compiler that broke either one passes every test it has,
# until the day somebody formats a file with an undefined name in it.
layering:                       # each stage knows only what it is allowed to know
	./scripts/layering-check.sh

# test-data/conformance is one file per GRAMMAR chapter and NOTHING RAN IT: twelve files,
# 375 lines, named in no runner and no test, and eleven of the twelve did not build the day
# this target was added. A corpus written to say "this is the language" was saying it to
# nobody. What it asserts, and why these files cannot be run-and-compared the way `corpus`
# does it, are set out in the script.
conformance:                    # every GRAMMAR chapter is read, or refused by name
	$(MAKE) build
	./scripts/conformance-check.sh

# `conformance` claims the sentence below and cannot deliver it: a chapter file stops at its
# FIRST refusal, so twelve files measure at most twelve of GRAMMAR's 171 productions — two
# of them were masking `mut &` and `unsafe fn`, forms that gate had never once put to the
# compiler. The unit here is the PRODUCTION: one sample each, one per file, so a refusal
# costs the one form it is about. Coverage is asserted, not assumed.
productions:                    # every GRAMMAR production is read, or refused by name
	$(MAKE) build
	./scripts/productions-check.sh

# Every gate above asks the compiler about programs that ARE Zerg, so none of them can see
# an OVER-ACCEPTANCE. That blind spot held a 1-tuple `(1, )`, a trailing comma in four
# bracket kinds, `0x_1F`, an unparenthesised `{`-opening head, `?? raise … if`, and a comma
# that was simply optional in nine readers — each accepted in silence, under a full board.
counterexamples:                # a program GRAMMAR does not derive must be refused
	$(MAKE) build
	./scripts/counterexample-check.sh

# A reserved word no production uses is a word nobody can write and every lexer refuses as a
# name — which is what `package` was for years. It is grammar-cites reversed: not "does every
# citation resolve" but "does every keyword get reached".
grammar-keywords:               # every reserved word is reached by the grammar that reserves it
	./scripts/grammar-keywords.sh

# The productions on docs/surface/grammar.md are a SECOND COPY of GRAMMAR's, and its zh-TW
# twin a third. This is what holds the copies to the original; the page may abbreviate with
# `…`, and may not say something else. Three had already drifted when it was written.
grammar-mirror:                 # the prose companion still says what GRAMMAR says
	./scripts/grammar-mirror.sh

# No `$(MAKE) build` above the recipe, unlike almost everything else here, and that is the
# one thing about this target worth knowing from the Makefile: it reads bin/ instead of
# filling it, so it must be run after a build rather than instead of one. What it compares
# and why nothing else can see it are set out in the script.
version-check:                  # VERSION, the generated source, both compilers and the READMEs agree
	./scripts/version-check.sh

cache-key-check:                # the build cache names the compiler that filled it
	./scripts/cache-key-check.sh

error-codes-check:              # every error code is reported once, asserted, and listed
	./scripts/error-codes-check.sh

seed-gaps:                      # the seed's gap list says the same thing in both languages
	./scripts/seed-gaps-check.sh

# `--strict`, and the tool without it exits 0 on a warning. That is not two answers to one
# question: `zerg lint` reports on somebody else's program, where a `#[test]` that ships and a
# suppression that will never apply are decisions they are allowed to have made. This board is
# over THIS project's own source, where neither is. A gate stricter than the tool has
# precedent next door — refuse-check asserts more about a refusal than `zerg build` requires.
lint:                           # lint the compiler and stdlib with zerg itself
	$(MAKE) build
	./bin/zerg lint --strict $(ZERG_ENTRY)

# `lint` asks whether the compiler is clean, and it is — which is exactly why it cannot tell a
# rule that finds nothing from a rule that is gone. This one makes every rule fire.
lint-check:                     # every linter rule has a program that makes it fire
	$(MAKE) build
	./scripts/lint-check.sh

fmt:                            # rewrite the compiler and stdlib in canonical style
	$(MAKE) build
	@for f in $(SELF_SRCS); do \
		./bin/zerg fmt $$f || { echo "fmt: failed on $$f"; exit 1; }; \
	done
