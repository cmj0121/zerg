-- `:checkhealth zerg` — why nothing is happening.
--
-- The client is deliberately SILENT. It starts no server when `zerg` is not on PATH and it
-- reports nothing when it does not, because a language client that errors on every `.zg`
-- file in a checkout without a built toolchain is one a person disables and never
-- re-enables (see `lsp.lua`).
--
-- That decision has a cost, and this file is the payment: with nothing said on failure,
-- there was no way to find out WHY a buffer had no diagnostics — an unbuilt toolchain, a
-- `zerg` shadowed by an older install, `vim.g.zerg_lsp` left false in a config months ago,
-- and a server that started and crashed all look exactly alike from inside the editor.
--
-- It answers the questions in the order they actually go wrong, and it asks the toolchain
-- rather than repeating anything about it: the version comes from `zerg --version`, and
-- whether a client is attached comes from nvim.

local M = {}

local function report(fn, ...)
	-- `vim.health.*` are the current names; `vim.health.report_*` were theirs before nvim
	-- 0.10. Both spellings are looked up rather than one being assumed, so a checkhealth on
	-- an older nvim reports instead of erroring inside the reporter.
	local h = vim.health
	local f = h[fn] or h['report_' .. fn]
	if f then
		f(...)
	end
end

-- run answers with the trimmed first line of a command's output, or nil.
local function run(cmd)
	local out = vim.fn.system(cmd)
	if vim.v.shell_error ~= 0 then
		return nil
	end
	return (vim.split(out, '\n')[1] or ''):gsub('%s+$', '')
end

function M.check()
	report('start', 'Zerg toolchain')

	if vim.g.zerg_lsp == false or vim.g.zerg_lsp == 0 then
		report('warn', 'vim.g.zerg_lsp is false — the language server is not started', {
			'Remove it, or set it to true, to get diagnostics and formatting.',
		})
	end

	local cmd = require('zerg.lsp').cmd()
	local exe = cmd[1]
	if vim.fn.executable(exe) == 0 then
		report('error', ('`%s` is not on PATH — the client starts nothing and says nothing'):format(exe), {
			'Build the toolchain with `make` and install it with `sudo make install`,',
			'or point vim.g.zerg_lsp_cmd at the binary in a checkout: { "./bin/zerg", "lsp" }.',
		})
		return
	end

	local path = vim.fn.exepath(exe)
	local version = run({ exe, '--version' })
	if version then
		report('ok', ('%s — %s'):format(version, path))
	else
		report('warn', ('`%s --version` failed, but the file is there: %s'):format(exe, path))
	end

	report('start', 'Language server')

	local clients = vim.lsp.get_clients({ name = 'zerg' })
	if #clients == 0 then
		report('info', 'no client is running — open a .zg file, then run this again')
	else
		for _, c in ipairs(clients) do
			local bufs = #vim.tbl_keys(c.attached_buffers or {})
			report('ok', ('client %d, attached to %d buffer(s), root %s'):format(c.id, bufs, c.root_dir or '(none)'))
		end
	end

	report('start', 'Settings')

	report('info', ('vim.g.zerg_lsp_cmd        = %s'):format(vim.inspect(cmd)))
	report('info', ('vim.g.zerg_format_on_save = %s'):format(tostring(vim.g.zerg_format_on_save or false)))
	report('info', ('vim.g.zerg_diagnostic     = %s'):format(tostring(vim.g.zerg_diagnostic ~= false)))
end

return M
