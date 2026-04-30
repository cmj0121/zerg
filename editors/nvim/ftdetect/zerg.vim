" Detect Zerg source files by extension.
"
" `set filetype=` (not `setfiletype`) is intentional: Zerg files often
" begin with `#! /usr/bin/env zerg`, which Neovim's content-based
" detector classifies as `conf`. We override that match here.
autocmd BufRead,BufNewFile *.zg set filetype=zerg
