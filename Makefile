BOOTSTRAP := src/bootstrap
EDITORS   := editors

.PHONY: all clean test test-ci run build install uninstall upgrade help

all: build                     # default action: build the bootstrap toolchain
	@[ -f .git/hooks/pre-commit ] || pre-commit install --install-hooks
	@git config commit.template .git-commit-template

clean:                         # clean-up environment
	$(MAKE) -C $(BOOTSTRAP) clean
	@find . -name '*.sw[po]' -delete

test:                          # run tests (bootstrap toolchain)
	$(MAKE) -C $(BOOTSTRAP) test

test-ci:                       # run tests; skip private corpus if submodule clone is unauthorized
	@if git submodule update --init --recursive >/dev/null 2>&1; then \
		echo "[test-ci] private corpus available — running full suite"; \
		$(MAKE) -C $(BOOTSTRAP) test; \
	else \
		echo "[test-ci] private corpus unavailable — running with ZERG_SKIP_PRIVATE_CORPUS=1"; \
		ZERG_SKIP_PRIVATE_CORPUS=1 $(MAKE) -C $(BOOTSTRAP) test; \
	fi

run:                           # run a sample program through the bootstrap
	$(MAKE) -C $(BOOTSTRAP) run

build:                         # build the bootstrap toolchain
	$(MAKE) -C $(BOOTSTRAP) build

install:                       # install editor integrations (use DEST=... to override)
	$(MAKE) -C $(EDITORS) install

uninstall:                     # uninstall editor integrations
	$(MAKE) -C $(EDITORS) uninstall

upgrade:                       # upgrade all the necessary packages
	pre-commit autoupdate

help:                          # show this message
	@printf "Usage: make [OPTION]\n"
	@printf "\n"
	@perl -nle 'print $$& if m{^[\w-]+:.*?#.*$$}' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?#"} {printf "    %-18s %s\n", $$1, $$2}'
