-- Zerg language server client for Neovim.
--
-- The server is `zerg lsp` — a sub-command of the compiler, not a separate program — so
-- there is no version to keep in step and nothing to install beyond the toolchain that is
-- already on PATH. This file is the twenty lines that tell nvim to start it.
--
-- It uses `vim.lsp.start` (nvim 0.8+) rather than nvim-lspconfig: the config for a server
-- with one command and no options is shorter than the dependency on a plugin that would
-- hold it, and a user who prefers lspconfig can ignore this file and register `zerg lsp`
-- there instead.
--
-- Four globals, all optional:
--   vim.g.zerg_lsp            set to false to not start the server at all
--   vim.g.zerg_lsp_cmd        the command to run, default {'zerg', 'lsp'}
--   vim.g.zerg_format_on_save set to true for `zerg fmt` on every write
--   vim.g.zerg_diagnostic     set to false to leave diagnostic display to nvim's own config
--
-- Quick fixes need nothing here. The server declares itself a `quickfix` provider, and
-- `vim.lsp.buf.code_action()` is nvim's own — so an `L502` finding offers "Write `1.0`"
-- wherever the user has already bound that, and this file stays the twenty lines it is.

local M = {}

-- root_dir is the project the buffer belongs to. The server checks a buffer against the
-- whole program it imports, and it resolves those imports relative to the FILE, so the root
-- is informational — it is what makes nvim reuse one client for a whole checkout rather
-- than starting one process per file.
--
-- `GRAMMAR` and `VERSION` are looked for beside `.git` because a Zerg checkout has no
-- project manifest to find: the language has no package manager, so there is no zerg.toml
-- for this to key on.
local function root_dir(buf)
	local path = vim.api.nvim_buf_get_name(buf)
	if path == '' then
		return vim.fn.getcwd()
	end
	local dir = vim.fs.dirname(path)
	local marker = vim.fs.find({ '.git', 'GRAMMAR', 'VERSION' }, { path = dir, upward = true })[1]
	if marker then
		return vim.fs.dirname(marker)
	end
	return dir
end

function M.cmd()
	return vim.g.zerg_lsp_cmd or { 'zerg', 'lsp' }
end

-- show_diagnostics puts the compiler's SENTENCE on the screen.
--
-- nvim's default `vim.diagnostic.config` has `virtual_text = false` (it changed in 0.11),
-- and what is left — an underline and a sign in the gutter — says a line is wrong without
-- saying what is wrong with it. The server's whole output is the sentence: `L502 the
-- literal `2` is a float here — write `2.0` and the page shows it` is the finding, and a
-- squiggle under the `2` is the part of it that carries no information. A person who has to
-- press a key to find out what the underline means reads it as noise and turns it off.
--
-- Scoped to this server's NAMESPACE and not set globally, which is the whole reason it is
-- acceptable for a language plugin to touch `vim.diagnostic` at all: it changes how a Zerg
-- finding draws and says nothing about anybody else's. A user with an opinion of their own
-- sets `vim.g.zerg_diagnostic = false` and keeps it.
--
-- The code travels with the text because the server sends one (`Diagnostic.code`) and it is
-- the name of a RULE — the thing to look up in docs/tooling/fmt.md, and the thing to say
-- when reporting the finding is wrong.
local function show_diagnostics(client_id)
	if vim.g.zerg_diagnostic == false or vim.g.zerg_diagnostic == 0 then
		return
	end
	local ok, ns = pcall(vim.lsp.diagnostic.get_namespace, client_id, false)
	if not ok then
		return
	end
	vim.diagnostic.config({
		virtual_text = {
			spacing = 2,
			format = function(d)
				return d.code and string.format('%s %s', d.code, d.message) or d.message
			end,
		},
		severity_sort = true,
	}, ns)
end

-- formatting_command is `:ZergFmt`, which is `zerg fmt` on the buffer.
--
-- nvim's own `gq` does NOT reach this server and cannot be made to. It sets `formatexpr` on
-- attach only when the server declares `textDocument/rangeFormatting`, and this one declares
-- whole-document formatting alone — correctly, since `zerg fmt` reads a whole source and has
-- no notion of formatting half of one. So `gq` is not the door, and without a command the
-- only way to format on demand was to type the `vim.lsp.buf.format` call by hand or to turn
-- on format-on-save and accept it everywhere.
--
-- Buffer-local, because it is only an answer where the server is attached.
local function formatting_command(buf, client_id)
	vim.api.nvim_buf_create_user_command(buf, 'ZergFmt', function()
		vim.lsp.buf.format({ bufnr = buf, id = client_id, timeout_ms = 2000 })
	end, { desc = 'Format this buffer with zerg fmt' })
end

-- attach starts (or joins) the server for one buffer.
--
-- It answers quietly when the compiler is not on PATH. A language server that throws an
-- error on every `.zg` file opened in a checkout without a built toolchain is one a person
-- disables and never re-enables, and the editor still has syntax highlighting either way.
function M.attach(buf)
	if vim.g.zerg_lsp == false or vim.g.zerg_lsp == 0 then
		return
	end
	local cmd = M.cmd()
	if vim.fn.executable(cmd[1]) == 0 then
		return
	end

	-- `vim.lsp.start` answers with a client ID, not a client.
	local client = vim.lsp.start({
		name = 'zerg',
		cmd = cmd,
		root_dir = root_dir(buf),
	}, { bufnr = buf })

	if client then
		show_diagnostics(client)
		formatting_command(buf, client)
	end

	if client and vim.g.zerg_format_on_save then
		vim.api.nvim_create_autocmd('BufWritePre', {
			buffer = buf,
			group = vim.api.nvim_create_augroup('ZergFormatOnSave', { clear = false }),
			callback = function()
				-- `zerg fmt` declines a buffer whose brackets do not close, and the server
				-- answers with no edits rather than an error — so a mid-edit save writes what
				-- was typed instead of failing.
				vim.lsp.buf.format({ bufnr = buf, name = 'zerg', timeout_ms = 2000 })
			end,
		})
	end

	return client
end

return M
