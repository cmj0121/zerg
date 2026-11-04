#!/usr/bin/env bash
#
# doc-serve — `zerg doc --serve` answers each path with the page `--html` writes for it, and the
# HTTP around those pages says what it should.
#
# The pages themselves are doc-check's business; what is measured here is that serving them did
# not change them. So the set of paths is not a list kept here: it is whatever `--html` wrote,
# read off the directory it wrote into, and each file is fetched by its name and held to those
# bytes with `cmp`. A page `--html` gains is covered the day it does, and a page the server
# renders differently from the file is a failure that names the file.
#
# Beside the pages, the rules of the HTTP subset, each asked once: `/` is the index, a path that
# is no page is 404, a method other than GET and HEAD is 405 and says which two are allowed, HEAD
# is the headers with no body, and a request line that is not one is 400.
#
# THE HOST MUST NAME THE SERVER, the guard against DNS rebinding: a `Host` that is another name is
# 400, as are none, two, and another port; `localhost:<port>` in any case is served as
# `127.0.0.1:<port>` is, and every response, whatever its status, says `nosniff`.
# A 500 is plain: the source path that could not be read goes to the server's stderr, not the page.
#
# A PAGE THAT CANNOT BE RENDERED IS ONE REQUEST, NOT THE SERVER. The pages are rendered on each
# request, so a source that goes away under a running server is an ordinary event. The second
# server here serves a copy of one module in a temporary directory — never a file of this
# repository — and the copy is moved away and back: away, its page answers 500; back, the same
# server answers `/` and the page again, which it can only do if the 500 did not end it.
#
# THE SERVER IS A BACKGROUND PROCESS, so every wait is bounded and every exit path kills it: a
# server that never prints its startup line fails here after STARTUP_SECS, and a request that
# hangs fails after CURL_SECS, rather than leaving the gate — or the board — waiting forever.
#
# `curl` only. It is on every runner this board runs on, and `nc` is spelled differently on each.
#
# A FLOOR, like every gate here: a scope whose `--html` wrote no module page would compare the
# index, the search and the style sheet and pass for having served no module at all.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

ZERG=${ZERG:-./bin/zerg}
STARTUP_SECS=${STARTUP_SECS:-60}
CURL_SECS=${CURL_SECS:-30}

command -v curl >/dev/null 2>&1 || {
	echo "doc-serve: \`curl\` is not on PATH — nothing was checked" >&2
	exit 1
}

tmp=$(mktemp -d) || exit 2
pids=""
cleanup() {
	for p in $pids; do
		kill "$p" 2>/dev/null
		wait "$p" 2>/dev/null
	done
	rm -rf "$tmp"
}
trap cleanup EXIT
trap 'exit 1' INT TERM HUP

fail=0
checks=0
bad() {
	echo "doc-serve: $1" >&2
	fail=1
}

# serve <log> [scope] — start `--serve 0` on the scope, wait for its startup line, and set `base`
# to the URL it names. A server that exits, or prints no line in time, ends the gate.
base=""
bound=""
serve() {
	local log=$1
	shift
	"$ZERG" doc --serve 0 "$@" >"$log" 2>&1 &
	local pid=$!
	pids="$pids $pid"
	local waited=0 port=""
	while [ "$waited" -lt $((STARTUP_SECS * 10)) ]; do
		port=$(sed -nE 's|^serving [0-9]+ module page\(s\) at http://127\.0\.0\.1:([0-9]+)/$|\1|p' "$log")
		[ -n "$port" ] && break
		kill -0 "$pid" 2>/dev/null || {
			echo "doc-serve: \`zerg doc --serve 0 $*\` exited before it was serving:" >&2
			sed 's/^/  /' "$log" >&2
			exit 1
		}
		sleep 0.1
		waited=$((waited + 1))
	done
	[ -n "$port" ] || {
		echo "doc-serve: \`zerg doc --serve 0 $*\` printed no startup line in ${STARTUP_SECS}s:" >&2
		sed 's/^/  /' "$log" >&2
		exit 1
	}
	base="http://127.0.0.1:$port"
	bound=$port
}

# fetch <out> <curl args...> — the status code on stdout, the body in <out>, the headers in
# <out>.h, neither left over from the request before — curl writes no file for an empty body. A
# transport failure is reported with curl's own words, never as a status.
fetch() {
	local out=$1
	shift
	rm -f "$out" "$out.h"
	curl -sS --max-time "$CURL_SECS" -o "$out" -D "$out.h" -w '%{http_code}' "$@" 2>"$out.err"
	local rc=$?
	[ "$rc" -eq 0 ] || echo " (curl exit $rc: $(head -1 "$out.err"))"
}

# status <want> <what> <curl args...> — the request answers <want>, with `nosniff` among its
# headers whatever the status; the response is left in $tmp/r for a caller that asks more of it.
status() {
	local want=$1 what=$2
	shift 2
	local got
	got=$(fetch "$tmp/r" "$@")
	checks=$((checks + 1))
	[ "$got" = "$want" ] || {
		bad "$what answered $got, not $want"
		return 1
	}
	grep -qi '^X-Content-Type-Options: nosniff' "$tmp/r.h" || bad "$what does not say \`X-Content-Type-Options: nosniff\`: $(tr -d '\r' <"$tmp/r.h" | tr '\n' '|')"
}

# --- the pages, against the files `--html` wrote ---------------------------------------------

if ! "$ZERG" doc --html "$tmp/html" >"$tmp/html.log" 2>&1; then
	echo "doc-serve: \`zerg doc --html\` failed, so there is nothing to compare against:" >&2
	sed 's/^/  /' "$tmp/html.log" >&2
	exit 1
fi

serve "$tmp/serve.log"

files=0
modules=0
for f in "$tmp/html"/*; do
	[ -f "$f" ] || continue
	name=$(basename "$f")
	files=$((files + 1))
	case $name in
	index.html | search.js | style.css) ;;
	*.html) modules=$((modules + 1)) ;;
	esac
	status 200 "/$name" "$base/$name" || continue
	cmp -s "$f" "$tmp/r" || {
		bad "/$name is not the bytes \`--html\` wrote to $name:"
		cmp "$f" "$tmp/r" 2>&1 | sed 's/^/  /' >&2
	}
done

if [ "$modules" -lt 1 ]; then
	bad "\`--html\` wrote no module page among $files file(s) — the comparison above held nothing"
fi

if status 200 "/" "$base/"; then
	cmp -s "$tmp/html/index.html" "$tmp/r" || bad "/ is not the bytes of index.html"
fi

status 404 "a path that is no page" "$base/no-such-page.html"

if status 405 "POST /" -X POST "$base/"; then
	grep -qi '^Allow: GET, HEAD' "$tmp/r.h" || bad "the 405 does not say \`Allow: GET, HEAD\`: $(tr -d '\r' <"$tmp/r.h" | tr '\n' '|')"
fi

# `--head` would have curl skip the body whatever the server sent. `-X HEAD` asks for the body
# the headers announce, so a body the server wrongly sent is read into the file. What is expected
# is the headers and then the close `Connection: close` promises: curl exits 0, or 18 for the
# announced body that never came. Any other exit — 28, a timeout, is a server that held the
# connection open — fails.
got=$(fetch "$tmp/r" -X HEAD "$base/index.html")
checks=$((checks + 1))
case $got in
200 | "200 (curl exit 18:"*)
	grep -qi '^Content-Length: [1-9]' "$tmp/r.h" || bad "HEAD /index.html announced no length: $(tr -d '\r' <"$tmp/r.h" | tr '\n' '|')"
	[ ! -s "$tmp/r" ] || bad "HEAD /index.html sent a body of $(wc -c <"$tmp/r" | tr -d ' ') byte(s)"
	;;
*) bad "HEAD /index.html answered $got, not 200 with the connection closed" ;;
esac

status 400 "a request line whose target is \`x\`" --request-target x "$base/"

status 400 "a request whose Host is evil.example" -H 'Host: evil.example' "$base/index.html"
status 400 "a request with no Host" -H 'Host:' "$base/index.html"
# curl sends one `Host` however many `-H` name it, so the second rides in after another header's
# CRLF, which curl passes through as written.
status 400 "a request with two Hosts" -H "Host: 127.0.0.1:$bound" -H $'X-Second: 1\r\nHost: localhost:'"$bound" "$base/index.html"
status 400 "a header line with no colon" -H "Host: localhost:$bound" -H $'X-A: 1\r\nnocolon' "$base/index.html"
status 400 "an obs-fold Host line" -H "Host: localhost:$bound" -H $'X-A: 1\r\n\tHost: localhost:'"$bound" "$base/index.html"
status 400 "a request whose Host is another port" -H "Host: localhost:$((bound + 1))" "$base/index.html"
if status 200 "a request whose Host is LocalHost:$bound" -H "Host: LocalHost:$bound" "$base/index.html"; then
	cmp -s "$tmp/html/index.html" "$tmp/r" || bad "/index.html for LocalHost:$bound is not the bytes of index.html"
fi

# --- a page that cannot be rendered, and the server after it --------------------------------

mkdir "$tmp/scope"
cp src/stdlib/ascii.zg "$tmp/scope/ascii.zg"
"$ZERG" doc --html "$tmp/one" "$tmp/scope/ascii.zg" >"$tmp/one.log" 2>&1 || {
	echo "doc-serve: \`zerg doc --html\` of a copy of ascii.zg failed:" >&2
	sed 's/^/  /' "$tmp/one.log" >&2
	exit 1
}
serve "$tmp/one-serve.log" "$tmp/scope/ascii.zg"

if status 200 "/ascii.html, its source there" "$base/ascii.html"; then
	cmp -s "$tmp/one/ascii.html" "$tmp/r" || bad "/ascii.html of the copy is not the bytes \`--html\` wrote"
fi
mv "$tmp/scope/ascii.zg" "$tmp/scope/away"
if status 500 "/ascii.html, its source moved away" "$base/ascii.html"; then
	! grep -qF "$tmp/scope" "$tmp/r" || bad "the 500 body names the source path: $(head -1 "$tmp/r")"
	grep -qF "$tmp/scope" "$tmp/one-serve.log" || bad "the 500's reason, with the source path, is not on the server's stderr"
fi
# A request target is said on stderr beside the reason, with its control bytes as `?`: an ESC
# sent there would reach the terminal of whoever runs the server.
status 500 "a target holding ESC, its source moved away" --request-target $'/ascii.html\033[2J' "$base/"
grep -qF '/ascii.html?[2J' "$tmp/one-serve.log" || bad "the stderr line for a target holding ESC does not show it as \`?[2J\`"
! grep -q $'\033' "$tmp/one-serve.log" || bad "the server's stderr holds a raw ESC byte from a request target"
mv "$tmp/scope/away" "$tmp/scope/ascii.zg"
if status 200 "/ after a 500" "$base/"; then
	cmp -s "$tmp/one/index.html" "$tmp/r" || bad "/ after a 500 is not the bytes of index.html"
fi
if status 200 "/ascii.html after a 500" "$base/ascii.html"; then
	cmp -s "$tmp/one/ascii.html" "$tmp/r" || bad "/ascii.html after a 500 is not the bytes \`--html\` wrote"
fi

if [ "$fail" -ne 0 ]; then
	echo "doc-serve: FAILED" >&2
	exit 1
fi
echo "doc-serve: $files page(s) served byte-identical to \`--html\` ($modules module page(s)); / is the index; 404, 405 with Allow, HEAD without a body and 400 as the subset says; a foreign, missing, doubled or other-port Host and a malformed header line refused, localhost served, nosniff on every status; a 500 for a source moved away, its stderr line free of control bytes, and the server serving after it ($checks requests)"
