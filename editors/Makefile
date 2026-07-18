# Editor integrations for Zerg.
#
# nvim syntax install: symlink nvim/{ftdetect,ftplugin,syntax}/zerg.vim into the
# local nvim config so the highlighting tracks this checkout. Honors
# XDG_CONFIG_HOME, defaults to ~/.config.
NVIM_SRC    := $(CURDIR)/nvim
NVIM_CONFIG := $(or $(XDG_CONFIG_HOME),$(HOME)/.config)/nvim
NVIM_DIRS   := ftdetect ftplugin syntax

.PHONY: all clean install uninstall

all:				# default action (nothing to build)

clean:				# clean-up environment (nothing to clean)

install:			# install nvim syntax into local nvim config
	@for d in $(NVIM_DIRS); do \
		mkdir -p "$(NVIM_CONFIG)/$$d"; \
		ln -sf "$(NVIM_SRC)/$$d/zerg.vim" "$(NVIM_CONFIG)/$$d/zerg.vim"; \
		printf '    linked %s/zerg.vim\n' "$$d"; \
	done
	@printf 'Zerg nvim syntax installed to %s\n' "$(NVIM_CONFIG)"

uninstall:			# remove nvim syntax from local nvim config
	@for d in $(NVIM_DIRS); do \
		rm -f "$(NVIM_CONFIG)/$$d/zerg.vim"; \
	done
	@printf 'Zerg nvim syntax removed from %s\n' "$(NVIM_CONFIG)"
