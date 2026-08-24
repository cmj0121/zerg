#!/usr/bin/env bash
#
# lsp-check — the language server must say what `zerg build` says.
#
# LSP.md states the invariant as a sentence: "if the server disagrees with `zerg build`
# about a program, the server is wrong. It has no analysis of its own." This is that
# sentence as a gate, and it is the LSP's analogue of `make oracle` — two ways of asking
# the same compiler one question, held to the same answer.
#
# It matters because the invariant is only structurally true while nobody adds a rule. The
# server calls `check_files_diag`, `lex_diags`, `lint_program` and `fmt_src_off` and
# owns none of them; the day one handler grows a shortcut — a special case for an empty
# buffer, a filter that drops a finding the author thought was noise — an editor starts
# reporting a language that the compiler does not implement, and no other gate here can
# see it.
#
# It also drives the WIRE, which nothing else does: a real session over stdio with real
# `Content-Length` frames. Every failure this found on the first run was in the framing,
# not in the diagnostics.

set -u

ZERG=${ZERG:-./bin/zerg}
PY=${PY:-python3}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fail=0
ran=0
outlined=0

# A literal tab, for the outline's patterns further down. See the comment there: `\t` written
# inside a pattern is a tab to BSD grep and the letter `t` to GNU grep, which is a gate that
# asks one question on a developer's machine and a different one in CI.
TAB=$(printf '\t')

# A session is scripted here rather than in a fixture file so the request and the assertion
# about its reply sit next to each other.
session() {
	"$PY" - "$@" <<'EOF'
import json, os, subprocess, sys

zerg, mode, path = sys.argv[1], sys.argv[2], sys.argv[3]
text = open(path, encoding="utf-8").read()
uri = "file://" + os.path.abspath(path)

msgs = [
    {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"capabilities": {}}},
    {"jsonrpc": "2.0", "method": "initialized", "params": {}},
    {"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": {
        "textDocument": {"uri": uri, "languageId": "zerg", "version": 1, "text": text}}},
]
if mode == "format":
    msgs.append({"jsonrpc": "2.0", "id": 2, "method": "textDocument/formatting",
                 "params": {"textDocument": {"uri": uri}, "options": {}}})
# The outline is asked for on EVERY session rather than in a mode of its own: it is a read
# of the same buffer, it costs one more frame, and a handler that answers only when it is
# the only thing asked is a handler nobody would catch.
msgs.append({"jsonrpc": "2.0", "id": 4, "method": "textDocument/documentSymbol",
             "params": {"textDocument": {"uri": uri}}})
msgs.append({"jsonrpc": "2.0", "id": 3, "method": "shutdown"})
msgs.append({"jsonrpc": "2.0", "method": "exit"})

wire = b"".join(
    b"Content-Length: %d\r\n\r\n%s" % (len(b), b)
    for b in (json.dumps(m).encode() for m in msgs)
)

# A server that never reads a frame would otherwise hang this gate forever, which in CI is
# a job that is killed at the step timeout with nothing said about why.
out = subprocess.run([zerg, "lsp"], input=wire, stdout=subprocess.PIPE,
                     stderr=subprocess.DEVNULL, timeout=120).stdout

# The frames are decoded rather than grepped: a Content-Length that disagrees with the body
# is the defect this parser exists to catch, and a regex over the whole stream would read
# straight past it.
frames, i = [], 0
while i < len(out):
    j = out.find(b"\r\n\r\n", i)
    if j < 0:
        break
    head = out[i:j].decode("ascii", "replace")
    n = None
    for line in head.split("\r\n"):
        k, _, v = line.partition(":")
        if k.strip().lower() == "content-length":
            n = int(v.strip())
    if n is None:
        print("FRAME no Content-Length in a header block", file=sys.stderr)
        sys.exit(2)
    body = out[j + 4:j + 4 + n]
    if len(body) != n:
        print("FRAME Content-Length %d but only %d bytes follow" % (n, len(body)), file=sys.stderr)
        sys.exit(2)
    frames.append(json.loads(body.decode("utf-8")))
    i = j + 4 + n

diags, edits, init, syms = None, None, None, None
for f in frames:
    if f.get("method") == "textDocument/publishDiagnostics":
        diags = f["params"]["diagnostics"]
    elif f.get("id") == 1:
        init = f.get("result")
    elif f.get("id") == 2:
        edits = f.get("result")
    elif f.get("id") == 4:
        syms = f.get("result")

if init is None:
    print("INIT the server did not answer initialize", file=sys.stderr)
    sys.exit(2)
if diags is None:
    print("DIAG the server published no diagnostics for the buffer", file=sys.stderr)
    sys.exit(2)

# The compiler's diagnostics and the linter's are counted apart, because they answer to
# two different commands: an ERROR (severity 1) is what `zerg build` refuses over, and
# everything else is a lint finding about a program that BUILDS — which `zerg lint`
# reports and `zerg build` does not. Comparing the total against the build was this
# gate's own first bug, and it read as the server inventing findings.
#
# A finding is put back together as `code message` before it is compared, because that is
# how the COMMANDS print one and the server is what has them apart. Comparing the sentence
# alone would have let the server drop `Diagnostic.code` entirely without this noticing —
# the assertion is that the two say the same thing, and the code is part of what is said.
def said(d):
    c = d.get("code", "")
    return "%s %s" % (c, d["message"]) if c else d["message"]

# THE SEVERITY IS PART OF WHAT IS SAID, so it is put back too. The linter has three levels
# and `zerg lint` prints the two that do not gate the build with an adjective in front of
# the code; the server sends them as LSP severities, in the same order. This table is the
# one place the two spellings meet, and it exists so a server that quietly flattened all
# three into one severity would be caught rather than agreeing about every count. An
# UNKNOWN severity renders as itself, so it fails the comparison instead of the script.
SEV = {2: "", 3: "warning: ", 4: "info: "}

result = {
    "errors": [said(d) for d in diags if d["severity"] == 1],
    "lints": [SEV.get(d["severity"], "severity-%s " % d["severity"]) + said(d)
              for d in diags if d["severity"] != 1],
    "ranges": [(d["range"]["start"]["line"], d["range"]["start"]["character"]) for d in diags],
}
if edits is not None:
    result["edits"] = edits

# A DECLARATION is what an outline names, so the comparison below is against the parser's own
# dump and only these kinds are in it. The numbers are LSP's SymbolKind: 6 method, 10 enum,
# 11 interface (a spec), 12 function, 23 struct.
if syms is not None:
    result["symbols"] = [s["name"] for s in syms if s["kind"] in (6, 10, 11, 12, 23)]
print(json.dumps(result))
EOF
}

# --- 1. a valid program is silent ---------------------------------------------------
#
# The first thing a server has to get right, and the easiest to get wrong: a check that
# reports nothing looks identical to a check that never ran. So this is asserted against
# programs the corpus already says are correct, and the count below is what says the
# session happened at all.
for src in "$@"; do
	got=$(session "$ZERG" diag "$src" 2>"$tmp/err") || {
		echo "SESSION   $src — the server did not complete a session"
		sed 's/^/  /' "$tmp/err"
		fail=$((fail + 1))
		continue
	}
	ran=$((ran + 1))

	n=$(printf '%s' "$got" | "$PY" -c 'import json,sys; print(len(json.load(sys.stdin)["errors"]))')

	# the ERROR half, against the command that refuses over one
	if "$ZERG" build --emit c "$src" >/dev/null 2>"$tmp/cc"; then
		want=0
	else
		want=1
	fi

	if [ "$want" -eq 0 ] && [ "$n" -ne 0 ]; then
		echo "DISAGREE  $src — the compiler builds it and the server reports $n error(s)"
		echo "  $got"
		fail=$((fail + 1))
	fi
	if [ "$want" -ne 0 ] && [ "$n" -eq 0 ]; then
		echo "DISAGREE  $src — the compiler refuses it and the server reports no error"
		echo "  $(head -1 "$tmp/cc")"
		fail=$((fail + 1))
	fi

	# the LINT half, against the command that reports one. `zerg lint` prints every finding
	# as `path:line:col: [severity: ]code message`, so the whole line after the place is
	# what the server has to have said — severity included. Only this file's lines are
	# compared, because a program is linted whole and the server publishes per document.
	# This is the assertion that keeps the two families from swapping severity — a server
	# that painted a lint finding red would still agree about every count.
	"$ZERG" lint "$src" >"$tmp/lint" 2>/dev/null || true
	if ! "$PY" - "$src" "$tmp/lint" "$got" <<'PYEOF'
import json, sys, re
src, lintfile = sys.argv[1], sys.argv[2]
got = json.loads(sys.argv[3])
want = []
for line in open(lintfile, encoding="utf-8"):
    m = re.match(r"^(.*?):(\d+):(\d+): (.*)$", line.rstrip("\n"))
    if m and m.group(1) == src:
        want.append(m.group(4))
if sorted(got["lints"]) != sorted(want):
    print("  server: %s" % sorted(got["lints"]))
    print("  lint:   %s" % sorted(want))
    sys.exit(1)
PYEOF
	then
		echo "DISAGREE  $src — the server's lint findings are not what zerg lint reports"
		fail=$((fail + 1))
	fi

	# THE OUTLINE, against the parser's own dump.
	#
	# `--emit ast` prints one line per top-level declaration, and `documentSymbol` is the same
	# list with positions on it — so if the two disagree about WHICH declarations a file has,
	# the server has grown a view of the program, which is the one thing it may not do.
	#
	# Only on a file that imports NOTHING. The driver merges a whole program into one `File`
	# before emission, so `--emit ast` on a file with imports prints the imported modules'
	# declarations too, while the outline is the buffer alone — deliberately, since an outline
	# full of declarations the reader cannot see on screen is not an outline. Where the two
	# questions differ the dump is not an oracle, so it is not asked.
	if ! grep -qE '^[[:space:]]*(pub[[:space:]]+)?import\b|^[[:space:]]*"' "$src"; then
		# A METHOD is dumped as `fn P.get(this: P)`, so the optional `Type.` in the pattern is
		# not decoration: without it the name read out of the dump was the RECEIVER, and the
		# gate reported a struct declared twice and a method that did not exist.
		#
		# The indent is a LITERAL tab built by printf, never `\t` inside the pattern, for the
		# reason editor-align.sh carries in full and layering-check.sh repeats: BSD grep reads
		# `\t` as a tab and GNU grep reads it as an undefined escape, i.e. the letter `t`. This
		# is the THIRD gate to walk into it. Here the pattern matched every declaration on
		# macOS and none at all on Linux, so `ast.names` came back empty against an outline
		# that was right — 55 files reported as the server having grown a view of the program,
		# on the one platform nobody was reading the dump on.
		"$ZERG" build --emit ast "$src" >"$tmp/ast" 2>/dev/null || true
		grep -E "^$TAB(pub )?(fn|struct|enum|spec) " "$tmp/ast" |
			sed -E "s/^$TAB(pub )?(fn|struct|enum|spec) ([A-Za-z_][A-Za-z0-9_]*\.)?([A-Za-z_][A-Za-z0-9_]*).*/\4/" |
			LC_ALL=C sort >"$tmp/ast.names"
		# LC_ALL=C, because the other side of this diff is sorted by Python and Python sorts by
		# CODE POINT. A UTF-8 locale puts `answer` before `Cmd` and a C locale puts `Cmd` first,
		# so without it the gate reported a disagreement about ORDER as a disagreement about
		# which declarations a file has.
		printf '%s' "$got" | "$PY" -c 'import json,sys; print("\n".join(sorted(json.load(sys.stdin).get("symbols", []))))' >"$tmp/sym.names"
		# An EMPTY dump is not a disagreement about declarations, it is this side of the
		# comparison having failed to be read — and saying so is the difference between one
		# line naming a broken extraction and 55 identical accusations against the server.
		if [ ! -s "$tmp/ast.names" ] && [ -s "$tmp/sym.names" ]; then
			echo "DISAGREE  $src — the parser dump named no declarations, so the outline was compared against nothing"
			fail=$((fail + 1))
		elif ! diff -q "$tmp/ast.names" "$tmp/sym.names" >/dev/null; then
			echo "DISAGREE  $src — the outline is not the declarations the parser read"
			diff "$tmp/ast.names" "$tmp/sym.names" | sed 's/^/  /'
			fail=$((fail + 1))
		else
			outlined=$((outlined + 1))
		fi
	fi
done

# --- 2. formatting is fmt's answer, not a second one --------------------------------
#
# `textDocument/formatting` must return exactly what `zerg fmt` writes. It is the same
# function, so the only way this fails is a bug in what the server does AROUND it — the
# range it claims to replace, or a buffer it formatted that was not the one it was asked
# about.
cat >"$tmp/messy.zg" <<'ZG'
fn main( ) {
    x:=1+2
  print   x
}
ZG
cp "$tmp/messy.zg" "$tmp/want.zg"
"$ZERG" fmt "$tmp/want.zg" >/dev/null 2>&1

got=$(session "$ZERG" format "$tmp/messy.zg" 2>"$tmp/err") || {
	echo "SESSION   formatting — the server did not complete a session"
	sed 's/^/  /' "$tmp/err"
	fail=$((fail + 1))
}
if [ -n "${got:-}" ]; then
	# The JSON goes in as an ARGUMENT, not on stdin: `python - <<EOF` reads its own program
	# from stdin, so a pipe into it is silently discarded and the script sees end of input.
	"$PY" - "$tmp/want.zg" "$tmp/messy.zg" "$got" <<'EOF'
import json, sys
want = open(sys.argv[1], encoding="utf-8").read()
src = open(sys.argv[2], encoding="utf-8").read()
got = json.loads(sys.argv[3])
edits = got.get("edits")
if not edits:
    print("FORMAT    the server returned no edit for a source `zerg fmt` rewrites")
    sys.exit(1)
if len(edits) != 1:
    print("FORMAT    the server returned %d edits; formatting is one whole-document edit" % len(edits))
    sys.exit(1)
e = edits[0]
if e["newText"] != want:
    print("FORMAT    the server's text is not what `zerg fmt` writes")
    print("  server: %r" % e["newText"][:80])
    print("  fmt:    %r" % want[:80])
    sys.exit(1)

# The RANGE has to cover the document, or the client appends instead of replacing and the
# file doubles. It is asserted against the source's own shape rather than against a large
# number, because a client is not obliged to clamp one.
lines = src.split("\n")
end = e["range"]["end"]
if e["range"]["start"] != {"line": 0, "character": 0}:
    print("FORMAT    the edit does not start at the top of the document")
    sys.exit(1)
if end["line"] != len(lines) - 1:
    print("FORMAT    the edit ends on line %d; the document has %d" % (end["line"], len(lines)))
    sys.exit(1)
EOF
	rc=$?
	[ $rc -eq 0 ] || fail=$((fail + 1))
fi

# --- 3. the protocol, not the answers ------------------------------------------------
#
# Everything above asks whether the server says the right thing. This asks whether it
# BEHAVES like a server, which is a different failure and a quieter one: an editor with a
# corrupted buffer or a client left waiting reports nothing at all.
#
# Every case here failed once. They are the final audit of this branch, written down.
if ! "$PY" - "$ZERG" "$tmp" <<'PYEOF'
import json, os, subprocess, sys

zerg, tmp = sys.argv[1], sys.argv[2]
src = "fn main() {\n\t# a comment — with an em-dash and 註解\n\tx := 1\n\tx = 2\n}\n"
path = os.path.join(tmp, "proto.zg")
open(path, "w", encoding="utf-8").write(src)
uri = "file://" + os.path.abspath(path)

def frame(m):
    b = json.dumps(m).encode()
    return b"Content-Length: %d\r\n\r\n%s" % (len(b), b)

def run(msgs, raw=b""):
    wire = b"".join(frame(m) for m in msgs[:1]) + raw + b"".join(frame(m) for m in msgs[1:])
    p = subprocess.run([zerg, "lsp"], input=wire, stdout=subprocess.PIPE,
                       stderr=subprocess.PIPE, timeout=120)
    out, frames, i = p.stdout, [], 0
    while i < len(out):
        j = out.find(b"\r\n\r\n", i)
        if j < 0:
            break
        n = None
        for line in out[i:j].decode("ascii", "replace").split("\r\n"):
            k, _, v = line.partition(":")
            if k.strip().lower() == "content-length":
                n = int(v.strip())
        if n is None:
            break
        frames.append(json.loads(out[j + 4:j + 4 + n].decode("utf-8")))
        i = j + 4 + n
    return p.returncode, frames

INIT = {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"capabilities": {}}}
OPEN = {"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": {
    "textDocument": {"uri": uri, "languageId": "zerg", "version": 1, "text": src}}}
EXIT = {"jsonrpc": "2.0", "method": "exit"}
DOWN = {"jsonrpc": "2.0", "id": 2, "method": "shutdown"}

def diags(frames):
    return [len(f["params"]["diagnostics"]) for f in frames
            if f.get("method") == "textDocument/publishDiagnostics"]

bad = 0
def check(ok, what, got):
    global bad
    if not ok:
        print("PROTOCOL  %s — got %r" % (what, got))
        bad += 1

# a diagnostic on a line that follows a line of non-ASCII: the column is UTF-16 units, and
# a byte column would be several characters out
_, fr = run([INIT, OPEN, EXIT])
d = [f for f in fr if f.get("method") == "textDocument/publishDiagnostics"][0]["params"]["diagnostics"]
check(len(d) == 1 and d[0]["range"]["start"] == {"line": 3, "character": 1},
      "a position after a line of CJK is in UTF-16 units", d and d[0]["range"])

# an EMPTY contentChanges must not replace the buffer with the empty string
_, fr = run([INIT, OPEN, {"jsonrpc": "2.0", "method": "textDocument/didChange", "params": {
    "textDocument": {"uri": uri, "version": 2}, "contentChanges": []}}, EXIT])
check(diags(fr) == [1], "an empty change leaves the buffer alone", diags(fr))

# an INCREMENTAL change is refused rather than applied as a fragment
_, fr = run([INIT, OPEN, {"jsonrpc": "2.0", "method": "textDocument/didChange", "params": {
    "textDocument": {"uri": uri, "version": 2}, "contentChanges": [
        {"range": {"start": {"line": 0, "character": 0}, "end": {"line": 0, "character": 1}},
         "text": "X"}]}}, EXIT])
check(diags(fr) == [1], "an incremental change is refused, not applied", diags(fr))

# a full change IS applied
_, fr = run([INIT, OPEN, {"jsonrpc": "2.0", "method": "textDocument/didChange", "params": {
    "textDocument": {"uri": uri, "version": 2},
    "contentChanges": [{"text": "fn main() {\n\tprint 1\n}\n"}]}}, EXIT])
check(diags(fr) == [1, 0], "a full change is applied", diags(fr))

# after `shutdown`, a request is answered with InvalidRequest rather than served
_, fr = run([INIT, DOWN, {"jsonrpc": "2.0", "id": 3, "method": "textDocument/formatting",
                          "params": {"textDocument": {"uri": uri}, "options": {}}}, EXIT])
after = [f for f in fr if f.get("id") == 3]
check(len(after) == 1 and after[0].get("error", {}).get("code") == -32600,
      "a request after shutdown is InvalidRequest", after)

# the exit status is part of the protocol
rc, _ = run([INIT, EXIT])
check(rc == 1, "exit without shutdown exits 1", rc)
rc, _ = run([INIT, DOWN, EXIT])
check(rc == 0, "shutdown then exit exits 0", rc)

# a notification is never answered, and a `$/` request is answered rather than dropped
_, fr = run([INIT, {"jsonrpc": "2.0", "method": "$/setTrace", "params": {"value": "verbose"}},
             {"jsonrpc": "2.0", "method": "workspace/didChangeConfiguration", "params": {}},
             {"jsonrpc": "2.0", "id": 4, "method": "$/unknown", "params": {}}, EXIT])
check(sorted(f.get("id") for f in fr) == [1, 4], "notifications get no reply and a request does", [f.get("id") for f in fr])

# a frame that is not JSON is dropped and the session survives it
_, fr = run([INIT, {"jsonrpc": "2.0", "id": 9, "method": "shutdown"}, EXIT],
            raw=b"Content-Length: 7\r\n\r\n{not js")
check([f.get("id") for f in fr] == [1, 9], "a malformed frame does not end the session", [f.get("id") for f in fr])

# a string id comes back a string, not a number or null
_, fr = run([{"jsonrpc": "2.0", "id": "abc-1", "method": "initialize", "params": {"capabilities": {}}}, EXIT])
check(fr and fr[0].get("id") == "abc-1", "a string id is echoed as a string", fr and fr[0].get("id"))

# --- code actions ----------------------------------------------------------------------
#
# The quick fix an editor applies has to be the compiler's own answer, at the compiler's own
# place. Both halves are asserted, because either alone is a rewrite that lands in the wrong
# column or writes the wrong text — and a code action that damages a buffer is the one bug a
# user cannot undo their way out of if they do not notice it.
fixsrc = "fn main() {\n\tx: float = 1 / 2\n\tprint x\n}\n"
fixpath = os.path.join(tmp, "fix.zg")
open(fixpath, "w").write(fixsrc)
fixuri = "file://" + os.path.abspath(fixpath)
FIXOPEN = {"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": {
    "textDocument": {"uri": fixuri, "languageId": "zerg", "version": 1, "text": fixsrc}}}

def actions(line, ch):
    _, fr = run([INIT, FIXOPEN, {"jsonrpc": "2.0", "id": 7, "method": "textDocument/codeAction",
        "params": {"textDocument": {"uri": fixuri},
                   "range": {"start": {"line": line, "character": ch},
                             "end": {"line": line, "character": ch}},
                   "context": {"diagnostics": []}}}, EXIT])
    got = [f for f in fr if f.get("id") == 7]
    return got[0]["result"] if got else None

_, fr = run([INIT, EXIT])
caps = fr[0]["result"]["capabilities"].get("codeActionProvider")
check(caps == {"codeActionKinds": ["quickfix"]}, "codeAction is declared as a quickfix provider", caps)

# the cursor ON the `1` offers `1.0`, and the edit covers the literal and nothing else
a = actions(1, 12)
check(a is not None and len(a) == 1 and a[0]["title"] == "Write `1.0`" and a[0]["kind"] == "quickfix",
      "the cursor on a literal offers its own fix", a)
if a and len(a) == 1:
    e = a[0]["edit"]["changes"][fixuri][0]
    check(e["newText"] == "1.0" and e["range"] == {"start": {"line": 1, "character": 12},
                                                   "end": {"line": 1, "character": 13}},
          "the edit replaces the literal and nothing else", e)

# THE SECOND LITERAL ON THE SAME LINE IS ITS OWN ACTION. The finding used to carry the
# STATEMENT's place, where both would answer to one position and one of them would be
# rewritten twice — which is the whole reason an integer literal now carries its own.
b = actions(1, 16)
check(b is not None and len(b) == 1 and b[0]["title"] == "Write `2.0`",
      "a second literal on one line is a second action", b)

# a place with no finding offers nothing rather than everything on the line
check(actions(2, 2) == [], "a position with no finding offers no action", actions(2, 2))

# a body larger than one read of the runtime's bounded leaf
big = "fn main() {\n" + "".join("\tprint %d\n" % i for i in range(1200)) + "}\n"
bigpath = os.path.join(tmp, "big.zg")
open(bigpath, "w").write(big)
biguri = "file://" + os.path.abspath(bigpath)
_, fr = run([INIT, {"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": {
    "textDocument": {"uri": biguri, "languageId": "zerg", "version": 1, "text": big}}}, EXIT])
check(diags(fr) == [0], "a %d-byte body is reassembled across reads" % len(big), diags(fr))

sys.exit(1 if bad else 0)
PYEOF
then
	fail=$((fail + 1))
fi

# --- 4. a member of a multi-file module ------------------------------------------------
#
# Everything above opens a file that IS a program. An editor mostly opens one that is not:
# a member of a directory module, whose types, whose callers and whose second source root
# all live outside it. Read as an entry, such a file reports `E4056 no type named ...` for a
# struct in the file next to it, `L102 private function ... is never called` for a function
# its sibling calls, and `E5002 cannot resolve import` for a module that sits beside its own
# directory rather than inside it — three sentences about correct code, which is the one
# failure that makes a person turn the server off.
#
# The fixture is built here rather than pointed at this repo's own sources, for the reason
# the cases above are scripted rather than fixtured: what is asserted and what it is
# asserted about have to be readable together. It is also the smallest program that has all
# three shapes at once —
#
#   app.zg      the entry, and the only file with a `main`
#   util.zg     a module beside the ENTRY, so `import "util"` from inside `widget/`
#               resolves only from the entry's directory
#   widget/     a directory module of three files, each using a name declared in another
#
# Each member is a different symptom read alone — `a.zg` the unresolvable import, `b.zg` the
# name declared next door, `c.zg` the private function whose only caller is next door — and
# the third is there because it is the only one of the three that is a LINT: an error aborts
# the check before the linter runs, so a fixture of errors alone can never show that the
# lint half reads the module too.
#
# `zerg build` is asked first and is the oracle: the program compiles, so nothing the server
# says about any member is a finding — errors and lints alike.
mkdir -p "$tmp/proj/widget"
cat >"$tmp/proj/app.zg" <<'ZG'
import "./widget"

fn main() {
	widget.greet()
}
ZG
cat >"$tmp/proj/util.zg" <<'ZG'
pub fn shout(s: str) -> str {
	return s + "!"
}
ZG
cat >"$tmp/proj/widget/mod.zg" <<'ZG'
# `widget/` is a module because it holds this file (#57 decision 4), and its surface is what
# this file re-exports.

import pub "./a"
ZG
cat >"$tmp/proj/widget/a.zg" <<'ZG'
import (
	"/util"

	"./b"
)

pub fn greet() {
	print util.shout(b.banner())
}

pub fn tagged(t: b.Tag) -> str {
	return t.name
}
ZG
cat >"$tmp/proj/widget/b.zg" <<'ZG'
import (
	"./a"
	"./c"
)

pub struct Tag {
	pub name: str
}

pub fn banner() -> str {
	return a.tagged(Tag(c.stamp()))
}
ZG
cat >"$tmp/proj/widget/c.zg" <<'ZG'
pub fn stamp() -> str {
	return "hello"
}
ZG

members=0
if ! "$ZERG" build --emit c "$tmp/proj/app.zg" >/dev/null 2>"$tmp/mod.cc"; then
	echo "MODULE    the fixture program does not build, so there is nothing to hold the server to"
	sed 's/^/  /' "$tmp/mod.cc"
	fail=$((fail + 1))
else
	for member in "$tmp/proj/widget/a.zg" "$tmp/proj/widget/b.zg" "$tmp/proj/widget/c.zg"; do
		got=$(session "$ZERG" diag "$member" 2>"$tmp/err") || {
			echo "SESSION   ${member#"$tmp"/} — the server did not complete a session"
			sed 's/^/  /' "$tmp/err"
			fail=$((fail + 1))
			continue
		}
		said=$(printf '%s' "$got" | "$PY" -c 'import json,sys; d=json.load(sys.stdin); print("\n".join(d["errors"] + d["lints"]))')
		if [ -n "$said" ]; then
			echo "MODULE    ${member#"$tmp"/} — the server reports findings against a file the compiler builds clean"
			printf '%s\n' "$said" | sed 's/^/  /'
			fail=$((fail + 1))
		else
			members=$((members + 1))
		fi
	done
fi

if [ $fail -ne 0 ]; then
	echo "lsp-check: $fail case(s) where the server does not say what the compiler says"
	exit 1
fi

if [ "$ran" -lt "${MIN_SESSIONS:-4}" ]; then
	echo "lsp-check: only $ran sessions ran — the list is empty, or the server is not starting"
	exit 1
fi

# The outline is compared on a SUBSET — the files that import nothing — so it needs a floor of
# its own. Without one, a change that made every file look like it imports something would
# leave this gate reporting success for having compared no outlines at all.
if [ "$outlined" -lt "${MIN_OUTLINES:-8}" ]; then
	echo "lsp-check: only $outlined outlines were compared — the import filter is eating the list"
	exit 1
fi

# The module members have a floor for the same reason, and it is not idle: both are opened
# inside a branch that a fixture which stopped building would skip, and a skipped branch
# reports nothing — which reads exactly like a server that found nothing.
if [ "$members" -lt 3 ]; then
	echo "lsp-check: only $members module members were opened — the fixture did not build, or the loop did not run"
	exit 1
fi
echo "lsp-check: $ran buffers agree with the compiler, $outlined outlines are the parser's own, $members module members are checked against their module, formatting is fmt's answer, and 15 protocol cases hold"
