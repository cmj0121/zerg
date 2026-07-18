" Zerg syntax highlighting
"
" Highlighting grows one grammar group at a time, tracking GRAMMAR. This file
" currently covers group 1 (nop), group 2 (lexical: comments, identifiers,
" keywords), and group 3 (literals). Later groups add operators and structure.
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
syntax keyword zergStatement spawn select defer del raise guard import derive impl print

" Declaration keywords. `fn` carries a nextgroup so the declaration name (if any)
" highlights as a function; anonymous `fn(...)` has no name and stays plain.
syntax keyword zergKeyword mut pub struct enum spec type extern package init
syntax keyword zergKeyword fn skipwhite nextgroup=zergFunction

" Keyword operators (the word-form logical/type operators).
syntax keyword zergOperator not and or is

" Built-in type names and generic constructors.
syntax keyword zergType bool byte rune int uint float str
syntax keyword zergType list map set chan Ref Result Either This

" Constants.
syntax keyword zergBoolean true false
syntax keyword zergConstant nil

" --- group 3: literals ---------------------------------------------------------

" Integers (decimal, hex, octal, binary) with '_' digit grouping.
syntax match zergNumber "\<\d\(_\?\d\)*\>"
syntax match zergNumber "\<0x\x\(_\?\x\)*\>"
syntax match zergNumber "\<0o\o\(_\?\o\)*\>"
syntax match zergNumber "\<0b[01]\(_\?[01]\)*\>"

" Floats: fractional and/or exponent.
syntax match zergFloat "\<\d\(_\?\d\)*\.\d\(_\?\d\)*\([eE][-+]\?\d\(_\?\d\)*\)\?\>"
syntax match zergFloat "\<\d\(_\?\d\)*[eE][-+]\?\d\(_\?\d\)*\>"

" Escape sequences, shared by rune/byte/str.
syntax match zergEscape "\\\([ntr0\\'\"]\|u{\x\+}\|x\x\x\)" contained

" byte b'x' (leftmost, so it wins over rune at the quote) and rune 'x'.
syntax match zergCharacter "b'\(\\\([ntr0\\']\|x\x\x\)\|[^'\\]\)'" contains=zergEscape
syntax match zergCharacter "'\(\\\([ntr0\\'\"]\|u{\x\+}\)\|[^'\\]\)'" contains=zergEscape

" raw string r"..." (no escapes) and str "..." (with escapes).
syntax region zergRawString start=+r"+ end=+"+
syntax region zergString start=+"+ skip=+\\"+ end=+"+ contains=zergEscape

" f-string f"...{expr}...": text is String, the {expr} holes are highlighted.
syntax region zergFString matchgroup=zergString start=+f"+ skip=+\\"+ end=+"+
      \ contains=zergInterp,zergEscape
syntax region zergInterp matchgroup=zergDelimiter start=+{+ end=+}+ contained
      \ contains=zergNumber,zergFloat,zergString,zergRawString,zergCharacter,
      \zergOperator,zergBoolean,zergConstant,zergType,zergKeyword,zergStatement

" --- group 4: operators --------------------------------------------------------

" Symbol operators (word operators not/and/or/is are keywords, group 2). Multi-
" character forms are listed before the single-char class so they match whole.
syntax match zergOperator "->\|==\|!=\|<=\|>=\|<<\|>>\|:=\|+%\|-%\|\*%\|[-+*/%&|^~<>=]"

" --- group 5: function declaration name ----------------------------------------

" The name after `fn` (see the fn keyword's nextgroup); contained, so a bare
" identifier elsewhere is not mistaken for a function name.
syntax match zergFunction "\h\w*" contained

" --- group 6: type & variant names ---------------------------------------------

" A highlighter can't run the compiler's name resolution, so it keys on case: by
" Zerg convention capitalized identifiers are types and enum variants (User, Shape,
" Circle, Left). This is a highlight heuristic, not a grammar rule (names are case-free).
syntax match zergType "\<\u\w*\>"

" --- highlight links ------------------------------------------------------------

highlight default link zergComment   Comment
highlight default link zergStatement Statement
highlight default link zergKeyword   Keyword
highlight default link zergOperator  Operator
highlight default link zergType      Type
highlight default link zergBoolean   Boolean
highlight default link zergConstant  Constant
highlight default link zergNumber    Number
highlight default link zergFloat     Float
highlight default link zergCharacter Character
highlight default link zergString    String
highlight default link zergRawString String
highlight default link zergFString   String
highlight default link zergDelimiter Delimiter
highlight default link zergEscape    SpecialChar
highlight default link zergFunction  Function

let b:current_syntax = 'zerg'
