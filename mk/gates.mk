# The gates: every target in this project that can say no.
#
# They are INCLUDED by the root Makefile rather than run through a sub-make, so there is
# one namespace and one database: `make corpus` works from the root, `make -n corpus`
# shows what it would run, and shell completion offers every name here. CI depends on
# that — it runs the gates as separate parallel jobs, one `run: make <gate>` per step,
# so that a red board says which gate rather than that the board is red.
#
# `make test` runs the lot, in the order LINUX_GATES gives below. `make help gates`
# lists them with the sentence each one is worth.
#
# Two gates are NOT here, and both are on the board: `build` and `lint`. They are verbs
# a person types on their own, so they live beside the other verbs in the root Makefile;
# `make gates` does not care which file a target is written in, only that it is on the
# board and run by CI.
#
# WHAT A GATE OWES: a `# comment` on its own line saying what it holds — that is the
# text `make help gates` prints and the only description a reader gets — a name on
# LINUX_GATES, and a step in .github/workflows/ci.yml. `make gates` holds all three
# to each other.

.PHONY: suites test-runner stdlib-test install-check examples corpus fixpoint sanitize-conc \
	mem-check refuse reject oracle lsp editor-align treesitter desugar gates reject-fuzz \
	check-equal fmt-corpus fmt-self fmt-tokens fmt-roundtrip docs-links docs-mirror docs-zerg \
	grammar-cites grammar-keywords grammar-mirror sha256 layering conformance productions \
	counterexamples version-check cache-key-check error-codes-check seed-gaps lint-check \
	doc-check stmt-walk entry-path examples-index mem-peak release-notes

# The unit suites each subdirectory keeps — the Go seed's, the runtime's C suite — plus the
# examples corpus. It answered to `test` until the board took that name, and `suites` is what
# it holds: four collections of unit tests, none of which is the whole toolchain.
#
# It fans out with an explicit `test` goal rather than through the `$(SUBDIR)` rule's
# `$(MAKECMDGOALS)`, and that is the one line the rename could not be done without: each
# sub-Makefile still calls its own suite `test`, so passing this target's name down would
# ask all three of them for a target none of them has.
suites: examples                # each subdirectory's own unit suite, and the examples corpus
	@for d in $(SUBDIR); do $(MAKE) -C $$d test VERSION=$(VERSION) || exit 1; done

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
# THE SUITES ARE BESIDE THE MODULES THEY TEST, which is where docs/runtime/package.md puts a
# white-box test, and this target reads `src/stdlib` for that reason. A test build resolves a
# test file's package the way an import is resolved, so `strings_test.zg` beside `strings.zg`
# is a package of that pair and none of `src/stdlib`'s other seventeen modules is compiled
# with it. They sat under `tests/` for years because that is where they were written; what it
# cost was the white-box position, and a suite that reached its module through `import` could
# ask nothing about a module-private name.
#
# A FLOOR, of the kind `corpus` and `test-runner` carry, and here it is not a formality: a
# `zerg test` over a tree it finds no test in prints `no tests` and EXITS 0. So a walk that
# broke, a directory that moved, or a suite somebody deleted all leave this target green for
# having asked nothing — the one failure a test gate must not have.
STDLIB_TEST_MIN ?= 159

# The modules whose comments carry runnable examples. An example nobody executes is an
# unverified claim, which is the shape this repository has spent a span removing, so the
# ` ```zerg ` / ` ```output ` pairs are COMPILED AND RUN and their stated output diffed
# against what came out. The list is a variable so that adding a module's examples is one
# name here rather than a second copy of the rule.
#
# IT NAMES THE MODULES AND NOT THE SUITES NOW BESIDE THEM, and it is a list rather than a
# glob, so the move did not quietly widen it: an example is a claim a module's DOC COMMENT
# makes to a reader, and a `*_test.zg` makes its claims in `assert`.
DOC_EXAMPLE_SRCS := src/stdlib/json.zg src/stdlib/log.zg src/stdlib/os.zg src/stdlib/strings.zg src/stdlib/time.zg

stdlib-test:                    # the standard library's own suites, and a floor under them
	$(MAKE) build
	./scripts/doc-examples-check.sh $(DOC_EXAMPLE_SRCS)
	@# `log`'s claims a suite inside the process cannot make — `fatal` exits, the default stream
	@# is stderr, one line is one write, colour follows the terminal, `ZERG_LOG_LEVEL` names a
	@# level, and the pattern `log` is the tree's reference for is still the shape of its
	@# source. It rides here rather than on the board of its own because it is the same question
	@# this target already asks: does the standard library do what it says.
	./scripts/log-check.sh
	@out=$$(./bin/zerg test src/stdlib); status=$$?; \
	printf '%s\n' "$$out"; \
	[ $$status -eq 0 ] || exit 1; \
	n=$$(printf '%s\n' "$$out" | sed -n 's/^\([0-9][0-9]*\) passed,.*/\1/p'); \
	[ -n "$$n" ] && [ $$n -ge $(STDLIB_TEST_MIN) ] || \
		{ echo "stdlib-test: $${n:-no} tests passed, and the floor is $(STDLIB_TEST_MIN) — the gate did not run itself"; exit 1; }

# `make install` is the first command a user runs and was the one command nothing ran: every
# other gate here uses the compiler out of ./bin, so a broken install was invisible until
# somebody hit it. This does the round trip into a temporary prefix — install, compile and
# RUN with nothing exported, uninstall, look at what is left.
install-check:                  # the installed toolchain works, and uninstall takes it away
	@./scripts/install-check.sh

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

# An example that ships a `.out` beside it is held to WHAT IT PRINTS and not merely to
# running. Almost every example already states its expected output in a comment — "Expected
# output: 40" — and nothing read those comments, so an example could print something else
# for a year and this gate would call it green. The file is OPT-IN because the concurrent
# ones have no single right output: `11_coroutines` interleaves, and pinning one interleaving
# would be a gate that fails on a correct program.

examples:                       # build every example with zerg itself, and run it
	$(MAKE) build
	@fail=0; n=0; mkdir -p bin/examples; \
	for src in $(EXAMPLE_SRCS); do \
		case " $(EXAMPLE_REFUSED) " in *" $$src "*) continue;; esac; \
		out=bin/examples/$$(echo $$src | sed 's|^examples/||; s|/|_|g; s|\.zg$$||'); \
		./bin/zerg build $$src --emit bin -o $$out >/dev/null 2>&1 || { echo "BUILD  $$src"; fail=1; continue; }; \
		got=$$($$out 2>/dev/null) || { echo "RUN    $$src"; fail=1; continue; }; \
		want=$${src%.zg}.out; \
		if [ -f $$want ] && [ "$$got" != "$$(cat $$want)" ]; then echo "OUTPUT $$src"; fail=1; continue; fi; \
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
# needs no submodule, so it runs everywhere. The set it checks is SELF_SRCS, which is
# defined beside `fmt` in the root Makefile because that verb writes what this one reads.
fmt-self:                       # the compiler and the stdlib are canonical too
	$(MAKE) build
	@./bin/zerg fmt --check $(SELF_TREES) \
		|| { echo "fmt-self: a compiler or stdlib source is not in canonical form"; exit 1; }
	@echo "fmt-self: the compiler and the standard library are fmt's fixpoint"

# A target of its own, and it stays one now that CI runs both: they ask different questions
# of the same cases. `fmt-corpus` asks whether a case is already canonical — a rule that is
# stably wrong passes it — and this asks the property that rule cannot fake, that the token
# stream survives being formatted. It is the gate written to catch `fn main( {` -> `fn main({`.
fmt-tokens:                     # formatting changes spacing, never the token stream
	$(MAKE) build
	@[ -d test-data/fmt ] || { echo "test-data submodule not initialized (git submodule update --init)"; exit 1; }
	@./scripts/fmt-tokens.sh

# The third question about the same cases, and the one that was missing. `fmt-corpus` and
# `fmt-self` ask whether a source is ALREADY canonical, which every rule that does not fire
# on it passes; `fmt-tokens` asks that spacing does not move a token, and turns the F4xx
# REWRITES off to ask it. So the rules that change the code's shape — the only ones that can
# write a form the grammar does not have — were measured by nothing, and F401 turned
# `if s := v { return s }` into `return s if s := v` for months while all three stayed green.
#
# This asks whether the formatter's output is still a PROGRAM. See the script for why the
# question is one-way — if the input parses, the output must — and for the two halves, since
# a module's file is not an entry point and cannot be parsed on its own.
#
# FMT_ROUNDTRIP_MIN is the floor, and the reason is fmt-corpus's: the directory guard below
# catches an ABSENT submodule and nothing else, while a partial or wrong-commit checkout
# leaves test-data/ there with a handful of cases in it and every assertion here satisfied.
# 180 against the 214 measured today.
FMT_ROUNDTRIP_MIN ?= 180

fmt-roundtrip:                  # what the formatter writes, the parser reads
	$(MAKE) build
	@[ -d test-data/fmt ] || { echo "test-data submodule not initialized (git submodule update --init)"; exit 1; }
	@MIN_SOURCES=$(FMT_ROUNDTRIP_MIN) ./scripts/fmt-roundtrip.sh

# The test-data corpus belongs to the self-hosting compiler: it describes the LANGUAGE,
# which is what `zerg` is growing toward, while the seed is covered by its own unit tests.
#
# The gate is EVERY case except the ones named below, and it is that way round on purpose.
# An allowlist makes "not gated" the default for a new case: adding one and forgetting to
# register it leaves it silently unenforced, which is the one failure a test corpus must
# not have. Naming what CANNOT pass instead makes a new case gated the moment it exists,
# and gives the list an end state — it shrinks toward empty as the features land, rather
# than growing forever.

# Cases awaiting a feature `zerg` does not have. Delete a name when its feature lands;
# that deletion IS the gate for the feature.
CORPUS_SKIP := \
	derive_enum derive_ord \
	dyn_witness \
	gen_enum gen_enum2 gen_struct

CORPUS_PASS := $(filter-out $(CORPUS_SKIP),$(basename $(notdir $(wildcard test-data/codegen/*.zg))))

# A FLOOR under how many cases the gate has to have run, of the kind fmt-tokens and
# reject-fuzz carry. The `[ -d test-data/codegen ]` guard in the recipe catches an ABSENT
# submodule and nothing else; a shallow, partial or wrong-commit checkout leaves the
# directory THERE with a handful of cases in it, the wildcard above shrinks to match, and the
# gate reports `1/1 cases pass` and exits 0 — success for having measured almost nothing,
# which is the one failure that still looks like a corpus.
#
# 60 against the 168 that pass today, and the sentence that number sat in was stale by half a
# corpus: it said 80. The gap is room for cases to move into CORPUS_SKIP while they wait for a
# feature, so that adding a case for something `zerg` cannot build yet is not also a chore
# here; it is nowhere near the two or three a broken checkout leaves behind. A figure quoted in
# a comment beside the thing it describes is a claim, and this one had drifted far enough that
# a reader would have taken the floor for tight.
CORPUS_MIN ?= 60

# A `conc_` case is run more than once. Every other case is a function of its source, so
# one run answers the question; a concurrent one is a function of its source AND of an
# interleaving the scheduler picks fresh each time, and a race that shows up one run in
# twenty would sail through a single attempt. Repetition is what makes it a gate rather
# than a coin toss. They are milliseconds each, so the whole corpus stays quick.
CORPUS_CONC_REPS ?= 10

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
# This is NOT in `make suites`, for the same reason `corpus` is not. `suites` is the fan-out
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
# correctly and leaves the damage behind. Deliberate rather than in `suites`, again: it
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
# because the case still "fails". Not in `suites` for the same reason `corpus` is not: it
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
	# SELF_SRCS AND NOT SELF_TREES, because this hands a FILE LIST to a script that reads each
	# one — the walk lives in `zerg`, and a shell script is not it. The two scopes are the
	# hazard named above, and this is the one place that still has to carry the second copy.
	@./scripts/treesitter-check.sh $(SELF_SRCS) $(EXAMPLE_SRCS) $$(ls test-data/codegen/*.zg test-data/fmt/*.zg 2>/dev/null)

desugar:                        # a program and the same program desugared do the same thing
	$(MAKE) build
	@MIN_COMPARED=$(DESUGAR_MIN) ./scripts/desugar-check.sh examples/[0-9][0-9]_*.zg $$(ls test-data/codegen/*.zg 2>/dev/null) $$(ls test-data/desugar/*.zg 2>/dev/null | grep -v '\.core\.zg$$')
	@./scripts/desugar-golden.sh

# Three places a gate has to appear before it protects anything: the makefiles, the board,
# and the workflow. Nine were on fewer than three when this was written, and the first of
# those three is now two files — this one and the root Makefile, where `build` and `lint`
# live. The script follows the root Makefile's `include` lines rather than being told.
gates:                          # every gate is on the board, and the board is run by CI
	./scripts/gates-check.sh

# `version-check` sits straight after `build` because it reads bin/ rather than filling it.
LINUX_GATES ?= build version-check suites test-runner stdlib-test examples corpus desugar lsp editor-align treesitter install-check refuse reject oracle reject-fuzz check-equal fmt-corpus fmt-tokens fmt-roundtrip fmt-self lint lint-check doc-check fixpoint docs-links docs-mirror docs-zerg grammar-cites grammar-keywords grammar-mirror layering stmt-walk entry-path examples-index conformance productions counterexamples error-codes-check seed-gaps cache-key-check sha256 gates mem-check mem-peak release-notes sanitize-conc

# `reject` holds the mistakes somebody thought of; this holds the ones nobody did. It takes
# the corpus's WELL-FORMED programs, breaks each in a way the language has a rule about,
# and holds the result to the standing contract — refused by the compiler, never by cc. It
# found two things on its first run: an enum payload whose type was not a type name, and
# how many refusals still carry no position.
reject-fuzz:                    # break the corpus's working programs and hold the contract
	$(MAKE) build
	@[ -d test-data/codegen ] || { echo "test-data submodule not initialized (git submodule update --init)"; exit 1; }
	./scripts/reject-fuzz.sh

# `--emit check` is the same walk as `--emit c` with the C dropped instead of accumulated,
# and this is the gate that says so. It is not a speed gate: the failure worth guarding
# against is a check that finds LESS than the build finds, because that is an editor
# painting a clean buffer for a file which will not compile — the one bug the language
# server already had once. Same corpus, same mutations, byte-for-byte the same report.
check-equal:                    # the check stage finds exactly what the build finds
	$(MAKE) build
	@[ -d test-data/codegen ] || { echo "test-data submodule not initialized (git submodule update --init)"; exit 1; }
	./scripts/check-equal.sh

docs-links:                     # every docs path resolves, and every `#fragment` names a heading
	./scripts/docs-links.sh

docs-mirror:                    # a page and its zh-TW twin are the same document
	./scripts/docs-mirror.sh

# docs-mirror's blind spot, and the reason this exists. It compares the LANGUAGE of a fence
# between a page and its twin, so a diagram tagged ` ```zerg ` in both is a pair that matches
# and a claim that is false — 21 of the 30 in the English chapters were exactly that. The only
# reader that can tell is the compiler, so this asks it.
docs-zerg:                      # every ```zerg block in the docs is a program that compiles
	$(MAKE) build
	./scripts/docs-zerg.sh

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

# The block form with no keyword in front of it, and therefore the arm a hand-written match
# forgets. Ten walks across three files had, and what fell out was a legal program refused
# with an untrue reason, two rules that never fired, a linter finding about correct code, and
# two escapes to cc. Every one was one arm; finding them was the work, so the rule is written
# down here rather than left to the next reader to rediscover.
stmt-walk:                      # a walk that reaches into a block reaches into a bare one
	./scripts/stmt-walk-check.sh

# Thirty-three example programs sat here and no document linked to any of them; `examples/README.md`
# is the door now, and an index of thirty-three names is a list written twice. This is the direction
# the drift goes — somebody adds an example, the glob starts building it, and nothing reminds them
# to write a line about it. `docs-links` cannot see it: an example nobody cites is invisible to a
# gate that asks whether a citation resolves.
examples-index:                 # every example the gate builds is named in the index a reader opens
	./scripts/examples-index-check.sh

# The compiler's own footprint. Nothing else on this board can see it: `mem-check` is about a
# value outliving the scope that made it. Building the compiler with itself once peaked at
# 2.4 GB to produce 3.6 MB of C, and it was rediscovered by hand twice, months apart. The
# ceiling is a REGRESSION ALARM and not a target — it sits above what a healthy build costs
# (89 MB today) and below what the quadratic accumulation cost (355 MB on the same command).
MEM_PEAK_MAX_MB ?= 320
mem-peak:                       # the compiler emits its own C under a memory ceiling
	$(MAKE) build
	MEM_PEAK_MAX_MB=$(MEM_PEAK_MAX_MB) ./scripts/mem-peak-check.sh

# The body of a GitHub release is rendered from CHANGELOG.md's section for the version in
# VERSION, and a script that runs only on a tag is a script whose first run is the day it must
# not fail. What this asserts is the invariant behind it: THE CHANGELOG HAS A SECTION FOR THE
# VERSION THIS BUILD REPORTS. A heading that drifts, or a VERSION bumped without an entry
# written, turns this red on the commit that did it rather than on release day.
release-notes:                  # the changelog has a section for the version being built
	@./scripts/release-notes.sh >/dev/null
	@echo "release-notes: CHANGELOG.md has a section for $$(cat VERSION)"

# `zerg build src/…/zergc.zg` and `zerg build /abs/…/zergc.zg` are one program, and nothing
# about it changed between the two commands — only the string a person typed. The C used to
# differ by 450 KB with the module tags shifted, because a module's identity is the directory
# it was read from and `c_mod_tag` sorts those to number them. Nothing else on this board asks
# whether the output depends on how the build was INVOKED.
entry-path:                     # the same program, however its entry was spelled
	$(MAKE) build
	./scripts/entry-path-check.sh

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

# `lint` asks whether the compiler is clean, and it is — which is exactly why it cannot tell a
# rule that finds nothing from a rule that is gone. This one makes every rule fire.
lint-check:                     # every linter rule has a program that makes it fire
	$(MAKE) build
	./scripts/lint-check.sh

# `zerg doc` reads the source and prints what a module exposes, and for its first release
# nothing measured it. The direction it fails in is the reason this is a gate rather than a
# test: a declaration it quietly leaves out makes the documentation look MORE complete than
# it is, and a reader cannot tell that from a module with nothing else in it. So the `pub`
# declarations in the SOURCE are compared by name against the ones in the DOCUMENT, module by
# module; the undocumented ones are counted and the number pinned; and every comment-
# attachment rule gets a fixture case of its own, because two of them were wrong once with
# no gate able to see it.
#
# It is NOT `zerg doc --check` — running a doc example and diffing its output is the second
# half of issue #17 and is not built. `stdlib-test` still runs the examples.
doc-check:                      # the document is every exposed declaration, and no more
	$(MAKE) build
	./scripts/doc-check.sh
