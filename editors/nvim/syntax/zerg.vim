" Vim syntax file
" Language:    Zerg
" Maintainer:  cmj <cmj@cmj.tw>
" Version:     0.1.0
" Source:      derived from docs/GRAMMAR (v0.1.0)

if exists("b:current_syntax")
  finish
endif

let s:cpo_save = &cpo
set cpo&vim

" ── Comments ────────────────────────────────────────────────
syn keyword zergTodo            contained TODO FIXME XXX NOTE
syn match   zergDocComment      "##.*$" contains=zergTodo,@Spell
syn match   zergComment         "#.*$"  contains=zergTodo,@Spell

" ── Keywords (control flow) ────────────────────────────────
syn keyword zergConditional     if elif else match
syn keyword zergRepeat          for in
syn keyword zergStatement       break continue return raise defer
syn keyword zergKeyword         import as type select try except finally
syn keyword zergKeyword         nop print spawn

" ── Storage / declaration ──────────────────────────────────
syn keyword zergStorageClass    pub mut const
syn keyword zergStructure       struct enum spec impl

" Function declaration: highlight name after `fn`
syn keyword zergKeyword         fn nextgroup=zergFuncName skipwhite
syn match   zergFuncName        contained "\<\h\w*\>"

" ── Constants and identifiers ──────────────────────────────
syn keyword zergBoolean         true false
syn keyword zergConstant        nil
syn keyword zergSelf            this

" ── Logical / membership operators (word form) ─────────────
syn keyword zergOperator        and or xor not in

" ── Built-in types ─────────────────────────────────────────
syn keyword zergType            int float bool str byte rune
syn keyword zergType            list map set tuple chan sync ptr
syn keyword zergType            Result Option

" ── Built-in specs ─────────────────────────────────────────
syn keyword zergType            Printable Iterable Iterator Exception
syn keyword zergType            Eq Comparable Hashable Index
syn keyword zergType            Add Sub Mul Div Mod Neg
syn keyword zergType            BitAnd BitOr BitXor BitNot Shl Shr

" ── Numbers ────────────────────────────────────────────────
" Order matters: hex/bin/oct must precede plain decimal.
syn match   zergNumber          "\<0x[0-9A-Fa-f][0-9A-Fa-f_]*\>"
syn match   zergNumber          "\<0b[01][01_]*\>"
syn match   zergNumber          "\<0o[0-7][0-7_]*\>"
syn match   zergFloat           "\<\d[0-9_]*\.\d[0-9_]*\>"
syn match   zergNumber          "\<\d[0-9_]*\>"

" ── Strings ────────────────────────────────────────────────
syn match   zergStringEscape    contained /\\["'\\nrt0{]/
syn region  zergStringInterp    contained matchgroup=zergStringInterpDelim
                              \ start=+{+ end=+}+
                              \ contains=TOP,zergComment,zergDocComment

" Raw string: r"..." — no escapes, no interpolation.
syn region  zergRawString       matchgroup=zergRawStringDelim
                              \ start=+r"+ end=+"+ oneline

syn region  zergString          start=+"+ skip=+\\"+ end=+"+ oneline
                              \ contains=zergStringEscape,zergStringInterp,@Spell

" Triple-quoted multi-line string. Defined LAST so on input `"""` it
" wins over the single-quote zergString region (vim's region priority
" is: later-defined item wins when multiple regions match at the same
" position).
syn region  zergMultiString     start=+"""+ end=+"""+
                              \ contains=zergStringEscape,zergStringInterp,@Spell

" ── Runes ──────────────────────────────────────────────────
syn match   zergRune            "'\\[\"'\\nrt0{]'"
syn match   zergRune            "'[^\\']'"

" ── Inline asm block ───────────────────────────────────────
" Inside asm, `#` is the ARM immediate prefix and must NOT be highlighted
" as a Zerg comment. Use `//` for asm line comments. `${expr}` interpolates.
syn region  zergAsmBlock        matchgroup=zergKeyword
                              \ start=+\<asm\>\s*{+ end=+}+
                              \ contains=zergAsmComment,zergAsmInterp,zergAsmImmediate
syn match   zergAsmComment      contained "//.*$"
syn match   zergAsmImmediate    contained "#-\?[0-9][0-9A-Fa-fx_]*\>"
syn region  zergAsmInterp       contained matchgroup=zergStringInterpDelim
                              \ start=+\${+ end=+}+
                              \ contains=TOP,zergComment,zergDocComment

" ── Operators (symbolic) ───────────────────────────────────
" Multi-char first so the longer match wins.
syn match   zergOperator        "<<=\|>>=\|\.\.=\|??\|?\.\|->\|=>\|:=\|<-\|//\|\*\*"
syn match   zergOperator        "==\|!=\|<=\|>=\|<<\|>>\|\.\."
syn match   zergOperator        "[-+*/%&|^~!<>=]=\?"
syn match   zergOperator        "[?]"

" ── Highlight links ────────────────────────────────────────
hi def link zergComment             Comment
hi def link zergDocComment          SpecialComment
hi def link zergTodo                Todo

hi def link zergConditional         Conditional
hi def link zergRepeat              Repeat
hi def link zergStatement           Statement
hi def link zergKeyword             Keyword
hi def link zergStorageClass        StorageClass
hi def link zergStructure           Structure

hi def link zergFuncName            Function

hi def link zergBoolean             Boolean
hi def link zergConstant            Constant
hi def link zergSelf                Special
hi def link zergOperator            Operator

hi def link zergType                Type

hi def link zergNumber              Number
hi def link zergFloat               Float

hi def link zergString              String
hi def link zergMultiString         String
hi def link zergRawString           String
hi def link zergRawStringDelim      Delimiter
hi def link zergStringEscape        SpecialChar
hi def link zergStringInterpDelim   Special

hi def link zergRune                Character

" Inside `"{name}"` interpolation, characters that don't match a more
" specific contained group (typically the variable identifier itself)
" render as PreProc — softer than Special and matches the Python
" f-string convention, while still standing out from the surrounding
" String highlight.
hi def link zergStringInterp        PreProc

" Asm block content not matching a more specific contained group renders
" plainly — most asm mnemonics and registers fall here.
hi def link zergAsmBlock            Normal

hi def link zergAsmComment          Comment
hi def link zergAsmInterp           Special
hi def link zergAsmImmediate        Number

let b:current_syntax = "zerg"

let &cpo = s:cpo_save
unlet s:cpo_save
