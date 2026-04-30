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

# Pattern rule: the destination depends on the in-tree source so make
# re-copies whenever the source is newer (e.g. after editing the syntax
# file). Without the explicit prerequisite, `make install` would be a
# no-op once the destination exists.
$(DEST)/%.vim: %.vim
	@install -d $(dir $@)
	@install -m644 $< $@
	@echo "  install  $@"
