DIR := bin
SRC := $(shell find . -type f -name '*.go')
BIN := $(DIR)/zerg

STD_SRC   := $(CURDIR)/../std
STD_EMBED := internal/loader/stdlib_embed

.PHONY: all build test run clean sync-stdlib install uninstall help

all:                        # default action

clean:                      # clean-up environment
	@rm -rf bin
	@find $(STD_EMBED) -mindepth 1 ! -name '.gitignore' -delete 2>/dev/null || true

sync-stdlib:                # mirror ../std/ into the gitignored embed dir
	@rsync -a --delete --exclude='.gitignore' $(STD_SRC)/ $(STD_EMBED)/

test: sync-stdlib           # go test ./...
	go test ./...

run: build                  # build and run against examples/01_hello.zg
	./bin/zerg run ../../examples/01_hello.zg

build: sync-stdlib          # build src/bootstrap/bin/zerg
	@mkdir -p $(DIR)
	go build -o $(BIN) ./cmd/zerg

install:                    # install all editor integrations

uninstall:                  # uninstall all editor integrations

help:            # show this message
	@printf "Usage: make [OPTION]\n\n"
	@perl -nle 'print $$& if m{^[\w-]+:.*?#.*$$}' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?#"} {printf "    %-12s %s\n", $$1, $$2}'
