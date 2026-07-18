" Zerg syntax highlighting
"
" Highlighting grows one grammar group at a time, tracking GRAMMAR. This file
" currently covers group 1 (nop) and group 2 (lexical: comments, identifiers,
" keywords). Later groups add: literals, operators, declarations' structure.
"
" Maintainer: Zerg project
" Filenames:  *.zg

if exists('b:current_syntax')
  finish
endif

" --- group 1 & 2: comments -----------------------------------------------------

" Line comment: '#' to end of line (Zerg has no block comments).
syntax match zergComment "#.*$" contains=@Spell

" --- group 2: reserved keywords ------------------------------------------------

" Statement keywords (control flow, effects, items introduced by a statement).
syntax keyword zergStatement nop return if else for in break continue match
syntax keyword zergStatement spawn select defer del raise guard import derive impl

" Declaration keywords.
syntax keyword zergKeyword fn mut pub struct enum spec type extern package init

" Keyword operators (the word-form logical/type operators).
syntax keyword zergOperator not and or is

" Built-in type names and generic constructors.
syntax keyword zergType bool byte rune int uint float str
syntax keyword zergType list map set chan Ref Result Either This

" Constants.
syntax keyword zergBoolean true false
syntax keyword zergConstant nil

" --- highlight links ------------------------------------------------------------

highlight default link zergComment   Comment
highlight default link zergStatement Statement
highlight default link zergKeyword   Keyword
highlight default link zergOperator  Operator
highlight default link zergType      Type
highlight default link zergBoolean   Boolean
highlight default link zergConstant  Constant

let b:current_syntax = 'zerg'
