# Zerg fmt — canonical style decisions (v0.10)

This document records the canonical style decisions made by `internal/fmt`
and pinned by the v0.10 corpus measurement. Each decision was made either by
PLAN.md or by counting how the existing v0.0–v0.9 deterministic corpus
expressed the construct (on a tie the smaller side follows PLAN.md).

## Layout

- **Indent.** 4 spaces. PLAN.md. The v0_2 corpus (9 programs) uses 2-space
  and is the U3 corpus rewrite list.
- **Block braces.** K&R: `fn x() -> int {` opens on the decl line; the
  closing `}` lives on its own line. Corpus uniform.
- **Soft 100-col target.** Not hard-enforced at v0.10.

## Blank lines

- **Top-level and inside blocks.** Source-driven: preserve a single blank
  line if the source had one or more between two adjacent statements;
  normalise multiples to one; never auto-insert.
- **File-head.** A blank between `Program.HeadComments` and the first stmt
  is emitted iff the source had one (every v0_8 / v0_9 program with a
  `# requires:` line directly above the first decl carries no blank).

## Lists / decls

- **Multi-line list / enum variant / struct field.** No trailing comma on
  the final entry. v0_4 majority (8 programs) has no comma; v0_5 (2
  programs) does — corpus rewrite at U3 wins on the majority side.
- **Single-line list.** No trailing comma.
- **Struct literal.** `Name { f: v }` with a space before the brace.
  Corpus measurement: 32 with space, 4 without — majority wins.
- **Empty `impl` body.** `impl T for U {}` (no space between braces).
- **Imports.** One `import "name"` per line; grouped `import (...)` is
  desugared by the parser, so fmt emits the flat form.

## Sugar

- `T?`, `?.`, `??` — preserved as the user wrote them.
- **Parens.** Kept when source had them; never elided. "When in doubt,
  KEEP" — PLAN.md.
- **Binary op spacing.** `a + b` with single spaces around every binary
  op. One corpus program writes `(i+1)` — U3 corpus rewrite.
- **Numeric literal underscores.** Not preserved (lexer strips them; the
  AST has no record). One corpus program (`v0_1/04_int_literals.zg`)
  uses underscores — U3 corpus rewrite.
- **Column alignment.** Collapsed to a single space. One corpus program
  uses column alignment — U3 corpus rewrite.

## Match / select / anon-fn body shapes

- **Match arm.** `pat => stmt` for single-stmt arms, `pat => { ... }` for
  multi-stmt or block-bodied arms. Corpus uniform.
- **Select arm.** `op -> { ... }` always brace-form; one-line body inlined
  `op -> { stmt }`. Corpus uniform.
- **Anon-fn body.** `fn() { ... }`; single-stmt one-line body emitted as
  `fn() { stmt }` when the source had it on one line.

## Comments

- **Leading-line comments.** Emitted verbatim before the decl/stmt with
  the current indent and a `#` prefix.
- **Trailing inline comments.** Stripped (documented v0.10 limit).
- **`#` normalisation.** Single space after `#` (`# body`); empty body
  renders as a bare `#`. The lexer preserves whatever leading whitespace
  the user wrote inside the comment; fmt collapses it so re-parse yields
  the same AST and the formatter is idempotent.

## Corpus survey (v0.10 measurement)

Out of 186 programs in the v0.0–v0.9 deterministic corpus, 17 differ from
canonical fmt output:

- 9 — 2-space → 4-space indent (v0_2 corpus)
- 2 — multi-line enum trailing comma (v0_5)
- 2 — `Cat{...}` no-space struct-lit form (v0_6)
- 1 — grouped-import desugaring (v0_5/09_grouped_imports)
- 1 — `(i+1)` binary op without surrounding spaces (v0_7/08_wait_group_fan_in)
- 1 — column-aligned `:=` (v0_2/01_byte_rune)
- 1 — numeric literal underscore separator `1_000_000` (v0_1/04_int_literals)

Each of these is a deliberate corpus rewrite at U3, not an ambiguity in
the canonical decisions.
