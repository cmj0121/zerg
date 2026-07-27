SUBDIR := editors src/bootstrap

.PHONY: all clean test run build install uninstall upgrade examples help $(SUBDIR)

all: $(SUBDIR)                  # default action
	@[ -f .git/hooks/pre-commit ] || pre-commit install --install-hooks
	@git config commit.template .git-commit-template

clean: $(SUBDIR)                # clean-up environment
	@find . -name '*.sw[po]' -delete
	@rm -rf bin/examples

test: $(SUBDIR) examples        # run test (unit suites + the examples/ corpus)

run: $(SUBDIR)                  # run in the local environment

build: $(SUBDIR)                # build the binary/library

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
