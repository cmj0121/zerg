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
-- Two globals, both optional:
--   vim.g.zerg_lsp            set to false to not start the server at all
--   vim.g.zerg_lsp_cmd        the command to run, default {'zerg', 'lsp'}
--   vim.g.zerg_format_on_save set to true for `zerg fmt` on every write

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

	local client = vim.lsp.start({
		name = 'zerg',
		cmd = cmd,
		root_dir = root_dir(buf),
	}, { bufnr = buf })

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
