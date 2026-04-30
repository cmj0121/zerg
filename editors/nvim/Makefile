DEST  ?= $(HOME)/.config/nvim

SRC := syntax/zerg.vim ftdetect/zerg.vim ftplugin/zerg.vim
DST := $(addprefix $(DEST)/, $(SRC))

.PHONY: all install uninstall help

all: 				# default action — show usage

install: $(DST) 	# copy syntax files

uninstall:			# remove installed syntax files
	@rm -rf $(DST)

help:				# show this message
	@printf "Usage: make [OPTION]\n\n"
	@perl -nle 'print $$& if m{^[\w-]+:.*?#.*$$}' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?#"} {printf "    %-12s %s\n", $$1, $$2}'
	@printf "\nCurrent DEST = $(DEST)\n"

%.vim:
	@install -d $(dir $@)
	@install -m644 $(subst $(DEST)/,,$@) $@
