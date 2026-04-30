SUBDIR := nvim

.PHONY: all install uninstall help $(SUBDIR)

all: 					# default action

install: $(SUBDIR)		# install all editor integrations

uninstall: $(SUBDIR)	# uninstall all editor integrations

help:			# show this message
	@printf "Usage: make [OPTION]\n\n"
	@perl -nle 'print $$& if m{^[\w-]+:.*?#.*$$}' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?#"} {printf "    %-12s %s\n", $$1, $$2}'

$(SUBDIR):
	$(MAKE) -C $@ $(MAKECMDGOALS)
