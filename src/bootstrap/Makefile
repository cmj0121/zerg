DIR := bin
BIN := $(DIR)/zerg

.PHONY: build test run clean

all:                        # default action

clean:                      # clean-up environment
	@rm -rf bin

test:                       # go test ./...
	go test ./...

run: build                  # build and run against examples/01_hello.zg
	./bin/zerg run ../../examples/01_hello.zg

build: $(BIN)               # build src/bootstrap/bin/zerg


install:                    # install all editor integrations

uninstall:                  # uninstall all editor integrations

help:            # show this message
	@printf "Usage: make [OPTION]\n\n"
	@perl -nle 'print $$& if m{^[\w-]+:.*?#.*$$}' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?#"} {printf "    %-12s %s\n", $$1, $$2}'

$(BIN):
	@mkdir -p $(DIR)
	go build -o $(BIN) ./cmd/zerg
