# Zerg Documentation Tool

`zerg doc` — what a module exposes, and the comments that document it, read from a terminal.
Part of the [Language Reference](../language.md). Also in [繁體中文](doc.zh-TW.md).

```sh
zerg doc                      # every module that can be read
zerg doc strings              # that module's whole document
zerg doc --brief strings      # its exposed surface, one line each
zerg doc strings.split        # one declaration; log.Logger.level for a method
zerg doc src/stdlib/log.zg    # a file, or a directory, documented where it stands
```

## The claim

**The source is the only copy of the documentation.** There is no second document to keep in
step, no comment copied into a page, and nothing here a writer maintains apart from the code.
What `zerg doc` prints is read out of the source every time it is asked.

That is the reason for every decision below, and it is what the tool is held to:

> **A declaration this tool leaves out makes the library look more complete than it is.** A
> reader cannot tell a module with nothing else in it from a module whose extraction stopped
> matching.

`make doc-check` is that sentence as a gate. It reads the `pub` declarations out of the source
with `sed` — a second opinion, deliberately coarse — and compares them by name against the
names in the document, **both directions**, for every standard library module.

## The four questions

They are read in this order, and the order is the whole of the disambiguation:

| What you name          | Answers with                                                    |
| ---------------------- | --------------------------------------------------------------- |
| nothing                | every module that can be read, with the first sentence of each  |
| a **path** that exists | that file, or that directory's files, documented where they are |
| a **module**           | that module's whole document                                    |
| `module.name`          | one declaration — `log.Logger.level` for a method               |
| anything else          | a refusal that lists what it can see, exit 1                    |

A path is recognised **only when it is really there**: a name carrying a `/` or ending in `.zg`
that names nothing on disk is a mistake, not a path, and it falls through to the same refusal
every other unknown name gets. A whole module is tried next, because `strings` is the common
question and is not a declaration of anything. Only then is the name split, **at the first `.`
and not the last** — splitting at the last leaves `log.Logger`, which resolves to nothing and
turns a question that has an answer into a refusal.

**Nothing here builds anything.** Reading a module is not importing it, so the checks an
`import` is held to go unasked: a module that ships and cannot be compiled is still a module
that can be read.

`--brief` shortens a **listing** — each declaration becomes its signature and the first
sentence of its comment, with fields, variants and methods left out. It is the same walk and
the same code the module index uses, so a declaration it dropped would disappear from the
first page a reader ever sees. It is not read for a single declaration: a listing of one is
the thing itself, so `zerg doc --brief strings.split` prints the whole entry.

## What is exposed is what is documented

Every `pub` form is in the document, and nothing else is:

| Form                | Shown with                                                         |
| ------------------- | ------------------------------------------------------------------ |
| `pub fn`            | its signature, including a method flattened out of an `impl` block |
| `pub struct`        | its **public** fields                                              |
| `pub enum`          | its variants                                                       |
| `pub const`         | its value, when that value is a literal                            |
| `pub type`          | what it stands for                                                 |
| `pub spec`          | its requirements                                                   |
| a `pub mut` binding | as it is written, filed under the constants                        |

A module-level `pub mut` binding is shown as it was written — `mut COUNTER := 0` — and the
`unsafe { }` group it has to sit in is **not** printed around it, though an `unsafe fn` does
keep its keyword. An `unsafe` group is a property of the module, not of the binding's line.

A private declaration is not documentation — `main` is in no document, and neither is anything
else without `pub`. A struct shown with two of its six fields would describe a literal that
will not compile, so one whose private fields were left out says `(private fields not shown)`.

**An exposed declaration with no comment is shown and marked `(undocumented)`.** It is never
omitted. The mark is keyed on there being no comment **at all**, and on nothing else: a comment
made only of a worked example is a comment, and calling that undocumented would be the same
lie as the silence, pointing the other way.

A **signature is spelled by the compiler's own type printer**, not by a second one. A signature
in a document and a signature in a diagnostic cannot disagree about the language, because there
is one function that writes both.

## Which comment documents which declaration

Comments are read from the **lexer's token stream**, not by scanning the source for `#`. That
is why a `#` inside a string literal is not a comment here, and why a `#[derive(Eq)]` is a
decorator rather than a note.

What a comment is attached to is decided by line geometry, and these are the rules a writer
has to know:

| In the source                                                   | In the document                           |
| --------------------------------------------------------------- | ----------------------------------------- |
| a run of whole-line comments directly above a declaration       | documents that declaration                |
| a **blank line** between the run and the declaration            | documents **nothing**                     |
| a `#[…]` decorator between the run and the declaration          | documents it — a decorator is not a break |
| a run opening with a `# --- banner ---`                         | documents **nothing**, whole run          |
| a comment at the end of a line of code                          | documents **nothing**                     |
| a comment whose whole body is a worked example                  | documents it                              |
| the first run in the file, above all code, that nothing claimed | is the **module header**                  |

A blank line needs no rule of its own — it emits no token, so the run simply ends further up
than the line above. A **section banner** is a mark on the file: it divides the source into
chapters for someone reading the source, and what a chapter is called is not what the first
declaration under it is called. The whole run goes, not only the dashes, because the prose
under a banner is written about the group and handing it to one declaration would attribute a
paragraph to something it was never about.

The **module header** is the run nothing claimed: first in the file, no code above it, and
still unclaimed once every declaration — private ones included — has taken its own. An
`import` claims nothing, which is what tells a file header sitting flush against
`import "ascii"` from a comment documenting the first declaration.

**The file named after the module is the one that speaks for the module** in the index. A
directory module without such a file has no module-level description, and the index says
nothing rather than something.

Here is every one of those rules in one file:

```zerg
# tally — counting things, and the comments that document them.
#
# This first run belongs to the FILE: no code stands above it and no
# declaration below it claims it.

# LIMIT is the largest tally this module will count to.
pub const LIMIT: int = 64

# --- the counter ----------------------------------------------------------
# A run that opens with a banner documents nothing, and the whole run goes.

pub struct Counter {
    pub n: int
}

# this comment is cut off from the declaration by a blank line

pub fn reset(c: Counter) -> Counter {
    return Counter(0)
}

# Bumped is a counter already raised, and the decorator does not detach this.
#[derive(Eq)]
pub struct Bumped {
    pub n: int
}

# hashy is documented by this comment, and the `#` in the string below is not one.
pub fn hashy() -> str {
    return "# not a comment"
}

fn main() {
    print LIMIT
}
```

and here is what `zerg doc tally.zg` prints for it:

```text
tally — counting things, and the comments that document them.

This first run belongs to the FILE: no code stands above it and no declaration
below it claims it.

CONSTANTS

  const LIMIT: int = 64
      LIMIT is the largest tally this module will count to.

TYPES

  struct Counter
      (undocumented)

      n: int

  struct Bumped
      Bumped is a counter already raised, and the decorator does not detach
      this.

      n: int

FUNCTIONS

  fn reset(c: Counter) -> Counter
      (undocumented)

  fn hashy() -> str
      hashy is documented by this comment, and the `#` in the string below is
      not one.
```

`main` is not in it, `Counter` lost its banner, `reset` lost its detached comment, `Bumped`
kept its comment across the decorator, and the string's `#` was never read as one.

The last rule has a live case rather than a fixture. `json.null`'s whole comment is one worked
example and its output, so `zerg doc json.null` prints the example and does **not** mark it.
Under `--brief` it prints the signature and stops: a comment with no sentence in it has no
summary, and a blank line meaning "documented, but not in a sentence" is a distinction no
reader can see.

## The form of an example

A worked example lives in the comment, as a pair of fenced blocks: **one expression per line** in a ` ```zerg `
fence, and what those lines print in an ` ```output ` fence written directly beside it. Nothing declares the lines
and nothing wraps them — the runner puts `print` in front of each one, in source order, and diffs what came out
against the `output` block **line for line**.

````text
# ```zerg
# strings.contains("hello world", "o w")
# strings.contains("hello", "z")
# ```
# ```output
# true
# false
# ```
````

Every example in one file becomes **one program**, so the lines run in source order in a single process and a later
one sees what an earlier one did. Both fences are carried into the document exactly as they were written, like any
other fenced block.

Two kinds of function cannot have an example in that form, and both are in the standard library today rather than
hypothetical:

- **One answering a `list`, a `map` or a struct.** `print` renders no composite in this compiler (`E9059`), so the
  example has to reduce the answer to something printable. `strings.split` detours through `join` for exactly that
  reason — `strings.join(strings.split("a,b,c", ","), "|")` prints `a|b|c`, which shows the pieces **and** that
  `join` inverts `split`. Issue [#16](https://github.com/cmj0121/zerg/issues/16) files it as `E449`, a composite
  has no rendering.
- **One answering nothing.** `print` of it needs a value and is handed nil (`E3086`), so `os.set_env` carries an
  **indented illustration** instead of a fence — printed in the document exactly as written, and never run. #16
  files that one as `E390`; the round trip is asserted in `src/stdlib/os_test.zg` instead, where a claim about a
  write can be made in full.

An illustration is the honest shape for either, and it is not an example: nothing executes it and nothing holds it
to what it says. That is the whole distinction — an example is a claim that is checked, and everything else in a
comment is a claim that is written down.

**`zerg doc --check` is not built**, so the fences are executed by
[`scripts/doc-examples-check.sh`](../../scripts/doc-examples-check.sh) instead, over the modules `mk/gates.mk` lists
in `DOC_EXAMPLE_SRCS` — `json`, `log`, `os`, `strings` and `time` — on the gate board under `stdlib-test`. A module
outside that list is not run at all: `cli.zg`'s one ` ```zerg ` fence has no `output` beside it and is a fragment of
a method chain, which is an illustration that happens to be fenced. Moving the run into the command is the rest of
[#17](https://github.com/cmj0121/zerg/issues/17).

## The shape of a document

Four indents, and every nesting is the same pair four columns further in:

| Column | Holds                                                             |
| ------ | ----------------------------------------------------------------- |
| 0      | the file's own header, and the section titles                     |
| 2      | a declaration's signature                                         |
| 6      | its comment; and a field, variant, requirement or method under it |
| 10     | that member's own comment                                         |

A file's section is its header, then **CONSTANTS**, **TYPES**, **FUNCTIONS** — each left out
entirely when there is nothing in it. A type's methods print under the **type**, in the section
the type was declared in rather than the one they were written in, because `impl T { … }` is
flattened into functions carrying a receiver long before this tool sees them.

Prose is **filled to 80 columns**, which is written down rather than asked of the device. A
fenced block and an indented line are carried through **exactly as written and never
re-wrapped**: the text inside one is meant to be copied out and run. A word longer than the
budget stands on its own line and is never cut, because what is long is a URL, a path or a
piece of inline code — text that looks right after a break in the middle and does not work.

The fill is measured in **display columns**, which is neither of the two counts that are easy to reach for. A byte
count reads the em-dash opening every stdlib header as three columns and stops the prose short of the margin for no
reason a reader can see. A rune count errs the other way and worse: a comment written in Chinese fills to 80
characters and prints at up to 160 columns, off the edge of the terminal the page was laid out for. So an East Asian
**Wide** or **Fullwidth** code point counts two, block by contiguous block, each block named in
[`doc_render.zg`](../../src/compiler/cmd/doc_render.zg) beside the range it covers.

What is left at one column, knowing better, is everything a block cannot say: the scattered single code points the
standard also calls Wide — the arrows, the dingbats, the emoji — a combining mark and a zero-width joiner, which
take no columns at all, and a grapheme cluster spelled with several code points. Those are a table and a block is a
comparison; the blocks are what text written in Chinese, Japanese or Korean is made of, and they are what the tool is
held to.

**A line may also end between two ideographs.** Chinese is written with no spaces in it, so a paragraph of it reaches
the fill as one unbreakable word, and a word longer than the budget takes the rule above and stands on its own line,
every line of it past the margin. A space is where a language that uses spaces allows a break; it is not the only
place a line may end. The cut is taken between two Han or Hangul characters and never beside a mark — `。` may not
open a line and `「` may not close one, and nothing here knows which side a mark belongs to, so a sentence with a
comma in the middle of it breaks somewhere else instead.

**The author's own line break becomes a space, or nothing.** Between two words it stands for the gap between them.
Between two **full-width** characters it becomes nothing: a language written with no spaces in it would otherwise
gain a character its author never typed, in the middle of a sentence the source has no gap in. That is a question
about WIDTH, and deliberately not the question above — a full-width comma is not a place a line MAY END, nothing
here knowing which side of the line a mark belongs on, and it is still a place no space belongs. When only **one**
side is full-width the space stays: `而 zerg doc 的排版` is how this tree's own zh-TW documents are written, and
that space is one the author put there.

`make doc-check` §7 is that as a gate. It documents a module whose comments are in Chinese and measures the display
width of every line that comes out: none over 80 columns, and the widest at least 70, or the paragraph was never
filled and the first assertion measured nothing. The same section reads the spaces, which no width can see: no line
carries one between two full-width characters, and none has lost the one between a Han character and a Latin word.

**A directory module is documented one file at a time.** One section per file, headed by the
path as a reader could type it back, each with that file's own header and that file's own
declarations. Concatenating every header instead opened `zerg doc src/compiler/zerg` with four
hundred lines of stacked prose before the first declaration — each of those headers is one file
arguing for itself, and read end to end they argue past each other. A module of one file, which
is every module in the standard library, has exactly one section and no heading above it.

A file with neither a header of its own nor an exposed declaration says `(nothing exposed)`. A
heading standing over a blank line reads as a rendering that broke rather than as a file with no
surface, which is the same failure `(undocumented)` keeps a declaration out of, one level up.

## Colour follows the terminal, and the shape never does

`NO_COLOR`'s **presence** decides first — any value at all, the empty one included — and after
that, whether stdout is a terminal. It is the line [`log`](../runtime/stdlib.md) already draws
and this command follows it.

Four spans carry colour and no more, all of them things the eye jumps between rather than
reads: the section titles, the keyword and name of each signature, the names in the index, and
the `(undocumented)` mark. A comment's own text is never coloured and **neither is a fence** —
an example is meant to be copied out, and an escape code pasted into a shell is a fault the
document introduced. Only the eight original SGR colours are used.

**The shape never changes with the device.** A pipe, a file and a terminal receive the same
characters in the same order; a terminal receives some SGR codes around four of the spans and
nothing else. `zerg doc strings > page` and `zerg doc strings | less` agree about where every
character is, which is what makes the output something a gate can compare against — and
`make doc-check` compares exactly that, by driving a pty, stripping the colour back off, and
diffing the result against the piped run byte for byte.

## A module this compiler cannot parse

`src/stdlib/atomic.zg` declares a generic struct, which this compiler refuses (`E9004`) — and
that refusal comes from the **parser**, so its declarations cannot be listed at all. The
document a reader gets is the honest one:

```text
atomic — the safe shared-mutable primitive (Phase 1f, bundle MVP).

  … the rest of the file's header, in full, and then …

note: `atomic.zg` does not parse under this compiler; its declarations are not
listed (see docs/runtime/stdlib.md)
```

The header survives because it comes out of the token stream and needs no tree. The `note:`
line is what keeps a missing chapter from reading as an empty one — a module rendered with no
declarations and nothing said is a module documenting itself as having no surface. It exits 0:
nothing failed, and the document of a module nobody can import is the only documentation that
module has.

The same note is printed for **any** file that will not parse, naming that file; the pointer
into the standard library's chapter is added only when the file is in the standard library.

## What is not built yet

Named here rather than left to be discovered, because a documentation tool that overstates
itself is the one failure it cannot recover from:

| Not built                                                | Issue                                            |
| -------------------------------------------------------- | ------------------------------------------------ |
| `--check` — build a doc example and diff its output      | [#17](https://github.com/cmj0121/zerg/issues/17) |
| `##` — the reader's document apart from the maintainer's | [#18](https://github.com/cmj0121/zerg/issues/18) |
| finding a declaration without naming its module          | [#19](https://github.com/cmj0121/zerg/issues/19) |
| static HTML pages                                        | [#20](https://github.com/cmj0121/zerg/issues/20) |
| `--serve`                                                | [#21](https://github.com/cmj0121/zerg/issues/21) |

`--check` is the second half of the issue this first version came from, deliberately left for
its own commit. Until it lands, the examples go on being run by
[`scripts/doc-examples-check.sh`](../../scripts/doc-examples-check.sh), over the modules
`DOC_EXAMPLE_SRCS` names — the form of an example, and what that script does with it, is a
section of its own above. Nothing in `zerg doc` overlaps it.

`##` waits **deliberately**. This codebase's comments are long and largely addressed to
maintainers, and separating the reader's half from the maintainer's half cannot be automated —
reading this tool's real output is how you learn where that line falls, so designing the marker
first would be guessing.

HTML will be a **second rendering of the same extraction**, never a second extractor. That is
why what a module contains and how it is laid out are already two separate pieces of code, with
neither owning the other. `--serve` is blocked on something larger: the language has no
networking at all.
