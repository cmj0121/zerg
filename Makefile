# The verbs. Everything a person types to build, run, install or clean this toolchain is
# in this file; every target that can say NO — the gates — is in mk/gates.mk.
#
# It is `include` and not a sub-make, so the two files are ONE database: `make corpus`
# works from here, `make -n corpus` shows what it would run, and shell completion offers
# every name in both. CI depends on that, because it names the gates individually.
#
# `build` and `lint` are the exception in each direction: both are on the board of gates
# and both are verbs a person types on their own, so they are written here.
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

# JOBS is how many units the self-hosted compiler builds at once.
JOBS ?= 4

GATES_MK := mk/gates.mk

# `make help gates` — the word after `help` is a TOPIC and not a second goal, and make has
# no notion of that. Left alone it would build `help` and then RUN the gate called `gates`;
# defining the topic as a no-op instead collides with the real target and make says so out
# loud. So a help run that names a topic leaves the gate file out of the parse, which is the
# one thing that makes the name free. Nothing else is affected — with any other first goal
# the include below is the ordinary one, and that is what `make -n` and shell completion
# see. Only the topics listed here are treated this way; `make help build` is two goals and
# help says it has no such topic.
HELP_TOPICS := gates verbs
HELP_TOPIC := $(if $(filter help,$(firstword $(MAKECMDGOALS))),$(word 2,$(MAKECMDGOALS)))

ifeq ($(filter $(HELP_TOPIC),$(HELP_TOPICS)),)
include $(GATES_MK)
else
.PHONY: $(HELP_TOPIC)
$(HELP_TOPIC): help
	@:
endif

.PHONY: all test clean run build install uninstall upgrade linux-ci lint fmt help release release-tarball release-smoke $(SUBDIR)

all: build                      # default action
	@[ -f .git/hooks/pre-commit ] || pre-commit install --install-hooks
	@git config commit.template .git-commit-template

clean: $(SUBDIR)                # clean-up environment
	@find . -name '*.sw[po]' -delete
	@rm -rf bin/examples
	@rm -f bin/zerg bin/zerg.c bin/.zerg-stage1 bin/.zerg-stage1.c
	@rm -rf .zerg-cache

# A PLACEHOLDER, and deliberately one. `run` is one of the three verbs this Makefile is meant
# to be readable through, and until there is something for it to run it does nothing rather
# than something wrong: it used to depend on $(SUBDIR), which reached the bootstrap
# Makefile's own `run` and executed ZERG0 — the seed a reader is told they never meet — then
# exited 0 on the seed's own usage error.
#
# What it is waiting for is `zerg run`, the build-then-execute subcommand this toolchain does
# not have. When that lands this becomes one line; until then a verb that does nothing is
# honest and a verb that runs the wrong program is not.
run:                            # run in the local environment (not yet built)
	@echo 'make run: nothing to run yet — the `zerg run` subcommand is not built'

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
install: $(SUBDIR)              # install the toolchain and the editor integrations into PREFIX
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
	@# and the suites are filtered out for the reason the line above filters `zrt_test.*`: a
	@# `*_test.zg` belongs to no program. Nothing would break if they went — `module_at`
	@# resolves no test file, so no import could ever reach one — but an install is what a
	@# user's `import "strings"` reads, and 2,400 lines of somebody else's assertions is not
	@# part of that answer. It is the same glob `src/stdlib/*.zg` everywhere else in this file,
	@# and the one place where widening it leaves this tree.
	@cp $(filter-out %_test.zg,$(wildcard src/stdlib/*.zg)) "$(PREFIX)/lib/zerg/stdlib/"
	@# cloc ships no Zerg, so a count of any tree holding some reports a large unnamed
	@# remainder. `.cloc.def` is the missing definition and cloc reads
	@# `$(CLOC_CONFIG)/options.txt` before every run, so installing it there is what makes
	@# plain `cloc .` count Zerg — no flag, and no target here to run instead of cloc.
	@cp .cloc.def "$(PREFIX)/lib/zerg/cloc.def"
	@./scripts/cloc-config.sh install "$(PREFIX)/lib/zerg/cloc.def" "$(CLOC_CONFIG)"
	@echo "installed: $(PREFIX)/bin/zerg with its runtime and stdlib under $(PREFIX)/lib/zerg"

# --- the release artifact -----------------------------------------------------------------
#
# `release-tarball` stages an install and tars it, so what a person downloads is decided by
# `install` above and never by a second list. `release-smoke` is the gate on that artifact and
# it is deliberately NOT part of the board: the board tests a repository, and this tests a
# tarball on a machine that has no repository — which is a question only a release can ask.
#
# A TARBALL THAT FAILS `release-smoke` IS NOT PUBLISHED. `release` is the pair, in that order,
# so there is one command a person or a workflow runs and no way to do the first without the
# second.
DIST ?= dist

release-tarball:                # stage an install and tar it, named by the compiler inside it
	@DIST=$(DIST) ./scripts/release-tarball.sh

release-smoke:                  # the artifact, on a machine with nothing else on it
	@for t in $(DIST)/*.tar.gz; do ./scripts/release-smoke.sh "$$t" || exit 1; done

release: release-tarball release-smoke

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

# The six a person needs to know. Fifty-two flat lines was the whole of `make help` and it
# answered nobody's question: a reader looking for how to build this thing had to pick
# `build` out of forty-odd gates, and a reader who wanted a gate got no more than a name.
#
# The rest are one topic away rather than absent, which is the trade — every target here
# and in mk/gates.mk still has a line, and `make help verbs` / `make help gates` are where
# the line is. A target that is in NEITHER listing is a target nobody can find.
HELP_VERBS := build run test install clean help

# `target: … # what it does` is where a description is written in this project, so a
# listing reads them out of the file instead of keeping a second copy that can disagree
# with the target it describes. $(1) is the name column's width.
#
# HASH, because a `#` in a variable assignment starts a make comment and would take the
# rest of the line with it — the field separator this splits on is the very character.
# It costs nothing in a recipe, which is why the one-line `help` this replaces never
# needed it, and it is silent: the truncated awk is still a program, and it prints the
# whole line in one column rather than failing.
# It splits at the FIRST `#` rather than with a field separator, and that is a fix and not
# a style: awk has no lazy repetition, so the `FS = ":.*?#"` this replaces was greedy and
# split at the LAST one. `docs-links` describes itself as ``every `#fragment` names a
# heading`` and `make help` printed it as ``fragment` names a heading``, silently, for as
# long as that sentence has been there.
HASH := \#
help-column = awk '{ n = $$0; sub(/:.*/, "", n); d = $$0; \
	sub(/^[^$(HASH)]*$(HASH)[ \t]*/, "", d); printf "    %-$(1)s %s\n", n, d }'

help:				            # show this message
	@case "$(HELP_TOPIC)" in \
	"") printf "Usage: make [target]\n\n"; \
		$(foreach v,$(HELP_VERBS),grep -hE '^$(v):' Makefile | $(call help-column,10);) \
		printf "\n  make help gates    the %s gates, and what each one holds\n" "$(words $(LINUX_GATES))"; \
		printf "  make help verbs    every verb, including the ones above\n" ;; \
	gates) printf "The gates. \`make test\` runs them all; each also runs alone by its own name.\n\n"; \
		grep -hE '^[a-z][a-z0-9-]*:.*#' $(GATES_MK) | $(call help-column,18); \
		printf "\n  build and lint are gates too, and are listed under \`make help verbs\`.\n" ;; \
	verbs) printf "Usage: make [target]\n\n"; \
		grep -hE '^[a-z][a-z0-9-]*:.*#' Makefile | $(call help-column,18) ;; \
	*) echo "make help: there is no topic called '$(HELP_TOPIC)' — try: $(HELP_TOPICS)"; exit 1 ;; \
	esac

# VERSION rides along on the fan-out because the seed's Makefile stamps it into the binary,
# and a sub-make inherits nothing that is not handed to it. It is passed to every
# subdirectory rather than only to src/bootstrap: the ones with no use for it ignore it, and
# a list of which subdirectory cares is a thing that goes stale the day another one starts to.
$(SUBDIR):
	$(MAKE) -C $@ $(MAKECMDGOALS) VERSION=$(VERSION)

# The whole board, natively. `linux-ci` runs the same list in a container for the defects
# only Linux shows; this is what a developer runs before asking for that. The LIST is the
# point — `make gates` holds it to the makefiles' own targets and to what CI runs, because
# a gate that is only on one of the three is a gate somebody has to remember.
#
# It is called `test` because a gate IS a test: every one of them is a question this
# project can be told no to, and a person who types `make test` wants all of them asked,
# not the four subdirectory suites that used to answer to the name. Those are `suites`.
#
# WHAT IT COSTS: about 26 minutes from a cold tree on an M-series laptop, half of that in
# the four gates that rebuild a corpus against the compiler. That is a verb worth typing
# before asking for review and not one to type while waiting; `make suites` is seconds and
# `make build` a minute, and any single gate runs alone by its own name.
test:                           # everything that can say no — the whole board of gates
	@fail=""; for t in $(LINUX_GATES); do printf '%-20s ' $$t; \
		$(MAKE) $$t >/dev/null 2>&1 && echo OK || { echo FAIL; fail="$$fail $$t"; }; \
	done; \
	[ -z "$$fail" ] || { echo "test: failed —$$fail (run each alone for its report)"; exit 1; }; \
	echo "test: $(words $(LINUX_GATES)) gates green"

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
		mkdir -p /w && cp -a /src/. /w/ && cd /w && rm -rf bin .zerg-cache && make test'

LINUX_IMAGE ?= golang:1.26-bookworm

# What this gate lints, and it is EVERY ZERG SOURCE THIS PROJECT WRITES — which is what the
# target has always claimed and what, for a long time, it did not do: the recipe below was one
# `zerg lint $(ZERG_ENTRY)`, so the compiler was gated and nothing else was. A stdlib module the
# compiler does not import was unlinted, and so was every test suite. Both shipped findings —
# `src/stdlib/os_test.zg` carried an `L103` into main, hours after it was written.
#
# THE UNIT IS AN ENTRY, NOT A FILE, and that is the one way this list differs from SELF_SRCS
# next door. `zerg lint` takes a program: it resolves the entry's imports and lints the merged
# whole, because L101 and L102 are whole-program questions — a private function called from
# another module of the same program is not dead. So the compiler contributes ONE entry and not
# its forty files, while a stdlib module and a test suite are each a program of their own.
#
# A SUITE IS STILL AN ENTRY OF ITS OWN, and now that it lives beside its module that is what
# the one glob below says rather than a second one: `src/stdlib/*.zg` reaches `strings.zg` and
# `strings_test.zg` alike, and both are handed to `zerg lint` separately. There is no
# `filter-out %_test.zg` here, and the absence is the DECISION and not an oversight — a suite
# the linter never reads is exactly how the `L103` above shipped, and it is the one file in a
# package a `zerg lint <module>` cannot see, because a normal build resolves no `*_test.zg`.
#
# WHAT IT COSTS is that the two halves of a package are asked separately, so neither sees the
# other's calls. A module-private function used ONLY from the suite beside it would be `L102`
# when the module is linted — correctly, in the sense the rule means it: nothing a shipping
# build compiles calls it. The way out is to delete it or to use it, not to widen this list;
# an entry that merged the two would be `zerg lint` inventing a package shape only `zerg test`
# has.
#
# `atomic` is the one exclusion and it is not a judgement about the module: it declares
# `Atomic[T]`, a generic struct this compiler has not built, so `import "atomic"` is refused by
# name (`E9104`, chk_unbuilt_module) and there is no program for the linter to be handed. The
# entry deletes itself the day a generic struct is built — the same end state CORPUS_SKIP has.
LINT_SKIP := src/stdlib/atomic.zg
LINT_ENTRIES := $(ZERG_ENTRY) $(filter-out $(LINT_SKIP),$(wildcard src/stdlib/*.zg))

# A FLOOR under how many entries were linted, of the kind `corpus`, `examples` and `fmt-corpus`
# carry. The one glob above reaches a directory, and a glob that matches nothing leaves a loop
# with nothing to iterate and a gate that exits 0 for having asked one question.
#
# 16 against the 20 there are today: far enough below that adding a module or retiring a suite
# is not a chore here, far enough above that a pattern which stopped matching cannot pass.
LINT_MIN ?= 16

# `--strict`, and the tool without it exits 0 on a warning. That is not two answers to one
# question: `zerg lint` reports on somebody else's program, where a `#[test]` that ships and a
# suppression that will never apply are decisions they are allowed to have made. This board is
# over THIS project's own source, where neither is. A gate stricter than the tool has
# precedent next door — refuse-check asserts more about a refusal than `zerg build` requires.
#
# EVERY ENTRY IS LINTED BEFORE THE GATE FAILS, rather than the loop stopping at the first —
# a board that reports one finding per run makes a reader run it once per finding.
lint:                           # lint the compiler, the stdlib and the suites with zerg itself
	$(MAKE) build
	@fail=0; n=0; \
	for f in $(LINT_ENTRIES); do \
		./bin/zerg lint --strict $$f || fail=1; \
		n=$$((n+1)); \
	done; \
	[ $$fail -eq 0 ] || { echo "lint: a source this project writes has a finding"; exit 1; }; \
	[ $$n -ge $(LINT_MIN) ] || { echo "lint: only $$n entries were linted, and the floor is $(LINT_MIN)"; exit 1; }; \
	echo "lint: $$n entries clean"

# Every source this repository writes IN Zerg. It is one variable because `fmt` and
# `fmt-self` have to name the same set: they were two globs, and the day the `lsp` module
# was added it landed in neither — `make fmt` did not reach it and `make fmt-self` did not
# notice, so a whole directory of the compiler was outside the rule that every other line
# of it is held to. A gate whose SCOPE is written twice is a gate with a blind spot the
# size of whatever was added last.
#
# `src/stdlib/*.zg` REACHES THE SUITES TOO now that they sit beside their modules, and there
# is no `filter-out` in front of it: a suite is checked here AS A FILE, which is the only
# thing `zerg fmt` ever asks about one. Formatting has no package — the tool reads a source,
# writes the canonical form of it and compares, and which module a file belongs to is not a
# question it can be handed. So `strings_test.zg` is held to the rule `strings.zg` is held
# to, and the glob that used to reach the suites under `tests/` is GONE rather than
# rewritten: the set is the same set, named once instead of twice.
# THE TREES, not a glob per directory. `zerg fmt` takes a path and a path is the tree under it,
# so the scope is two names and adding a directory under either changes nothing here — which is
# what this list existed to get wrong. It is still a list for `treesitter`, and the comment
# there says why.
SELF_TREES := src/compiler src/stdlib

SELF_SRCS := src/compiler/*.zg src/compiler/cmd/*.zg src/compiler/zerg/*.zg src/compiler/lsp/*.zg src/stdlib/*.zg

fmt:                            # rewrite the compiler and stdlib in canonical style
	$(MAKE) build
	@./bin/zerg fmt $(SELF_TREES)
