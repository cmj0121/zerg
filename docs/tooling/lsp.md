# Zerg Language Server

`zerg lsp` — the compiler answering an editor's questions instead of a shell's. Part of the
[Language Reference](../language.md). Also in [繁體中文](lsp.zh-TW.md).

```sh
zerg lsp        # speak JSON-RPC 2.0 over stdin/stdout; the editor starts and stops it
```

## The claim

The language server is **not a new program**. It is the compiler that already exists, asked a
different question: not _"lower this to C"_ but _"what is wrong with this buffer, right now"_. The
compiler has always answered that one — `check_files_diag` is `zerg build --emit check` — so what ships
here is the plumbing that carries the answer to where a person is looking.

That is also the invariant, and it is enforced rather than asserted:

> **If the server disagrees with `zerg build` about a program, the server is wrong.** It has no
> analysis of its own.

`make lsp` is that sentence as a gate. It drives a real session over stdio for every example and 40
corpus programs, and holds what the server publishes to what `zerg build` and `zerg lint` say about
the same file — errors against the command that refuses over one, lint findings against the command
that reports one. It is `make oracle`'s argument applied to the second front end.

It carries **protocol cases** beside that, and every one of them failed once: the exit status, the
post-shutdown reply, an empty change, an incremental change, a full change, a `$/` notification
versus a `$/` request, a malformed frame, a string id, a UTF-16 column after a line of CJK, a body
larger than one read of the runtime's bounded leaf, an abort published where `zerg build` places it,
and the [quick fix](#a-quick-fix-is-the-compilers-answer-not-the-servers).
Those are a different kind of failure and a quieter one — an editor with a corrupted buffer, or a
client left waiting, reports nothing at all.

## Where it lives

`src/compiler/lsp/` — a module of its own, importing `src/compiler/zerg/` across the `pub` boundary
like any other consumer, wired in by one more `.sub(cmd.lsp_cmd())` in `zergc.zg`. The command that
starts it is `src/compiler/cmd/lsp_cmd.zg`, beside the other five.

**One binary, not two.** A sub-command makes version skew between the compiler and the server
physically impossible — they are the same file — and an editor needs nothing on `PATH` that is not
already there.

**A separate module, not more files in `src/compiler/zerg/`.** A directory is the privacy unit, so
inside that module the server would reach every private declaration and grow entangled the way
everything else that could has. Forcing it through `pub` is what earns the option of splitting it
out later.

**It does not resolve `import`.** The driver already knows where a module lives — an environment
variable, then an installation root, then the checkout — so `serve` takes a **function**: hand it a
path and the text of a buffer, get back the whole program with that buffer standing in for what is
on disk. The module owns the protocol; the driver owns the filesystem.

## What is built

| Request                                                       | Answered by                                        |
| ------------------------------------------------------------- | -------------------------------------------------- |
| `initialize` / `shutdown` / `exit`                            | the session                                        |
| `textDocument/didOpen` · `didChange` · `didSave` · `didClose` | full-text sync                                     |
| `textDocument/publishDiagnostics`                             | `lex_diags`, `check_lint_index`                    |
| `textDocument/formatting`                                     | `fmt_src_off` — the same function `zerg fmt` calls |
| `textDocument/codeAction`                                     | the `fix` a finding carries, as one quick fix      |
| `textDocument/documentSymbol`                                 | `file_symbols` — the parsed file's declarations    |
| `textDocument/definition` · `references`                      | the [name index](#a-name-answers-from-one-index)   |
| `textDocument/hover`                                          | `doc_decl_at` — the document `zerg doc` prints     |

**`initialize` declares a capability for every request above past the document sync** —
`documentFormattingProvider`, `codeActionProvider`, `documentSymbolProvider`, `definitionProvider`,
`referencesProvider`, `hoverProvider` — which is the part a client reads before it sends anything.
Every other request is answered with a **method-not-found error**, not with silence: a client left
waiting for a reply it will never get stops sending the next one, and the editor goes quiet with
nothing said.

**The session is a state machine, and the exit status is part of it.** `shutdown` closes the server
to everything but `exit`; a request that arrives after it is answered with `InvalidRequest`, because a
client left waiting for a reply stops sending the next one. `exit` after `shutdown` exits **0** and
`exit` without one — or a standard input that simply ended — exits **1**. A server that always exits 0
tells its client that every crash was a clean shutdown.

**Sync is full, and a change that is not full is refused.** `textDocumentSync: 1` means the client
sends the whole document, so a change carrying a `range` is an incremental edit this server never
asked for, and applying its `text` would replace the document with a fragment of itself. An **empty**
`contentChanges` leaves the buffer alone rather than replacing it with the empty string — the flag is
not redundant with an empty string, since a client clearing a file to nothing sends `""`.

**Diagnostics are checked against the whole program**, not the buffer alone. A file that imports
another module has to be checked with that module or every name it borrowed reads as undefined —
a server that underlines correct code is one a person turns off.

**And which program that is, is found rather than assumed.** An editor says only which file is open,
and an open file is usually not an entry: it is one member of a directory module, whose types, whose
callers and whose second source root all live outside it. Read as an entry it reports `E4056` for a
struct declared in the file beside it, `L102` for a private function its sibling calls, and `E5002`
for a module that sits beside its own directory rather than inside it — three sentences about correct
code. So the driver **searches for an entry that reaches the buffer**: the `.zg` files directly in
the parent of the buffer's directory, then its parent, stopping at the first level that holds any
source at all, and taking the first one whose program contains the buffer. What counts as reaching it
is the loader itself — the same `module_files`, the same `module_at` — so the server never grows a
second answer to what a module is. When nothing reaches the buffer, the buffer **is** its own entry,
which is what a single-file program, a stdlib module and a test file all are.

That search is why "the buffer's directory is a module" is not the rule, tempting as it is. Nothing
local to a directory says whether it is one: `src/stdlib/` is a directory of `.zg` files where each
file is a module of its own, and `examples/` is a directory of twenty separate programs. A directory
is a module when something imports it, and only a walk from an entry knows that.

**Four severities, from two places.** An **error** — LSP severity 1 — is what the check walk
reports and `zerg build` refuses over, and it is the only severity the compiler's own diagnostics
use. Everything else on the wire came from the linter's rules, and every one of those is a **legal**
program that builds, so none of them is ever an error: a server that paints a working program red
teaches its user to ignore red. The linter's own three levels are ordered — a **finding** fails
`zerg lint`, a **warning** prints and exits 0, an **info** never gates anything
([the linter's severities](lint.md)) — so they land on LSP's remaining three in that order:

| `Finding.sev` | `zerg lint` prints | LSP severity    |
| ------------- | ------------------ | --------------- |
| `""`          | `L103 …`           | 2 — warning     |
| `"warning"`   | `warning: L601 …`  | 3 — information |
| `"info"`      | `info: L106 …`     | 4 — hint        |

The words do not line up, and they are not meant to: the left column is what a **command's exit
status** turns on and the right is how loudly an **editor** draws. The mapping is written once, in
`ls_severity`, and `make lsp` holds it — the gate reassembles each published diagnostic as the line
`zerg lint` would have printed, adjective included, so a server that flattened all three into one
severity would fail rather than agree about every count.

**A code travels as a code.** `Diag` carries the rule's identity — `E3006`, `L502` — in a field of
its own, so the server sends LSP's `Diagnostic.code` and an editor can filter, group and link by it.
That is the rule this page is about, applied to itself: a code spelled only inside the sentence is
one every reader has to parse back out, and the server reading it out would be a second copy of a
language fact. A finding with no code omits the field rather than sending an empty one.

**An abort lands where its sentence says.** A parse error and a `NotImplemented` refusal are
`raise`d sentences, not `Diag`s, and the place rides in the sentence as the last line `zerg build`
prints under it: `--> file:line:col`. Where that line names this buffer's file, the finding is
published at that place and the line is dropped from the message, because the range now says it.
Exactly that form is read and nothing looser — a pathless `--> line:col` knows no file.

Two findings land as a zero-width range at the top of the file instead, for two different reasons. One
that names **no place** lands there because the compiler did not say where. One placed in **another**
file of the program lands there because that place is not in this buffer, and it keeps its `-->`
line, which is then the only thing saying where to look. Not the word at 1:1 in either case: an
underline drawn under `fn` says the `fn` is wrong, and that is the one thing neither finding says.

## Positions

The compiler answers with a 1-based line and a 1-based **byte** column marking where a thing starts.
LSP wants a 0-based line, a 0-based character in **UTF-16 code units**, and a **range**. Both halves
of that conversion are the server's, and neither is optional:

- the **unit**, because a byte column and a UTF-16 column agree only while a line is ASCII, and this
  tree's own sources are full of em-dashes;
- the **range**, because `Diag` has no end position. Adding a field and filling it with the start
  would be a span that is really a point wearing a second name, so the end is derived from the
  **source**: the identifier at the position, or one character.

## Neovim

`make -C editors install` symlinks the syntax files and three more:

- `ftplugin/zerg.lua` — starts the server for a `.zg` buffer;
- `lua/zerg/lsp.lua` — `vim.lsp.start` (nvim 0.8+), no plugin manager and no `nvim-lspconfig`;
- `lua/zerg/health.lua` — what `:checkhealth zerg` runs.

```lua
vim.g.zerg_lsp = false            -- do not start the server
vim.g.zerg_lsp_cmd = { 'zerg', 'lsp' }
vim.g.zerg_format_on_save = true  -- zerg fmt on every write
vim.g.zerg_diagnostic = false     -- leave diagnostic display to nvim's own config
```

It answers **quietly** when `zerg` is not on `PATH`. A server that errors on every `.zg` file opened
in a checkout without a built toolchain is one a person disables and never re-enables.

The quick fixes need no configuration — `vim.lsp.buf.code_action()` is nvim's own, and the server
declares itself a `quickfix` provider so a client that asks for that kind alone still gets them.
nvim binds them to `gra` by default, and `gO` to the outline.

### What the ftplugin does, and why each number is the compiler's

`ftplugin/zerg.vim` is editing behaviour that must hold with **no toolchain installed**, so none of
it asks a running `zerg` anything. What it does instead is state facts the compiler owns — and
[`make editor-align`](#keeping-the-editor-honest) holds every one of them to their source.

| Setting                    | Is                                                           | Held to            |
| -------------------------- | ------------------------------------------------------------ | ------------------ |
| `noexpandtab`, `tabstop=4` | one tab per level, four columns wide                         | `F101`, `F403`     |
| `colorcolumn=121`          | the first column past the budget `F403` wraps at             | `fmt_wrap_max()`   |
| `foldexpr` / `indentexpr`  | the lowest delimiter depth a line reaches                    | one shared scanner |
| `makeprg` / `errorformat`  | `:make` runs `--emit check` and reads both diagnostic shapes | the compiler's own |

**Folding and indenting are one rule, asked twice.** A line's level is the lowest delimiter depth it
reaches, which leaves both lines that bracket a block on screen when it is folded — `fn f() {` above
and `}` below — and makes `}` dedent itself as it is typed. What differs is the delimiters: folding
counts braces alone, because a wrapped argument list is not a fold; indenting counts `(`, `[` and `{`,
because `F403` and `F404` indent inside all three.

Neither existed before. `indentexpr` was empty and so were `autoindent`, `smartindent` and `cindent`,
so `<CR>` after `fn f() {` put the cursor in column 1 and every level was a tab the person pressed —
with the formatter tidying it on the next write, which meant the file only ever looked right after a
tool had run over it. The check that it is now right is `gg=G` over this repository: re-indenting
every source the formatter wrote must change nothing, and finding the two cases where it did — a
wrapped `+`-chain and a doctest comment ending in `# >>>` — is what the rule is shaped by.

**`:make` is the compiler in the quickfix list**, and it is worth having beside a language server
because the two fail differently: when a program aborts in a file other than the buffer, the server
can only publish that finding at the top of the buffer, while `:make` jumps to the place in the file
the compiler named.

```vim
:make | copen           " compile this buffer, list what it said
:ZergFmt                " zerg fmt, on demand
:checkhealth zerg       " why nothing is happening
```

`:ZergFmt` exists because `gq` cannot reach this server: nvim wires `formatexpr` up only for a server
that declares `textDocument/rangeFormatting`, and this one declares whole-document formatting alone —
correctly, since `zerg fmt` reads a whole source and has no notion of formatting half of one.

`:checkhealth zerg` is the counterpart to the silence above. A client that starts nothing and says
nothing leaves an unbuilt toolchain, a `zerg` shadowed by an older install, a `vim.g.zerg_lsp` left
false in a config months ago, and a server that started and crashed all looking exactly alike; the
health check tells them apart, and asks the toolchain rather than repeating anything about it.

### A finding you can read without pressing a key

nvim's default `vim.diagnostic.config` has **`virtual_text = false`** (it changed in 0.11), so what
a published finding draws by default is an underline and a sign in the gutter — a mark saying a line
is wrong, with the part that says _what_ is wrong left where nobody looks. The server's entire output
is that sentence, so the client turns virtual text on for its own namespace and prefixes the rule's
code:

```text
    ratio: float = 2      ■ L502 the literal `2` is a float here — write `2.0` and the page shows it
```

**Its own namespace, not the global config.** `vim.diagnostic.config(opts, ns)` against the namespace
`vim.lsp.diagnostic.get_namespace()` hands back changes how a **Zerg** finding draws and says nothing
about anybody else's — which is the only arrangement under which a language plugin has any business
touching `vim.diagnostic` at all. A user with an opinion sets `vim.g.zerg_diagnostic = false` and
keeps theirs.

Whatever the display, nvim's own keys reach the same text, and they are worth knowing because they
say **more** than the line ever can:

| Key / call                                        | Shows                                                     |
| ------------------------------------------------- | --------------------------------------------------------- |
| `<C-w>d` — `vim.diagnostic.open_float()`          | every finding on the cursor's line, in full, in a window  |
| `]d` / `[d` — `vim.diagnostic.jump()`             | the next / previous one                                   |
| `<C-w>d` twice                                    | enters the float, where the text can be yanked            |
| `vim.diagnostic.setloclist()`                     | all of them in the location list, one line each           |
| `vim.diagnostic.config({ virtual_lines = true })` | the sentence on its own line under the code, never elided |
| `:lua =vim.diagnostic.get(0)`                     | the raw findings — `code`, `severity`, `source`, range    |

A finding drawn as virtual text is **truncated by the window**, and a severity-3 sentence carrying a
fix is exactly the one that runs long. `<C-w>d` is what to press when the line ends in `…`.

**`zerg build` and `zerg lint` are the other way to read it**, and they are the authority — the
server holds itself to them by `make lsp`:

```sh
zerg lint examples/01_bindings.zg
# examples/01_bindings.zg:10:17: L502 the literal `2` is a float here — write `2.0` and the page shows it
```

A finding at **severity 3** is INFORMATION about a legal program, not an error. `examples/01` and
`examples/03` both carry one, because both exist to show a literal adopting the type of its position
— `ratio: float = 2` is the lesson, and `L502` is the linter naming it. They compile and run.

## A quick fix is the compiler's answer, not the server's

A **code action** is what an editor offers at a diagnostic: a named edit the user can apply with a
keystroke. `L502` has one — the finding already says what to write, so the editor may as well write
it:

```zerg
x: float = 1 / 2      # two findings: the `1` is a float here, and so is the `2`
                      # quick fix on each: Write `1.0`, Write `2.0`
```

Two things had to be true first, and neither was:

- **The finding has to point at the literal.** `Diag` carries the place of the STATEMENT, which is
  the grain the compiler's marker has — and both literals above are in one statement, so an editor
  told to fix "the `1`" would have been handed the position of the `x`. An integer literal now
  carries the token's own line and column, which is also what stops two findings on one line from
  deduping into one.
- **The replacement has to come from the compiler.** It rides in `Diag.fix`, beside the message,
  because the two are one decision. A server that read `1.0` back out of the sentence "write `1.0`"
  would be the second copy this page exists to forbid — and the day the wording changes, the copy
  rewrites source into whatever it managed to parse.

A finding with no mechanical answer carries no `fix` and offers no action. An editor that offers a
quick fix and then does nothing is worse than one that offers none, because the user learns the menu
lies.

The rewrite is **not** `zerg fmt`'s. The formatter reads tokens and must work on source the compiler
cannot compile ([Formatter Rules](fmt.md)); knowing that `1` became a `float` needs types, so a
formatter that did this would fail in exactly the buffer a person reaches for it in. It is also an
opinion — `1.5 + 1` is a legal program — and the formatter has none.

## The outline is the parser's list, not the server's

`textDocument/documentSymbol` is what fills an editor's outline, its breadcrumbs and its `gO`. It is
the one interactive answer that needs **no name resolution** — a declaration knows what it is called
and where it was written — which is why it was built first.

The rule this page is about decides its shape. The compiler answers `file_symbols`, which walks a
parsed file and returns a name, a **kind as a word**, and a place; the server maps the word onto
LSP's `SymbolKind` number and nothing else. Neither half can drift into the other's job: the compiler
would have to be edited if it spelled `12` for a function, and a server that decided what counts as a
declaration would be the analysis this one does not have.

**A purpose-built `Symbol` rather than making the AST public.** `FnDecl` and its siblings stayed
private and one small type crosses the `pub` boundary instead. Widening that boundary to every field
of every declaration — for a list of names — would hand the server reach over the parser's shape and
a reason to grow.

**The buffer alone, not the whole program.** Every other answer here is computed against the modules
the file imports, because a borrowed name is undefined without them. An outline is the opposite
question — what is _in_ this file — and pulling the imports in would fill it with declarations the
reader cannot see on screen.

A buffer that does not parse answers with **nothing** rather than with an error. An outline is a view
and not a verdict, and the diagnostic saying the buffer is broken has already been published by the
check that runs on every keystroke.

`make lsp` holds it to `--emit ast`: the outline must name exactly the declarations the parser read.
That comparison runs only on files that import nothing, because the driver merges a whole program
into one `File` before emission — so the dump is the buffer's own declarations only where there are
no imports, and where the two questions differ the dump is not an oracle and is not asked. The gate
counts how many it compared, for the reason every floor here exists.

**Two things it does not do.** A struct's fields and an enum's variants are not children — the
protocol has a tree and this is a flat list, which is what an editor shows anyway. And `range` and
`selectionRange` are the same range, the identifier's, because the compiler has no end position for
either — the same gap the diagnostics have. A jump lands on the name; a client cannot highlight the
whole declaration a cursor is inside.

## A name answers from one index

`textDocument/definition` and `textDocument/references` are two views of **one table**: the
position-to-declaration index, `NameIndex`. The use under the cursor names a declaration, and a
declaration's references are every use that names it. Hover (#192) and completion, signature help,
workspace symbol and rename (#194) read the same table; none of them resolves a name on its own, and
a question the index cannot answer is a reason for the index to grow, not for a handler to walk the
program.

**It is recorded where the compiler resolves each name.** The check walk already decides, for every
name it lowers, which declaration that name means — the innermost binding still in scope, the method
the dispatch picked, the field of the struct the target's type names. With `want_index` set it writes
that decision down in the branch that makes it, so the declaration the index gives for a use is the
one the compiler lowered it to — this page's rule, applied to names.

**Two answers are derived rather than recorded, because the compiler never resolves them.** A TYPE
NAME is looked up in the type registry the checker reads, after the walk, since no lowering passes a
type; a TYPE PARAMETER is matched to the innermost declaration whose extent holds it, since
substitution removes it before anything could resolve it. Both are what `make lsp`'s positions and
rename properties hold to the compiler.

**It is the same walk.** `check_lint_index` is `check_and_lint` that also hands back the index, and
the cost is recording, not walking, and `make lsp`'s one-walk case measures a check that builds it. A
build asks for no index and pays one test per recording site — the tables beside the walk do not
even grow a column.

**The key is the declaration's name token** — its file, line and byte column. A generic's
specializations are copies that keep their template's place, so a template called at two types is
**one** declaration with every call among its references. A declaration is recorded as its own use,
so the references of a name include where it is declared and a cursor on the declaration answers too.

| Name at the cursor             | Answers with                                                         |
| ------------------------------ | -------------------------------------------------------------------- |
| a binding, a parameter         | the innermost binding still in scope — shadowing is resolved         |
| a closure's captured name      | the binding the closure copies                                       |
| a call, a function value       | the function; a generic's template                                   |
| `name: value` in a call        | the parameter it names; in a construction, the field                 |
| a method                       | the implementation the dispatch picked                               |
| a method inside generic code   | the spec requirement, when the type parameter's bound declares it    |
| a method a body resolves twice | the spec requirement both implementations keep                       |
| a field, `?.`, a variant       | the field, the variant, the associated function or value             |
| a field a body resolves twice  | every field it resolved to — a generic `x.n` at two types is SHARED  |
| a namespace, `ns.f`            | the `import` that bound it in this file, and the member it names     |
| a type name, a type parameter  | the declaration; the `[T]` whose scope holds the use                 |
| a built-in, an intrinsic       | nothing — `null`, which is the honest answer for a name nobody wrote |

**An aborted check drops it.** Every check replaces the session's one slot, and only a walk that
reached its end stores a new one. A buffer that stops lexing, loading or lowering answers both
requests with `null` until the next check that completes, rather than with the positions of a program
the compiler no longer agrees is this one.

**Positions are converted once.** The compiler records a 1-based line and a byte column; the index is
converted to a 0-based line and UTF-16 columns as it is stored, counted by the one rule the
diagnostics' columns are (`ls_utf16_from`), and the sources are not kept. What stays is columns of
integers and the declared names — on the compiler's own program, a few megabytes against a check
that peaks in the hundreds.

**A shared use answers with every declaration it may be.** A generic body is walked once per
instantiation, and a field it reads through a type parameter — `x.n` called with a `P` and a `Q` —
resolves to `P.n` in one walk and `Q.n` in the other. The index keeps both as candidates, so
`definition` answers with a LIST of locations (LSP allows one), and the use is among the references
of each. Neither is "the first walked": the answer may not depend on the order of instantiation. A
method that resolves two ways answers instead with the spec requirement both implementations keep,
when there is one. So `definition` answers ONE `Location` object for a name with one declaration and
a `Location` LIST for a shared use, ordered by where each is declared — file, line, column.

`make lsp` holds it three ways, and each catches a different wrong index. Hand-written **positions**:
a shadowed binding, a parameter beside a local of its name, a generic method called at two types, a
call through a spec bound, a method, a field and a named argument a body resolves two ways (each
asked again with the instantiations the other way round, since the answer may not depend on which
was walked first), a variant and an associated name reached through a type a binding shadows, a
closure's capture, an `impl`'s type parameter, a named argument, a construction's field, a `?.`
field, a destructured binding, a member of another file, a namespace, the standard library, an
intrinsic, and a name after a line of CJK. **Symmetry**: every name in the fixture that has a
definition is among that definition's references, and no declaration is left with none while a name
spelled like it answers nothing — the shape a dropped use leaves. The shared uses are derived from
what the index answers and must be exactly the ones the fixture writes, in both directions. And
**rename**, held to `zerg build --emit check` and to what the program prints: renaming a declaration
and every reference to a fresh name leaves both as they were, and renaming every reference but the
declaration does not build — a reference the index missed is a use left under the old name, and one
it attributed to the wrong binding reads another value. A shared use and every declaration it may be
are renamed together, as one group. What the rename leaves out — the standard library, an import's
path, a contract — has a floor per reason, so a filter that grew could not turn the property green
by renaming nothing.

**What it does not do.**

- **Read a comment.** The index finds the declaration and stops there; the document that declaration
  carries is [the hover](#hover-is-the-declarations-document), extracted when it is asked for.
- **Completion, signature help, workspace symbol, rename.** Each is a view of this index, and each is
  #194.
- **A template nobody instantiated.** A generic body is walked once per specialization, so a template
  no call reaches is never walked and its names have no entries.
- **A contract.** A spec requirement and each method keeping it are separate declarations. Renaming
  one alone breaks the program by design, and what a rename does with a contract is #194's to decide.
- **A name an or-pattern binds.** `A(x) | B(x)` binds `x` on each side, and the body reads whichever
  matched — two declarations for one name, so it answers `null` rather than one of them.
- **One declaration for a shared use.** A field, a named argument, a variant pattern or a method a
  generic body resolves two ways — the method with no common requirement — answers with every
  declaration it resolved to, never with whichever instantiation was walked first. Which of them a
  rename should take is #194's to decide.

## Hover is the declaration's document

`textDocument/hover` is what an editor shows when the cursor rests on a name. The
[index](#a-name-answers-from-one-index) says which declaration that name is; what is shown is the
**document `zerg doc` prints for it** — the comment above it, and the signature the compiler spells.

> **There is one reader of comments in this tree.** A hover that scanned for `#` itself would be a
> document the terminal does not have and a document the editor does not share.

So the text comes from `doc_decl_at`, which is `zerg doc`'s own extraction asked for one place rather
than for a whole module: the same attachment rules — a run of whole-line comments directly above the
declaration, a decorator is not a break, a banner claims nothing ([Which comment documents which
declaration](doc.md#which-comment-documents-which-declaration)) — the same signature from the
compiler's type printer, and the same `(undocumented)` where nothing was written. One extraction, two
readers.

**It is asked for every declaration, and `zerg doc` for the exposed ones.** That is one walk with two
questions, not two answers: a document is what a module _exposes_, and a cursor is on whatever the
author is looking at — the compiler's own sources are private almost throughout, and a hover that
went quiet inside them would be a tool for reading other people's code only.

**The index carries positions, not text.** A comment kept per declaration would be a string per row
held for the whole session and paid on every check, whether or not anybody hovers — the accumulation
that issue #23 measures. What is stored is what `definition` already needed, and the document is
extracted when the question is asked: **one parse of the one file the declaration is in**, kept by
nothing afterwards.

**The peak is the check's, and a hover does not add to it.** One check of the program rooted at this
compiler's own entry peaks at **0.156 GB**; the same check followed by one, ten and a hundred hovers
peaks at 0.155, 0.162 and 0.156 — a spread no wider than the one between two runs of the same
measurement (0.156 and 0.153). Five checks and no hovers peak at **0.188 GB**, and five checks with
five hovers at 0.182: another check costs more than a hundred hovers do. What a REQUEST leaves
behind is below what a peak can see — three hundred `definition` requests leave it where one check
put it (0.154 against 0.156) — and what a finer measurement does find there, a `definition` request
leaves as much of as a hover: it is the request path's residue, not this answer's.

**The source is the loader's.** The declaration is routinely in another file — a standard library
function, a sibling of the module — so the whole program is loaded the way a check loads it, with the
unsaved buffer standing in for what is on disk. A hover therefore answers about the text as it is
being typed, and a load that raises answers with what the index knows and nothing more. It answers
only for a buffer the client has **open**, because that buffer is what the document is read from.

**And it is fetched only when it is somewhere else.** The declaration under the cursor is usually
declared in the file under the cursor, whose text the client already sent — and it is the same text
the loader would have substituted — so the program is loaded only for a declaration in ANOTHER file.
It is not loaded at all for a kind no document can hold: `doc_covers` is the extraction's own answer
to that, asked before paying for a source rather than after reading nothing out of it, so a hover on
a local binding or a parameter — most of the identifiers in a body — loads nothing. What the
remaining case costs is worth writing down: on the program rooted at this compiler's own entry, a
hover that must load it takes about half a second, nearly all of it turning the buffer back into that
program; the parse of the one file is the small half, and a one-file program is immediate.

**It is markdown.** LSP's `MarkupContent` takes either, and a doc comment in this tree is already
prose with ` ```zerg ` fences in it, so it is passed through as written and an editor renders the
examples as examples. The signature goes in a fence of its own above the prose, which is the line a
reader is looking for. Plain text would show every fence as three backticks.

| The cursor is on                  | The hover shows                                    |
| --------------------------------- | -------------------------------------------------- |
| a documented declaration          | its signature, then its comment                    |
| one with no comment               | its signature, then `(undocumented)`               |
| a field, a variant, a requirement | the line the document prints, then its own comment |
| a method                          | its signature, the receiver dropped                |
| a private declaration             | the same, though no document lists it              |
| a binding, a parameter            | what it is, and what it is called                  |
| a type parameter, a namespace     | the same                                           |
| a built-in, an intrinsic          | nothing                                            |

A binding is **not** marked `(undocumented)`. Nobody could have written a comment for it, so the mark
would be a complaint about the author rather than a fact about the code; what it gets is the word for
what it is — `binding`, `parameter`, `type parameter`, `import` — and its name. A **private**
declaration is marked, and that is the same rule rather than an exception to it: the mark answers
whether a declaration carries document text, and a private function could have carried some. It is
about what the author wrote, not about what the page lists.

**A shared use shows every declaration it may be**, in the order `definition` lists them, one under
the next. A hover that picked one of them would be the editor saying something the jump does not.

**And three answers are nothing:** a name nobody declared, a position in a file this session never
opened, and a buffer whose check **aborted** — every check replaces the one index and only a walk
that reached its end stores a new one, so a hover after a refusal would be a document for a program
the compiler no longer agrees is this one.

`make lsp` holds the two texts together as a **property**, not a transcript: for each declaration it
hovers, it asks `zerg doc` for the same one and compares the signature and the comment under it. The
entry is found by the **declared name the case asked about**, never by the text the hover answered
with, and there must be exactly one of that name — two declarations printing one signature (`log`
has a `Logger.trace` and a free `trace`) would otherwise let a lookup keyed on the answer pick the
wrong entry and agree with itself.

Prose is compared **paragraph by paragraph** with whitespace collapsed, because `zerg doc` wraps to
a page and a hover does not; a **fence is compared line for line**, because a ` ```zerg ` block
inside a comment is code and its lines are not prose to reflow. Their **order** is compared with
them — the blocks line up in the sequence they were written. Collapsing both would let a hover that
joined a comment into one line agree about every word while destroying every paragraph break and
every example in it.

It holds the shape of the answers that are not a document — the mark, its absence on a binding and
its absence on an uncommented member, both of which the fixture writes and hovers — and it holds
hover to the index itself: over every identifier **of the open buffer**, hover answers for exactly
the names `definition` answers for. Only of the open buffer, and that is the one place the two
differ by design: a hover is read out of the text the client sent, so a position in a file this
session never opened has none, while `definition` answers from the index for any file of the
program. A hover that read the comments a second way disagrees about a word; one that answered a
type instead of a document disagrees about all of them.

## A grammar written twice

`editors/tree-sitter-zerg` is a **tree-sitter** grammar for Zerg — a real parser, for the
editors that want a tree rather than a pattern.

```sh
make -C editors treesitter    # generate, build, install the parser and its queries
:lua vim.treesitter.start()   # in a .zg buffer
```

**It breaks this page's rule and cannot help it.** Everything else here is held to the compiler
by calling it, and where an editor file must repeat a language fact a diff holds the two
together. A tree-sitter grammar is a **second implementation of `GRAMMAR`** — around a hundred
productions — and nothing can diff a tree-sitter rule against a BNF production or against
`parser.zg`.

So what holds it is a **corpus**: `make treesitter` parses every `.zg` file in the tree — the
compiler's own sources, the standard library, the examples, and the private corpus when it is
checked out — and fails on a single `ERROR` or `MISSING` node. That is weaker than the other
gates on the board, in exactly the way `fmt-corpus` is: it can only see a form some file
contains. It is the strongest check available for a grammar written twice, and it is why the
file list is everything rather than a sample. One part _is_ diffable and is diffed —
`editor-align` holds the grammar's keyword list to `lookup_keyword`, the same fact it already
holds the vim file's to.

**What it buys.** `syntax/zerg.vim` colours by regular expression and admits its load-bearing
guess in its own comment: `\<\u\w*\>` makes every capitalised word a type, "a highlight
heuristic, not a grammar rule", because a highlighter that cannot parse cannot tell a type from
a variant from a constructor call. A parser does not guess — a lowercase type name is coloured
correctly, an f-string's holes are highlighted as the expressions they are, and folds follow
the construct rather than the brace.

**The generated parser is not committed.** `grammar.js` produces close to seven megabytes of C,
larger than the rest of this repository put together and derived entirely from a file already
under review. `make -C editors treesitter` writes it; `.gitignore` keeps it out. That is also
why it is its own target rather than part of `make -C editors install`: it needs node, which
this toolchain does not, and an install that failed on a missing editor tool would be the worse
trade.

**Two things it needed a scanner for.** A newline is a statement separator (`GRAMMAR#stmt-sep`)
and is insignificant inside a group, which is the standard automatic-semicolon problem: the
scanner is asked only for the tokens the parser can accept, so a newline becomes a separator
exactly where a statement could end. And a literal's contents are not code — `comment` is an
`extra`, so it is a candidate at every position, and `#` inside `f"{recv}#{name}"` matched
further than any string rule and swallowed the closing quote. Neither a token precedence nor an
`immediate` token settled that; being asked first does.

## Keeping the editor honest

Everything else in this tree is held to the compiler by **calling** it — `zerg fmt` is the formatter,
and the server asks `check_lint_index` rather than checking anything itself, so there is no second copy
to drift. The editor files are the one exception and cannot be anything else: vim highlights from a
keyword list written in vimscript, and nvim has to know how to indent before any Zerg tool has run.

So those facts get a gate of their own — `make editor-align`:

- every reserved word `lookup_keyword` returns is one `zerg.vim` colours, and every word it colours
  as a keyword is one the lexer reserves (built-in **type** names are held to the parser's list
  instead, since `int` is an ordinary identifier the lexer has never heard of);
- the indent **character** the ftplugin and `.editorconfig` configure is the one `zerg fmt` actually
  **writes**;
- the indent **width** they configure is the one `F403` measures a tab as. That is not decoration:
  F403 decides whether a line has run past column 120 by counting a tab as `fmt_wrap_tab()`, so an
  editor displaying it as anything else is applying a different 120-column rule than the formatter
  did. One number, three places, one gate;
- the **ruler** the ftplugin draws is one past `fmt_wrap_max()` — the column a flat group must end
  before. A ruler in the wrong place looks exactly like a ruler, which is why this one is read out of
  the formatter rather than written down twice.

`.editorconfig` is there for the editors this repository ships no plugin for — VSCode, JetBrains,
Emacs, Zed all read it — and it is held to the same probe as the ftplugin rather than to the
ftplugin, so the two cannot agree with each other and both be wrong.

Neither is hypothetical. `zerg.vim`'s own comment records `close` having been missing from its list
"entirely — the statement that ends a stream has never been coloured", found by reading. And the
ftplugin set `expandtab` with a four-space shift while `F101` indents with a **tab** and `make
fmt-self` holds every source in the tree to it — so a person typing in nvim produced spaces that the
next save turned into tabs: a whole-file diff per write, from the editor and the formatter
disagreeing about one rule. Both are fixed here, and now both are measured.

The rule the two gates express: **the server may not know a language fact the compiler could tell
it, and where an editor file must repeat one, a diff holds the two together.**

## What is not built, and what each one is waiting on

Tracked as issue [#15](https://github.com/cmj0121/zerg/issues/15).

| Missing                                           | Waiting on                                                    |
| ------------------------------------------------- | ------------------------------------------------------------- |
| `completion`, `signatureHelp`, `workspace/symbol` | #194 — views of the same index                                |
| `rename`                                          | #194 — and what a rename does with a spec contract            |
| `semanticTokens`                                  | `Kind`'s variants cannot be matched outside the `zerg` module |
| a diagnostic **end** position                     | the compiler tracks where a thing starts, not where it ends   |
| incremental sync, debounce, cancellation          | a measurement; Phase 1 re-checks the program per keystroke    |

Those rows and the hover above them were one gap, and [the name
index](#a-name-answers-from-one-index) is what closed its first half: given a path and a position,
what is declared there and where. [The hover](#hover-is-the-declarations-document) is the second half
— the document of the declaration it finds — and what is left reads the same index rather than
building a second one: the declarations in scope at a position, and every use of one.

`semanticTokens` is a different kind of missing and worth naming as such: it would need a table
mapping token kinds to LSP token types, which is exactly the sort of **repeated list of language
facts** the section above exists to prevent. The vim syntax file already highlights Zerg, and it is
gated.

The last row is a cost, not a gap. The scheduler is cooperative and non-preemptive, so a long check
occupies its worker until it finishes; `emit.zg` is the worst case in this repository and is the
number to measure against before designing anything here.

**The memory half of that cost is closed.** One check of the program rooted at
`src/compiler/zergc.zg` — which is what opening **any** file under `src/compiler/` asks for, now that
a module member is checked against its module, and which was 24 files when this was measured and is
27 today — used to take 6.7 s and peak at **6.7 GB**, and a
long-lived session was killed by the operating system after three or four of them. The ceiling was
the **emitter's**, not the protocol's: the compiler's checks live inside the lowering walk, so the
only way to reach them was `emit_files_diag`, which lowers the whole program to C first.

`check_files_diag` is that walk with the C dropped rather than accumulated, and the same check now
takes **4.9 s and peaks at 0.32 GB**. In one session: before, SIGKILL after the third check; after,
sixty published, 2.9 GB, and no slowdown across them. The saving is not the size of the C — 3.6 MB —
but the shape of building it: `defs = defs + c_fn(…)` copies everything emitted so far on every one
of ~1500 steps. `make check-equal` is what keeps the two paths honest, and it compares their
diagnostics byte for byte because a check that finds LESS than the build finds is an editor showing a
clean buffer for a file that will not compile.

**So is what a check left behind.** A long session still climbed after that — the figure written
here was 0.32 GB after one check and 2.9 GB after sixty — and measured again once the second walk
below was gone it was smaller and still a climb: a peak footprint of 0.12 GB after one check, 0.18
after twenty, 0.21 after sixty. Nothing the SERVER held was growing; its heap between checks was a
few megabytes. The leak was in the code the compiler generates, which the check is written in: about
two megabytes a check of small cells, one per identifier the walk read. A field or an element read
out of a value nobody held (`cur(p).lexeme`) was copied a second time; a `match` on a value it made
never gave that value back; an owned `str` that was compared, and an owned argument handed to a
runtime intrinsic, were never released. Each cell was tiny; what cost was where they sat, one to a
page across the allocator's regions, so the pages they pinned were pages the next check could not
reuse. Fixed, a check leaves a few kilobytes, and the footprint settles within the first twenty
checks and stays: 0.12 GB after one, 0.15 after twenty, 0.16 after sixty. The fix is one rule —
an expression that names no storage hands over a value of its own — and holding the compiler to it
meant making it true where it was only assumed: an `if` whose branch names storage, a `?` on a
carrier that does and an optional chain each hand over their own, and a value a statement discards
is given back where it is dropped. A channel end is counted rather than copied, so it has a question
of its own, and the consumers that count one ask it: an end is counted once when a binding, a
return, an argument to a free function, a spawn or a `for … in` takes it. What does not hold
yet, the same on `main`: an owned end — a call's result, `mk()` — handed to a method argument, a
struct field, a list element or a `defer` argument keeps its count, and a `defer` of one can
deadlock rather than only leak; and a statement that discards a channel end, `buffered()` on its
own, keeps its count too, because the discard gives back values that are copied, and a channel is
counted. `make lsp` holds a session of N checks to K times a session of one, which sees the leak in
aggregate; `make mem-check` holds each leaking shape to zero, which is the per-shape guarantee; and
the corpus runs every one of those consumers under the sanitizers.

**The second walk is gone too.** Roughly half of a check's time was that `publishDiagnostics` walked
the program TWICE — once for the errors, and once more inside `lint_program` for the `L5xx` conversion
lints, with its own merge and its own walk. `check_and_lint` answers both from one walk: the notes are
kept on the way past, which changes no error, so the errors are still the ones `check-equal` holds to
the build's. `make lsp` measures what one check of a file under `src/compiler/` costs against
`zerg build --emit check` on the same program — one walk by definition — in instructions retired,
from outside the process: 2.07 walks before, 1.09 after, and it fails at one and a half. Instructions
rather than seconds, because the seconds depend on the machine and which core the process lands on,
and the instructions are the work. A debounce would hide what is left.
