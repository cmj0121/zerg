# Zerg — notes for coding agents

`AGENTS.md` is a symlink to this file. It holds working rules only; the language lives in the docs it points at.

## What this repository is

Zerg is a compiled language that emits C and hands it to `cc`. Two compilers are in the tree:

- `zerg` — the shipping compiler, written in Zerg and compiled by itself. Every status claim and spec marker is about
  it. Library in `src/compiler/zerg/`, drivers in `src/compiler/cmd/`, language server in `src/compiler/lsp/`, entry
  `src/compiler/zergc.zg`.
- `zerg0` — the Go seed in `src/bootstrap/` (kong for the CLI, zerolog for logs). Its only job is building `zerg`; its
  narrower subset is recorded in `src/bootstrap/README.md`, never in `docs/`.

Elsewhere: `src/runtime/csrc/` is the C runtime floor, `src/stdlib/` the pure-Zerg standard library, `examples/` the
reader-facing corpus (every file built and run by a gate), `scripts/` the gate scripts, `mk/gates.mk` the gate targets,
`notes/<minor>/` the full release notes behind `CHANGELOG.md`.

Zero dependency, like Go: a compiled program links no third-party library. The stdlib reaches the OS through the
runtime's own primitives, never by FFI-binding libc or anything else.

## Where the truth is

- Syntax is normative in `GRAMMAR`; semantics in `docs/` (start at `docs/README.md`, markers in
  `docs/conformance.md`). English is authoritative; each `*.zh-TW.md` is a lockstep translation.
- Markers are measured against `zerg`: implemented is plain prose, `[not yet]` raises `NotImplemented`, `[deviation]`
  names an open issue. A gap in the seed alone goes in `src/bootstrap/README.md`.
- The contract: a form is lowered correctly or refused by name at compile time — never a crash, never a silently wrong
  answer, never an error reported by `cc` or the linker against generated C.
- Error codes are declared in `src/compiler/zerg/rule.zg` and listed in `docs/tooling/diagnostics.md`; fmt/lint rule
  codes in `docs/tooling/fmt.md` and `docs/tooling/lint.md`.

## Commands

| Command                  | Use                                                                             |
| ------------------------ | ------------------------------------------------------------------------------- |
| `make build`             | `bin/zerg0`, then `bin/zerg` built by itself; must pass at every commit         |
| `make <gate>`            | any one gate alone; `make help gates` lists them with what each one holds       |
| `make test`              | the whole board, serially; heavy — never run it from parallel agents            |
| `make suites`            | each subdirectory's own unit suite (the seed's `go test` included) and examples |
| `make lint` / `make fmt` | `zerg lint --strict` over every source this repo writes / rewrite them in place |
| `make fixpoint`          | the compiler still emits the same C for itself                                  |
| `make gates`             | every gate is on the board and in CI                                            |
| `make linux-ci`          | the board in a Linux container, as CI runs it (needs docker)                    |

A gate that needs the corpus fails without the `test-data/` submodule (`git submodule update --init`). Wait on a long
run with `run_in_background` and one `tail` of its output file — never a CPU-spinning `while` loop, which starves the
build and flakes the concurrency gates.

## Branches, commits, PRs

- One task gets one branch off `main` (`feat/<topic>`, `fix/<issue>-<slug>`), one PR, pushed to the remote named
  `GITHUB` (`git push -u GITHUB <branch>`). `origin` is an unreachable gitea.
- Merge to `main` only when the user has authorized that merge; an earlier "merge" does not carry to the next one.
  Merges are merge commits (`gh pr merge <n> --merge`, or `--no-ff` locally), with every gate green first.
- One purpose per commit. Code lands as `feat(x)`/`fix(x)`; its `*_test.zg` files, gate cases and corpus as a separate
  `test(x)`; Makefile/scripts/CI as `build(x)` (CI is `build(ci)`, never `ci:`); docs as `doc(x)`.
- Every commit builds. A signature change carries every non-test caller in the same commit; two changes that only
  build together are one commit.
- Order a branch feat/fix → test → build → doc. Before pushing: review the diff for reuse and simplification
  (`/simplify` in Claude Code), fold its fixes into the commits they belong to, reorder, re-run the gates, then push
  and open the PR.
- Reorganize with cherry-pick + `git commit --amend` onto a fresh branch from `main` — never `rebase -i`. Make a
  `backup/<name>` branch first and prove `git diff backup/<name> HEAD` is empty before dropping it. Before any
  `filter-repo`, back up outside the repo (`git bundle create <file> --all`): it rewrites every local branch too.
- Message: subject `<type>(scope): <subject>`, blank line, body indented 4 spaces (bullet continuations at 6). No
  `Co-Authored-By` or other footer. Write it to a uniquely named temp file (parallel agents share the scratchpad) and
  `git commit -F <file>`; re-read it after an `--amend`.
- Stage only paths the task created or changed. Never commit `PLAN.md` or `GAPS.md` (untracked working notes) or
  unrelated files that were dirty before the task started. Check `git show --stat` after every commit: a failed commit
  leaves its paths staged for the next one.
- `test-data/` is a private submodule (`cmj0121/zerg-testdata`) and stays private: never propose publishing it or
  copying cases into this repo, and never list its privacy as a gap. Commit inside the submodule first; bump
  its pointer in one `test(corpus)` commit placed before the tests that need it. Coverage outsiders must see goes in
  the in-repo gates (`refuse`, `reject`, `oracle`, `examples`, `fixpoint`, `lint`, `fmt-self`, `docs-links`).

## Parallel agents

Agents sharing one worktree are edit-only on disjoint files: no `git`, no `pre-commit`, no stash, no checkout. Their
stashes race and silently erase each other's edits. The parent lints and commits, and only while no agent is running.

## Writing code

- Follow the neighbouring code's style; `make fmt` decides spacing. Run `make lint` before committing Zerg.
- A new surface form owes a `test-data/fmt/` case in the same change: the formatter's failures are stable, so
  `fmt-corpus` only sees a shape it contains.
- A literal AST node keeps its source lexeme and `zerg fmt` prints that text, never a rendering of the value.
- A new or retired error code, fmt rule or lint rule changes its table, its doc row (en and zh-TW) and its gate case in
  the same commit.
- A by-value `list` or `File` parameter inside a loop is the recurring performance trap here; check new signatures.
- A C file under `src/runtime/csrc/` that touches a non-ISO API defines its feature-test macro before every
  `#include`: `-std=c11` with glibc hides it, and macOS never shows the failure (`make linux-ci` does).
- Before building a type rule, probe `bin/zerg0`: the seed is often the reference behaviour, and `make oracle` holds
  the two compilers to each other.

## Writing docs

- An en page and its `.zh-TW.md` twin change together (`make docs-mirror`). zh-TW means Taiwan usage (函式, 型別,
  記憶體, 實作, 最佳化), with code and identifiers left in English.
- Lines are at most 120 (`.markdownlint.yaml` MD013). markdownlint cannot break a table row or a `GRAMMAR` line, so
  measure those; zh-TW lines are measured in display columns (CJK counts two). Prettier re-joins hand-wrapped prose:
  shorten a sentence rather than breaking it.
- Check with `npx --yes prettier@3 --check <files>`, markdownlint, `make docs-links` and `make docs-mirror`.
- No counts or enumerations of what the code computes, in prose, comments or gate headers: name the rule instead, or
  add the gate that holds the number in the same change.
- Reading a chapter against the compiler finds bugs: probe the positive half of each `[not yet]` — the sentence that
  says what does work — not only the refusal its gate already pins.

## Gates and measurement

- Never report a gate green from a status you did not read. Run gates one at a time, each printing its own OK/FAIL
  (`make <g> && echo OK || echo FAIL`), and `tee` the output to a file. `make a; echo $?` reports echo's status.
  Watch one CI run with `gh run watch <id>`, not `gh pr checks --watch`.
- Chaining goals (`make build <gate>`) forwards them to the subdirectory fan-out and fails early; run one goal per
  `make`.
- A new gate is shown RED on a deliberate break before its GREEN is trusted. Give it a floor, so an extraction that
  stops matching fails instead of passing on an empty set.
- Anchor a gate to a fixture it writes, a rule rather than an instance, and a list it derives. When a code is retired
  or a declaration renamed, grep the gates for it: absence-assertions go quiet exactly then.
- A skip list, a missing expectation file or a stale reason is a claim; write the rule so the absence is asserted.
- A case about choosing the right one of two things makes the two differ (types, arity, values); ask whether it would
  still pass if the other were picked.
- Derive the set under review from the code (say which query), not from a list a previous change left behind. After
  widening a predicate, read the callers of every narrower predicate that could stand in for it.
- When a count comes out asymmetric, suspect the ruler: re-derive it another way and read one raw hit in full.
- Gate scripts run under bash 3.2 on macOS CI (`/bin/bash`) while `env bash` finds bash 5 locally. After changing a
  script under `scripts/`, run it with `/bin/bash` too and avoid `mapfile`, `declare -A`, `${v,,}`, `${a[-1]}`,
  `&>>`, `|&`, empty `"${arr[@]}"` under `set -u`, and `case` patterns inside `$( )` without a leading `(`. Use
  `sed -E`, not GNU-only BRE.
