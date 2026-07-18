" Zerg syntax highlighting
"
" Highlighting grows one grammar group at a time, tracking GRAMMAR. This file
" currently covers group 1 (nop & program skeleton) plus the '#' line comment.
" Later groups add: keywords, literals, operators, types, declarations.
"
" Maintainer: Zerg project
" Filenames:  *.zg

if exists('b:current_syntax')
  finish
endif

" --- group 1: nop & comments ---------------------------------------------------

" The empty-statement placeholder.
syntax keyword zergStatement nop

" Line comment: '#' to end of line (Zerg has no block comments).
syntax match zergComment "#.*$" contains=@Spell

" --- highlight links ------------------------------------------------------------

highlight default link zergStatement Statement
highlight default link zergComment   Comment

let b:current_syntax = 'zerg'
