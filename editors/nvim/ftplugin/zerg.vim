" Zerg filetype plugin
" Buffer-local editing conventions for Zerg source.
if exists('b:did_ftplugin')
  finish
endif
let b:did_ftplugin = 1

" Zerg's line comment is '#' (see GRAMMAR, group 1).
setlocal commentstring=#\ %s
setlocal comments=:#

" Small and crisp: 4-space indent, no tabs.
setlocal expandtab
setlocal shiftwidth=4
setlocal softtabstop=4

let b:undo_ftplugin = 'setlocal commentstring< comments< expandtab< shiftwidth< softtabstop<'
