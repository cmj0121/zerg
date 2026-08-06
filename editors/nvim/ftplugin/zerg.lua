-- Start the Zerg language server for this buffer.
--
-- A second ftplugin beside zerg.vim rather than lines added to it: nvim sources every
-- ftplugin for a filetype, the vim one is editing conventions that hold with no toolchain
-- installed, and this one needs `zerg` on PATH. Keeping them apart means the file that can
-- fail is the file that does nothing else.
require('zerg.lsp').attach(0)
