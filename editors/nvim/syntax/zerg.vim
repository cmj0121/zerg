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

" Type and function declarations — the IDENT immediately following
" any of `fn`, `struct`, `spec` is captured as zergDeclName and
" rendered with the Function highlight. `enum` follows a separate
" chain so that, after the enum name, a body region recognises
" line-leading variant names. `impl` is intentionally excluded: the
" IDENT after `impl` references an existing type rather than
" declaring a new one.
syn keyword zergStructure       struct spec nextgroup=zergDeclName skipwhite
syn keyword zergStructure       enum nextgroup=zergEnumName skipwhite
syn keyword zergStructure       impl
syn keyword zergKeyword         fn nextgroup=zergDeclName skipwhite
syn match   zergDeclName        contained "\<\h\w*\>"
syn match   zergEnumName        contained "\<\h\w*\>"
                              \ nextgroup=zergEnumBody skipwhite skipnl

" Enum body — line-leading IDENT followed by `(`, end of line, or
" `#` is a variant name. Type names and other content inside the
" body keep their usual highlighting via the contains list.
syn region  zergEnumBody        contained start=+{+ end=+}+
                              \ contains=zergEnumVariant,zergType,zergComment,zergDocComment,zergStorageClass,zergKeyword,zergStructure,zergOperator,zergNumber,zergFloat,zergConstant,zergBoolean,zergSelf,zergString,zergMultiString,zergRawString,zergRune
syn match   zergEnumVariant     contained "\v^\s*\zs<\h\w*>\ze\s*(\(|$|#)"

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
" ARM64 GNU as embedded inside `asm { ... }`. Recognises mnemonics,
" registers, immediates (#imm), labels, directives, comments, and
" strings. The `#` character is the ARM immediate prefix and must
" NOT be treated as a Zerg comment, so the generic asm.vim cannot
" be reused — these rules are tailored to ARM64.
" `${expr}` interpolates Zerg-side syntax.

syn keyword zergAsmTodo         contained TODO FIXME XXX NOTE

" Mnemonic: any line-leading word. Defined first so a label (which
" extends one char further to consume the `:`) wins on conflict.
syn match   zergAsmMnemonic     contained "^\s*\zs\<\h\w*\>"

" Label: line-leading word followed by `:`.
syn match   zergAsmLabel        contained "^\s*\zs\<\h\w*\>:"he=e-1

" Register: x0..x30, w0..w30, plus named registers.
syn match   zergAsmRegister     contained "\c\<[xw]\d\+\>"
syn match   zergAsmRegister     contained "\c\<\(sp\|lr\|pc\|fp\|xzr\|wzr\)\>"

" Immediate: #-prefixed number (decimal, hex, binary).
syn match   zergAsmImmediate    contained "#[+-]\?\(0[xX][0-9A-Fa-f_]\+\|0[bB][01_]\+\|\d[0-9_]*\)"

" Directives: .text, .global, .word, etc.
syn match   zergAsmDirective    contained "\.\h\w*"

" Comments: `//` line, `/* */` block.
syn match   zergAsmLineComment  contained "//.*$" contains=zergAsmTodo,@Spell
syn region  zergAsmBlockComment contained start=+/\*+ end=+\*/+ contains=zergAsmTodo,@Spell

" Numbers in non-immediate context (e.g. inside .word 0x100).
syn match   zergAsmNumber       contained "\<0[xX][0-9A-Fa-f_]\+\>"
syn match   zergAsmNumber       contained "\<0[bB][01_]\+\>"
syn match   zergAsmNumber       contained "\<\d[0-9_]*\>"

" String literals inside asm (e.g. `.ascii "msg"`).
syn region  zergAsmString       contained start=+"+ skip=+\\"+ end=+"+ oneline

" Zerg-side `${expr}` interpolation. Lives only inside asm blocks.
syn region  zergAsmInterp       contained matchgroup=zergStringInterpDelim
                              \ start=+\${+ end=+}+
                              \ contains=TOP,zergComment,zergDocComment

" The asm block region. `asm` itself highlights via matchgroup.
syn region  zergAsmBlock        matchgroup=zergKeyword
                              \ start=+\<asm\>\s*{+ end=+}+
                              \ contains=zergAsmMnemonic,zergAsmLabel,zergAsmRegister,zergAsmImmediate,zergAsmDirective,zergAsmLineComment,zergAsmBlockComment,zergAsmNumber,zergAsmString,zergAsmInterp,zergAsmTodo

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

hi def link zergDeclName            Function
hi def link zergEnumName            Function
hi def link zergEnumVariant         Constant

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
" plainly — punctuation (commas, brackets) and unrecognised tokens.
hi def link zergAsmBlock            Normal

hi def link zergAsmMnemonic         Statement
hi def link zergAsmLabel            Label
hi def link zergAsmRegister         Identifier
hi def link zergAsmImmediate        Number
hi def link zergAsmNumber           Number
hi def link zergAsmDirective        PreProc
hi def link zergAsmLineComment      Comment
hi def link zergAsmBlockComment     Comment
hi def link zergAsmString           String
hi def link zergAsmTodo             Todo
hi def link zergAsmInterp           Special

let b:current_syntax = "zerg"

let &cpo = s:cpo_save
unlet s:cpo_save
