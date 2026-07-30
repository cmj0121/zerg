" Zerg syntax highlighting
"
" Highlighting grows one grammar group at a time, tracking GRAMMAR. This file
" covers the core groups 1-7: nop, lexical (comments/identifiers/keywords),
" literals, operators, functions, control flow, and types — plus the group-12
" danger surface (`unsafe` / `asm`, in an alarming colour) and cross-cutting
" polish: call highlighting, TODO markers, invalid-escape errors, and sync.
" Folding lives in the ftplugin — see the note above `syntax sync` below.
"
" Maintainer: Zerg project
" Filenames:  *.zg

if exists('b:current_syntax')
  finish
endif

" --- group 1 & 2: comments -----------------------------------------------------

" '##' is a doc comment (attaches to the following declaration); '#[' starts a decorator
" (below); any other '#' is an ordinary line comment. TODO-style markers are highlighted.
syntax keyword zergTodo contained TODO FIXME XXX HACK BUG NOTE
syntax match zergDocComment "##.*$" contains=zergTodo,@Spell
syntax match zergComment "#\%(#\|\[\)\@!.*$" contains=zergTodo,@Spell

" A decorator '#[derive(...)]' — a compiler directive. Capitalized spec names
" inside highlight as types; the rest reads as a preprocessor directive.
syntax region zergDecorator matchgroup=zergDecorator start="#\[" end="\]"
      \ contains=zergType,zergNumber,zergString

" --- group 2: reserved keywords ------------------------------------------------

" Statement keywords (control flow, effects, items introduced by a statement).
syntax keyword zergStatement nop return if else break continue match with
syntax keyword zergStatement spawn select defer del raise guard import impl print

" `close` was missing from this list entirely — the statement that ends a stream has never
" been coloured. It is a reserved word, so nothing named `close` can exist to be miscoloured,
" and one unconditional keyword is the whole rule. (It briefly headed a select's terminal arm
" too; that arm is gone — `for select { … }` is what ends now.)
syntax keyword zergStatement close

" `for` is a match (not a keyword) so the `impl … for` override below can win.
syntax match zergStatement "\<for\>"
" In `impl X for Y`, `for` is a plain keyword, not the loop keyword. The match must
" anchor at `for` (to win over the zergStatement `for` above by later definition), so
" `impl` is asserted in a lookbehind, width-bounded (\@80<=) to cap the back-scan on a
" long line. A forward `\<impl\>…\zs\<for\>` can't be used: `impl` is a separate keyword
" token, so a match anchored there never fires.
syntax match zergKeyword "\%(\<impl\>.\+\)\@80<=\<for\>"

" Declaration keywords.
syntax keyword zergKeyword mut const pub package init

" --- group 12: the danger surface (unsafe / asm) -------------------------------

" `unsafe` (the trust boundary — raw pointers, `unsafe fn`, `unsafe mut` globals)
" and `asm` (inline assembly) get their OWN alarming colour, not the ordinary
" keyword colour, so bare-metal code stands out on sight (linked to a bold-red
" highlight below). `unsafe` still leads `unsafe fn …`, whose name the `fn`
" nextgroup below picks up as usual.
syntax keyword zergDanger unsafe asm

" Type- and function-declaring keywords carry a nextgroup, so the declared NAME
" (fn/struct/enum/spec/type) highlights the same as a function name — see
" zergDeclName. Anonymous `fn(...)` has no name and stays plain.
syntax keyword zergKeyword fn struct enum spec type skipwhite nextgroup=zergDeclName

" Keyword operators (the word-form logical/type/binding operators).
syntax keyword zergOperator not and or is in as from

" Built-in type names and generic constructors. Capitalized built-ins (Ref, Result,
" Either, This) are already caught by the `\<\u\w*\>` type match below, so only the
" lowercase built-ins need naming here.
syntax keyword zergType bool byte rune int uint float str ptr
syntax keyword zergType list map set chan

" Constants. `this` (the method receiver) is highlighted like nil/true/false.
syntax keyword zergBoolean true false
syntax keyword zergConstant nil this

" --- group 3: literals ---------------------------------------------------------

" Integers (decimal, hex, octal, binary) with '_' digit grouping.
syntax match zergNumber "\<\d\(_\?\d\)*\>"
syntax match zergNumber "\<0x\x\(_\?\x\)*\>"
syntax match zergNumber "\<0o\o\(_\?\o\)*\>"
syntax match zergNumber "\<0b[01]\(_\?[01]\)*\>"

" Floats: fractional and/or exponent.
syntax match zergFloat "\<\d\(_\?\d\)*\.\d\(_\?\d\)*\([eE][-+]\?\d\(_\?\d\)*\)\?\>"
syntax match zergFloat "\<\d\(_\?\d\)*[eE][-+]\?\d\(_\?\d\)*\>"

" Escape sequences (shared by rune/byte/str). An invalid '\x' is flagged as an
" error; the valid set is matched afterwards, so it wins where it applies.
syntax match zergEscapeError "\\." contained
syntax match zergEscape "\\\([ntr0\\'\"]\|u{\x\+}\|x\x\x\)" contained

" byte b'x' (leftmost, so it wins over rune at the quote) and rune 'x'.
syntax match zergCharacter "b'\(\\\([ntr0\\']\|x\x\x\)\|[^'\\]\)'" contains=zergEscape,zergEscapeError
syntax match zergCharacter "'\(\\\([ntr0\\'\"]\|u{\x\+}\)\|[^'\\]\)'" contains=zergEscape,zergEscapeError

" raw string r"..." (no escapes) and str "..." (with escapes); triple-quoted str spans
" lines. The triple region is defined first so its longer start wins over a plain '"'.
syntax region zergRawString start=+r"+ end=+"+
syntax region zergString start=+"""+ end=+"""+ contains=zergEscape,zergEscapeError
syntax region zergString start=+"+ skip=+\\"+ end=+"+ contains=zergEscape,zergEscapeError

" f-string f"...{expr}...": text is String, the {expr} holes are highlighted.
" Python-style holes carry an optional !conversion and :format-spec.
syntax match zergFormatConv "![rsa]" contained
syntax match zergFormatSpec ":[^}]*" contained
syntax region zergFString matchgroup=zergString start=+f"+ skip=+\\"+ end=+"+
      \ contains=zergInterp,zergEscape,zergEscapeError
syntax region zergInterp matchgroup=zergDelimiter start=+{+ end=+}+ contained
      \ contains=zergNumber,zergFloat,zergString,zergRawString,zergCharacter,
      \zergOperator,zergBoolean,zergConstant,zergType,zergKeyword,zergStatement,
      \zergFormatConv,zergFormatSpec

" --- group 4: operators --------------------------------------------------------

" Symbol operators (word operators not/and/or/is are keywords, group 2). Multi-
" character forms are listed before the single-char class so they match whole.
syntax match zergOperator "->\|<-\|??\|?\.\|==\|!=\|<=\|>=\|<<\|>>\|:=\|+%\|-%\|\*%\|\.\.\|[-+*/%&|^~<>=?!]"

" --- group 5: declared names (fn/struct/enum/spec/type) and labels -------------

" The name right after a declaring keyword (via nextgroup); contained, so a bare
" identifier elsewhere is not mistaken for one.
syntax match zergDeclName "\h\w*" contained

" A field / parameter / argument label `name:` — the same colour as a declared
" name. Excludes the `:=` binding operator (colon followed by '=').
syntax match zergDeclName "\<\h\w*\ze\s*:[^=]"

" An inferred binding target `name :=` — coloured like a typed binding's name so
" both binding forms (`x := e` and `x: T = e`) highlight their target the same.
syntax match zergDeclName "\<\h\w*\ze\s*:="

" A function / method call `name(` — lowercase-initial (the highlighter treats a
" Capitalized name as a type by convention, so a constructor/variant like `Circle(`
" stays a type, matched below).
syntax match zergCall "\<\%(\l\|_\)\w*\ze("

" --- group 6: type & variant names, wildcard -----------------------------------

" A highlighter can't run the compiler's name resolution, so it keys on case: by
" Zerg convention capitalized identifiers are types and enum variants (User, Shape,
" Circle, Left). This is a highlight heuristic, not a grammar rule (names are case-free).
syntax match zergType "\<\u\w*\>"

" The match wildcard `_`, highlighted as special.
syntax match zergWildcard "\<_\>"

" --- example code inside a comment ---------------------------------------------

" A code example in a comment is Zerg, and reads as Zerg. It is marked with a
" doctest prompt: `>>>` opens a top-level item, `...` continues it.
"
"   # >>> fn main() {
"   # ...     print greet("world")
"   # ... }
"
" Which prompt a line carries is an authoring convention, not a distinction this
" file parses — both mean "the rest of this line is Zerg".
"
" The marker is EXPLICIT, and that is the load-bearing decision. A comment carries
" two kinds of indented block — source, and a sample of what the program PRINTS —
" and inferring from layout alone highlights the second as if it were the first:
" in this repo's own `cli` module a pasted help screen would light up `Options:`
" as a field name, `--output` as operators and `VALUE` as a type. Wrong
" highlighting is worse than none, so the author says which is which and the
" highlighter never guesses.
"
" It is a PROMPT rather than a ``` fence because comments are to become
" documentation, and that generator emits markdown — so ``` is the output syntax.
" Spelling the input the same way would leave a generator that must pass one
" through while producing the other with no way to tell them apart by looking.
"
" There is no `matchgroup`: with one, the whole start match — including the '#' —
" takes the group's colour, and the '#' would read as a different kind of thing
" than every other '#' in the file. Without one the start text belongs to the
" region and its contained items claim it, so the bar takes the '#' and the prompt
" takes the prompt.
"
" The bar is matched at the START of the line, so it wins over the ordinary comment
" rule (which begins at the '#' itself, one column later), while a '#' further
" along the line is still an ordinary comment — the example's own comments keep
" working. The contents are named rather than taken as `TOP`: TOP would include
" that ordinary comment rule, which swallows the line from the '#' onward and
" leaves the example a comment again.
"
" Being explicit means the list can be forgotten, and it had been — zergDocComment
" was missing, so a '##' line inside an example went unhighlighted. Anything added
" above belongs here too.
syntax match zergCommentBar "^\s*#" contained

" The prompt is anchored behind the '#' so a `...` in code is never mistaken for
" one. (`..` and `..=` are range operators; `...` is not an operator at all.) The
" lookbehind is width-bounded, the same idiom the `impl … for` rule above uses.
syntax match zergDocPrompt "\%(^\s*#\s\+\)\@40<=\%(>>>\|\.\.\.\)" contained

syntax cluster zergCodeItems contains=zergCommentBar,zergDocPrompt,zergComment,
      \zergDocComment,zergDecorator,zergStatement,zergKeyword,zergDanger,
      \zergOperator,zergType,zergBoolean,zergConstant,zergNumber,zergFloat,
      \zergCharacter,zergString,zergRawString,zergFString,zergDeclName,zergCall,
      \zergWildcard

" One line, one region: `end="$"` with keepend means there is no start/end pair to
" get wrong and no unterminated-marker failure mode.
syntax region zergCommentCode start="^\s*#\s\+\%(>>>\|\.\.\.\)" end="$"
      \ keepend contains=@zergCodeItems

" --- sync ----------------------------------------------------------------------

" Folding is NOT done here. A syntax fold always begins on the line its region
" opens on, which would hide the `fn name(...)` line of the function being
" folded; the ftplugin's 'foldexpr' folds the body and leaves that line visible.
" This file still carries the folding's other half: the syntax items below are
" what tell a brace inside a string or a comment from one that opens a block.

" Zerg has no multi-line tokens, but resync a little above the screen top so
" folds and any long line stay correct when scrolling.
syntax sync minlines=50

" --- highlight links ------------------------------------------------------------

highlight default link zergComment   Comment
highlight default link zergDocComment SpecialComment
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
highlight default link zergInterp     Identifier
highlight default link zergDeclName   Function
highlight default link zergCall       Function
highlight default link zergWildcard   Special
highlight default link zergTodo       Todo
highlight default link zergEscapeError Error
highlight default link zergDecorator  PreProc
highlight default link zergCommentBar   Comment
highlight default link zergDocPrompt   SpecialComment
highlight default link zergFormatSpec Special
highlight default link zergFormatConv Special

" The danger surface (`unsafe` / `asm`) is NOT linked to an ordinary group — it is
" given an explicit alarming bold-red so it reads as dangerous under any colorscheme.
" `default` keeps it overridable by a user's own theme.
highlight default zergDanger term=bold,underline cterm=bold ctermfg=red gui=bold guifg=#ff3b30

let b:current_syntax = 'zerg'
