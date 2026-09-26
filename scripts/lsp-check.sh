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
# server calls `lex_diags`, `check_lint_index` and `fmt_src_off` and owns none of them; the
# day one handler grows a shortcut — a special case for an empty buffer, a filter that
# drops a finding the author thought was noise — an editor starts reporting a language that
# the compiler does not implement, and no other gate here can see it.
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
#
# PLUS ONE BUFFER THAT IS NOT a program: an error, and beside it a literal the lowering walk
# notes as an L502 adoption. `zerg lint` gives no conversion advice about a program whose
# types are wrong, and the server takes its notes from the same walk that found the error, so
# dropping them (`lint_conversions_of`) is on its path too. Every other buffer here is either
# valid or has no literal to note, so none of them could see that drop go missing.
cat >"$tmp/noted.zg" <<'ZG'
fn main() {
	x: float = 1 / 2
	y: int = "one"
	print x
	print y
}
ZG
noted='{"errors": [], "lints": []}'

# AND TWO BUFFERS WITH ONE UNREAD BINDING each, told apart by how the walk stops (#246). One
# COLLECTS its error and goes on, so `zerg lint` prints L103 beside it; the other ABORTS, so
# `zerg lint` prints the refusal alone — the rules never ran. The editor was once measured
# publishing the abort alone while the command printed L103 beside it. The loop holds each to
# the command; the collecting twin is what says the binding is one the rules would name.
cat >"$tmp/collected.zg" <<'ZG'
fn main() {
	unused := 3
	x: int = "s"
	print x
}
ZG
cat >"$tmp/aborted.zg" <<'ZG'
fn main() {
	unused := 3
	x := 2
	print match x { _ => 1  2 => 2 }
}
ZG
collected='{"errors": [], "lints": []}'
aborted='{"errors": [], "lints": []}'
for src in "$@" "$tmp/noted.zg" "$tmp/collected.zg" "$tmp/aborted.zg"; do
	got=$(session "$ZERG" diag "$src" 2>"$tmp/err") || {
		echo "SESSION   $src — the server did not complete a session"
		sed 's/^/  /' "$tmp/err"
		fail=$((fail + 1))
		continue
	}
	ran=$((ran + 1))
	[ "$src" = "$tmp/noted.zg" ] && noted=$got
	[ "$src" = "$tmp/collected.zg" ] && collected=$got
	[ "$src" = "$tmp/aborted.zg" ] && aborted=$got

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
	#
	# IN ORDER, not sorted: the order is part of the linter's answer — the tree rules, then the
	# walk's conversion notes, then what `#[allow(…)]` says about itself — and the server
	# publishes the list it was handed. A sorted comparison could not see the two disagree.
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
if got["lints"] != want:
    print("  server: %s" % got["lints"])
    print("  lint:   %s" % want)
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

# The agreement above holds only while `zerg lint` is right about the same buffer, so the
# noted buffer's answer is also asserted outright: an error, and no L5xx note beside it.
if ! "$PY" - "$noted" <<'PYEOF'
import json, re, sys
got = json.loads(sys.argv[1])
notes = [l for l in got["lints"] if re.match(r"^(warning: |info: )?L5\d\d ", l)]
if not got["errors"] or notes:
    print("NOTED     a buffer with an error: errors %s, conversion notes %s — want an error and no note"
          % (got["errors"], notes))
    sys.exit(1)
PYEOF
then
	fail=$((fail + 1))
fi

# The same for the two unread-binding buffers, outright: the collecting one publishes its error
# AND the L103 beside it, the aborting one its refusal alone. Without the first, an aborting
# fixture whose binding no rule names would agree with the command by saying nothing.
if ! "$PY" - "$collected" "$aborted" <<'PYEOF'
import json, sys
col, ab = json.loads(sys.argv[1]), json.loads(sys.argv[2])
l103 = [l for l in col["lints"] if l.startswith("L103 ")]
if not col["errors"] or not l103:
    print("BESIDE    a collected error: errors %s, lints %s — want the error and L103 beside it"
          % (col["errors"], col["lints"]))
    sys.exit(1)
if len(ab["errors"]) != 1 or not ab["errors"][0].startswith("E4032 ") or ab["lints"]:
    print("BESIDE    a walk abort: errors %s, lints %s — want the E4032 refusal alone"
          % (ab["errors"], ab["lints"]))
    sys.exit(1)
PYEOF
then
	fail=$((fail + 1))
fi

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

# --- an abort is published where `zerg build` says it is ---------------------------------
#
# A parse error and a `NotImplemented` refusal end the run with a raised sentence, not a
# Diag, and `zerg build` prints the place under it as `  --> file:line:col`. The server used
# to publish every such abort at the top of the file, so the editor and the command
# disagreed about where the program is wrong. The place asserted here is the one the COMMAND
# printed, read out of its stderr, never a number written into this script.
#
# The command's column is a 1-based BYTE column and the server's is a 0-based UTF-16
# character, so the expected character is the UTF-16 length of the line's bytes before it.
# Two sources put the place after non-ASCII on the same line — CJK, and a pair outside the
# basic plane — where a byte column published as-is would land several characters late.
import re
PLACE = re.compile(r"^  --> (.+):(\d+):(\d+)$")

def built_place(p):
    err = subprocess.run([zerg, "build", "--emit", "c", p], stdout=subprocess.DEVNULL,
                         stderr=subprocess.PIPE, timeout=120).stderr.decode("utf-8")
    for line in err.split("\n"):
        m = PLACE.match(line)
        if m:
            return m.group(1), int(m.group(2)), int(m.group(3))
    return None

def utf16_char(text, line, col):
    head = text.split("\n")[line - 1].encode("utf-8")[:col - 1].decode("utf-8")
    return len(head.encode("utf-16-le")) // 2

def abort_diag(name, text):
    p = os.path.join(tmp, name)
    open(p, "w", encoding="utf-8").write(text)
    u = "file://" + os.path.abspath(p)
    _, fr = run([INIT, {"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": {
        "textDocument": {"uri": u, "languageId": "zerg", "version": 1, "text": text}}}, EXIT])
    ds = [f for f in fr if f.get("method") == "textDocument/publishDiagnostics"]
    d = ds[0]["params"]["diagnostics"] if ds else []
    return os.path.abspath(p), (d[0] if len(d) == 1 else None)

TOP = {"start": {"line": 0, "character": 0}, "end": {"line": 0, "character": 0}}

for name, text, what in [
    ("abort-parse.zg", "fn main() {\n\tx := (1 +)\n\tprint x\n}\n", "a parse abort"),
    ("abort-notimpl.zg", "#[derive(Hash)]\nstruct P {\n\tx: int\n}\n\nfn main() {\n\tprint 1\n}\n",
     "a NotImplemented raise"),
    ("abort-cjk.zg", 'fn main() {\n\ts := "日本語"; x := (1 +)\n}\n', "an abort after CJK"),
    ("abort-astral.zg", 'fn main() {\n\tprint("😀😀" + str((1 +)))\n}\n', "an abort after a surrogate pair"),
]:
    p, d = abort_diag(name, text)
    at = built_place(p)
    check(at is not None and at[0] == p, "%s: zerg build names a place in the file" % what, at)
    if at is not None and d is not None:
        check(d["range"]["start"] == {"line": at[1] - 1, "character": utf16_char(text, at[1], at[2])},
              "%s is published at %s:%d:%d, where zerg build puts it" % (what, name, at[1], at[2]),
              d["range"])
        check("-->" not in d["message"], "%s: the range carries the place, not the sentence" % what,
              d["message"])
    else:
        check(False, "%s publishes one diagnostic" % what, d)

# an abort in ANOTHER file of the program names a place this buffer does not have: it stays
# at the top, and its sentence keeps the trailer, which is then the only thing saying where
open(os.path.join(tmp, "abortdep.zg"), "w").write("pub fn f() {\n\tx := (1 +)\n}\n")
p, d = abort_diag("abort-import.zg", 'import "./abortdep"\n\nfn main() {\n\tabortdep.f()\n}\n')
at = built_place(p)
check(at is not None and at[0] == os.path.join(os.path.dirname(p), "abortdep.zg"),
      "an imported file's abort: zerg build names the imported file", at)
check(d is not None and d["range"] == TOP and at is not None
      and d["message"].endswith("  --> %s:%d:%d" % at),
      "an abort placed in another file lands at the top and keeps its place in the sentence", d)

# an abort that names NO place still lands at the top. A dangling import is one: the loader
# resolves `./dangle` to a file it then cannot open, and says so with no `-->` at all.
os.symlink("nowhere.zg", os.path.join(tmp, "dangle.zg"))
p, d = abort_diag("abort-noplace.zg", 'import "./dangle"\n\nfn main() {\n\tprint 1\n}\n')
at = built_place(p)
check(at is None, "a dangling import: zerg build names no place", at)
check(d is not None and d["range"] == TOP and "-->" not in d["message"],
      "an abort naming no place lands at the top of the file", d)

# --- a refusal raised by the lowering walk is published, sentence and place ----------------
#
# Everything above aborts in the LOADER. The walk that checks a loaded program refuses by raise
# too, and the server reads a raise's text to tell it from a walk that returned — so a refusal
# whose text came back empty (#237: a function body's abort is re-raised on its way out) was
# published as a clean buffer, and the lint findings with it. One program per ROUTE the walk
# raises out by — each pass of emit_unit_at that lowers or judges something — held to what
# `zerg build --emit check` prints: the code, the sentence and the place. A fixture the command
# stops refusing fails here rather than dropping out. An abort inside a decorator's expansion is
# `BUFFERS` J's.
ABORT = re.compile(r"(E\d+) (.*)\n  --> (.+):(\d+):(\d+)\n?$")

for name, text, what in [
    ("walk-body.zg", "fn main() {\n\tx := 2\n\tprint match x { _ => 1  2 => 2 }\n}\n",
     "a refusal in a function body"),
    ("walk-generic.zg", "fn pick[T](v: T, x: int) -> int {\n\treturn match x { _ => 1  2 => 2 }\n}\n\n"
     "fn main() {\n\tprint pick(true, 2)\n}\n", "a refusal in a specialized template"),
    ("walk-method.zg", "struct P {\n\tpub x: int\n}\n\nimpl P {\n\tfn pick(this) -> int {\n"
     "\t\treturn match this.x { _ => 1  2 => 2 }\n\t}\n}\n\nfn main() {\n\tprint P(2).pick()\n}\n",
     "a refusal in a method"),
    ("walk-decl.zg", "struct A {\n\tb: B\n}\n\nstruct B {\n\ta: A\n}\n\nfn main() {\n\tprint 1\n}\n",
     "a refusal in a declaration pass"),
    ("walk-entry.zg", 'fn main() -> str {\n\treturn "x"\n}\n', "a refusal of the entry"),
    ("walk-global.zg", "x := match 2 {\n\t_ => 1\n\t2 => 2\n}\n\nfn main() {\n\tprint x\n}\n",
     "a refusal in a module-level initializer"),
]:
    p = os.path.join(tmp, name)
    open(p, "w", encoding="utf-8").write(text)
    u = "file://" + os.path.abspath(p)
    _, fr = run([INIT, {"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": {
        "textDocument": {"uri": u, "languageId": "zerg", "version": 1, "text": text}}}, EXIT])
    ds = [f for f in fr if f.get("method") == "textDocument/publishDiagnostics"]
    got = [("%s %s" % (x.get("code", ""), x["message"]), x["range"]["start"])
           for x in (ds[0]["params"]["diagnostics"] if ds else [])]
    err = subprocess.run([zerg, "build", "--emit", "check", p], stdout=subprocess.DEVNULL,
                         stderr=subprocess.PIPE, timeout=120).stderr.decode("utf-8")
    m = ABORT.match(err)
    if not m or os.path.abspath(m.group(3)) != os.path.abspath(p):
        check(False, "%s: zerg build --emit check refuses %s with one coded abort in it" % (what, name), err)
        continue
    line, col = int(m.group(4)), int(m.group(5))
    want = [("%s %s" % (m.group(1), m.group(2)),
             {"line": line - 1, "character": utf16_char(text, line, col)})]
    check(got == want, "%s is published as zerg build --emit check prints it" % what, got)

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

# --- 5. the outline's OTHER reading carries the parameters ------------------------------
#
# The outline above is compared against `--emit ast`, so a fact missing from BOTH is agreed
# on and invisible. That is exactly what happened to a declaration's type parameters: the
# dump printed `struct Box` for `struct Box[T]` and `fn wrap` for `fn wrap[T]`, and the
# comparison was green because the outline reports a NAME, which is `Box` either way.
#
# So this case does not compare the two readings — it asserts the content of the one that is
# supposed to carry more. `--emit ast` answers "did the parser see what I wrote", and a
# reader asking why a generic did not specialize was shown a declaration with no parameters
# at all.
gen="$tmp/generic.zg"
cat >"$gen" <<'ZG'
struct Box[T] {
	v: T
}

fn wrap[T](n: T) -> Box[T] {
	return Box(n)
}

fn main() {
	b := wrap(1)
	print(b.v)
}
ZG
gen_dump=$("$ZERG" build --emit ast "$gen" 2>&1 || true)
for want in "struct Box[T]" "fn wrap[T](" ; do
	case "$gen_dump" in
	*"$want"*) ;;
	*)
		echo "lsp-check: --emit ast does not carry \`$want\` — a declaration's type parameters are"
		echo "           dropped from the dump, and the outline cannot see it because it reports a name"
		printf '%s\n' "$gen_dump" | sed -n '1,12p'
		exit 1
		;;
	esac
done

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

# --- 6. one check is one walk -------------------------------------------------------------
#
# Every case above asks WHAT a check says, and a check that lowers the program twice says the
# same thing as one that lowers it once — `check-equal` is green on both. The server did exactly
# that for a while: the errors from one walk, then `lint_program` walking again for the L5xx
# notes, and the second walk was about half of every check's seconds on the compiler's own
# program (#22).
#
# So this measures the COST, from outside the process, against the command that is one walk by
# definition: `zerg build --emit check` on the same program. The server additionally runs the
# tree rules and the protocol, which on this program is about a tenth of a walk; a second walk
# doubles it. 1.5 sits between the two with room on both sides.
#
# THE UNIT IS INSTRUCTIONS RETIRED where the platform counts them (macOS `time -l`), and CPU
# time otherwise. Not wall time, which a busy machine inflates, and on Apple silicon not CPU time
# either: the same work costs about three times the seconds on an efficiency core, and which
# core a process lands on is the scheduler's choice, not the program's. An instruction count is
# the same number however the machine is loaded.
#
# `src/compiler/zergc.zg` because it is the program the issue is about — opening ANY file under
# `src/compiler/` checks this program — and because it is large enough that the walk is the
# whole cost. On a small example the process start-up is, and a second walk would not show.
if ! "$PY" - "$ZERG" src/compiler/zergc.zg <<'PYEOF'
import json, os, re, resource, subprocess, sys

zerg, path = sys.argv[1], sys.argv[2]

# The shape of the available `time` is DISCOVERED, as mem-peak-check.sh does, rather than
# assumed from the platform name — and decided once, so the two runs cannot be measured in
# different units and compared anyway.
TIMER = ["/usr/bin/time", "-l"]
try:
    probe = subprocess.run(TIMER + ["true"], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
    counts = probe.returncode == 0 and b"instructions retired" in probe.stderr
except OSError:
    counts = False
unit = "instructions" if counts else "CPU seconds"

def cost(cmd, stdin=b""):
    run = lambda c: subprocess.run(c, input=stdin, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=600)
    if counts:
        p = run(TIMER + cmd)
        m = re.search(rb"(\d+)\s+instructions retired", p.stderr)
        if not m:
            print("WALK      `time -l` counted no instructions for `%s`" % " ".join(cmd[1:]))
            sys.exit(1)
        return p, int(m.group(1))
    before = resource.getrusage(resource.RUSAGE_CHILDREN)
    p = run(cmd)
    after = resource.getrusage(resource.RUSAGE_CHILDREN)
    return p, (after.ru_utime + after.ru_stime) - (before.ru_utime + before.ru_stime)

ref, one = cost([zerg, "build", "--emit", "check", path])
if ref.returncode != 0:
    print("WALK      `zerg build --emit check %s` failed, so there is no walk to measure against" % path)
    sys.exit(1)

text = open(path, encoding="utf-8").read()
uri = "file://" + os.path.abspath(path)
msgs = [
    {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"capabilities": {}}},
    {"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": {
        "textDocument": {"uri": uri, "languageId": "zerg", "version": 1, "text": text}}},
    {"jsonrpc": "2.0", "id": 2, "method": "shutdown"},
    {"jsonrpc": "2.0", "method": "exit"},
]
wire = b"".join(b"Content-Length: %d\r\n\r\n%s" % (len(b), b) for b in (json.dumps(m).encode() for m in msgs))
lsp, got = cost([zerg, "lsp"], wire)

# A session that published nothing did not check anything, and costs less than a walk for
# that reason alone — which would pass this case for the wrong one. So does a check that
# ABORTED: it publishes the one error it raised and stops part-way. The reference
# build exited 0, so a complete check of this program publishes no error at all.
published = None
out, i = lsp.stdout, 0
while i < len(out):
    j = out.find(b"\r\n\r\n", i)
    if j < 0:
        break
    n = int(re.search(rb"Content-Length:\s*(\d+)", out[i:j], re.I).group(1))
    frame = json.loads(out[j + 4:j + 4 + n].decode("utf-8"))
    if frame.get("method") == "textDocument/publishDiagnostics":
        published = frame["params"]["diagnostics"]
    i = j + 4 + n
if published is None:
    print("WALK      the server published no diagnostics for %s, so no check was measured" % path)
    sys.exit(1)
errors = [d["message"] for d in published if d.get("severity") == 1]
if errors:
    print("WALK      the server reports an error in %s, which builds: %s" % (path, errors[0]))
    sys.exit(1)

ratio = got / one
print("WALK      one check of %s costs %.2f walks (%s: check %s, server %s)" % (path, ratio, unit, one, got))
if ratio >= 1.5:
    print("WALK      a check walks the program more than once")
    sys.exit(1)
PYEOF
then
	echo "lsp-check: a check of the compiler's own program is not one complete walk"
	exit 1
fi
# --- 7. a name answers with the declaration `zerg build` resolved it to ---------------------
#
# `definition` and `references` are two views of the name index the check builds (#24), and
# the index is the compiler's resolution recorded as it happened. Three properties hold the
# server to that, each of which a DIFFERENT wrong index fails:
#
#   positions   hand-written cases — a shadowed binding, a parameter beside a local of its
#               name, a generic method called at two types, a call through a spec bound, a
#               call in a spec's provided method and in the `impl` of a bounded generic type
#               (asked again with the instantiations in the other order), a closure's capture,
#               an `impl`'s type parameter, a named argument, a construction's field name, a
#               `?.` field, a destructured binding, a variant, a field, a member of another
#               file, a namespace, the standard library, an intrinsic (no declaration), and a
#               name after a line of CJK — each with the one place it must answer. An index
#               that resolved by SPELLING answers most of them somewhere plausible and wrong.
#   symmetry    every identifier in the fixture that has a definition D is among D's
#               references — and no declaration is left with none while a name spelled like
#               it answers nothing, which is what a use the index DROPPED looks like. The
#               SHARED places — a generic body's `x.n` at two types, answering with every
#               declaration it resolved to — are derived from the answers and must be exactly
#               the ones the fixture writes: one more is a place wrongly shared.
#   rename      held to `zerg build --emit check` and to what the program PRINTS: renaming a
#               declaration and every reference to a fresh name must leave both exactly as
#               they were, and renaming every reference BUT the declaration must not build. A
#               reference the index missed is a use left under the old name; one it gave the
#               wrong declaration is a use renamed away from the binding it read, which often
#               still builds and prints something else. A shared place and the declarations
#               it may be are renamed together, as one group, which is a rename that is valid.
#
# The fixture builds clean, and that is asserted first: the rename property compares against
# its diagnostics, and a fixture that stopped building would make every rename "identical".
mkdir -p "$tmp/names"
cat >"$tmp/names/lib.zg" <<'ZG'
pub struct Point {
	pub x: int
	pub y: int
}

pub fn twice(m: int) -> int {
	return m * 2
}

pub LIMIT := 3
ZG
cat >"$tmp/names/main.zg" <<'ZG'
import (
	"math"
	"strings"

	"./lib"
)

enum Colour {
	Red
	Green
}

spec Named {
	fn name() -> str
}

spec Greet {
	fn hi() -> str

	fn twice() -> str {
		return this.hi() + this.hi()
	}
}

struct Dog {
	pub age: int
}

struct Cat {
	pub lives: int
}

impl Greet for Dog {
	fn hi() -> str {
		return "woof"
	}
}

impl Greet for Cat {
	fn hi() -> str {
		return "meow"
	}
}

struct Box[T: Named] {
	pub v: T
}

impl Box[T] {
	fn label() -> str {
		return this.v.name()
	}

	fn value() -> T {
		return this.v
	}

	fn count() -> int {
		return this.v.n + 0
	}
}

spec Scaled {
	fn scale(by: int) -> int
}

fn getn[T](x: T) -> int {
	return x.n
}

fn sc[T: Scaled](x: T) -> int {
	return x.scale(by: 2)
}

struct Node {
	pub val: int
	pub nxt: Node?
}

enum Opt[X] {
	Has(X)
	Nope
}

struct Wrap[A] {
	pub a: A
}

struct Two[X, Y] {
	pub x: X
	pub y: Y
}

impl Two[Wrap[int], U] {
	fn gety() -> U {
		return this.y
	}
}

struct Tri[X, Y] {
	pub x: X
	pub y: Y
}

impl Tri[
	int,
	V] {
	fn getv() -> V {
		return this.y
	}
}

fn opt_val(o: Opt[int]) -> int {
	return match o {
		Opt.Has(v) => v
		Opt.Nope => 0
	}
}

fn dflt(a: int, b: int = 2) -> int {
	return a + b
}

fn pair() -> (int, int) {
	return (1, 2)
}

struct P {
	pub n: int
}

struct Q {
	pub n: int
}

impl Named for P {
	fn name() -> str {
		return "p"
	}
}

impl Named for Q {
	fn name() -> str {
		return "q"
	}
}

impl P {
	LIMIT := 40

	fn pick[U](t: U) -> U {
		print this.n
		return t
	}

	fn make() -> P {
		return P(1)
	}
}

impl Scaled for P {
	fn scale(by: int) -> int {
		return this.n * by
	}
}

impl Scaled for Q {
	fn scale(by: int) -> int {
		return this.n + by
	}
}

# a BINDING of a type's spelling does not stop `T.x` naming the type: the compiler asks for the
# enum or the struct first, and the index is recorded where it does
fn shadowed() -> int {
	Colour := 5
	P := 3
	return paint(Colour.Red) + P.LIMIT + P.make().n + Colour + P
}

fn show[T: Named](v: T) -> str {
	return v.name()
}

fn f(x: int) -> int {
	return x + 1
}

fn paint(c: Colour) -> int {
	return match c {
		Colour.Red => 1
		Colour.Green => 2
	}
}

fn main() {
	sh := 1
	if true {
		sh := 2
		print sh
	}
	p := P(f(sh))
	print p.pick(1)
	print p.pick("s")
	print show(p)
	print show(Q(2))
	print paint(Colour.Red)
	q := lib.Point(lib.twice(lib.LIMIT), p.n)
	print q.x
	print strings.has_prefix("ab", "a")
	s := "日本語" + str(sh)
	print s
	print math.trunc(2.5)
	k := 10
	g := fn (z: int) -> int {
		return z + k
	}
	print g(2)
	print Dog(age: 1).twice()
	print Cat(lives: 9).twice()
	print Box(Q(1)).label()
	print Box(P(1)).label()
	print Box(P(3)).value().n
	nd := Node(val: 1, nxt: Node(val: 2, nxt: nil))
	print nd.nxt?.val ?? 0
	print dflt(a: 1, b: 3)
	(u, w) := pair()
	print u + w
	print getn(P(1)) + getn(Q(2))
	print Box(Q(4)).count() + Box(P(5)).count()
	print sc(P(1)) + sc(Q(2))
	print shadowed()
	a := Opt.Has(3)
	b: Opt[int] = Opt.Nope
	print opt_val(a) + opt_val(b)
	print Two(Wrap(1), 5).gety() + Tri(1, 6).getv()
	x := 5
	print f(x) + x
}
ZG
if ! "$ZERG" build --emit check "$tmp/names/main.zg" >/dev/null 2>"$tmp/names.cc"; then
	echo "NAMES     the fixture program does not build, so there is nothing to hold the index to"
	sed 's/^/  /' "$tmp/names.cc"
	exit 1
fi
if ! "$PY" - "$ZERG" "$tmp/names" src/stdlib/strings.zg src/stdlib/math.zg <<'PYEOF'
import json, os, re, shutil, subprocess, sys

zerg, root, strings_zg, math_zg = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
MAIN, LIB = os.path.join(root, "main.zg"), os.path.join(root, "lib.zg")
src = {MAIN: open(MAIN, encoding="utf-8").read(), LIB: open(LIB, encoding="utf-8").read()}

def uri_of(p):
    return "file://" + os.path.abspath(p)

def u16(text, line, col):
    head = text.split("\n")[line - 1].encode("utf-8")[:col - 1].decode("utf-8")
    return len(head.encode("utf-16-le")) // 2

def frame(m):
    b = json.dumps(m).encode()
    return b"Content-Length: %d\r\n\r\n%s" % (len(b), b)

# ONE SESSION answers every question: the buffer is opened once and each request is a frame
# after it, so the index asked is the one that check built.
def ask(path, reqs):
    msgs = [{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"capabilities": {}}},
            {"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": {"textDocument": {
                "uri": uri_of(path), "languageId": "zerg", "version": 1, "text": src[path]}}}]
    for k, (method, p, line, ch) in enumerate(reqs):
        params = {"textDocument": {"uri": uri_of(p)}, "position": {"line": line, "character": ch}}
        if method == "textDocument/references":
            params["context"] = {"includeDeclaration": True}
        msgs.append({"jsonrpc": "2.0", "id": 100 + k, "method": method, "params": params})
    msgs += [{"jsonrpc": "2.0", "id": 2, "method": "shutdown"}, {"jsonrpc": "2.0", "method": "exit"}]
    out = subprocess.run([zerg, "lsp"], input=b"".join(frame(m) for m in msgs),
                         stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=300).stdout
    got, i = {}, 0
    while i < len(out):
        j = out.find(b"\r\n\r\n", i)
        if j < 0:
            break
        n = int(re.search(rb"Content-Length:\s*(\d+)", out[i:j], re.I).group(1))
        f = json.loads(out[j + 4:j + 4 + n].decode("utf-8"))
        if "id" in f and f["id"] >= 100:
            got[f["id"] - 100] = f.get("result")
        i = j + 4 + n
    return [got.get(k, "no reply") for k in range(len(reqs))]

def place(loc):
    return (loc["uri"][len("file://"):], loc["range"]["start"]["line"], loc["range"]["start"]["character"])

# at(path, snippet, name, nth) is the place of the nth `name` on the first line holding
# `snippet`, as LSP spells it: a 0-based line and a UTF-16 character
def at(path, snippet, name, nth=0):
    lines = src[path].split("\n") if path in src else open(path, encoding="utf-8").read().split("\n")
    for ln, text in enumerate(lines):
        if snippet in text:
            cols = [m.start() for m in re.finditer(r"(?<![A-Za-z0-9_])%s(?![A-Za-z0-9_])" % re.escape(name), text)]
            b = len(text[:cols[nth]].encode("utf-8"))
            head = text.encode("utf-8")[:b].decode("utf-8")
            return (os.path.abspath(path), ln, len(head.encode("utf-16-le")) // 2)
    raise SystemExit("NAMES     the fixture has no line holding %r" % snippet)

# member(path, head, name) is the first `name` on a line after the first line holding `head` —
# the field `n` of `struct Q {`, where the line alone is not unique
def member(path, head, name):
    lines = src[path].split("\n")
    start = [i for i, t in enumerate(lines) if head in t][0]
    for ln in range(start + 1, len(lines)):
        m = re.search(r"(?<![A-Za-z0-9_])%s(?![A-Za-z0-9_])" % re.escape(name), lines[ln])
        if m:
            return (os.path.abspath(path), ln, len(lines[ln][:m.start()].encode("utf-16-le")) // 2)
    raise SystemExit("NAMES     the fixture has no %r after %r" % (name, head))

bad = 0
def check(ok, what, got):
    global bad
    if not ok:
        print("NAMES     %s — got %r" % (what, got))
        bad += 1

# --- positions ---
spec_req = at(MAIN, "\tfn name() -> str", "name")
CASES = [
    ("a shadowing binding", at(MAIN, "\t\tprint sh", "sh"), at(MAIN, "\t\tsh := 2", "sh")),
    ("the binding it shadows", at(MAIN, "p := P(f(sh))", "sh"), at(MAIN, "\tsh := 1", "sh")),
    ("a parameter, beside a local of its name", at(MAIN, "return x + 1", "x"), at(MAIN, "fn f(x: int)", "x")),
    ("the local beside it", at(MAIN, "print f(x) + x", "x", 1), at(MAIN, "\tx := 5", "x")),
    ("a generic method at one type", at(MAIN, "p.pick(1)", "pick"), at(MAIN, "fn pick[U]", "pick")),
    ("the same method at another", at(MAIN, "p.pick(\"s\")", "pick"), at(MAIN, "fn pick[U]", "pick")),
    ("a type parameter", at(MAIN, "fn pick[U]", "U", 1), at(MAIN, "fn pick[U]", "U")),
    ("a call through a spec bound", at(MAIN, "return v.name()", "name"), spec_req),
    ("a qualified variant", at(MAIN, "print paint(Colour.Red)", "Red"), at(MAIN, "\tRed", "Red")),
    ("a variant in a pattern", at(MAIN, "Colour.Green => 2", "Green"), at(MAIN, "\tGreen", "Green")),
    ("the enum qualifying it", at(MAIN, "print paint(Colour.Red)", "Colour"), at(MAIN, "enum Colour", "Colour")),
    ("a field of the struct the target is", at(MAIN, "lib.LIMIT), p.n)", "n"), at(MAIN, "\tpub n: int", "n")),
    ("a field declared in another file", at(MAIN, "print q.x", "x"), at(LIB, "\tpub x: int", "x")),
    ("a function another file declares", at(MAIN, "lib.twice(", "twice"), at(LIB, "pub fn twice", "twice")),
    ("a constant another file declares", at(MAIN, "lib.LIMIT", "LIMIT"), at(LIB, "pub LIMIT", "LIMIT")),
    ("a type another file declares", at(MAIN, "q := lib.Point", "Point"), at(LIB, "pub struct Point", "Point")),
    ("a namespace", at(MAIN, "q := lib.Point", "lib"), at(MAIN, "\"./lib\"", "lib")),
    ("a standard-library function", at(MAIN, "strings.has_prefix", "has_prefix"), at(strings_zg, "pub fn has_prefix(", "has_prefix")),
    ("a name after a line of CJK", at(MAIN, "\"日本語\" + str(sh)", "sh"), at(MAIN, "\tsh := 1", "sh")),
    ("a closure's capture", at(MAIN, "return z + k", "k"), at(MAIN, "\tk := 10", "k")),
    ("a closure's parameter", at(MAIN, "return z + k", "z"), at(MAIN, "g := fn (z: int)", "z")),
    ("a call in a spec's provided method", at(MAIN, "return this.hi() + this.hi()", "hi"), at(MAIN, "\tfn hi() -> str", "hi")),
    ("a call in the impl of a bounded generic type", at(MAIN, "return this.v.name()", "name"), spec_req),
    ("an impl's type parameter", at(MAIN, "fn value() -> T", "T"), at(MAIN, "impl Box[T]", "T")),
    ("an impl's type parameter after a nested application", at(MAIN, "fn gety() -> U", "U"), at(MAIN, "impl Two[Wrap[int], U]", "U")),
    ("an impl's type parameter on a line of its own", at(MAIN, "fn getv() -> V", "V"), at(MAIN, "\tV] {", "V")),
    ("a named argument", at(MAIN, "print dflt(a: 1, b: 3)", "b"), at(MAIN, "fn dflt(a: int, b: int", "b")),
    ("a construction's field name", at(MAIN, "print Dog(age: 1)", "age"), at(MAIN, "\tpub age: int", "age")),
    ("a field read through `?.`", at(MAIN, "print nd.nxt?.val", "val"), at(MAIN, "\tpub val: int", "val")),
    ("a destructured binding", at(MAIN, "print u + w", "w"), at(MAIN, "(u, w) := pair()", "w")),
    ("a variant through an enum a binding shadows", at(MAIN, "return paint(Colour.Red)", "Red"), at(MAIN, "\tRed", "Red")),
    ("the enum a binding shadows", at(MAIN, "return paint(Colour.Red)", "Colour"), at(MAIN, "enum Colour", "Colour")),
    ("an associated value through a struct a binding shadows", at(MAIN, "+ P.LIMIT +", "LIMIT"), at(MAIN, "\tLIMIT := 40", "LIMIT")),
    ("an associated fn through a struct a binding shadows", at(MAIN, "+ P.make().n", "make"), at(MAIN, "\tfn make() -> P", "make")),
    ("the struct a binding shadows", at(MAIN, "+ P.LIMIT +", "P"), at(MAIN, "struct P {", "P")),
    ("a field a generic body reads at two types", at(MAIN, "\treturn x.n", "n"), [member(MAIN, "struct P {", "n"), member(MAIN, "struct Q {", "n")]),
    ("a field a generic impl reads at two types", at(MAIN, "return this.v.n + 0", "n"), [member(MAIN, "struct P {", "n"), member(MAIN, "struct Q {", "n")]),
    ("a named argument a generic body passes at two types", at(MAIN, "return x.scale(by: 2)", "by"), [member(MAIN, "impl Scaled for P {", "by"), member(MAIN, "impl Scaled for Q {", "by")]),
    ("a variant of a generic enum, qualified", at(MAIN, "a := Opt.Has(3)", "Has"), at(MAIN, "\tHas(X)", "Has")),
    ("the generic enum qualifying it", at(MAIN, "a := Opt.Has(3)", "Opt"), at(MAIN, "enum Opt[X]", "Opt")),
    ("a generic enum's variant read as a value", at(MAIN, "b: Opt[int] = Opt.Nope", "Nope", 0), at(MAIN, "\tNope", "Nope")),
    ("an intrinsic, which no reader declared", at(math_zg, "return __zrt_trunc(x)", "__zrt_trunc"), None),
    ("a built-in conversion", at(MAIN, "\"日本語\" + str(sh)", "str"), None),
]
defs = ask(MAIN, [("textDocument/definition", p, l, c) for _, (p, l, c), _ in CASES])
# an answer as the test compares it: a place, the LIST of places a shared use answers with, or
# None. The list is compared IN ORDER, and the order wanted is where each is declared: the first
# entry is where a client jumps, and it may not be whichever instantiation was walked first.
def answer(got):
    if isinstance(got, dict):
        return place(got)
    if isinstance(got, list):
        return [place(x) for x in got]
    return got

def wanted(want):
    return sorted(want) if isinstance(want, list) else want

for (what, use, want), got in zip(CASES, defs):
    if want is None:
        check(got is None, "%s answers null" % what, got)
        continue
    check(answer(got) == wanted(want), "%s resolves to %s" % (what, want), answer(got))
positions = len(CASES)

# the SHARED places the fixture writes: a place one body resolves two ways, answering with both
EXPECT_SHARED = set(use for what, use, want in CASES if isinstance(want, list))
check(len(EXPECT_SHARED) >= 3, "the fixture has the places one body resolves two ways", EXPECT_SHARED)

# THE ANSWER MAY NOT DEPEND ON WHICH INSTANTIATION WAS WALKED FIRST: the same two calls with the
# instantiations the other way round must answer the same
swapped = src[MAIN].replace("\tprint Dog(age: 1).twice()\n\tprint Cat(lives: 9).twice()", "\tprint Cat(lives: 9).twice()\n\tprint Dog(age: 1).twice()")
swapped = swapped.replace("\tprint Box(Q(1)).label()\n\tprint Box(P(1)).label()", "\tprint Box(P(1)).label()\n\tprint Box(Q(1)).label()")
for a, b in (("getn(P(1)) + getn(Q(2))", "getn(Q(2)) + getn(P(1))"),
             ("Box(Q(4)).count() + Box(P(5)).count()", "Box(P(5)).count() + Box(Q(4)).count()"),
             ("sc(P(1)) + sc(Q(2))", "sc(Q(2)) + sc(P(1))")):
    check(a in swapped, "the fixture has `%s` to swap" % a, None)
    swapped = swapped.replace(a, b)
check(swapped != src[MAIN], "the fixture has the pairs of calls to swap", None)
keep = src[MAIN]
src[MAIN] = swapped
SWAPPED = ("a call in a spec's provided method", "a call in the impl of a bounded generic type",
           "a field a generic body reads at two types", "a field a generic impl reads at two types",
           "a named argument a generic body passes at two types")
for what, use, want in [c for c in CASES if c[0] in SWAPPED]:
    got = ask(MAIN, [("textDocument/definition", use[0], use[1], use[2])])[0]
    check(answer(got) == wanted(want), "%s answers the same with the instantiations swapped" % what, answer(got))
src[MAIN] = keep

# --- symmetry ---
# every identifier token of both files, strings and comments blanked so a word inside one
# is not asked about
def idents(path):
    out = []
    for ln, text in enumerate(src[path].split("\n")):
        code = re.sub(r'"[^"]*"', lambda m: " " * len(m.group(0)), text.split("#")[0])
        for m in re.finditer(r"[A-Za-z_][A-Za-z0-9_]*", code):
            out.append((path, ln, len(text[:m.start()].encode("utf-16-le")) // 2))
    return out
toks = idents(MAIN) + idents(LIB)
answers = ask(MAIN, [("textDocument/definition", p, l, c) for p, l, c in toks])
pairs = [(t, place(a)) for t, a in zip(toks, answers) if isinstance(a, dict)]
pairs += [(t, place(x)) for t, a in zip(toks, answers) if isinstance(a, list) for x in a]

# SHARED IS DERIVED, from what the index answers, and held to the fixture in both directions: a
# place the fixture writes that is not shared was resolved to one instantiation's declaration,
# and a shared place it does not write is a use the index wrongly gave up on
shared = {}
for t, a in zip(toks, answers):
    if isinstance(a, list):
        shared[(os.path.abspath(t[0]), t[1], t[2])] = [place(x) for x in a]
check(set(shared) == EXPECT_SHARED, "the shared places are exactly the fixture's",
      sorted(set(shared) ^ EXPECT_SHARED))
check(len(pairs) >= 60, "most of the fixture's names have a definition", len(pairs))
refs = ask(MAIN, [("textDocument/references", d[0], d[1], d[2]) for _, d in pairs])
decls = {}
for (use, d), r in zip(pairs, refs):
    ps = [place(x) for x in r] if isinstance(r, list) else []
    check((os.path.abspath(use[0]), use[1], use[2]) in ps,
          "%s:%d:%d is among the references of its definition %s:%d:%d"
          % (os.path.basename(use[0]), use[1] + 1, use[2] + 1, os.path.basename(d[0]), d[1] + 1, d[2] + 1), ps)
    decls[d] = r if isinstance(r, list) else []

# A DROPPED USE leaves its declaration with no reference but itself and the use with no answer,
# and symmetry cannot see that: a name with no definition is asked nothing. So every declaration
# referenced by nothing else is held to the spelling — no identifier of the fixture spelled like
# it may answer null.
def spelled(p, line, ch):
    t = src[p].split("\n")[line]
    k, n = 0, 0
    while n < ch:
        n += len(t[k].encode("utf-16-le")) // 2
        k += 1
    return re.match(r"[A-Za-z_][A-Za-z0-9_]*", t[k:]).group(0)
unanswered = {}
for t, a in zip(toks, answers):
    if a is None:
        unanswered.setdefault(spelled(*t), []).append(t)
lonely = 0
for d, r in decls.items():
    if d[0] in src and [place(x) for x in r] == [d]:
        lonely += 1
        nm = spelled(*d)
        check(nm not in unanswered, "`%s` at %s:%d:%d has no reference, and %d name(s) spelled like it answer nothing (first %s:%d:%d)"
              % (nm, os.path.basename(d[0]), d[1] + 1, d[2] + 1, len(unanswered.get(nm, [])), *(lambda u: (os.path.basename(u[0]), u[1] + 1, u[2] + 1))((unanswered.get(nm) or [d])[0])), None)

# --- rename ---
def check_out(files):
    tmp = root + ".rename"
    shutil.rmtree(tmp, ignore_errors=True)
    os.mkdir(tmp)
    for p, t in files.items():
        open(os.path.join(tmp, os.path.basename(p)), "w", encoding="utf-8").write(t)
    r = subprocess.run([zerg, "build", "--emit", "check", os.path.join(tmp, "main.zg")],
                       stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=120)
    return r.returncode, r.stdout.decode("utf-8").replace(tmp, root)

# and what the program PRINTS: a use renamed with the wrong declaration still builds when another
# binding of the old spelling is in scope, and then reads that binding instead
def run_out(files):
    tmp = root + ".run"
    shutil.rmtree(tmp, ignore_errors=True)
    os.mkdir(tmp)
    for p, t in files.items():
        open(os.path.join(tmp, os.path.basename(p)), "w", encoding="utf-8").write(t)
    exe = os.path.join(tmp, "prog")
    b = subprocess.run([zerg, "build", "--emit", "bin", "-o", exe, os.path.join(tmp, "main.zg")],
                       stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=300)
    if b.returncode != 0:
        return "build failed: " + b.stdout.decode("utf-8")[:200]
    return subprocess.run([exe], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=60).stdout.decode("utf-8")

def renamed(locs, fresh):
    files = dict(src)
    for p, line, ch in sorted(locs, key=lambda x: (x[0], x[1], -x[2])):
        ls = files[p].split("\n")
        t = ls[line]
        # the UTF-16 character back to a str index: the fixture's one astral-free CJK line
        # makes the two agree everywhere but after it, and the walk is exact either way
        k, n = 0, 0
        while n < ch:
            n += len(t[k].encode("utf-16-le")) // 2
            k += 1
        m = re.match(r"[A-Za-z_][A-Za-z0-9_]*", t[k:])
        ls[line] = t[:k] + fresh + t[k + len(m.group(0)):]
        files[p] = "\n".join(ls)
    return files

# a member of a `spec` or of an `impl … for` is one side of a CONTRACT: the requirement and each
# method keeping it are separate declarations, and renaming one of them alone breaks the program
# by design. What a rename does with a contract is #194's to decide; it is not this property's.
#
# A MEMBER, not the spec's own name: the line the declaration sits on is indented, and the first
# line above it that is not names the contract it is inside.
def in_contract(d):
    lines = src[d[0]].split("\n")
    if not lines[d[1]][:1].isspace():
        return False
    for text in reversed(lines[:d[1]]):
        if text and not text[0].isspace():
            return re.match(r"^(spec |impl(\[[^]]*\])? \S+ for )", text) is not None
    return False

base = check_out(src)
base_run = run_out(src)
check(base_run != "" and not base_run.startswith("build failed"), "the fixture builds and prints", base_run[:200])
renames = 0
skipped = {"stdlib": 0, "import path": 0, "contract": 0, "unreferenced": 0}

# A GROUP is what one rename takes: a declaration alone, or every declaration a shared place may
# be, joined through that place — `P.n` and `Q.n` through a generic `x.n`
group_of = {d: d for d in decls}
def root_of(d):
    while group_of[d] != d:
        d = group_of[d]
    return d
for cands in shared.values():
    for c in cands[1:]:
        if c in group_of and cands[0] in group_of:
            group_of[root_of(c)] = root_of(cands[0])
groups = {}
for d in decls:
    groups.setdefault(root_of(d), []).append(d)
grouped = sum(1 for g in groups.values() if len(g) > 1)

for n, members in enumerate(sorted(sorted(g) for g in groups.values())):
    locs = sorted(set(place(x) for d in members for x in decls[d]))
    d = members[0]
    # one outside the fixture (the standard library) is not the fixture's to edit; a declaration
    # inside a string is an import's path segment, and renaming a path is not a rename
    if any(p not in src for p, _, _ in locs):
        skipped["stdlib"] += 1
        continue
    if any(src[m[0]].split("\n")[m[1]][m[2] - 1:m[2]] in ('"', "/") for m in members):
        skipped["import path"] += 1
        continue
    if any(in_contract(m) for m in members):
        skipped["contract"] += 1
        continue
    others = [l for l in locs if l not in members]
    if not others:
        # held above: no name spelled like it answers nothing
        skipped["unreferenced"] += 1
        continue
    fresh = "zq_renamed_%d" % n
    what = ", ".join("%s:%d:%d" % (os.path.basename(m[0]), m[1] + 1, m[2] + 1) for m in members)
    files = renamed(locs, fresh)
    all_of = check_out(files)
    check(all_of == base, "renaming %s and its %d reference(s) leaves the diagnostics as they were"
          % (what, len(others)), all_of[1][:300])
    ran_out = run_out(files)
    check(ran_out == base_run, "renaming %s and its %d reference(s) leaves what the program prints as it was"
          % (what, len(others)), ran_out[:300])
    but_decl = check_out(renamed(others, fresh))
    check(but_decl != base, "renaming every reference of %s but the declarations is refused" % what,
          but_decl[1][:200])
    renames += 1
# FLOORS, each asked of the fixture as written: a filter that grew to swallow everything would
# otherwise leave this property green for having renamed nothing
check(renames >= 30, "enough declarations were renamed to mean something", renames)
check(grouped >= 2, "the fixture's shared places join declarations into groups", grouped)
for why, floor in (("stdlib", 2), ("import path", 3), ("contract", 5)):
    check(skipped[why] >= floor, "the fixture has at least %d declarations skipped as %s" % (floor, why), skipped[why])
check(skipped["unreferenced"] <= lonely, "every unreferenced declaration skipped was held to its spelling", skipped)

print("NAMES     %d positions, %d uses symmetric with their definition, %d shared, %d renames of which %d group several declarations (skipped: %s)" % (positions, len(pairs), len(shared), renames, grouped, ", ".join("%d %s" % (v, k) for k, v in sorted(skipped.items()))))
sys.exit(1 if bad else 0)
PYEOF
then
	echo "lsp-check: a name does not answer with the declaration the compiler resolved it to"
	exit 1
fi
# --- 8. a long session stays in the band of one check ---------------------------------------
#
# Every case above is one check, and a server can answer each of them correctly while every
# check leaves something behind. This one did (#23): whatever a check allocated and never gave
# back stayed, and a session's memory rose with the number of publishes — the leaks were a few
# small cells per identifier the parser named, scattered across the allocator's pages, and each
# scattered cell pins a page the next check cannot hand back.
#
# So this measures a SESSION: N checks of the compiler's own program in one process against a
# session of one, and fails when the long one peaks above K times the short one. A RATIO and not
# a number, because the number is the machine's — Linux and macOS count differently, and the
# same program is a different size on each — while "the same band as one check" is a ratio on
# either.
#
# THE COUNTER IS DISCOVERED, as section 6's is, and both sessions are measured by the one found,
# so its unit cancels in the ratio. Where `time -l` reports a PEAK FOOTPRINT (macOS) that is the
# one read, because the resident size cannot see this defect there: macOS compresses pages
# nobody touches, and the pages a leak pins are exactly those — the climbing server's resident
# size had all but stopped moving after twenty checks while its footprint kept rising. Elsewhere
# it is the maximum resident size, BSD `time -l` in bytes or GNU `time -v` in kilobytes, as
# mem-peak-check.sh reads it; Linux has no compressor in the way.
#
# THE ALLOCATOR IS TOLD NOT TO HOARD (`MallocSpaceEfficient=1`, macOS's own switch; nothing
# else reads it). By default the footprint also counts the free pages macOS's allocator keeps
# for reuse, and a server that leaks nothing grows that cache over its first checks and then
# holds it — how far depends on the machine and on which threads the walk ran on, so the healthy
# ratio was anywhere from 1.1 to 1.5 and crossed any K that also caught the climb. Told to give
# pages back, the healthy server's footprint after N checks is its footprint after one, and
# what a leak pins is all that is left to see.
#
# K IS THE COUNTER'S, because the two counters see a leak with different sharpness. The Linux
# resident size is quiet — a healthy server reads 1.00 to 1.01 at N, every run — so its K sits
# just above that noise and catches a leak a fraction the size of the one #23 was: the compiler
# with ONE of its fixes reverted (a `match` that never gave its scrutinee back, a few hundred
# kilobytes a check) read 1.09 to 1.10 against it, and the climbing server of #23 1.53. The macOS
# footprint is noisier — a healthy session reads 1.00 to 1.12 alone and once 1.36 among three
# sessions at once — so its K sits above that, and it sees the climb of #23 (1.30 to 1.57) but not
# a leak of that one reverted fix, which the footprint does not separate from flat.
#
# SO THIS IS THE AGGREGATE CHECK, AND IT IS NOT THE ONLY ONE. On Linux it catches a single
# reverted fix; on macOS it catches a climb of the size #23 was. The per-shape guarantee is
# `make mem-check`'s: each shape that leaked is a program there, counted allocation by allocation,
# and red on its own the day its fix is reverted, on either platform. Reading leaked bytes here
# instead would take `leaks(1)`, which needs a debuggable binary — a signing step this gate will
# not add to CI.
LSP_SESSION_N=30
LSP_SESSION_K_FOOTPRINT=1.2
LSP_SESSION_K_RSS=1.05
if ! "$PY" - "$ZERG" src/compiler/zergc.zg "$LSP_SESSION_N" "$LSP_SESSION_K_FOOTPRINT" "$LSP_SESSION_K_RSS" <<'PYEOF'
import json, os, re, subprocess, sys

zerg, path, n = sys.argv[1], sys.argv[2], int(sys.argv[3])
k_footprint, k_rss = float(sys.argv[4]), float(sys.argv[5])

TIMER, counter, label = None, None, None
for flag, pattern, what, kk in (("-l", rb"(\d+)\s+peak memory footprint", "peak footprint", k_footprint),
                                ("-l", rb"(\d+)\s+maximum resident set size", "maximum resident size", k_rss),
                                ("-v", rb"Maximum resident set size[^:]*:\s*(\d+)", "maximum resident size", k_rss)):
    try:
        probe = subprocess.run(["/usr/bin/time", flag, "true"], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
    except OSError:
        break
    if probe.returncode == 0 and re.search(pattern, probe.stderr):
        TIMER, counter, label, k = ["/usr/bin/time", flag], pattern, what, kk
        break
if TIMER is None:
    print("SESSION   /usr/bin/time understands neither -l nor -v, so nothing here can measure a peak")
    sys.exit(1)

text = open(path, encoding="utf-8").read()
uri = "file://" + os.path.abspath(path)

def peak(checks):
    msgs = [{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"capabilities": {}}},
            {"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": {
                "textDocument": {"uri": uri, "languageId": "zerg", "version": 1, "text": text}}}]
    for v in range(2, checks + 1):
        msgs.append({"jsonrpc": "2.0", "method": "textDocument/didChange", "params": {
            "textDocument": {"uri": uri, "version": v}, "contentChanges": [{"text": text}]}})
    msgs += [{"jsonrpc": "2.0", "id": 2, "method": "shutdown"}, {"jsonrpc": "2.0", "method": "exit"}]
    wire = b"".join(b"Content-Length: %d\r\n\r\n%s" % (len(b), b) for b in (json.dumps(m).encode() for m in msgs))
    env = dict(os.environ, MallocSpaceEfficient="1")
    p = subprocess.run(TIMER + [zerg, "lsp"], input=wire, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=1800, env=env)

    # A session that checked fewer times than it was asked to did not measure N checks, and one
    # that ABORTED part-way costs less for that reason alone. The program builds, so every
    # complete check publishes no error.
    published, out, i = [], p.stdout, 0
    while i < len(out):
        j = out.find(b"\r\n\r\n", i)
        if j < 0:
            break
        m = int(re.search(rb"Content-Length:\s*(\d+)", out[i:j], re.I).group(1))
        frame = json.loads(out[j + 4:j + 4 + m].decode("utf-8"))
        if frame.get("method") == "textDocument/publishDiagnostics":
            published.append(frame["params"]["diagnostics"])
        i = j + 4 + m
    if len(published) != checks:
        print("SESSION   a session of %d checks published %d times" % (checks, len(published)))
        sys.exit(1)
    errors = [d["message"] for ds in published for d in ds if d.get("severity") == 1]
    if errors:
        print("SESSION   the server reports an error in %s, which builds: %s" % (path, errors[0]))
        sys.exit(1)
    m = re.search(counter, p.stderr)
    if not m or int(m.group(1)) == 0:
        print("SESSION   `%s` printed no peak for a session of %d checks" % (" ".join(TIMER), checks))
        sys.exit(1)
    return int(m.group(1))

# THE NOISE ONLY EVER ADDS. A machine under memory pressure moves pages in and out of the
# footprint on the kernel's schedule, not the program's, and every stray reading measured here
# was HIGH — a one-check session at a third over its usual peak, a healthy long one at a
# quarter over flat. So each side is the lower of its readings: the baseline is the lower of two
# one-check sessions, which costs one check, and a long session over K is run once more before
# it fails the gate. A leak climbs every time it is run, so the confirmation keeps it red; a
# passing run costs nothing extra.
one = min(peak(1), peak(1))
many = peak(n)
ratio = many / one
print("SESSION   %d checks in one session peak at %.2f times one check (%s: %d, then %d)" % (n, ratio, label, one, many))
if ratio > k:
    again = peak(n)
    print("SESSION   confirming: a second session of %d checks peaks at %.2f times one check" % (n, again / one))
    ratio = min(ratio, again / one)
if ratio > k:
    print("SESSION   a long session climbs past %.2f times one check" % k)
    sys.exit(1)
PYEOF
then
	echo "lsp-check: a long session climbs out of the band of one check"
	exit 1
fi

# --- 9. hover is the document `zerg doc` prints for that declaration ------------------------
#
# #192: the index finds the declaration, this prints its document. So the property is not a
# transcript of what a hover looks like — it is that the two commands say the SAME THING about
# the same declaration:
#
#   the document  for every exposed declaration hovered here, `zerg doc` is asked for the same
#                 one and the two texts must match — the signature line it prints, and the
#                 comment under it word for word. THE ENTRY IS FOUND BY THE DECLARED NAME the
#                 case names, never by the text the hover produced, and it must be the only
#                 entry of that name: two declarations printing one signature (`log` has
#                 `Logger.trace` and a free `trace`) would otherwise let a lookup keyed on the
#                 answer pick the wrong entry and agree with itself.
#   the structure `zerg doc` wraps its prose to a page and a hover does not, so prose is
#                 compared PARAGRAPH BY PARAGRAPH with whitespace collapsed — and a fence is
#                 compared LINE FOR LINE, because a ` ```zerg ` block inside a comment is code
#                 and its lines are not prose to be reflowed. Collapsing everything would let a
#                 hover that joined the comment into one line agree about every word while
#                 destroying every paragraph break and every example in it.
#   the shape     a declaration NO document covers — a local binding, a parameter, the
#                 namespace an import bound — says what it is and is NOT marked undocumented;
#                 an exposed DECLARATION with no comment carries the mark the document prints;
#                 and a MEMBER with no comment carries nothing, which is how the page prints
#                 one.
#   the index     hover answers for exactly the names `definition` answers for, over every
#                 identifier in the fixture. Hover reads that index and resolves nothing, so a
#                 name with a declaration has a document and a name without one has no hover.
#
# and three answers that are silence: a name nobody declared, a position in a file the client
# never opened, and a buffer whose check aborted — the index is dropped, and a hover from the
# program the compiler no longer agrees is this one would be worse than none.
mkdir -p "$tmp/hover"
cat >"$tmp/hover/lib.zg" <<'ZG'
## lib — a module written to be read.

## twice doubles `m`.
# twice's maintainer note, which neither the page nor a hover shows.
##
## ```zerg
## lib.twice(3)
## ```
## ```output
## 6
## ```
pub fn twice(m: int) -> int {
	return m * 2
}

## Point is a place on a grid.
pub struct Point {
	## x is how far along it is.
	pub x: int

	pub y: int
}

pub fn plain(n: int) -> int {
	return n
}

## Weighed is what has a weight.
pub spec Weighed {
	fn weight() -> int
}
ZG
cat >"$tmp/hover/main.zg" <<'ZG'
import (
	"strings"

	"./lib"
)

## Colour is what a thing can be.
enum Colour {
	## Red is the loud one.
	Red

	Green
}

## quiet is private, and documented all the same.
# quiet's maintainer note, which a hover does not show.
fn quiet(n: int) -> int {
	return n + 1
}

spec Sized {
	fn size() -> int
}

struct Tin {
	pub n: int
}

impl Sized for Tin {
	fn size() -> int {
		return this.n
	}
}

fn measure[T: Sized](x: T) -> int {
	return x.size()
}

fn main() {
	p := lib.Point(1, 2)
	total := lib.twice(p.x) + lib.plain(p.y)
	print total + quiet(1)
	print strings.has_prefix("ab", "a")
	print int(Colour.Red)
	print measure(Tin(4))
}
ZG
if ! "$ZERG" build --emit check "$tmp/hover/main.zg" >/dev/null 2>"$tmp/hover.cc"; then
	echo 'HOVER     the fixture program does not build, so there is no index to hover from'
	sed 's/^/  /' "$tmp/hover.cc"
	exit 1
fi
if ! "$PY" - "$ZERG" "$tmp/hover" <<'PYEOF'
import json, os, re, subprocess, sys

zerg, root = sys.argv[1], sys.argv[2]
MAIN, LIB = os.path.join(root, "main.zg"), os.path.join(root, "lib.zg")
src = {MAIN: open(MAIN, encoding="utf-8").read(), LIB: open(LIB, encoding="utf-8").read()}

def uri_of(p):
    return "file://" + os.path.abspath(p)

def frame(m):
    b = json.dumps(m).encode()
    return b"Content-Length: %d\r\n\r\n%s" % (len(b), b)

# ONE SESSION per call: main.zg is opened, optionally changed, and every question is a frame
# after it — so the index asked is the one that check built.
def ask(reqs, change=None):
    msgs = [{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"capabilities": {}}},
            {"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": {"textDocument": {
                "uri": uri_of(MAIN), "languageId": "zerg", "version": 1, "text": src[MAIN]}}}]
    if change is not None:
        msgs.append({"jsonrpc": "2.0", "method": "textDocument/didChange", "params": {
            "textDocument": {"uri": uri_of(MAIN), "version": 2},
            "contentChanges": [{"text": change}]}})
    for k, (method, p, line, ch) in enumerate(reqs):
        msgs.append({"jsonrpc": "2.0", "id": 100 + k, "method": method, "params": {
            "textDocument": {"uri": uri_of(p)}, "position": {"line": line, "character": ch}}})
    msgs += [{"jsonrpc": "2.0", "id": 2, "method": "shutdown"}, {"jsonrpc": "2.0", "method": "exit"}]
    out = subprocess.run([zerg, "lsp"], input=b"".join(frame(m) for m in msgs),
                         stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=300).stdout
    got, i = {}, 0
    while i < len(out):
        j = out.find(b"\r\n\r\n", i)
        if j < 0:
            break
        n = int(re.search(rb"Content-Length:\s*(\d+)", out[i:j], re.I).group(1))
        f = json.loads(out[j + 4:j + 4 + n].decode("utf-8"))
        if "id" in f and f["id"] >= 100:
            got[f["id"] - 100] = f.get("result")
        i = j + 4 + n
    return [got.get(k, "no reply") for k in range(len(reqs))]

# at(path, snippet, name, nth) is the place of the nth `name` on the first line holding
# `snippet`, as LSP spells it. The fixture is ASCII, so a byte column is a UTF-16 one.
def at(path, snippet, name, nth=0):
    for ln, text in enumerate(src[path].split("\n")):
        if snippet in text:
            cols = [m.start() for m in re.finditer(r"(?<![A-Za-z0-9_])%s(?![A-Za-z0-9_])" % re.escape(name), text)]
            return (path, ln, cols[nth])
    raise SystemExit("HOVER     the fixture has no line holding %r" % snippet)

bad = 0
def check(ok, what, got):
    global bad
    if not ok:
        print("HOVER     %s — got %r" % (what, got))
        bad += 1

# what a hover carries: the fenced signature, and the prose under it
def parts(md):
    lines = md.split("\n")
    if lines[0] != "```zerg" or "```" not in lines[1:]:
        return None, None
    end = lines.index("```", 1)
    return "\n".join(lines[1:end]), "\n".join(lines[end + 1:]).strip()

# `zerg doc` wraps prose to a page and a hover does not, so what is compared is every word in
# its order — which a wrap moves between lines and cannot change.
def norm(s):
    return " ".join(s.split())

# a comment as STRUCTURE: its blocks in order — a paragraph collapsed to its words, a fence
# kept LINE FOR LINE. A wrap moves words inside a paragraph and cannot move them across a blank
# line, and it never touches a fence, so this is everything about the shape that survives
# rendering — including where the fence sits among the prose.
def shape(text):
    out, cur, fence = [], [], None
    for line in text.split("\n") + [""]:
        if fence is not None:
            fence.append(line.strip() if line.strip() == "```" else line)
            if line.strip() == "```":
                out.append(("fence", fence))
                fence = None
            continue
        if line.strip().startswith("```"):
            if cur:
                out.append(("p", norm(" ".join(cur))))
                cur = []
            fence = [line.strip()]
            continue
        if line.strip() == "":
            if cur:
                out.append(("p", norm(" ".join(cur))))
                cur = []
            continue
        cur.append(line)
    return out

# what `zerg doc` prints for one name, asked once per name however many cases read it
DOCS = {}
def doc_of(name):
    if name not in DOCS:
        env = dict(os.environ, NO_COLOR="1")
        r = subprocess.run([zerg, "doc", name], stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                           env=env, timeout=300)
        DOCS[name] = r.stdout.decode("utf-8")
    return DOCS[name]

# the hovers for a table of rows whose second column is a place
def hovers_at(rows):
    return ask([("textDocument/hover", r[1][0], r[1][1], r[1][2]) for r in rows])

# the name a signature line DECLARES, or None for a line that is not one. It is how an entry is
# found below: by the name the case asked about, and never by the text a hover answered with.
def declared(line):
    t = line.strip()
    m = re.match(r"^(?:unsafe )?(?:fn|struct|enum|spec|type)\s+([A-Za-z_][A-Za-z0-9_]*)", t)
    if m:
        return m.group(1)
    m = re.match(r"^(?:const |mut )?([A-Za-z_][A-Za-z0-9_]*)(?=:|\s*:=|\(|$)", t)
    return m.group(1) if m else None

# the entry `zerg doc` prints for one declared name: its signature line, and the block under it
# dedented — to the next line indented no further than the signature itself. It is an error for
# a document to hold two of that name here, rather than a reason to take the first.
def doc_entry(text, name):
    lines = text.split("\n")
    at = [i for i, line in enumerate(lines) if declared(line) == name]
    if len(at) != 1:
        return None, None
    i = at[0]
    ind = len(lines[i]) - len(lines[i].lstrip())
    out = []
    for rest in lines[i + 1:]:
        if rest.strip() and len(rest) - len(rest.lstrip()) <= ind:
            break
        out.append(rest[ind + 4:] if rest.strip() else "")
    return lines[i].strip(), "\n".join(out).strip()

# (what, where the cursor is, what `zerg doc` is asked, the name the entry is found by, whether
# the entry is the WHOLE of what the document says about it). A TYPE's entry carries its
# members and a hover carries the declaration's own comment, so that one is held to the start
# of the entry rather than to all of it — every other case here is exact.
NOCOMMENT = "an exposed declaration with no comment"
CASES = [
    ("a function another file declares", at(MAIN, "lib.twice(p.x)", "twice"), LIB, "twice", True),
    ("a field of it", at(MAIN, "lib.twice(p.x)", "x"), LIB, "x", True),
    ("the type that holds it", at(MAIN, "p := lib.Point", "Point"), LIB, "Point", False),
    (NOCOMMENT, at(MAIN, "lib.plain(p.y)", "plain"), LIB, "plain", True),
    ("a standard-library function", at(MAIN, "strings.has_prefix", "has_prefix"), "strings.has_prefix", "has_prefix", True),
    ("an enum", at(MAIN, "int(Colour.Red)", "Colour"), None, "Colour", True),
    ("a variant of it", at(MAIN, "int(Colour.Red)", "Red"), None, "Red", True),
    ("a private function", at(MAIN, "print total + quiet", "quiet"), None, "quiet", True),
]
documented = 0
fenced = 0
nocomment = None
for (what, _, key, name, whole), got in zip(CASES, hovers_at(CASES)):
    if not isinstance(got, dict):
        check(False, "%s has a hover" % what, got)
        continue
    kind = got["contents"].get("kind")
    check(kind == "markdown", "%s is markdown, which is what carries a fence" % what, kind)
    hsig, body = parts(got["contents"]["value"])
    if what == NOCOMMENT:
        nocomment = body
    if key is None:
        # a declaration NO document has: `zerg doc` shows what a module exposes, and a private
        # function and an enum of the entry file are in nobody's document. The comment above it
        # in the source is the second opinion, read the coarse way `doc-check` reads a `pub` —
        # its `##` lines, which are the reader's, and none of its `#` ones.
        lines = src[MAIN].split("\n")
        decl = [i for i, t in enumerate(lines) if declared(t) == name]
        check(len(decl) == 1, "the fixture declares `%s` exactly once" % name, decl)
        if len(decl) != 1:
            continue
        ln = decl[0]
        run = []
        i = ln - 1
        while i >= 0 and lines[i].strip().startswith("#"):
            if lines[i].strip().startswith("##"):
                run.insert(0, lines[i].strip()[2:].strip())
            i -= 1
        # as a document prints the head: without the brace that opens a body. There is no
        # `pub` to take off — a row reaches this branch because no document covers it
        want_sig = re.sub(r"\s*\{$", "", lines[ln].strip())
        want = "\n".join(run)
    else:
        want_sig, want = doc_entry(doc_of(key), name)
        check(want_sig is not None, "`zerg doc %s` prints exactly one entry declaring `%s`" % (key, name), want_sig)
        if want_sig is None:
            continue
    check(hsig == want_sig, "%s carries the signature the document prints" % what, hsig)

    # BLOCK FOR BLOCK, IN ORDER: a paragraph by its words, a fence by its lines. A hover that
    # reflowed a comment, joined it into one line, or moved its example fails here while
    # agreeing about every word. A TYPE's entry carries its members, so it is held to the
    # start of the entry — every other case to all of it.
    hb = shape(body)
    wb = shape(want)
    check(hb == (wb if whole else wb[:len(hb)]) and hb != [],
          "%s says what `zerg doc` says, block for block" % what, hb)
    if [b for b in hb if b[0] == "fence"]:
        fenced += 1
    # A `#` LINE IS THE MAINTAINER'S, and the fixture writes one into two of these comments. The
    # equality above holds a hover to the page, so this is the half that holds them both to the
    # marker: a hover and a page that each printed the note would still agree with each other.
    check("maintainer note" not in body, "%s shows none of its comment's `#` lines" % what, body)
    documented += 1
check(documented >= 6, "enough declarations were held to their document", documented)
check(fenced >= 2, "enough of them carry a worked example, which is what a fence protects", fenced)

# THE MARK IS THE DOCUMENT'S. An exposed declaration with no comment is marked, and a local
# binding is NOT: nobody could have written a comment for it, so the mark would be a complaint
# about the author rather than a fact about the code.
MARK = "(undocumented)"
check(nocomment == MARK, "an exposed declaration with no comment carries the document's mark", nocomment)

# AND A MEMBER WITH NO COMMENT CARRIES NOTHING, which is what the page prints for one. The
# fixture writes both an uncommented field and an uncommented variant, and both are hovered:
# a member the gate never asked about is a rule the gate cannot see.
BARE = [
    ("a field with no comment", at(MAIN, "lib.plain(p.y)", "y"), "y: int", "as the document prints it", LIB, "y"),
    ("a spec requirement with no comment", at(MAIN, "return x.size()", "size"), "fn size() -> int", "as the document prints it", None, None),
    ("a variant with no comment", at(MAIN, "\tGreen", "Green"), "Green", "as the document prints it", None, None),
    ("a local binding", at(MAIN, "print total + quiet", "total"), "binding total", "and is not marked undocumented", None, None),
    ("a parameter", at(MAIN, "return n + 1", "n"), "parameter n", "and is not marked undocumented", None, None),
    ("the namespace an import bound", at(MAIN, "p := lib.Point", "lib"), "import lib", "and is not marked undocumented", None, None),
]
for (what, place, sig, why, key, name), got in zip(BARE, hovers_at(BARE)):
    if not isinstance(got, dict):
        check(False, "%s has a hover" % what, got)
        continue
    hsig, body = parts(got["contents"]["value"])
    check(hsig == sig, "%s says what it is" % what, hsig)
    check(body == "", "%s carries nothing under it, %s" % (what, why), body)
    if key is not None:
        check(doc_entry(doc_of(key), name)[1] == "", "`zerg doc` prints that member bare too", name)

# --- silence ---
# THE PAGE PRINTS A REQUIREMENT BARE TOO. The hover side of that rule is `size` above, in the
# buffer; the page side needs a requirement a document holds, and `lib`'s is one — a renderer
# that started marking a member would disagree with the hover about this line and about no
# other, which is the drift `doc_marks_absence` exists to make impossible.
check(doc_entry(doc_of(LIB), "weight")[1] == "", "`zerg doc` prints a spec's requirement bare", "weight")

SILENT = [
    ("a built-in conversion, which no reader declared", at(MAIN, "int(Colour.Red)", "int")),
    ("a position in a file the client never opened", at(LIB, "pub fn twice", "twice")),
]
for (what, place), got in zip(SILENT, hovers_at(SILENT)):
    check(got is None, "%s has no hover" % what, got)

# AN ABORTED CHECK DROPS THE INDEX, and a hover from a program the compiler no longer agrees is
# this one would be a document for code nobody wrote.
p, l, c = at(MAIN, "lib.twice(p.x)", "twice")
broken = src[MAIN].replace("fn main() {", "fn main( {")
got = ask([("textDocument/hover", p, l, c)], change=broken)[0]
check(got is None, "a buffer whose check aborted has no hover", got)

# --- the index answers both, or neither ---
# every identifier token of the buffer, strings and comments blanked so a word inside one is not
# asked about. Hover reads the index `definition` reads and resolves nothing of its own, so for
# a name IN THE OPEN BUFFER the two must answer for exactly the same ones. Only the open buffer:
# a hover is read out of the text the client sent and answers null for a file it never sent,
# while `definition` answers from the index for any file of the program.
# the fixture is ASCII throughout, which is what lets a byte offset stand as a UTF-16 one here;
# the CJK case that holds the conversion itself is section 7's
toks = []
for ln, text in enumerate(src[MAIN].split("\n")):
    code = re.sub(r'"[^"]*"', lambda m: " " * len(m.group(0)), text.split("#")[0])
    for m in re.finditer(r"[A-Za-z_][A-Za-z0-9_]*", code):
        toks.append((MAIN, ln, m.start()))
answers = ask([("textDocument/definition", p, l, c) for p, l, c in toks]
              + [("textDocument/hover", p, l, c) for p, l, c in toks])
defs, hovs = answers[:len(toks)], answers[len(toks):]
both = 0
for (p, l, c), d, h in zip(toks, defs, hovs):
    check((d is None) == (h is None), "%s:%d:%d is answered by definition and hover alike"
          % (os.path.basename(p), l + 1, c + 1), (d, h))
    if d is not None:
        both += 1
check(both >= 20, "enough names in the fixture have both", both)

print("HOVER     %d declarations say what `zerg doc` says (%d with a fence), %d names of the open buffer answer definition and hover alike" % (documented, fenced, both))
sys.exit(1 if bad else 0)
PYEOF
then
	echo 'lsp-check: a hover is not the document `zerg doc` prints for that declaration'
	exit 1
fi

# --- 10. the outline is a view of the program ----------------------------------------------
#
# Section 1 asks WHICH declarations the outline names, against the parser's own dump. This asks
# what each entry SAYS about one: its children, and the two ranges LSP wants — `range` for the
# whole construct and `selectionRange` for the name it is selected by. They used to be the same
# range, the word at the declaration's first column, because the compiler had no end to give.
#
# The assertions are made by SLICING THE BUFFER with the range that came back and comparing the
# text to what was written. A comparison against hand-written line and character numbers would
# pass on a server that had the UTF-16 conversion backwards for the end — the numbers would be
# the ones this script was told to expect — and the fixture is deliberately full of CJK for
# exactly that reason.
#
# The same end is what a diagnostic underlines, so both halves are asked here of one buffer.
if ! "$PY" - "$ZERG" "$tmp" <<'PYEOF'
import json, os, re, subprocess, sys

zerg, tmp = sys.argv[1], sys.argv[2]

def frame(m):
    b = json.dumps(m).encode()
    return b"Content-Length: %d\r\n\r\n%s" % (len(b), b)

def session(name, text):
    path = os.path.join(tmp, name)
    open(path, "w", encoding="utf-8").write(text)
    uri = "file://" + os.path.abspath(path)
    wire = b"".join(frame(m) for m in [
        {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"capabilities": {}}},
        {"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": {
            "textDocument": {"uri": uri, "languageId": "zerg", "version": 1, "text": text}}},
        {"jsonrpc": "2.0", "id": 4, "method": "textDocument/documentSymbol",
         "params": {"textDocument": {"uri": uri}}},
        {"jsonrpc": "2.0", "method": "exit"},
    ])
    out = subprocess.run([zerg, "lsp"], input=wire, stdout=subprocess.PIPE,
                         stderr=subprocess.DEVNULL, timeout=120).stdout
    frames, i = [], 0
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
    reply = [f for f in frames if f.get("id") == 4]
    diags = [f["params"]["diagnostics"] for f in frames
             if f.get("method") == "textDocument/publishDiagnostics"]
    return (reply[0] if reply else {}), (diags[0] if diags else [])

# The buffer a range names, read the way a CLIENT reads one: line by line, and a character is a
# UTF-16 code unit. This is the assertion's whole point — a server that counted bytes into a
# line of CJK would answer a range that slices to the wrong text rather than to nothing.
def slice_of(text, r):
    lines = text.split("\n")
    def cut(ln, ch, tail):
        u = lines[ln].encode("utf-16-le")
        return u[ch * 2:].decode("utf-16-le") if tail else u[:ch * 2].decode("utf-16-le")
    s, e = r["start"], r["end"]
    if s["line"] == e["line"]:
        return cut(s["line"], e["character"], False)[s["character"]:]
    out = [cut(s["line"], s["character"], True)]
    out += lines[s["line"] + 1:e["line"]]
    out.append(cut(e["line"], e["character"], False))
    return "\n".join(out)

bad = 0
def check(ok, what, got):
    global bad
    if not ok:
        print("OUTLINE   %s — got %r" % (what, got))
        bad += 1

def inside(outer, inner):
    def le(a, b):
        return (a["line"], a["character"]) <= (b["line"], b["character"])
    return le(outer["start"], inner["start"]) and le(inner["end"], outer["end"])

# ONE FIXTURE, and every line of it carries text the byte and the UTF-16 readings disagree
# about: a name is reached past a CJK string, a field's default is one, and the statement the
# diagnostic is about sits under two of them.
SRC = (
    'K := "日本語"\n'
    "\n"
    "pub struct Point {\n"
    '\tpub label: str = "點"\n'
    "\tpub x: int\n"
    "}\n"
    "\n"
    "enum Shape {\n"
    "\tDot\n"
    "\tLine(int, int)\n"
    "}\n"
    "\n"
    "pub fn tag() -> str {\n"
    '\treturn "t"\n'
    "}\n"
    "\n"
    "fn main() {\n"
    "\tp := Point(\"一\", 1)\n"
    "\tp = Point(\"二\", 2)\n"
    "\tprint K\n"
    "\tprint p.x\n"
    "\tprint Shape.Dot\n"
    "\tprint tag()\n"
    "}\n"
)
reply, diags = session("outline-precision.zg", SRC)
syms = reply.get("result")
check(isinstance(syms, list) and syms, "a buffer that parses answers with an outline", reply)
syms = syms or []
by_name = {s["name"]: s for s in syms}

# A DECLARATION'S RANGE IS THE DECLARATION, and its selectionRange is the name inside it.
for name, whole, sel in [
    ("K", 'K := "日本語"', "K"),
    ("Point", 'pub struct Point {\n\tpub label: str = "點"\n\tpub x: int\n}', "Point"),
    ("Shape", "enum Shape {\n\tDot\n\tLine(int, int)\n}", "Shape"),
]:
    s = by_name.get(name)
    if s is None:
        check(False, "the outline names `%s`" % name, sorted(by_name))
        continue
    check(slice_of(SRC, s["range"]) == whole, "`%s`'s range is the whole declaration" % name,
          slice_of(SRC, s["range"]))
    check(slice_of(SRC, s["selectionRange"]) == sel, "`%s`'s selectionRange is its name" % name,
          slice_of(SRC, s["selectionRange"]))
    check(s["range"] != s["selectionRange"], "`%s`'s two ranges are not one range" % name, s["range"])

# CHILDREN, nested and in source order — and NOT beside their parent, which is the difference
# between an outline and a list of names. The kinds are LSP's: 8 Field, 22 EnumMember.
p = by_name.get("Point", {})
check([(k["name"], k["kind"]) for k in p.get("children", [])] == [("label", 8), ("x", 8)],
      "a struct's fields are its children, in source order", p.get("children"))
e = by_name.get("Shape", {})
check([(k["name"], k["kind"]) for k in e.get("children", [])] == [("Dot", 22), ("Line", 22)],
      "an enum's variants are its children, in source order", e.get("children"))
check(not ({"label", "x", "Dot", "Line"} & set(by_name)),
      "a child is not also a top-level symbol", sorted(by_name))
check(slice_of(SRC, p.get("children", [{}])[0].get("range", {"start": {"line": 0, "character": 0},
                                                            "end": {"line": 0, "character": 0}}))
      == 'pub label: str = "點"',
      "a field's range is the field, to the end of its default",
      p.get("children", [{}])[0].get("range"))

# `pub` IS PART OF THE DECLARATION, so it is inside the range — for a `struct` and a `fn` as
# much as for the module binding and the struct field that always included theirs. A range that
# began one token later put the same marker inside the range for two forms and outside it for
# five, and a client highlighting "the declaration" drew a box starting after its marker.
# `Point` above already proves it by exact equality, and its `label` field with it, so what is
# left to say is the form that has no case of its own: a `pub fn`.
tag = by_name.get("tag")
check(tag is not None and slice_of(SRC, tag["range"]) == 'pub fn tag() -> str {\n\treturn "t"\n}',
      "a `pub fn`'s range starts at the `pub` and ends at its `}`",
      tag and slice_of(SRC, tag["range"]))

# THE PROTOCOL'S ONE RULE about the pair, asked of every entry and every child rather than of
# the three above: a selection outside its range is a range a client cannot use.
for s in syms:
    for x in [s] + s.get("children", []):
        check(inside(x["range"], x["selectionRange"]),
              "`%s`'s selectionRange is inside its range" % x["name"], x)

# THE SAME END IS THE DIAGNOSTIC'S. `p = Point(…)` is refused at the statement, so the underline
# is the statement — not the word `p`, which is what a server deriving an end from the source
# could give. The place is taken from the one the compiler names, so this does not repeat it.
check(len(diags) == 1, "the buffer reports one error", diags)
if len(diags) == 1:
    d = diags[0]
    check(slice_of(SRC, d["range"]) == 'p = Point("二", 2)',
          "a checked diagnostic underlines the statement the compiler named", slice_of(SRC, d["range"]))
    check(d.get("code", "") != "", "a checked diagnostic carries its rule as a code", d)

# A BUFFER THAT WILL NOT PARSE IS NOT ANSWERED WITH AN EMPTY OUTLINE. It used to be, which is
# the outline of a file that declares nothing — indistinguishable from a server that crashed on
# the request. The request fails instead, and the failure carries the compiler's sentence.
reply, diags = session("outline-broken.zg", "fn main() {\n\tx := (1 +)\n\tprint x\n}\n")
check("result" not in reply and reply.get("error", {}).get("code") == -32803,
      "a buffer that will not parse answers RequestFailed, not an empty outline", reply)
check(reply.get("error", {}).get("message", "").strip() != "",
      "the failure says what the compiler said", reply.get("error"))

# AND THE ABORT ITSELF CARRIES ITS RULE. A raise packs the code into the front of its sentence;
# published as it arrived, every abort reached the editor as a finding with no rule while every
# checked finding beside it had one.
# `E` and digits, which is exactly what `rule_code` emits and `rule_msg_code` reads back. A
# looser pattern here would pass a server that had widened its reader past the writer, which
# is the drift the splitter was moved beside its packer to prevent.
check(len(diags) == 1 and re.match(r"^E[0-9]+$", diags[0].get("code", "")),
      "an abort carries its code as a code", diags and diags[0])
check(len(diags) == 1 and not re.match(r"^E[0-9]+ ", diags[0].get("message", "")),
      "and not also as the first word of its message", diags and diags[0].get("message"))

print("OUTLINE   %d symbols, %d children, ranges sliced out of the buffer they name"
      % (len(syms), sum(len(s.get("children", [])) for s in syms)))
sys.exit(1 if bad else 0)
PYEOF
then
	echo "lsp-check: the outline is not a view of the program"
	exit 1
fi

# --- 11. an outline is not a walk -----------------------------------------------------------
#
# Section 10 asks what the outline SAYS. This asks what it COSTS, because an outline that is
# right and takes a hundred seconds is an outline no keystroke waits for — and the failure is
# invisible to every other case here, which runs on files of a few hundred lines.
#
# It measures against the CHECK OF THE SAME BUFFER, the way section 6 measures a check against
# `zerg build --emit check`: one session that only opens the file, one that opens it and asks
# for the outline three times, and the difference over three is one outline. The check is the
# expensive thing a keystroke already pays for, so a fraction of it is the honest unit — and it
# moves with the machine exactly as the measurement does.
#
# THE FILE IS THE LARGEST ONE, derived rather than named, because a gate anchored to a filename
# stops measuring the day the file is renamed or another overtakes it. It is the worst case by
# construction: the cost that was here grew as the file times the declarations in it.
#
# Measured: 102.59 s per outline against an 8.38 s check — 12.2 — when `ls_line_at` re-split the
# whole buffer for every line it was asked for. 0.25 is far above where splitting once lands and
# far below anything that re-reads the buffer per symbol.
if ! "$PY" - "$ZERG" <<'PYEOF'
import json, os, re, resource, subprocess, sys, time

zerg = sys.argv[1]

biggest, size = None, -1
for root, _, names in os.walk("src/compiler"):
    for n in names:
        if not n.endswith(".zg"):
            continue
        p = os.path.join(root, n)
        if os.path.getsize(p) > size:
            biggest, size = p, os.path.getsize(p)
if biggest is None:
    print("OUTLINE   no source under src/compiler to measure an outline over")
    sys.exit(1)

TIMER = ["/usr/bin/time", "-l"]
try:
    probe = subprocess.run(TIMER + ["true"], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
    counts = probe.returncode == 0 and b"instructions retired" in probe.stderr
except OSError:
    counts = False
unit = "instructions" if counts else "CPU seconds"

# The deadline for the asking session is DERIVED from the one that has already run, so a
# regression reports rather than sitting there: the measured quadratic was twelve checks per
# outline, so three of them is minutes. Twice the baseline plus a generous minute per ask is
# past anything healthy and far short of waiting for the bad case to finish — and a session
# that hits it is the finding, not a traceback.
def cost(stdin, limit):
    run = lambda c: subprocess.run(c, input=stdin, stdout=subprocess.PIPE,
                                   stderr=subprocess.PIPE, timeout=limit)
    if counts:
        p = run(TIMER + [zerg, "lsp"])
        m = re.search(rb"(\d+)\s+instructions retired", p.stderr)
        if not m:
            print("OUTLINE   `time -l` counted no instructions for a session")
            sys.exit(1)
        return p, int(m.group(1))
    before = resource.getrusage(resource.RUSAGE_CHILDREN)
    p = run([zerg, "lsp"])
    after = resource.getrusage(resource.RUSAGE_CHILDREN)
    return p, (after.ru_utime + after.ru_stime) - (before.ru_utime + before.ru_stime)

text = open(biggest, encoding="utf-8").read()
uri = "file://" + os.path.abspath(biggest)
ASKS = 3

def wire(n):
    msgs = [
        {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"capabilities": {}}},
        {"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": {
            "textDocument": {"uri": uri, "languageId": "zerg", "version": 1, "text": text}}},
    ]
    for i in range(n):
        msgs.append({"jsonrpc": "2.0", "id": 10 + i, "method": "textDocument/documentSymbol",
                     "params": {"textDocument": {"uri": uri}}})
    msgs += [{"jsonrpc": "2.0", "id": 2, "method": "shutdown"},
             {"jsonrpc": "2.0", "method": "exit"}]
    return b"".join(b"Content-Length: %d\r\n\r\n%s" % (len(b), b)
                    for b in (json.dumps(m).encode() for m in msgs))

began = time.time()
_, check = cost(wire(0), 600)
baseline = time.time() - began
try:
    asked, both = cost(wire(ASKS), int(2 * baseline) + 60 * ASKS)
except subprocess.TimeoutExpired:
    print("OUTLINE   %d outlines of %s did not finish in %ds, against a %.1fs check of it"
          % (ASKS, biggest, int(2 * baseline) + 60 * ASKS, baseline))
    sys.exit(1)

# A SESSION THAT ANSWERED NOTHING costs nothing and would pass this for that reason. The reply
# is read back and counted: three outlines, each naming declarations.
outlines, out, i = [], asked.stdout, 0
while i < len(out):
    j = out.find(b"\r\n\r\n", i)
    if j < 0:
        break
    n = int(re.search(rb"Content-Length:\s*(\d+)", out[i:j], re.I).group(1))
    f = json.loads(out[j + 4:j + 4 + n].decode("utf-8"))
    if f.get("id") in (10, 11, 12):
        outlines.append(f.get("result"))
    i = j + 4 + n
empty = [o for o in outlines if not (isinstance(o, list) and o)]
if len(outlines) != ASKS or empty:
    print("OUTLINE   %s answered %d of %d outlines, %d of them empty or an error — nothing was measured"
          % (biggest, len(outlines), ASKS, len(empty)))
    sys.exit(1)

one = (both - check) / float(ASKS)
ratio = one / float(check)
# an instruction count is a whole number and a CPU second is not, so the report says each in
# the unit it was measured in rather than truncating one to look like the other
shown = (lambda v: "%d" % v) if counts else (lambda v: "%.2f" % v)
print("OUTLINE   one outline of %s (%d symbols) costs %.3f of its check (%s: check %s, outline %s)"
      % (biggest, len(outlines[0]), ratio, unit, shown(check), shown(one)))
if ratio >= 0.25:
    print("OUTLINE   an outline costs a quarter of a check of the same buffer — it is re-reading it")
    sys.exit(1)
PYEOF
then
	echo "lsp-check: an outline of the largest source is not keystroke-fast"
	exit 1
fi

# --- 12. every open buffer is the program ----------------------------------------------------
#
# Every case above opens ONE buffer, and a server that reads every other file from disk answers
# all of them correctly. An editor has several open at once, and the program is the one it
# holds: an unsaved change in `lib.zg` is part of the program `main.zg` is checked against, and
# a change that reaches only the file it was typed in leaves the other buffer showing findings
# about a text nobody has any more (#193).
#
# THE FIXTURE IS TWO FILES IN ONE DIRECTORY, and that shape is the point rather than an
# accident. `main.zg` imports `./lib`, so `main.zg`'s program contains both — and `lib.zg`'s
# program, found the way section 4 describes, is `lib.zg` ALONE, because the search for an entry
# never looks in the buffer's own directory. So the two directions are not symmetric and both
# are asserted: a change in `main.zg` reaches `lib.zg` out of one walk's findings, and a change
# in `lib.zg` reaches `main.zg` only because the server asks that buffer's own program whether
# it contains the file that changed. A server that partitioned one check and stopped there would
# pass half of this.
#
# WHAT IS ASSERTED IS "PUBLISHED IN THIS STEP", not "published at some point", which is what a
# barrier request between the steps makes observable — a stale buffer that is never re-published
# looks exactly like a correct one to a reader of the last frame alone.
# AND A SECOND FIXTURE, because the first one alone cannot say whether the dependants pass is
# needed: widen the entry search to the buffer's own directory and `main.zg` would arrive in
# the partition, leaving a deleted pass green. `many/` is the shape no search rule absorbs —
# TWO entries importing one module. `load_buffer` answers with ONE program, the first candidate
# that reaches the buffer, so a change in `shared/` can reach the second entry only by asking
# that buffer's own program what it contains.
mkdir -p "$tmp/dep" "$tmp/many/shared" "$tmp/drv"
cat >"$tmp/dep/lib.zg" <<'ZG'
pub fn greet(s: str) -> str {
	return "hello, " + s
}
ZG
cat >"$tmp/dep/main.zg" <<'ZG'
import "./lib"

fn main() {
	print lib.greet("world")
}
ZG
cat >"$tmp/many/shared/mod.zg" <<'ZG'
pub fn greet(s: str) -> str {
	return "hello, " + s
}
ZG
cat >"$tmp/many/one.zg" <<'ZG'
import "./shared"

fn main() {
	print shared.greet("one")
}
ZG
cat >"$tmp/many/two.zg" <<'ZG'
import "./shared"

fn main() {
	print shared.greet("two")
}
ZG

# AND A DECORATOR'S TREE, whose findings name a file the editor cannot have open: `#[derive]`
# expands at `<derive:FILE>`, and a partition that compares a finding's path against a buffer's
# drops every one of them. The fixture is #203's program, the smallest where the expansion is
# the only thing that refuses — `Q` has no `Eq`, so the `==` the derived member writes does not
# compile, while both structs and `main` are correct as written.
#
# AND A PROGRAM OF TWO FILES WITH THREE DECORATORS, each expansion refusing over a different
# type, because every expansion of one file is walked at the one path `<derive:FILE>` from its
# own line 1: the place a finding carries cannot say which decorator wrote it. `main.zg` holds
# two, so a server that drew every finding of a file at one decorator puts one of them at the
# wrong line; `R`'s decorator has a doc comment above it and a blank line and a `pub` under it,
# the three things that may stand between a decorator and its declaration or around it.
cat >"$tmp/drv/main.zg" <<'ZG'
struct Q {
	pub n: int
}

#[derive(Eq)]
struct P {
	pub q: Q
}

fn main() {
	print P(Q(1)) == P(Q(1))
}
ZG
mkdir -p "$tmp/drv2"
cat >"$tmp/drv2/lib.zg" <<'ZG'
struct Raw {
	pub n: int
}

#[derive(Eq)]
struct Wrap {
	pub r: Raw
}

pub fn same() -> bool {
	return Wrap(Raw(1)) == Wrap(Raw(1))
}
ZG
cat >"$tmp/drv2/main.zg" <<'ZG'
import "./lib"

struct Q {
	pub n: int
}

pub struct S {
	pub s: str
}

#[derive(Eq)]
struct P {
	pub q: Q
}

## R is compared by its S.
#[derive(Eq)]

pub struct R {
	pub s: S
}

fn main() {
	print P(Q(1)) == P(Q(1))
	print R(S("a")) == R(S("a"))
	print lib.same()
}
ZG
# AND AN EXPANSION THAT ABORTS rather than reports: a spec derived onto an enum by delegation
# calls the spec's method on each payload, and a payload that does not implement it stops the
# walk. The abort is a string whose place is `<derive:FILE>`, so it names neither the file's
# buffer nor the decorator by itself; `Plain` is decorated first, so the decorator the abort
# belongs to is not the first one in the file.
mkdir -p "$tmp/drv3"
cat >"$tmp/drv3/lib.zg" <<'ZG'
pub spec Size {
	fn size() -> int
}

struct Raw {
	pub n: int
}

#[derive(Eq)]
struct Plain {
	pub n: int
}

#[derive(Size)]
enum Shape {
	Box(Raw)
}

pub fn measure() -> int {
	return Shape.Box(Raw(1)).size()
}
ZG
cat >"$tmp/drv3/main.zg" <<'ZG'
import "./lib"

fn main() {
	print lib.measure()
}
ZG
if ! "$PY" - "$ZERG" "$tmp/dep" "$tmp/many" "$tmp/drv" "$tmp/drv2" "$tmp/drv3" <<'PYEOF'
import json, os, re, subprocess, sys

zerg, root, many, drv, drv2, drv3 = sys.argv[1:7]
main_p, lib_p = os.path.join(root, "main.zg"), os.path.join(root, "lib.zg")
main_uri = "file://" + os.path.abspath(main_p)
lib_uri = "file://" + os.path.abspath(lib_p)
MAIN = open(main_p, encoding="utf-8").read()
GOOD = open(lib_p, encoding="utf-8").read()

# The edit is a SIGNATURE, because that is the change whose consequence is in the other file:
# `lib.zg` on its own builds either way, and `main.zg` is where the argument stops fitting.
BAD = 'pub fn greet(n: int) -> str {\n\treturn "hello"\n}\n'

bad = 0


def opened(uri, text):
    return {"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": {
        "textDocument": {"uri": uri, "languageId": "zerg", "version": 1, "text": text}}}


def changed(uri, text, v):
    return {"jsonrpc": "2.0", "method": "textDocument/didChange", "params": {
        "textDocument": {"uri": uri, "version": v}, "contentChanges": [{"text": text}]}}


def saved(uri):
    return {"jsonrpc": "2.0", "method": "textDocument/didSave", "params": {"textDocument": {"uri": uri}}}


def closed(uri):
    return {"jsonrpc": "2.0", "method": "textDocument/didClose", "params": {"textDocument": {"uri": uri}}}


# A BARRIER is what makes "published in THIS step" a question the wire can answer. The server
# handles one request at a time in the order they arrive, so a reply is a fence: every publish
# before it belongs to the steps before it. `documentSymbol` is the barrier because it is a
# request that publishes nothing of its own.
def barrier(k):
    return {"jsonrpc": "2.0", "id": 100 + k, "method": "textDocument/documentSymbol",
            "params": {"textDocument": {"uri": main_uri}}}


def uri_of(path):
    return "file://" + os.path.abspath(path)


# A step is the LIST of publishes it carried, in order, and not a map of the last one per uri:
# a buffer published twice in one round with two answers reads as one answer to a map, which is
# the shape of a round that is not deterministic. `replies` carries whatever a step's requests
# answered, for the cases that ask a question rather than type.
def session(steps, asks=None):
    msgs = [{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"capabilities": {}}}]
    for k, st in enumerate(steps):
        msgs += st
        msgs.append(barrier(k))
    msgs += [{"jsonrpc": "2.0", "id": 2, "method": "shutdown"}, {"jsonrpc": "2.0", "method": "exit"}]
    wire = b"".join(b"Content-Length: %d\r\n\r\n%s" % (len(b), b)
                    for b in (json.dumps(m).encode() for m in msgs))
    out = subprocess.run([zerg, "lsp"], input=wire, stdout=subprocess.PIPE,
                         stderr=subprocess.DEVNULL, timeout=600).stdout
    per, cur, i = [], [], 0
    while i < len(out):
        j = out.find(b"\r\n\r\n", i)
        if j < 0:
            break
        n = int(re.search(rb"Content-Length:\s*(\d+)", out[i:j], re.I).group(1))
        f = json.loads(out[j + 4:j + 4 + n].decode("utf-8"))
        if f.get("method") == "textDocument/publishDiagnostics":
            cur.append((f["params"]["uri"], f["params"]["diagnostics"]))
        elif isinstance(f.get("id"), int) and f["id"] >= 100:
            per.append(cur)
            cur = []
        elif asks is not None and f.get("id") in asks:
            asks[f["id"]] = f.get("result")
        i = j + 4 + n
    if len(per) != len(steps):
        print("BUFFERS   a session of %d steps answered %d barriers" % (len(steps), len(per)))
        sys.exit(1)
    return per


# as_map is a step read as "what each buffer was last told", which is what every case asserting
# CONTENT wants; `sent` below is the one that asserts how often.
def as_map(step):
    return dict(step)


def sent(step, uri):
    return [u for u, _ in step].count(uri)


def said(ds):
    return [("%s %s" % (d.get("code", ""), d["message"])).strip() for d in ds if d.get("severity") == 1]


def where(ds):
    return [(d["range"]["start"]["line"], d["range"]["start"]["character"])
            for d in ds if d.get("severity") == 1]


def want_errors(step, uri, name, what):
    global bad
    m = as_map(step)
    if uri not in m:
        print("BUFFERS   %s: %s was not published at all" % (what, name))
        bad += 1
    elif not said(m[uri]):
        print("BUFFERS   %s: %s is silent, and the compiler refuses the program it is in" % (what, name))
        bad += 1


def want_silent(step, uri, name, what):
    global bad
    m = as_map(step)
    if uri not in m:
        print("BUFFERS   %s: %s was not published at all" % (what, name))
        bad += 1
    elif said(m[uri]):
        print("BUFFERS   %s: %s says %s about a program the compiler builds" % (what, name, said(m[uri])))
        bad += 1


# A. THE UNSAVED CHANGE, the save that follows it, and the change that undoes it. `lib.zg` is
# edited in the editor and never written, and `main.zg` — whose program contains it — answers
# for the text the editor is holding.
a = session([
    [opened(main_uri, MAIN), opened(lib_uri, GOOD)],
    [changed(lib_uri, BAD, 2)],
    [saved(lib_uri)],
    [changed(lib_uri, GOOD, 3)],
])
want_silent(a[0], main_uri, "main.zg", "on open")
want_silent(a[0], lib_uri, "lib.zg", "on open")
want_errors(a[1], main_uri, "main.zg", "an unsaved change in lib.zg")
want_silent(a[1], lib_uri, "lib.zg", "an unsaved change in lib.zg")
if sent(a[2], main_uri) == 0:
    print("BUFFERS   saving lib.zg did not re-publish main.zg")
    bad += 1
want_silent(a[3], main_uri, "main.zg", "the change that undoes it")

# B. THE CLOSE. A closed buffer is the disk again, so what its text was causing is taken back
# from the buffers whose program it is in.
b = session([
    [opened(main_uri, MAIN), opened(lib_uri, GOOD)],
    [changed(lib_uri, BAD, 2)],
    [closed(lib_uri)],
])
want_errors(b[1], main_uri, "main.zg", "an unsaved change in lib.zg")
want_silent(b[2], main_uri, "main.zg", "closing lib.zg")

# C. THE BUFFER OPENED AFTERWARDS reads the same program: `lib.zg` is already open and already
# changed when `main.zg` arrives, and the disk still holds the version that builds. This is the
# half a re-publish cannot fake — the check itself has to read the other buffer's text.
c = session([
    [opened(lib_uri, GOOD)],
    [changed(lib_uri, BAD, 2)],
    [opened(main_uri, MAIN)],
])
want_errors(c[2], main_uri, "main.zg", "a buffer opened after the change")

# E. TWO ENTRIES, ONE MODULE — the shape that says the dependants pass is not a patch over the
# entry search. `one.zg` and `two.zg` each import `shared/`, and `shared/mod.zg`'s own program
# is whichever of them the search reaches first; the other is stale after a change to `shared/`
# and no partition of one walk can answer for it.
one_p, two_p = os.path.join(many, "one.zg"), os.path.join(many, "two.zg")
sh_p = os.path.join(many, "shared", "mod.zg")
one_uri, two_uri, sh_uri = uri_of(one_p), uri_of(two_p), uri_of(sh_p)
ONE, TWO, SH = (open(p, encoding="utf-8").read() for p in (one_p, two_p, sh_p))
SH_BAD = 'pub fn greet(n: int) -> str {\n\treturn "hello"\n}\n'
e = session([
    [opened(one_uri, ONE), opened(two_uri, TWO), opened(sh_uri, SH)],
    [changed(sh_uri, SH_BAD, 2)],
])
want_silent(e[0], one_uri, "one.zg", "on open")
want_silent(e[0], two_uri, "two.zg", "on open")
want_errors(e[1], one_uri, "one.zg", "an unsaved change in shared/mod.zg")
want_errors(e[1], two_uri, "two.zg", "an unsaved change in shared/mod.zg")

# F. A BUFFER THAT DOES NOT LEX, which is what a buffer is for part of every word typed into
# it. Its importers must not be checked against a program it is missing from: dropping it made
# the open `main.zg` report `E3084 module `lib` has no `greet`` — a sentence about correct code
# — for as long as the quote was unclosed. The lexical finding is `lib.zg`'s and is published
# there; `main.zg` is left exactly as it was.
HALF = 'pub fn greet(s: str) -> str {\n\treturn "hello, \n}\n'
f = session([
    [opened(main_uri, MAIN), opened(lib_uri, GOOD)],
    [changed(lib_uri, HALF, 2)],
    [changed(main_uri, MAIN + "\n", 2)],
])
if not said(as_map(f[1]).get(lib_uri, [])):
    print("BUFFERS   a buffer mid-word: lib.zg does not report the string it left open")
    bad += 1
for step, what in ((f[1], "the buffer that stopped lexing"), (f[2], "a keystroke in main.zg while it does not lex")):
    fabricated = said(as_map(step).get(main_uri, []))
    if fabricated:
        print("BUFFERS   %s: main.zg is told %s, about a file the compiler will not even read"
              % (what, fabricated))
        bad += 1

# G. ONE PUBLISH PER BUFFER PER ROUND, and the same answer whichever buffer is typed in.
# `shared/mod.zg` belongs to two programs, so two checks of one round can each speak for it;
# published twice, what an editor ends up showing is whichever landed last. TYPING IN IT is the
# case that does it — the round then checks both entries' programs, and both contain it.
rounds = {}
for subject, subject_text, name in ((one_uri, ONE, "one.zg"), (two_uri, TWO, "two.zg"), (sh_uri, SH, "shared/mod.zg")):
    g = session([
        [opened(one_uri, ONE), opened(two_uri, TWO), opened(sh_uri, SH)],
        [changed(subject, subject_text + "\n", 2)],
    ])
    for u, who in ((one_uri, "one.zg"), (two_uri, "two.zg"), (sh_uri, "shared/mod.zg")):
        if sent(g[1], u) > 1:
            print("BUFFERS   typing in %s: %s is published %d times in one round"
                  % (name, who, sent(g[1], u)))
            bad += 1
    rounds[name] = said(as_map(g[1]).get(sh_uri, []))
for name in ("two.zg", "shared/mod.zg"):
    if rounds[name] != rounds["one.zg"]:
        print("BUFFERS   shared/mod.zg is told %s when one.zg is typed in and %s when %s is"
              % (rounds["one.zg"], rounds[name], name))
        bad += 1

# H. A NAME STILL ANSWERS AFTER THE ROUND. The index is the ONE table `definition` reads, and a
# round checks as many programs as the change reached — so the index has to be the SUBJECT's,
# not whichever dependant was checked last, or a keystroke in a third file empties the answer
# for the file the cursor is in.
asks = {9: None}
session([
    [opened(one_uri, ONE), opened(two_uri, TWO), opened(sh_uri, SH)],
    [changed(sh_uri, SH + "\n", 2)],
    [{"jsonrpc": "2.0", "id": 9, "method": "textDocument/definition",
      "params": {"textDocument": {"uri": one_uri}, "position": {"line": 3, "character": 15}}}],
], asks)
if not asks[9] or "uri" not in json.dumps(asks[9]):
    print("BUFFERS   after a keystroke in shared/mod.zg, `greet` in one.zg has no declaration: %s" % (asks[9],))
    bad += 1

# I. A FINDING THE COMPILER RAISED IN A TREE IT WROTE ITSELF. `#[derive(Eq)]` expands at
# `<derive:FILE>`, so its findings name no file the editor has open and were dropped by the
# partition — an editor silent about an error `zerg build` prints and refuses over.
#
# AND IT IS DRAWN AT THE DECORATOR THAT WROTE THE TREE (#203): its own line and column are a
# place in text nobody has, and the decorator is the line a reader goes and changes. What each
# buffer is told is held to `zerg build --emit check`, sentence for sentence, over the findings
# whose path is an expansion OF THAT FILE — so a finding published on the other buffer, or not
# at all, fails — and each is placed at the decorator above the type its sentence is about.
def derive_oracle(entry):
    p = subprocess.run([zerg, "build", "--emit", "check", entry],
                       stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=600)
    out = {}
    for m in re.finditer(r"^error: (\S+) (.*)\n\s*--> <derive:(.*)>:\d+:\d+$",
                         p.stdout.decode("utf-8", "replace"), re.M):
        out.setdefault(os.path.abspath(m.group(3)), []).append(m.group(1) + " " + m.group(2))
    return out


# decorator_line is the 0-based line of the `#[derive` above the type NAME declares, read off
# the fixture so the expectation moves with the text rather than being a number copied from it.
def decorator_line(text, name):
    lines = text.split("\n")
    at = [k for k, l in enumerate(lines) if re.match(r"(pub )?(struct|enum|spec) %s\b" % name, l)][0]
    return max(k for k in range(at) if lines[k].startswith("#[derive"))


def derive_case(label, entry, files, owner):
    texts = {f: open(f, encoding="utf-8").read() for f in files}
    step = session([[opened(uri_of(f), texts[f]) for f in files]])
    oracle = derive_oracle(entry)
    n = 0
    for f in files:
        ds = [d for d in as_map(step[0]).get(uri_of(f), []) if d.get("severity") == 1]
        want = sorted(oracle.get(os.path.abspath(f), []))
        n += len(want)
        if sorted(said(ds)) != want:
            print("BUFFERS   %s %s: the server says %s and `zerg build --emit check` says %s"
                  % (label, os.path.basename(f), sorted(said(ds)), want))
            return -1
        for d in ds:
            m = re.match(r"\S+ `==` on a (\w+):", said([d])[0])
            if not m or m.group(1) not in owner:
                print("BUFFERS   %s %s: `%s` is about no type this fixture derives over"
                      % (label, os.path.basename(f), said([d])[0][:40]))
                return -1
            ty = m.group(1)
            line = decorator_line(texts[f], owner[ty])
            got = (d["range"]["start"]["line"], d["range"]["start"]["character"])
            if got != (line, 0):
                print("BUFFERS   %s %s: `%s` is drawn at %s, and the decorator above `%s` is at %s"
                      % (label, os.path.basename(f), said([d])[0][:40], got, owner[ty], (line, 0)))
                return -1
    return n


drv_p = os.path.join(drv, "main.zg")
n = derive_case("#203's program", drv_p, [drv_p], {"Q": "P"})
if n != 1:
    if n >= 0:
        print("BUFFERS   #203's program: %d derived findings, where `zerg build` prints one" % n)
    bad += 1

drv2_main, drv2_lib = os.path.join(drv2, "main.zg"), os.path.join(drv2, "lib.zg")
n = derive_case("two files", drv2_main, [drv2_main, drv2_lib], {"Q": "P", "S": "R", "Raw": "Wrap"})
if n != 3:
    if n >= 0:
        print("BUFFERS   two files: %d derived findings, where the fixture writes three" % n)
    bad += 1

# J. AN ABORT RAISED INSIDE AN EXPANSION is published the same way: on the buffer of the file
# the decorator is in, at that decorator, with the sentence `zerg build` prints — and not on
# the buffer whose check raised it. `lib.zg` is opened first and then `main.zg`, whose program
# holds `lib.zg`: the second check's abort is `lib.zg`'s, and `main.zg` is told nothing.
drv3_main, drv3_lib = os.path.join(drv3, "main.zg"), os.path.join(drv3, "lib.zg")
lib3 = open(drv3_lib, encoding="utf-8").read()
p = subprocess.run([zerg, "build", "--emit", "check", drv3_main],
                   stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=600)
cli = p.stdout.decode("utf-8", "replace")
m = re.match(r"(E\d+ .*)\n\s*--> <derive:(.*)>:\d+:\d+\n?$", cli)
if not m or os.path.abspath(m.group(2)) != os.path.abspath(drv3_lib):
    print("BUFFERS   the aborting fixture does not abort inside lib.zg's expansion:")
    print("  " + cli.replace("\n", "\n  "))
    bad += 1
else:
    j = session([[opened(uri_of(drv3_lib), lib3)],
                 [opened(uri_of(drv3_main), open(drv3_main, encoding="utf-8").read())]])
    want = (m.group(1), decorator_line(lib3, "Shape"))
    for k, step in enumerate(j):
        got = [(said([d])[0], d["range"]["start"]["line"]) for d in as_map(step).get(uri_of(drv3_lib), [])]
        if got != [want]:
            print("BUFFERS   an abort in an expansion, step %d: lib.zg is told %s, and the decorator "
                  "above `Shape` is owed %s" % (k, got, [want]))
            bad += 1
    if sent(j[1], uri_of(drv3_main)) != 0:
        print("BUFFERS   an abort in lib.zg's expansion was published on main.zg: %s"
              % as_map(j[1]).get(uri_of(drv3_main)))
        bad += 1

# D. AND WHAT THE SESSION SAYS IS WHAT THE COMPILER SAYS about those same two texts on disk.
# The oracle is `zerg build --emit check` over the program, whose findings are partitioned by
# the file each one names — which is the partition the server publishes. Place included: a
# server that published the right sentence against the wrong file, or at the place the finding
# has in another buffer, would agree about every count.
open(lib_p, "w", encoding="utf-8").write(BAD)
p = subprocess.run([zerg, "build", "--emit", "check", main_p],
                   stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=600)
text = p.stdout.decode("utf-8", "replace")
oracle = {}
for m in re.finditer(r"^error: (\S+) (.*)\n\s*--> (.*):(\d+):(\d+)$", text, re.M):
    code, msg, path, line, col = m.groups()
    oracle.setdefault(os.path.abspath(path), []).append((code + " " + msg, int(line) - 1, int(col) - 1))
open(lib_p, "w", encoding="utf-8").write(GOOD)
if not oracle:
    print("BUFFERS   `zerg build --emit check` named no file for its findings, so there is no oracle")
    print("  " + text.replace("\n", "\n  "))
    sys.exit(1)
for uri, path, name in ((main_uri, main_p, "main.zg"), (lib_uri, lib_p, "lib.zg")):
    ds = as_map(a[1]).get(uri, [])
    got = [(s, l, c) for s, (l, c) in zip(said(ds), where(ds))]
    want = oracle.get(os.path.abspath(path), [])
    if got != want:
        print("BUFFERS   %s: the session says %s and `zerg build --emit check` says %s" % (name, got, want))
        bad += 1

if bad:
    sys.exit(1)
print("BUFFERS   two buffers are one program: an unsaved change, a save, a close and a later open all reach it, "
      "and a module's change reaches both entries that import it")
PYEOF
then
	echo "lsp-check: an open buffer is not the program the editor holds"
	exit 1
fi
# --- 12b. a keystroke costs one question per PROGRAM, not one per buffer --------------------
#
# Section 6 and section 8 each open exactly ONE buffer, and section 12 asserts what a session
# says and not what it costs — so the dimension this change added is invisible to all three: a
# buffer the checked program does not contain is asked whether ITS program contains the file
# that changed, and asking means loading. Asked per buffer, four buffers of one other program
# cost four loads of it on every keystroke; asked per program, one answers for all four.
#
# What is typed in is a fixture of one file whose program is itself, so its own check is
# nothing; what is open beside it is a program of one entry and a four-file module that contains
# none of it. Both sessions type the same keystrokes into the same file, and the only difference
# between them is how many buffers of that other program are open — one, and then four.
#
# K IS NAMED HERE. The fix reads at or under 1, and asking per buffer reads several times that —
# not quite four, because the one-buffer session reads the module's other files from disk where
# the four-buffer one holds them; K sits between the two with room either side.
#
# THE KEYSTROKES HAVE TO BE MOST OF WHAT IS MEASURED, or the ratio is the noise of everything
# else. A keystroke is the difference between a session with many of them and a session with
# one, so whatever else a session costs is subtracted out — exactly where the unit counts
# instructions, and only up to its noise where it is CPU seconds (a CI runner's virtual machine
# counts no instructions). Opening a buffer CHECKS its program, and a check of the compiler's own
# sources, which this used to open, is a lowering walk of them: four opens outweighed ten
# keystrokes several times over, and on a runner timing in seconds the difference of two large
# noisy numbers read 6.2 for the fix. So the other program is one whose LOAD is its cost: each
# module file is one function under a long run of comment lines, which the loader reads and lexes
# on every question and a walk never visits. And the probe refuses to judge — it fails, loudly —
# when a session's keystrokes cost less than FLOOR times the rest of that session, because a
# ratio the measurement cannot see is no answer in either direction.
#
# THE SUBJECT'S OWN ROUND HAS TO BE SMALL too. `probe/anchor.zg` is there to stop the entry
# search climbing into the fixtures the sections above left lying in `$tmp` — the search stops at
# the first level holding any source, and a program of one file that does not reach
# `sub/solo.zg` costs nothing to try.
LSP_PROBE_K=1.5
LSP_PROBE_FLOOR=0.5
mkdir -p "$tmp/probe/sub" "$tmp/probe/other/mod"
cat >"$tmp/probe/anchor.zg" <<'ZG'
fn main() {
	print "anchor"
}
ZG
cat >"$tmp/probe/sub/solo.zg" <<'ZG'
fn main() {
	print "solo"
}
ZG
cat >"$tmp/probe/other/main.zg" <<'ZG'
import "./mod"

fn main() {
	print mod.a() + mod.b() + mod.c() + mod.d()
}
ZG
probe_rc=0
"$PY" - "$ZERG" "$tmp/probe/sub/solo.zg" "$LSP_PROBE_K" "$LSP_PROBE_FLOOR" "$tmp/probe/other" <<'PYEOF' || probe_rc=$?
import json, os, re, resource, subprocess, sys

zerg, solo, k, floor, other = sys.argv[1], sys.argv[2], float(sys.argv[3]), float(sys.argv[4]), sys.argv[5]

# The module's four files, each one function under a run of comment lines — the load is the
# cost, and the walk has one function per file to lower.
others = []
for name, fn in (("mod", "a"), ("b", "b"), ("c", "c"), ("d", "d")):
    path = os.path.join(other, "mod", name + ".zg")
    with open(path, "w", encoding="utf-8") as f:
        f.write("pub fn %s() -> int {\n\treturn 1\n}\n" % fn)
        for i in range(40000):
            f.write("# line %d of a comment the loader lexes on every question and no walk visits\n" % i)
    others.append(path)
ref = subprocess.run([zerg, "build", "--emit", "check", os.path.join(other, "main.zg")],
                     stdout=subprocess.PIPE, stderr=subprocess.PIPE)
if ref.returncode != 0:
    print("PROBE     the other program does not check, so its buffers are not one program: %s"
          % ref.stderr.decode("utf-8", "replace").strip())
    sys.exit(1)

# The counter is DISCOVERED and decided once, as sections 6 and 8 do, so the two sessions
# cannot be measured in different units and compared anyway.
TIMER = ["/usr/bin/time", "-l"]
try:
    probe = subprocess.run(TIMER + ["true"], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
    counts = probe.returncode == 0 and b"instructions retired" in probe.stderr
except OSError:
    counts = False
unit = "instructions" if counts else "CPU seconds"


def uri_of(p):
    return "file://" + os.path.abspath(p)


def cost(wire):
    run = lambda c: subprocess.run(c, input=wire, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=1800)
    if counts:
        p = run(TIMER + [zerg, "lsp"])
        m = re.search(rb"(\d+)\s+instructions retired", p.stderr)
        if not m:
            print("PROBE     `time -l` counted no instructions for a session")
            sys.exit(1)
        return p, float(m.group(1))
    before = resource.getrusage(resource.RUSAGE_CHILDREN)
    p = run([zerg, "lsp"])
    after = resource.getrusage(resource.RUSAGE_CHILDREN)
    return p, (after.ru_utime + after.ru_stime) - (before.ru_utime + before.ru_stime)


SOLO = open(solo, encoding="utf-8").read()


def session(open_others, keystrokes):
    msgs = [{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"capabilities": {}}},
            {"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": {
                "textDocument": {"uri": uri_of(solo), "languageId": "zerg", "version": 1, "text": SOLO}}}]
    for p in open_others:
        msgs.append({"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": {
            "textDocument": {"uri": uri_of(p), "languageId": "zerg", "version": 1,
                             "text": open(p, encoding="utf-8").read()}}})
    for v in range(keystrokes):
        msgs.append({"jsonrpc": "2.0", "method": "textDocument/didChange", "params": {
            "textDocument": {"uri": uri_of(solo), "version": v + 2},
            "contentChanges": [{"text": SOLO + "\n" * (v + 1)}]}})
    msgs += [{"jsonrpc": "2.0", "id": 2, "method": "shutdown"}, {"jsonrpc": "2.0", "method": "exit"}]
    return b"".join(b"Content-Length: %d\r\n\r\n%s" % (len(b), b) for b in (json.dumps(m).encode() for m in msgs))


# One keystroke is the DIFFERENCE between a session of MANY and a session of one, divided by
# the keystrokes between them, so the process start-up and the opens cancel out of both sides —
# and the floor says whether what is left is large enough to be read at all.
MANY = 21


def per_keystroke(open_others):
    p1, one = cost(session(open_others, 1))
    pn, many = cost(session(open_others, MANY))
    for p in (p1, pn):
        if b"publishDiagnostics" not in p.stdout:
            print("PROBE     a session published nothing, so no keystroke was measured")
            sys.exit(1)
    typed = many - one
    if typed < floor * one:
        print("PROBE     %d keystrokes with %d of the other program's buffers open cost %.4g %s against %.4g "
              "for the rest of the session, under the floor of %.2g times it: the measurement cannot see "
              "a keystroke, so it does not judge one" % (MANY - 1, len(open_others), typed, unit, one, floor))
        sys.exit(2)
    return typed / (MANY - 1)


near = per_keystroke(others[:1])
far = per_keystroke(others)
ratio = far / near
print("PROBE     a keystroke with %d buffers of another program costs %.2f times one with 1 (%s: %.4g, %.4g)"
      % (len(others), ratio, unit, near, far))
if ratio > k:
    print("PROBE     the cost of a keystroke grows with the number of open buffers of one program")
    sys.exit(1)
PYEOF
if [ "$probe_rc" -eq 2 ]; then
	echo "lsp-check: a keystroke is too small a part of its session to be measured, so its cost was not judged"
	exit 1
elif [ "$probe_rc" -ne 0 ]; then
	echo "lsp-check: a keystroke asks its question once per buffer instead of once per program"
	exit 1
fi
# --- 13. a quick fix is the check's answer, not a second walk -------------------------------
#
# `textDocument/codeAction` used to answer from a lowering walk of its own — over the program the
# check that published the same buffer had just walked — so a request for the menu cost half a
# check, and a request that offered nothing cost the same (#204). It answers now from the fixes
# that check found, kept with the buffer.
#
# TWO QUESTIONS, and the first is WHAT it offers, which has to be the set the walk offered. The
# set is asserted against the compiler rather than against a list written here: it is the L502
# findings `zerg lint` places in `lib.zg`, minus the negated literal, whose finding carries no
# fix (chk_fix_unless) — so a server that offered every finding, or dropped the fix of one, reads
# differently. Several carry one, two of them on one line. The same set is asked three ways: of
# `lib.zg` checked as its own program; of `lib.zg` spoken for by the check of `main.zg`, the
# program beside it that imports it, after a keystroke there; and of `lib.zg` after its own `1`
# is written `1.0`, where the kept set has to be the new text's and not the one before it. None of
# it names the path an answer took, so the walk that answered on main passes it too — which is
# what says the kept set is the one the walk offered.
#
# The second is what it COSTS, measured from outside the process as section 6 measures a check:
# a session that opens a program and then asks for code actions A times, against the same session
# asking none. A walk per request makes the difference A walks; an answer from the check makes it
# the cost of A replies. The program is one whose WALK is its cost — many small functions, each a
# lowering and each holding two fixable literals — so the check the difference is judged against
# is well above the process's start-up. And the probe refuses to judge — it fails, loudly — when
# that check costs under FLOOR times a session that opens nothing, because on a runner timing in
# CPU seconds a ratio over a check the timer cannot see is no answer either way.
#
# K IS NAMED HERE: A requests cost under K checks together. A walk per request reads about A/2 —
# a check is a walk and the tree rules — and an answer from the check reads near 0.
LSP_CA_ASKS=8
LSP_CA_K=0.5
LSP_CA_FLOOR=4
mkdir -p "$tmp/ca" "$tmp/cacost"
cat >"$tmp/ca/lib.zg" <<'ZG'
pub fn half() -> float {
	x: float = 1 / 2
	return x
}

pub fn shifted() -> float {
	y: float = -3
	return y + 4
}
ZG
cat >"$tmp/ca/main.zg" <<'ZG'
import "./lib"

fn main() {
	print lib.half() + lib.shifted()
}
ZG
ca_rc=0
"$PY" - "$ZERG" "$tmp/ca" "$tmp/cacost" "$LSP_CA_ASKS" "$LSP_CA_K" "$LSP_CA_FLOOR" <<'PYEOF' || ca_rc=$?
import json, os, re, resource, subprocess, sys

zerg, ca, cost_dir = sys.argv[1], sys.argv[2], sys.argv[3]
asks, k, floor = int(sys.argv[4]), float(sys.argv[5]), float(sys.argv[6])
lib, main = os.path.join(ca, "lib.zg"), os.path.join(ca, "main.zg")


def uri_of(p):
    return "file://" + os.path.abspath(p)


def opened(p):
    return {"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": {"textDocument": {
        "uri": uri_of(p), "languageId": "zerg", "version": 1, "text": open(p, encoding="utf-8").read()}}}


def asked(i, p):
    return {"jsonrpc": "2.0", "id": i, "method": "textDocument/codeAction", "params": {
        "textDocument": {"uri": uri_of(p)},
        "range": {"start": {"line": 0, "character": 0}, "end": {"line": 100000, "character": 0}},
        "context": {"diagnostics": []}}}


INIT = {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"capabilities": {}}}
DOWN = [{"jsonrpc": "2.0", "id": 2, "method": "shutdown"}, {"jsonrpc": "2.0", "method": "exit"}]


def wire(msgs):
    return b"".join(b"Content-Length: %d\r\n\r\n%s" % (len(b), b) for b in (json.dumps(m).encode() for m in msgs))


def frames(out):
    got, i = [], 0
    while i < len(out):
        j = out.find(b"\r\n\r\n", i)
        if j < 0:
            break
        n = int(re.search(rb"Content-Length:\s*(\d+)", out[i:j], re.I).group(1))
        got.append(json.loads(out[j + 4:j + 4 + n].decode("utf-8")))
        i = j + 4 + n
    return got


def offered(fr, i, p):
    got = [f for f in fr if f.get("id") == i]
    if not got or not isinstance(got[0].get("result"), list):
        return None
    out = set()
    for a in got[0]["result"]:
        for e in a["edit"]["changes"][uri_of(p)]:
            r = e["range"]
            out.add((a["title"], e["newText"], r["start"]["line"], r["start"]["character"],
                     r["end"]["line"], r["end"]["character"]))
    return out


def published(fr, p):
    ds = [f["params"]["diagnostics"] for f in fr if f.get("method") == "textDocument/publishDiagnostics"
          and f["params"]["uri"] == uri_of(p)]
    return ds[-1] if ds else None


bad = 0

# --- what it offers ---
run = lambda msgs: frames(subprocess.run([zerg, "lsp"], input=wire(msgs), stdout=subprocess.PIPE,
                                         stderr=subprocess.DEVNULL, timeout=600).stdout)


def changed(p, text):
    return {"jsonrpc": "2.0", "method": "textDocument/didChange", "params": {
        "textDocument": {"uri": uri_of(p), "version": 2}, "contentChanges": [{"text": text}]}}


# what `zerg lint` places — 1-based byte columns, and the fixture is ASCII — minus the negated one
lint = subprocess.run([zerg, "lint", lib], stdout=subprocess.PIPE, stderr=subprocess.STDOUT).stdout.decode()
want, negated = set(), 0
for line, col, lit in re.findall(r"lib\.zg:(\d+):(\d+): L502 the literal `(-?\d+)`", lint):
    if lit.startswith("-"):
        negated += 1
    else:
        want.add((int(line) - 1, int(col) - 1))
if len(want) < 3 or negated < 1:
    print("QUICKFIX  `zerg lint` placed %d fixable and %d negated literals in lib.zg, under the fixture's 3 and 1:\n%s"
          % (len(want), negated, lint))
    sys.exit(1)

own = offered(run([INIT, opened(lib), asked(7, lib)] + DOWN), 7, lib)
main_text = open(main, encoding="utf-8").read()
via = offered(run([INIT, opened(lib), opened(main), changed(main, main_text + "\n"), asked(7, lib)] + DOWN), 7, lib)
for what, got in (("checked as its own program", own), ("spoken for by main.zg's check", via)):
    at = got and set((a[2], a[3]) for a in got)
    if at != want:
        print("QUICKFIX  lib.zg %s offers fixes at %r, and the fixable literals are at %r"
              % (what, got and sorted(at), sorted(want)))
        bad += 1
if own and via and own != via:
    print("QUICKFIX  lib.zg offers %r as its own program and %r from main.zg's" % (sorted(own), sorted(via)))
    bad += 1

# a changed buffer is answered from ITS check, not the one before it
edited = open(lib, encoding="utf-8").read().replace("1 / 2", "1.0 / 2")
now = offered(run([INIT, opened(lib), changed(lib, edited), asked(7, lib)] + DOWN), 7, lib)
if now is None or len(now) != len(want) - 1 or any(a[1] == "1.0" for a in now):
    print("QUICKFIX  after the `1` was written `1.0`, a code action offers %r" % (now and sorted(now)))
    bad += 1
if bad:
    sys.exit(1)
print("QUICKFIX  %d quick fixes in lib.zg, the ones `zerg lint` places, from either check" % len(want))

# --- what it costs ---
prog = os.path.join(cost_dir, "many.zg")
with open(prog, "w", encoding="utf-8") as f:
    for i in range(2000):
        f.write("fn f%d() -> float {\n\tx: float = 1 / 2\n\treturn x\n}\n\n" % i)
    f.write("fn main() {\n\tprint f0() + f1999()\n}\n")
ref = subprocess.run([zerg, "build", "--emit", "check", prog], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
if ref.returncode != 0:
    print("QUICKFIX  the cost fixture does not check: %s" % ref.stderr.decode("utf-8", "replace").strip())
    sys.exit(1)

TIMER = ["/usr/bin/time", "-l"]
try:
    probe = subprocess.run(TIMER + ["true"], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
    counts = probe.returncode == 0 and b"instructions retired" in probe.stderr
except OSError:
    counts = False
unit = "instructions" if counts else "CPU seconds"


def cost(msgs):
    run = lambda c: subprocess.run(c, input=wire(msgs), stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=1800)
    if counts:
        p = run(TIMER + [zerg, "lsp"])
        m = re.search(rb"(\d+)\s+instructions retired", p.stderr)
        if not m:
            print("QUICKFIX  `time -l` counted no instructions for a session")
            sys.exit(1)
        return p, float(m.group(1))
    before = resource.getrusage(resource.RUSAGE_CHILDREN)
    p = run([zerg, "lsp"])
    after = resource.getrusage(resource.RUSAGE_CHILDREN)
    return p, (after.ru_utime + after.ru_stime) - (before.ru_utime + before.ru_stime)


# the cursor on the first function's `1`, which offers exactly one fix
def ask_at(i):
    return {"jsonrpc": "2.0", "id": i, "method": "textDocument/codeAction", "params": {
        "textDocument": {"uri": uri_of(prog)},
        "range": {"start": {"line": 1, "character": 12}, "end": {"line": 1, "character": 12}},
        "context": {"diagnostics": []}}}


_, bare = cost([INIT] + DOWN)
p0, none = cost([INIT, opened(prog)] + DOWN)
pa, some = cost([INIT, opened(prog)] + [ask_at(10 + i) for i in range(asks)] + DOWN)
fr = frames(pa.stdout)
answers = [offered(fr, 10 + i, prog) for i in range(asks)]
if published(frames(p0.stdout), prog) is None or any(a is None or len(a) != 1 for a in answers):
    print("QUICKFIX  a session did not check the program or answer every request with its one fix: %r" % answers)
    sys.exit(1)

check = none - bare
if check < floor * bare:
    print("QUICKFIX  a check costs %.4g %s against %.4g for a session that opens nothing, under the floor of "
          "%.2g times it: the measurement cannot see a walk, so it does not judge one" % (check, unit, bare, floor))
    sys.exit(2)
ratio = (some - none) / check
print("QUICKFIX  %d code actions on an unchanged buffer cost %.2f checks (%s: check %.4g, the requests %.4g)"
      % (asks, ratio, unit, check, some - none))
if ratio >= k:
    print("QUICKFIX  a code action on an unchanged buffer walks the program")
    sys.exit(1)
PYEOF
if [ "$ca_rc" -eq 2 ]; then
	echo "lsp-check: a check is too small a part of its session to be measured, so a code action's cost was not judged"
	exit 1
elif [ "$ca_rc" -ne 0 ]; then
	echo "lsp-check: a quick fix is not the check's answer, or costs a walk of its own"
	exit 1
fi
echo "lsp-check: $ran buffers agree with the compiler, $outlined outlines are the parser's own, $members module members are checked against their module, formatting is fmt's answer, every protocol case holds, the dump carries the type parameters the outline cannot show, a check is one walk, a name answers with the declaration the compiler resolved it to, a long session stays in the band of one check, a hover is the document zerg doc prints for it, the outline is a view of the program, an outline of the largest source is not a walk of it, every open buffer is the program the editor holds, and a quick fix is the check's answer and costs no walk of its own"
