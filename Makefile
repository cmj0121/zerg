SUBDIR := editors src/bootstrap

# The self-hosting compiler. `zerg` (the Go seed) resolves imports itself, so it builds
# zergc from the entry file alone; zergc has no module loader, so rebuilding it with
# itself takes the whole source list, driver first.
ZERGC_SRCS := src/compiler/zergc.zg \
	src/stdlib/io.zg src/stdlib/ascii.zg src/stdlib/strconv.zg \
	src/compiler/zerg/token.zg src/compiler/zerg/lexer.zg src/compiler/zerg/ast.zg \
	src/compiler/zerg/parser.zg src/compiler/zerg/emit.zg

.PHONY: all clean test run build install uninstall upgrade examples selfhost help $(SUBDIR)

all: $(SUBDIR)                  # default action
	@[ -f .git/hooks/pre-commit ] || pre-commit install --install-hooks
	@git config commit.template .git-commit-template

clean: $(SUBDIR)                # clean-up environment
	@find . -name '*.sw[po]' -delete
	@rm -rf bin/examples
	@rm -f bin/zergc bin/zergc-stage2 bin/zergc.c bin/zergc-stage2.c

test: $(SUBDIR) examples        # run test (unit suites + the examples/ corpus)

run: $(SUBDIR)                  # run in the local environment

build: $(SUBDIR)                # build the toolchain: the Go seed, then the self-hosting compiler
	./bin/zerg build src/compiler/zergc.zg -o ./bin/zergc

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

examples:
	$(MAKE) build
	@for src in examples/[0-9][0-9]_*.zg examples/modules/main.zg examples/1g/init/main.zg examples/1g/reexport/main.zg; do \
		echo "Building $$src..."; \
		./bin/zerg build $$src --emit c      >/dev/null || exit 1; \
	done

selfhost:                       # have the built zergc rebuild itself, closing the chain
	$(MAKE) build
	./bin/zergc build ./bin/zergc-stage2 $(ZERGC_SRCS)
	@echo "self-host chain closed: bin/zergc-stage2 built by bin/zergc"
