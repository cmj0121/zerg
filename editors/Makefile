# Editor integrations for Zerg.
#
# nvim install: symlink nvim/{ftdetect,ftplugin,syntax}/zerg.vim into the local nvim
# config so the highlighting tracks this checkout, plus the two files that start the
# language server — ftplugin/zerg.lua and lua/zerg/lsp.lua. Honors XDG_CONFIG_HOME,
# defaults to ~/.config.
#
# Symlinks and not copies, which is the whole point: the syntax file states language facts
# that `make editor-align` holds to the compiler, and a copy in a config directory is a
# copy that gate cannot see.
NVIM_SRC    := $(CURDIR)/nvim
NVIM_CONFIG := $(or $(XDG_CONFIG_HOME),$(HOME)/.config)/nvim
NVIM_DIRS   := ftdetect ftplugin syntax

.PHONY: all clean build test run install uninstall

all:				# default action (nothing to build)

build:				# (no-op) editor integrations need no build

test:				# (no-op) editor integrations have no tests

run:				# (no-op)

clean:				# clean-up environment (nothing to clean)

install:			# install nvim syntax and the LSP client into local nvim config
	@for d in $(NVIM_DIRS); do \
		mkdir -p "$(NVIM_CONFIG)/$$d"; \
		ln -sf "$(NVIM_SRC)/$$d/zerg.vim" "$(NVIM_CONFIG)/$$d/zerg.vim"; \
		printf '    linked %s/zerg.vim\n' "$$d"; \
	done
	@# The language server client. `ftplugin/zerg.lua` starts it and `lua/zerg/lsp.lua` is
	@# what it calls — a second ftplugin rather than lines in the vim one, because nvim
	@# sources both and only this half needs `zerg` on PATH.
	@mkdir -p "$(NVIM_CONFIG)/lua/zerg"
	@ln -sf "$(NVIM_SRC)/ftplugin/zerg.lua" "$(NVIM_CONFIG)/ftplugin/zerg.lua"
	@ln -sf "$(NVIM_SRC)/lua/zerg/lsp.lua" "$(NVIM_CONFIG)/lua/zerg/lsp.lua"
	@printf '    linked ftplugin/zerg.lua and lua/zerg/lsp.lua\n'
	@printf 'Zerg nvim support installed to %s\n' "$(NVIM_CONFIG)"

uninstall:			# remove nvim syntax and the LSP client from local nvim config
	@for d in $(NVIM_DIRS); do \
		rm -f "$(NVIM_CONFIG)/$$d/zerg.vim"; \
	done
	@rm -f "$(NVIM_CONFIG)/ftplugin/zerg.lua" "$(NVIM_CONFIG)/lua/zerg/lsp.lua"
	@rmdir "$(NVIM_CONFIG)/lua/zerg" 2>/dev/null || true
	@printf 'Zerg nvim support removed from %s\n' "$(NVIM_CONFIG)"
