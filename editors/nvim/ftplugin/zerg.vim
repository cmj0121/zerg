" Filetype plugin for Zerg.
" Sets buffer-local options matching the conventions in examples/.

if exists("b:did_ftplugin")
  finish
endif
let b:did_ftplugin = 1

setlocal commentstring=#\ %s
setlocal comments=:##,:#

setlocal expandtab
setlocal shiftwidth=4
setlocal tabstop=4
setlocal softtabstop=4

setlocal suffixesadd=.zg

let b:undo_ftplugin = "setlocal commentstring< comments< expandtab< shiftwidth< tabstop< softtabstop< suffixesadd<"
