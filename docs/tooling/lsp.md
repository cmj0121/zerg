# Zerg Language Server

`zerg lsp` — the compiler answering an editor's questions instead of a shell's. Part of the
[Language Reference](../language.md). Also in [繁體中文](lsp.zh-TW.md).

```sh
zerg lsp        # speak JSON-RPC 2.0 over stdin/stdout; the editor starts and stops it
```

## The claim

The language server is **not a new program**. It is the compiler that already exists, asked a
different question: not _"lower this to C"_ but _"what is wrong with this buffer, right now"_. The
compiler has always answered that one — `emit_files_diag` is what `zerg build` calls — so what ships
here is the plumbing that carries the answer to where a person is looking.

That is also the invariant, and it is enforced rather than asserted:

> **If the server disagrees with `zerg build` about a program, the server is wrong.** It has no
> analysis of its own.

`make lsp` is that sentence as a gate. It drives a real session over stdio for every example and 40
corpus programs, and holds what the server publishes to what `zerg build` and `zerg lint` say about
the same file — errors against the command that refuses over one, information findings against the
command that reports one. It is `make oracle`'s argument applied to the second front end.

It carries **ten protocol cases** beside that, and every one of them failed once: the exit status, the
post-shutdown reply, an empty change, an incremental change, a full change, a `$/` notification versus
a `$/` request, a malformed frame, a string id, a UTF-16 column after a line of CJK, and a body larger
than one read of the runtime's bounded leaf. Those are a different kind of failure and a quieter one —
an editor with a corrupted buffer, or a client left waiting, reports nothing at all.

## Where it lives

`src/compiler/lsp/` — a module of its own, importing `src/compiler/zerg/` across the `pub` boundary
like any other consumer, wired in by one more `.sub(lsp_cmd())` in `zergc.zg`.

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
| `textDocument/publishDiagnostics`                             | `lex_diags`, `emit_files_diag`, `lint_conversions` |
| `textDocument/formatting`                                     | `fmt_src_off` — the same function `zerg fmt` calls |
| `textDocument/codeAction`                                     | the `fix` a finding carries, as one quick fix      |

Every other request is answered with a **method-not-found error**, not with silence. A client left
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

**Two severities, from two places.** An **error** is what `emit_files_diag` reports and `zerg build`
refuses over. The `L5xx` conversion findings are about **legal** programs — a literal that took a type
the page does not show — so they arrive as **information**. A server that paints a working
program red teaches its user to ignore red.

**A code travels as a code.** `Diag` carries the rule's identity — `E307`, `L502` — in a field of
its own, so the server sends LSP's `Diagnostic.code` and an editor can filter, group and link by it.
That is the rule this page is about, applied to itself: a code spelled only inside the sentence is
one every reader has to parse back out, and the server reading it out would be a second copy of a
language fact. A finding with no code omits the field rather than sending an empty one.

**An abort has no position.** A parse error and a `NotImplemented` refusal are `raise`d sentences,
and the compiler does not carry a place on either — so they land as a zero-width range at the top of
the file with the compiler's own words. Not the word at 1:1: an underline drawn under `fn` says the
`fn` is wrong, and the one thing known about such a finding is that nobody knows where it is.

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

`make -C editors install` symlinks the syntax files and two more:

- `ftplugin/zerg.lua` — starts the server for a `.zg` buffer;
- `lua/zerg/lsp.lua` — `vim.lsp.start` (nvim 0.8+), no plugin manager and no `nvim-lspconfig`.

```lua
vim.g.zerg_lsp = false            -- do not start the server
vim.g.zerg_lsp_cmd = { 'zerg', 'lsp' }
vim.g.zerg_format_on_save = true  -- zerg fmt on every write
```

It answers **quietly** when `zerg` is not on `PATH`. A server that errors on every `.zg` file opened
in a checkout without a built toolchain is one a person disables and never re-enables.

The quick fixes need no configuration — `vim.lsp.buf.code_action()` is nvim's own, and the server
declares itself a `quickfix` provider so a client that asks for that kind alone still gets them.

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
cannot compile ([Formatter & Linter](fmt.md)); knowing that `1` became a `float` needs types, so a
formatter that did this would fail in exactly the buffer a person reaches for it in. It is also an
opinion — `1.5 + 1` is a legal program — and the formatter has none.

## Keeping the editor honest

Everything else in this tree is held to the compiler by **calling** it — `zerg fmt` is the formatter,
and the server asks `emit_files_diag` rather than checking anything itself, so there is no second copy
to drift. The editor files are the one exception and cannot be anything else: vim highlights from a
keyword list written in vimscript, and nvim has to know how to indent before any Zerg tool has run.

So those facts get a gate of their own — `make editor-align`:

- every reserved word `lookup_keyword` returns is one `zerg.vim` colours, and every word it colours
  as a keyword is one the lexer reserves (built-in **type** names are held to the parser's list
  instead, since `int` is an ordinary identifier the lexer has never heard of);
- the indent **character** the ftplugin and `.editorconfig` configure is the one `zerg fmt` actually
  **writes**;
- the indent **width** they configure is the one `F403` measures a tab as. That is not decoration:
  F403 decides whether a line has run past column 80 by counting a tab as `fmt_wrap_tab()`, so an
  editor displaying it as anything else is applying a different 80-column rule than the formatter
  did. One number, three places, one gate.

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

| Missing                                       | Waiting on                                                       |
| --------------------------------------------- | ---------------------------------------------------------------- |
| `hover`, `definition`, `references`, `rename` | nothing maps a position to a declaration                         |
| `completion`                                  | the same query surface                                           |
| `documentSymbol`                              | `File` is `pub`; `FnDecl` and its siblings are not               |
| `semanticTokens`                              | `Kind`'s variants cannot be matched outside the `zerg` module    |
| a `zerg lint` finding's **code** as data      | those rules answer `list[str]` and render their code into it     |
| a diagnostic **end** position                 | the compiler tracks where a thing starts and not where it ends   |
| the `lint_files` findings                     | they answer `list[str]` and carry no position to place           |
| incremental sync, debounce, cancellation      | a measurement; Phase 1 re-checks the whole program per keystroke |

The first row is the real gap and everything interactive is behind it. The information exists —
`check.zg` computes all of it — and is discarded after the build. What is needed is not those types
made public one by one but a **query surface**: given a path and a position, what is declared there,
where was it declared, and what is its type.

`semanticTokens` is a different kind of missing and worth naming as such: it would need a table
mapping token kinds to LSP token types, which is exactly the sort of **repeated list of language
facts** the section above exists to prevent. The vim syntax file already highlights Zerg, and it is
gated.

The last row is a cost, not a gap. The scheduler is cooperative and non-preemptive, so a long check
occupies its worker until it finishes; `emit.zg` at 9264 lines is the worst case in this repository
and is the number to measure against before designing anything here.
