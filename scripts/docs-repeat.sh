#!/bin/sh
# docs-repeat — no paragraph of prose is written twice, verbatim, in one document.
#
# `docs-mirror` holds a page and its translation to the same SHAPE — how many sections and how
# they nest, how many code blocks and in what language, how many table rows, how many status
# markers — and it says in its own comment what that shape is for: *a section that never crossed
# changes all four; a sentence that was rewritten changes none.* A PARAGRAPH sits between those
# two, and it changes none of the four either: a whole one repeated inside a section, or dropped
# from a translation, leaves every count the same.
#
# THIS ASKS THE NARROWER QUESTION THAT NEEDS NO TRANSLATION. A paragraph repeated verbatim in
# ONE document is compared against itself, so nothing about the words has to survive anything —
# and there is no legitimate reason to write the same paragraph twice. The pair-wise version of
# this was measured and does not hold: per-section line counts differ in all 34 pairs, because a
# translation is shorter, so a gate on that would be red on every page and assert nothing.
#
# WHAT IT CANNOT SEE is a paragraph that says the same thing in different words, and a paragraph
# that never crossed into a translation. The first is not checkable; the second is, and is not
# checked here — it needs a pairing this gate deliberately does not do.
#
# A ONE-LINE paragraph is not counted. A single line legitimately repeats: a `---` rule, a
# one-sentence note under two sections, a shared caption.
set -eu

DOCS=${DOCS:-.}
PY=${PY:-python3}

command -v "$PY" >/dev/null 2>&1 || { echo "docs-repeat: no $PY" >&2; exit 1; }

"$PY" - "$DOCS" <<'EOF'
import io, os, sys

root = sys.argv[1]
skip = ('/node_modules/', '/.git/', '/LICENSES/', '/test-data/', '/.zerg-cache/')
files = []
for dirpath, dirnames, filenames in os.walk(root):
    for fn in sorted(filenames):
        if not fn.endswith('.md'):
            continue
        p = os.path.join(dirpath, fn)
        if any(s in '/' + p.replace('\\', '/') for s in skip):
            continue
        files.append(p)
files.sort()

if not files:
    print('docs-repeat: no document was read from %s — the extraction has gone stale' % root, file=sys.stderr)
    sys.exit(1)


def paragraphs(path):
    """runs of non-blank lines outside a fenced block, each with the line it starts on"""
    out, cur, at, fenced = [], [], 0, False
    for n, line in enumerate(io.open(path, encoding='utf-8').read().split('\n'), 1):
        if line.startswith('```'):
            fenced = not fenced
            if cur:
                out.append((at, cur))
                cur = []
            continue
        if fenced:
            continue
        if line.strip() == '':
            if cur:
                out.append((at, cur))
                cur = []
            continue
        if not cur:
            at = n
        cur.append(line.rstrip())
    if cur:
        out.append((at, cur))
    return out


found = 0
for path in files:
    seen = {}
    for at, para in paragraphs(path):
        if len(para) < 2:
            continue
        key = '\n'.join(para)
        if key in seen:
            print('REPEATED  %s:%d — a paragraph of %d lines, already written at line %d' % (path, at, len(para), seen[key]), file=sys.stderr)
            print('          %s' % para[0][:88], file=sys.stderr)
            found += 1
            continue
        seen[key] = at

if found:
    print('docs-repeat: %d paragraph(s) written twice in one document' % found, file=sys.stderr)
    sys.exit(1)

print('docs-repeat: %d documents, no paragraph written twice in one of them' % len(files))
EOF
