#!/usr/bin/env bash
#
# treesitter-check — the tree-sitter grammar reads every Zerg file this repository has.
#
# THIS GATE IS WEAKER THAN THE OTHERS HERE, and that is worth saying rather than hiding.
# Everything else is held to the compiler by CALLING it: `zerg fmt` IS the formatter, the
# language server asks `emit_files_diag` rather than checking anything itself, and the two
# facts `zerg.vim` must repeat are held to the lexer by a diff. `editors/tree-sitter-zerg` is
# a SECOND IMPLEMENTATION of GRAMMAR — around a hundred productions — and no diff can compare
# a tree-sitter rule with a BNF production or with `parser.zg`.
#
# So what holds it is a corpus: every `.zg` file in the tree must parse with no ERROR and no
# MISSING node. That can only see a form some file contains, which is the same blindness
# `fmt-corpus` has and the reason the file list is EVERYTHING rather than a sample — the
# compiler's own sources, the standard library, the examples, and the private corpus when it
# is checked out.
#
# It needs the tree-sitter CLI, which needs node. That is a developer's tool and not a
# dependency of the language, so a machine without it SKIPS rather than fails: the toolchain
# does not need this parser to build, and a gate that turns a missing editor tool into a red
# board teaches people to ignore the board.

set -u

# The pin travels from `editors/Makefile`, which is also what `make -C editors treesitter`
# builds with. Two spellings of it would mean this gate checking a grammar against a different
# tree-sitter than the one that produces the parser a person actually loads.
TS=${TS:-$(make -s -C editors print-ts-cli 2>/dev/null || echo 'npx --yes tree-sitter-cli@0.24.7')}
DIR=${DIR:-editors/tree-sitter-zerg}

# The floor is measured against what this repository GUARANTEES, not against what a
# developer's checkout happens to hold. `SELF_SRCS` and `EXAMPLE_SRCS` are 57 files and are
# always here; test-data is a private submodule and is not. The first number written here
# was 60 — the count on a machine with the corpus checked out — and it failed every CI run
# on the day it landed, because the gate ran before the fetch and could therefore never see
# more than 57. A floor that nothing without credentials can clear is not a floor, it is an
# outage.
#
# So: below the in-tree count, with room for an example to be renamed, and far enough above
# zero to catch the failure this exists for — a glob in the Makefile going stale and handing
# this script a handful of files, or none, while it reports success.
MIN_FILES=${MIN_FILES:-50}

# REQUIRE=1 turns the skip into a failure, for a caller that knows the tool is meant to be
# there. Without it a runner that lost node would leave the only gate over a second
# implementation of GRAMMAR green forever, and the annotation below is what makes that visible
# in a log rather than silent — the same shape `ci-fetch-testdata.sh` uses for the same reason.
if ! command -v npx >/dev/null 2>&1; then
	if [ "${REQUIRE:-0}" = 1 ]; then
		echo "treesitter-check: npx is not installed and REQUIRE=1 — the grammar was not checked"
		exit 1
	fi
	echo "::warning title=treesitter did not run::npx is absent, so the tree-sitter grammar was not checked against the corpus; this job being green does not mean it passed"
	echo "treesitter-check: npx is not installed — skipped (the toolchain does not need it)"
	exit 0
fi

# The scope comes from the CALLER, as every other gate's does. The Makefile already owns one
# definition of what Zerg source this repository has — `SELF_SRCS`, whose own comment records
# a whole directory of the compiler having been outside a rule because the scope was written
# twice — and a second copy here would go stale the same way.
files=$(ls "$@" 2>/dev/null)

n=$(printf '%s\n' $files | grep -c . || true)
if [ "$n" -lt "$MIN_FILES" ]; then
	echo "treesitter-check: only $n files were found, and the floor is $MIN_FILES — this gate is measuring nothing"
	exit 1
fi

# The generated parser is not in the repository (see the directory's .gitignore), so it is
# written here. That also makes this the check that grammar.js still GENERATES, which is a
# failure mode of its own: a conflict introduced by an edit stops the parser existing.
( cd "$DIR" && $TS generate ) >/dev/null 2>&1 || {
	echo "treesitter-check: grammar.js does not generate — run \`$TS generate\` in $DIR for the conflict"
	exit 1
}

out=$( cd "$DIR" && $TS parse --quiet --stat $(printf '../../%s ' $files) 2>&1 )
bad=$(printf '%s\n' "$out" | grep -E '\((ERROR|MISSING)' || true)

if [ -n "$bad" ]; then
	echo "treesitter-check: the grammar cannot read files the compiler can"
	printf '%s\n' "$bad" | sed 's/^/  /' | head -20
	exit 1
fi

echo "treesitter-check: $n files parse with no ERROR and no MISSING node"
