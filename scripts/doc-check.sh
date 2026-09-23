#!/usr/bin/env bash
#
# doc-check — every exposed declaration is in the document, and every attachment rule has a
# case that shows it working.
#
# `zerg doc` reads the source and prints what a module exposes. Nothing measured it. That is
# a worse hole than it sounds, because of the direction this tool fails in: a declaration it
# quietly leaves out makes the documentation look MORE complete than it is, and the reader
# has no way to tell a module with nothing else in it from a module whose extraction stopped
# matching. Issue #16 names that failure as the one the whole line exists to prevent, so it
# is the first thing here — for each module, the `pub` declarations in the SOURCE and the
# declarations in the DOCUMENT are compared by NAME, both directions.
#
# The source side is read with `sed` and not with the compiler, deliberately. An extraction
# held against itself agrees with itself; this is the second opinion, and it is a coarse one
# on purpose — it can only see a `pub` keyword at the start of a line, which is exactly the
# property a reader uses to decide whether something is exposed.
#
# THE ATTACHMENT RULES ARE WHERE THIS TOOL IS ACTUALLY WRONG WHEN IT IS WRONG. Which comment
# documents which declaration is a token-stream geometry with seven or eight rules in it, and
# two of them were wrong in the first implementation with no gate able to see it — the
# document still rendered, still looked complete, and simply attributed a sentence to the
# wrong declaration. A rule with no case is a rule that does not exist, so §4 writes a
# fixture whose whole purpose is to make each rule visible one at a time.
#
# §5 is the other blind spot, and it is a blind spot of the CORPUS rather than of the tool:
# the standard library declares no `pub const`, no `pub type` and no `spec` at all, so the
# document's rendering of three of the six exposed forms is measured by nothing that reads
# the stdlib. The fixture carries them, along with the three spellings a signature must not
# silently drop — a `mut` binding, an `unsafe fn` and a `mut fn`.
#
# §6 is `log`'s rule on this command's device: colour follows the terminal and the SHAPE does
# not. It drives a pty the way scripts/log-check.sh does, because `os.isatty(1)` answers about
# a real descriptor and there is no other way to ask. The assertion is stronger than "there
# are escapes": the terminal's output with its colour taken back off has to be the piped
# output, byte for byte, or redirecting `zerg doc` into a file has changed the document.
#
# §7 is the width of the page, which nothing above measures. The fill is laid out to 80
# columns and was counted in RUNES, so a full-width CJK glyph — two terminal columns — was
# counted as one and a paragraph in Chinese printed at up to 160 columns, off the right edge
# of the terminal it was laid out for. §6 cannot see that: it compares the two devices to each
# other and says nothing about how wide either one is. The tree makes the case an ordinary
# one rather than a hypothetical — every `docs/*.md` here has a zh-TW twin — while the standard
# library contains not one comment in Chinese, so the fixture is beside §5's for the same
# reason. The SPACES in that paragraph are asserted there too, for being invisible to every
# width check in it: joining two source lines wrote a half-width space into the middle of a
# Chinese sentence, one column wide, and the line stayed inside the budget.
#
# §8 is the last row of the chapter's four-question table — anything else is a refusal that
# lists what it can see, exit 1 — asked of the three names that used to end somewhere else:
# `.`, which resolved to the standard library's own directory; a directory with no `.zg` in
# it, which printed nothing and exited 0; and a declaration asked of a module that did not
# parse. A file with nothing to document is beside them, for being the same silence one level
# down.
#
# FLOORS, like every other gate here. An extraction that stops matching finds nothing, and
# nothing satisfies every claim above — the comparison passes, each fixture rule is vacuous,
# and the gate reports success for having measured no declarations at all. So the module
# count, the declaration count and the number of checks that ran all have one, and the count
# of UNDOCUMENTED declarations is pinned exactly rather than bounded: it moves when somebody
# writes a comment or adds a bare `pub`, and it should move because they did.
#
# §9 is the examples. `zerg doc --check` builds every ` ```zerg ` fence in the standard library
# and diffs what it prints against the ` ```output ` fence beside it — every module, derived by
# the command rather than listed here. The number of example lines it ran is held EQUAL to a
# count this script derives from the sources with a wider pattern, runs at once must answer
# what a single run answers, and a fixture holds one case for each way an example can be wrong —
# a fence spelled another way, a build that fills a pipe, an example that never returns or leaves
# a process running, a run interrupted — plus the two shapes of module a program is written
# beside, and the undocumented `pub` the standard library no longer has an instance of.
#
# §11 is the search (#19). §1 holds every module's name list to its page, qualified, so a
# declaration no search reaches is caught the way a declaration no page prints is; its own
# fixture asks the matching rule's edges, and a private name that matches is the case that has to
# stay out.
#
# It needs no corpus: the standard library ships in this repository and the fixture is
# written by the script, so it runs the same everywhere.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

ZERG="${ZERG:-./bin/zerg}"

# The counts this gate would report success for having measured nothing. Each floor is the
# number this tree MEASURES rather than a round one under it, so it moves in the change that
# moves the measurement.
#
# THE FLOORS ARE THE MEASUREMENT AND NOT A MARGIN UNDER IT. `MIN_CHECKS` stood at 45 against a
# run of 55, so ten checks could stop running with nothing said; a floor with slack in it
# reports success for the checks that are left.
#
# `MIN_MODULES` counts the STANDARD LIBRARY's modules, and nothing else. It used to be 16
# because `examples/` was the sixteenth name — the local half, the modules standing beside the
# reader — and #57 decision 4 made a folder a module only when it holds a `mod.zg`, which a
# folder of example programs is not. The local half is asked for by its own check at the end,
# in a directory built to have one, rather than by a number that would have gone on passing
# while the walk answered nothing.
#
# `MIN_CHECKS` is the count on a host with no `script`, which is the smaller of the two runs: the
# terminal half of §6 adds more. A floor pinned to the larger one would turn every host
# without a pty red for a section it says out loud it did not run.
MIN_DECLS="${MIN_DECLS:-183}"
MIN_MODULES="${MIN_MODULES:-15}"
UNDOC_LIST="${UNDOC_LIST:-scripts/doc-undocumented}"
MIN_CHECKS="${MIN_CHECKS:-92}"
MIN_RULES="${MIN_RULES:-26}"

# The column budget the document is filled to — `DOC_WIDTH` in cmd/doc_render.zg, written
# again here because §7 is the second opinion about it and a second opinion that read the
# number out of the source would be the same opinion. `MIN_FILLED` is that section's own
# floor: a document laid out at one word per line satisfies "no line is too wide" by never
# having filled a line at all.
DOC_COLUMNS="${DOC_COLUMNS:-80}"
MIN_FILLED="${MIN_FILLED:-70}"

[ -x "$ZERG" ] || {
	printf 'doc-check: %s is not built — run `make build` first\n' "$ZERG" >&2
	exit 2
}
# An absolute path, because §4 runs the command from inside the fixture's own directory —
# a module is found relative to the working directory, which is how a local module is
# reached at all.
ZERG_ABS="$(cd "$(dirname "$ZERG")" && pwd)/$(basename "$ZERG")"

tmp="$(mktemp -d "${TMPDIR:-/tmp}/zerg-doccheck.XXXXXX")" || exit 2
trap 'rm -rf "$tmp"' EXIT

fail=0
checks=0
rules=0

note() {
	printf 'doc-check: %s\n' "$1" >&2
	fail=1
}

# --- the two readings of one module -------------------------------------------------
#
# exposed_in_source — a `pub` declaration is one the source opens a line with. `unsafe` and
# `mut` may stand between `pub` and the keyword (GRAMMAR groups 5 and 12), and a `spec` is
# exposed by the same `pub` every other form is.
exposed_in_source() {
	awk '
		/^pub[ \t]+spec[ \t]/ { inspec = 1 }
		inspec && /^}/         { inspec = 0 }

		# A pub SPEC MEMBER is part of that surface. They carry no `pub` of their own — a spec
		# is exposed or it is not — so the line below would miss every one of them, and the
		# document that prints them looked like it had invented names.
		inspec && match($0, /^[ \t]+(unsafe[ \t]+)?(mut[ \t]+)?fn[ \t]+[A-Za-z_][A-Za-z0-9_]*/) {
			line = $0
			sub(/^[ \t]+(unsafe[ \t]+)?(mut[ \t]+)?fn[ \t]+/, "", line)
			sub(/[^A-Za-z0-9_].*$/, "", line)
			print line
			next
		}

		match($0, /^[ \t]*pub[ \t]+(unsafe[ \t]+)?(mut[ \t]+)?(fn|struct|enum|const|type|spec)[ \t]+[A-Za-z_][A-Za-z0-9_]*/) {
			line = $0
			sub(/^[ \t]*pub[ \t]+(unsafe[ \t]+)?(mut[ \t]+)?(fn|struct|enum|const|type|spec)[ \t]+/, "", line)
			sub(/[^A-Za-z0-9_].*$/, "", line)
			print line
		}
	' "$1" |
		LC_ALL=C sort -u
}

# named_in_document — the declarations the document actually prints. A signature is at indent
# 2 and a method's at 6; a function's name is required to be followed by `(` or `[` so that a
# reflowed comment line beginning with the word `fn` cannot be counted as a declaration.
named_in_document() {
	sed -nE '
		s/^  (unsafe )?(mut )?fn ([A-Za-z_][A-Za-z0-9_]*)[[(].*/\3/p
		s/^      (unsafe )?(mut )?fn ([A-Za-z_][A-Za-z0-9_]*)[[(].*/\3/p
		s/^  (struct|enum|spec) ([A-Za-z_][A-Za-z0-9_]*)$/\2/p
		s/^  (const|mut) ([A-Za-z_][A-Za-z0-9_]*)[ :].*/\2/p
		s/^  type ([A-Za-z_][A-Za-z0-9_]*) =.*/\1/p
	' | LC_ALL=C sort -u
}

# qualified_on_page <module> — the names a reader could type back into `zerg doc`, read off the
# page: every declaration at indent 2 as `module.name`, and a method at indent 6 as
# `module.Type.name` under the type above it. A spec's requirement is at indent 6 too and is
# no declaration of its own — `zerg doc log.Sink.write` answers nothing — so it is not one.
qualified_on_page() {
	awk -v m="$1" '
		function after(s, lead) {
			sub(lead, "", s)
			sub(/[^A-Za-z0-9_].*$/, "", s)
			return s
		}
		/^  (unsafe )?(mut )?fn [A-Za-z_][A-Za-z0-9_]*[[(]/ {
			print m "." after($0, "^  (unsafe )?(mut )?fn ")
			kind = ""
			next
		}
		/^  (struct|enum|spec|type|impl) [A-Za-z_]/ {
			split($0, w, " ")
			kind = w[1]
			owner = after($0, "^  [a-z]+ ")
			if (kind != "impl") print m "." owner
			next
		}
		/^  (const|mut) [A-Za-z_]/ {
			print m "." after($0, "^  [a-z]+ ")
			kind = ""
			next
		}
		/^      (unsafe )?(mut )?fn [A-Za-z_][A-Za-z0-9_]*[[(]/ && kind != "" && kind != "spec" {
			print m "." owner "." after($0, "^      (unsafe )?(mut )?fn ")
		}
	' | LC_ALL=C sort -u
}

# --- 1. every exposed declaration is in the document ---------------------------------
#
# The module list is derived from the tree rather than written here, so a new standard
# library module is covered the day it lands rather than the day somebody remembers this
# file. `atomic` is left out by name and asked about separately in §3 — it does not parse
# under this compiler, which is a fact this gate holds the tool to rather than hides.
# A `*_test.zg` is a suite and not a module's surface.
modules=""
for src in src/stdlib/*.zg; do
	m="$(basename "$src" .zg)"
	case $m in
	*_test | atomic) continue ;;
	esac
	modules="$modules $m"
done

decls=0
for m in $modules; do
	exposed_in_source "src/stdlib/$m.zg" >"$tmp/src.names"
	"$ZERG" doc "$m" >"$tmp/full.out" 2>"$tmp/err" || {
		note "\`zerg doc $m\` failed: $(head -1 "$tmp/err")"
		continue
	}
	named_in_document <"$tmp/full.out" >"$tmp/doc.names"

	if ! diff -u "$tmp/src.names" "$tmp/doc.names" >"$tmp/diff"; then
		note "the document of \`$m\` is not the module's exposed declarations (- source, + document)"
		sed '1,2d;s/^/          /' "$tmp/diff" >&2
		continue
	fi
	# `--brief` is the same extraction rendered short, and it is the shape `zerg doc` with no
	# argument uses for the module list — so a declaration it drops disappears from the first
	# page a reader ever sees. It is held to the same set rather than trusted to be one.
	"$ZERG" doc --brief "$m" 2>/dev/null | named_in_document >"$tmp/brief.names"
	if ! diff -u "$tmp/doc.names" "$tmp/brief.names" >"$tmp/diff"; then
		note "\`zerg doc --brief $m\` names a different set of declarations than the whole document"
		sed '1,2d;s/^/          /' "$tmp/diff" >&2
		continue
	fi
	# THE NAME LIST (#19) is held to the page, qualified. `-s "$m."` is every name a search can
	# find in the module, and a declaration missing from it is one no search reaches — the same
	# silence as a page that dropped it, in the one place a reader goes when they do not know
	# the page.
	qualified_on_page "$m" <"$tmp/full.out" >"$tmp/page.names"
	"$ZERG" doc -s "$m." "$m" 2>/dev/null | sed -nE 's/^  ([^ ]+).*/\1/p' |
		LC_ALL=C sort -u >"$tmp/search.names"
	if ! diff -u "$tmp/page.names" "$tmp/search.names" >"$tmp/diff"; then
		note "\`zerg doc -s $m.\` finds a different set of declarations than the whole document (- document, + search)"
		sed '1,2d;s/^/          /' "$tmp/diff" >&2
		continue
	fi

	decls=$((decls + $(wc -l <"$tmp/doc.names" | tr -d ' ')))
	checks=$((checks + 1))
done

# --- 2. the undocumented ones are NAMED, and the names are pinned ---------------------
#
# They are SHOWN by design (issue #16, point 4). What a gate adds is that the set moves
# deliberately — and a COUNT moves deliberately in one direction only. The pin here was
# `UNDOCUMENTED=18`, an equality on a total, and an equality on a total is met by two errors
# cancelling: write the comment for one declaration and add a bare `pub` in the same change,
# and 18 is still 18 while the set underneath it has moved twice.
#
# So the pin is the NAMES, and `zerg doc --check` is what produces them — the tool answering
# the question directly, rather than this script counting a mark in a rendering.
: >"$tmp/undoc.names"
for m in $modules; do
	"$ZERG" doc --check "$m" 2>/dev/null |
		sed -nE 's/^.*: `([^`]*)` is exposed and carries no `##` comment$/\1/p' >>"$tmp/undoc.names"
done
LC_ALL=C sort -o "$tmp/undoc.names" "$tmp/undoc.names"
grep -v '^#' "$UNDOC_LIST" | grep -v '^[[:space:]]*$' | LC_ALL=C sort >"$tmp/undoc.pinned"

if ! diff -u "$tmp/undoc.pinned" "$tmp/undoc.names" >"$tmp/diff"; then
	note "the undocumented declarations are not the pinned set (- $UNDOC_LIST, + \`zerg doc --check\`) — write the comment, or move the line"
	sed '1,2d;s/^/          /' "$tmp/diff" >&2
else
	checks=$((checks + 1))
fi

undoc=$(grep -c . "$tmp/undoc.names" || true)

# THE READER'S VIEW OF THE SAME FACT, which is the one thing the list above cannot see: a
# tool that stopped MARKING them would look like a library somebody had finished documenting,
# while `--check` went on naming them to nobody. The two views are held to one size.
marked=0
for m in $modules; do
	n=$("$ZERG" doc "$m" 2>/dev/null | grep -c '(undocumented)')
	marked=$((marked + n))
done
if [ "$marked" -ne "$undoc" ]; then
	note "the document marks $marked declarations (undocumented) and \`zerg doc --check\` names $undoc — two views of one fact disagree"
else
	checks=$((checks + 1))
fi

# --- 3. a module that does not parse says so ------------------------------------------
#
# The honest answer is the one thing that must not go missing: the header still reads — it
# comes from the trivia and needs no AST — and a `note:` line says the rest is absent. Silence
# here would be a module that documents itself as empty.
#
# THE FIXTURE IS WRITTEN HERE rather than pointed at a stdlib module. It used to name
# `atomic`, on the standing fact that a generic struct did not parse; a generic struct is
# built now and `zerg doc atomic` lists its declarations, so the case was asserting something
# about a module that had stopped being an example of it. A gate anchored to which module
# happens to be broken this month measures the month.
# THE BLANK LINE AFTER THE HEADER IS THE FIXTURE'S OWN CONTENT. Without it the block attaches
# to the declaration under it — the comment-attachment rule case 4 below is about — and the
# module has no header at all, which is what this case would then be asserting the absence of.
mkdir -p "$tmp/unparseable"
cat >"$tmp/unparseable/mod.zg" <<'ZG'
## unparseable — a module whose declarations cannot be listed.
##
## The header above is trivia and reads without an AST; the line below is not a declaration
## this or any parser can finish.

pub fn (
ZG
"$ZERG" doc "$tmp/unparseable" >"$tmp/unparseable.out" 2>&1
grep -q '^note: ' "$tmp/unparseable.out" ||
	note "\`zerg doc\` on a module that does not parse lists no declarations and says nothing about why — it must be reported, not rendered empty"
grep -q '^unparseable — ' "$tmp/unparseable.out" ||
	note "\`zerg doc\` lost the module header of a file it could not parse; the header comes from the trivia and does not need the AST"
checks=$((checks + 2))

# --- 4. the comment-attachment rules, one case each ------------------------------------
#
# Which comment documents which declaration is decided by line geometry over the token
# stream, and every rule below is a line in that algorithm. They are written into one
# fixture rather than one file each so that the rules sit next to each other in the order
# a reader meets them, and so the whole set is one parse.
mkdir -p "$tmp/proj"
cat >"$tmp/proj/attach.zg" <<'ZG'
## attach — the module header, which belongs to the MODULE and to no declaration below it.
##
## A second paragraph of it, so that a header stolen by the first declaration would be
## visible as a declaration carrying two paragraphs it never had.

## MAX is documented by this comment and by nothing above it.
pub const MAX: int = 64

# --- a section banner, which documents nothing ---------------------------------------
## after_banner would be documented by this line, were the run not opened by a banner.
pub fn after_banner() -> int {
	return 1
}

## this comment is separated from the declaration by a blank line, so it attaches to nothing

pub fn detached() -> int {
	return 2
}

## Derived carries a decorator between its comment and its declaration.
#[derive(Eq)]
pub struct Derived {
	pub n: int
}

impl Derived {
	## doubled is documented by a comment indented inside the impl block.
	pub fn doubled() -> int {
		return this.n * 2
	}
}

## adjacent is documented; the declaration under it has no comment of its own.
pub fn adjacent() -> int {
	return 3
}
pub fn crowded() -> int {
	return 4
}

## hashy is documented by this comment, and the `#` in the string below is not one.
pub fn hashy() -> str {
	return "# not a comment"
}
## tail_commented is documented by this comment and not by the one at the end of the line
## above it.
pub fn tail_commented() -> int {
	return 5
}

## ```zerg
## attach.only_example()
## ```
## ```output
## 6
## ```
pub fn only_example() -> int {
	return 6
}

## tag is the free function, and Widget has a method of the same name.
pub fn tag() -> str {
	return "free"
}

## Widget is a struct.
pub struct Widget {
	pub n: int
}

impl Widget {
	## tag is the method, whose answer is not the free function's.
	pub fn tag() -> int {
		return this.n
	}
}
ZG
# The line-tail comment goes in with `sed`, because writing `}  # note` into the heredoc
# above and then asserting the rule about it in the same breath is easy to read as decoration.
# It sits on the closing brace of `hashy`, which is the line immediately above the comment
# that documents `tail_commented` — so a rule that sewed a trailing comment onto the run
# below it would give `tail_commented` a sentence about braces.
#
# NO BLANK LINE MAY SEPARATE THE TWO, and one did. A blank line ends a run, so the trailing
# comment formed a run of its own that the blank-line rule then dropped — it never reached the
# run below it, and the case measured a rule other than the one it names. Deleting `continue if
# prev == t.line` from zerg/doc.zg left every check here green, which is this script's own
# standard turned on itself: a rule with no case is a rule that does not exist.
sed -i.bak '/^	return "# not a comment"$/{n;s/^}$/}  ## this trailing comment documents nothing/;}' \
	"$tmp/proj/attach.zg" && rm -f "$tmp/proj/attach.zg.bak"

"$ZERG" doc "$tmp/proj/attach.zg" >"$tmp/attach.out" 2>"$tmp/attach.err" || {
	note "the attachment fixture did not render: $(head -1 "$tmp/attach.err")"
}
# THE FIXTURE HAS TO PARSE, or every rule below is vacuously satisfied by a document with
# nothing in it — the shape this gate's floors exist to refuse, arriving through the door
# marked "the fixture".
grep -q '^note: ' "$tmp/attach.out" &&
	note "the attachment fixture does not parse, so every rule below was asserted against an empty document: $(grep -m1 '^note: ' "$tmp/attach.out")"

# doc_of <signature-substring> — the first non-blank line printed UNDER that signature, which
# is the comment the tool decided documents it. This is the whole question §4 asks, so it is
# asked once and every rule below is one call. It reads `$doc_out`, which is §4's document
# until §10 points it at its own.
doc_out="$tmp/attach.out"
doc_of() {
	awk -v want="$1" '
		index($0, want) && !seen { seen = 1; next }
		seen && NF { print; exit }
	' "$doc_out"
}

# rule <name> <signature> <wanted-substring-of-its-first-doc-line>
rule() {
	local name=$1 sig=$2 want=$3 got
	got="$(doc_of "$sig")"
	case $got in
	*"$want"*)
		checks=$((checks + 1))
		rules=$((rules + 1))
		;;
	*) note "$name — \`$sig\` is documented by \"$got\", wanted \"$want\"" ;;
	esac
}

rule 'a decorator between comment and declaration attaches through' \
	'struct Derived' 'Derived carries a decorator'
rule 'a comment indented inside an impl documents the method' \
	'fn doubled()' 'doubled is documented by a comment indented'
rule 'a blank line between comment and declaration does not attach' \
	'fn detached()' '(undocumented)'
rule 'a section banner documents nothing' \
	'fn after_banner()' '(undocumented)'
rule 'a comment documents the declaration under it and not the next one' \
	'fn adjacent()' 'adjacent is documented'
rule 'two declarations with no blank line: the second is undocumented' \
	'fn crowded()' '(undocumented)'
rule 'a `#` inside a string literal is not a comment' \
	'fn hashy()' 'hashy is documented by this comment'
rule 'a line-tail comment documents nothing' \
	'fn tail_commented()' 'tail_commented is documented by this comment'
rule 'a comment that is nothing but an example still documents' \
	'fn only_example()' '```zerg'

# the string's own text must never be read as documentation anywhere in the document
grep -q '^      # not a comment$' "$tmp/attach.out" &&
	note 'the text inside a string literal was read as a comment — the extraction is scanning text rather than the lexer trivia'
checks=$((checks + 1))

# THE MODULE HEADER IS NOT STOLEN BY THE FIRST DECLARATION. Both halves are asserted: the
# header prints at the top, at column 0, and `MAX` is documented by its own comment. Either
# alone passes on a tool that prints the header twice.
head -1 "$tmp/attach.out" | grep -q '^attach — the module header' ||
	note "the module header is not the document's first line: $(head -1 "$tmp/attach.out")"
rule 'the first declaration does not steal the module header' \
	'const MAX: int = 64' 'MAX is documented by this comment'

# ONE NAME, TWO DECLARATIONS, BOTH ANSWERED. `tag` is a free function and a method of
# `Widget`, and a lookup that resolved to whichever came first would answer one of them
# twice. They are asked by the keys the renderer groups them under, from inside the
# fixture's own directory, which is how a local module is reached.
free_tag="$(cd "$tmp/proj" && "$ZERG_ABS" doc attach.tag 2>&1)"
meth_tag="$(cd "$tmp/proj" && "$ZERG_ABS" doc attach.Widget.tag 2>&1)"
case $free_tag in
*'fn tag() -> str'*) checks=$((checks + 1)) ;;
*) note "\`attach.tag\` did not answer the free function: $(printf '%s' "$free_tag" | head -1)" ;;
esac
case $meth_tag in
*'fn tag() -> int'*) checks=$((checks + 1)) ;;
*) note "\`attach.Widget.tag\` did not answer the method: $(printf '%s' "$meth_tag" | head -1)" ;;
esac

# --- 5. the forms the stdlib does not contain ------------------------------------------
#
# `pub const`, `pub type` and `spec` are three of the six exposed forms and the standard
# library declares none of them, so §1 above can say nothing about how they render. The three
# modifiers are here for the same reason and a sharper one: a signature that drops `mut` or
# `unsafe` documents a declaration as something the language would not accept — and `mut fn`
# is the one whose absence misdirects the CALL SITE, which GRAMMAR#fn-decl requires to hold
# the receiver in a `mut` binding.
cat >"$tmp/proj/forms.zg" <<'ZG'
## forms — the exposed forms the standard library happens not to declare.

## LIMIT is a public constant.
pub const LIMIT: int = 64

## Name is a public type.
pub type Name = str

## Named is a public spec.
pub spec Named {
	## label is what a Named answers to.
	fn label() -> str
}

## Counter is a struct whose method mutates it.
pub struct Counter {
	pub n: int
}

impl Counter {
	## bump adds one to the receiver, in place.
	pub mut fn bump() {
		this.n = this.n + 1
	}
}

unsafe {
	## COUNTER is the one mutable global the language has.
	pub mut COUNTER := 0

	## peek reads a raw address, and is an `unsafe fn` because the group says so.
	pub fn peek(p: ptr) -> int {
		return 0
	}
}
ZG
"$ZERG" doc "$tmp/proj/forms.zg" >"$tmp/forms.out" 2>&1
grep -q '^note: ' "$tmp/forms.out" &&
	note "the forms fixture does not parse, so none of the five forms below was rendered: $(grep -m1 '^note: ' "$tmp/forms.out")"

# form <name> <wanted-line>
form() {
	if grep -qF "$2" "$tmp/forms.out"; then
		checks=$((checks + 1))
		rules=$((rules + 1))
	else
		note "$1 — the document does not print \`$2\`"
	fi
}

form 'a public constant' '  const LIMIT: int = 64'
form 'a public type alias' '  type Name = str'
form 'a public spec' '  spec Named'
form 'a spec requirement' '      fn label() -> str'
form 'a mutable global' '  mut COUNTER := 0'
form 'an unsafe fn' '  unsafe fn peek(p: ptr) -> int'
form 'a mutating method' '      mut fn bump()'

# and each of them keeps its own comment, which is the §4 question asked of the forms §4's
# fixture cannot declare
for want in 'LIMIT is a public constant' 'Name is a public type' 'Named is a public spec' \
	'label is what a Named answers to' 'COUNTER is the one mutable global' \
	'peek reads a raw address' 'bump adds one to the receiver'; do
	if grep -qF "$want" "$tmp/forms.out"; then
		checks=$((checks + 1))
		rules=$((rules + 1))
	else
		note "the comment \"$want\" is not in the document of its declaration"
	fi
done

# --- 6. colour follows the terminal, and the shape does not -----------------------------
#
# `os.isatty(1)` answers about the descriptor a real process was given, so the only way to
# ask this is to give the command a pipe and then a pty and read what came out of each. The
# technique is scripts/log-check.sh's, down to trying both argument orders of `script`:
# macOS and Linux disagree about them, and a host with neither skips the terminal half
# LOUDLY rather than passing for not having run it.
#
# STANDARD INPUT IS EXPLICITLY /dev/null, which log-check.sh's copy does not say and this one
# has to: `script` forwards its own stdin into the pty, and a caller that hands it a CLOSED
# descriptor — which is what a CI runner does — leaves it waiting on a read that can never
# complete. The gate then hangs until the job's step timeout, with nothing said about why.
on_a_pty() {
	if script -q /dev/null "$@" >"$tmp/pty.raw" 2>&1 </dev/null; then
		return 0
	fi
	if script -q -c "$*" /dev/null >"$tmp/pty.raw" 2>&1 </dev/null; then
		return 0
	fi
	return 1
}

esc="$(printf '\033')"
bs="$(printf '\b')"

# plain — a pty's transcript as the document it carries. Three things are removed and each
# is the terminal rather than the program: the SGR sequences (the point of the comparison),
# the carriage return a tty adds to every line ending, and the `^D` a `script` session echoes
# when the master closes — a literal caret and D followed by backspaces, at the very start of
# the stream and nowhere else.
plain() {
	tr -d "$bs\r" <"$tmp/pty.raw" | sed -e "1s/^\\^D//" -e "s/${esc}\[[0-9;]*m//g"
}

"$ZERG" doc log >"$tmp/pipe.out" 2>&1
grep -q "$esc" "$tmp/pipe.out" &&
	note 'a piped document carries ANSI colour — colour must follow the terminal'
checks=$((checks + 1))

if on_a_pty "$ZERG" doc log; then
	grep -q "$esc\[1mFUNCTIONS$esc\[0m" "$tmp/pty.raw" ||
		note 'a section title at a terminal is not bold — colour did not follow the device'
	checks=$((checks + 1))

	# THE SHAPE DOES NOT FOLLOW THE DEVICE. This is the assertion the two greps above cannot
	# make: with its colour taken back off, the terminal's document has to be the piped one
	# character for character. Anything else means redirecting `zerg doc` into a file changed
	# the document rather than its appearance.
	plain >"$tmp/pty.plain"
	if diff -u "$tmp/pipe.out" "$tmp/pty.plain" >"$tmp/shape.diff"; then
		checks=$((checks + 1))
	else
		note 'the document at a terminal is not the piped document with colour added — the SHAPE followed the device'
		sed '1,2d;s/^/          /' "$tmp/shape.diff" | head -20 >&2
	fi

	# NO_COLOR wins over the terminal, in both spellings — no-color.org says any value counts
	# and an exported-empty one is a value.
	NO_COLOR=1 on_a_pty "$ZERG" doc log
	grep -q "$esc" "$tmp/pty.raw" && note 'NO_COLOR=1 did not turn colour off at a terminal'
	NO_COLOR='' on_a_pty "$ZERG" doc log
	grep -q "$esc" "$tmp/pty.raw" &&
		note 'an exported-empty NO_COLOR did not turn colour off at a terminal — see no-color.org'
	checks=$((checks + 2))

	# THE (undocumented) MARK IS ASSERTED ON THE FIXTURE, and this is the last thing §6 does
	# because it overwrites `pty.raw` for everything above.
	#
	# It used to be read out of `zerg doc log`, which carried ten of them. The library carries
	# NONE now, and that grep would have gone on asserting the colour of a mark no document
	# contained — a check that can only pass while somebody has left work undone, and which
	# fails the day the work is finished. §4's fixture declares `after_banner()` and
	# `detached()` for the attachment rules and both render the mark, so it is the one corpus
	# here that cannot run out of one.
	if (cd "$tmp/proj" && on_a_pty "$ZERG_ABS" doc ./attach.zg); then
		grep -q "$esc\[33m(undocumented)$esc\[0m" "$tmp/pty.raw" ||
			note 'the (undocumented) mark at a terminal is not coloured'
		checks=$((checks + 1))
	else
		note 'the attachment fixture could not be rendered on a pty, so the mark was not asserted'
	fi
else
	printf 'doc-check: no `script` on this host, so the terminal half of §6 was not run\n' >&2
fi

# --- 7. a comment in Chinese lays out inside the page ------------------------------------
#
# The document is filled to a number of COLUMNS, and a column is not a character: a
# full-width glyph takes two of them. Every assertion above is about which text is in the
# document and none is about where the right edge of it falls, so a fill that counted
# characters wrapped a Chinese paragraph at 80 of them and printed 160 columns of it.
#
# The width is counted in `perl`, which is on both platforms this repository builds on. It is
# not counted in `awk` because `length` there counts BYTES in the awk macOS ships and
# CHARACTERS in the gawk Linux ships, and neither of those is columns; and a host with no
# perl is TOLD so rather than passed over, because a gate whose whole subject is the width of
# a character cannot report success from a machine that could not measure one. The block list
# below is the same list `doc_rune_width` carries, written out a second time on purpose: the
# assertion is that the renderer applied it, and a check that asked the renderer what it
# thought a column was would be the renderer agreeing with itself.
cat >"$tmp/proj/width.zg" <<'ZG'
## width — 一份用中文寫成的 module，它量的是文件的欄寬。
##
## 全形字在終端機上佔兩欄。一段以「字數」折行的文字會折到八十個字，印出來卻是一百六十欄，
## 整份文件因此跑出終端機的右緣；以顯示欄寬折行的則每一行都落在八十欄以內。這一段刻意寫得
## 夠長，長到只要折行是以字數計算就一定有一行超出。兩個全形字之間不該多出作者沒寫的空格，而
## zerg doc 這種西文詞前後的空格是作者寫的，得留在原處。

## LIMIT 是這份文件量出來的欄數。
pub const LIMIT: int = 80

## label 回答一個標籤。它的說明也是中文的，而且夠長：縮排六欄之後的內文仍然必須折行，折在
## 哪一個字上才是這道 gate 真正在看的東西，而 terminal 與 column 這類西文詞夾在其中，讓貪
## 心填字有得選。
pub fn label() -> str {
	return "寬"
}
ZG
"$ZERG" doc "$tmp/proj/width.zg" >"$tmp/width.out" 2>&1
grep -q '^note: ' "$tmp/width.out" &&
	note "the width fixture does not parse, so the page below was measured on an empty document: $(grep -m1 '^note: ' "$tmp/width.out")"

# THE FIXTURE'S OWN TEXT IS IN THE DOCUMENT. A header that failed to render leaves a file
# whose every line is inside the budget for holding no Chinese at all.
grep -q '^width — 一份用中文寫成的 module' "$tmp/width.out" ||
	note 'the Chinese module header is not the first line of its document'
checks=$((checks + 1))

if command -v perl >/dev/null 2>&1; then
	# widest <file> — the widest line in display columns, and how many lines are over the
	# budget, as two numbers on one line.
	widest() {
		perl -CSD -e '
			my $budget = shift;
			my ($max, $over) = (0, 0);
			while (<>) {
				chomp;
				my $w = 0;
				for my $c (split //) {
					my $o = ord $c;
					$w += (($o >= 0x1100 && $o <= 0x115F)     # Hangul Jamo initial consonants
						|| ($o >= 0x2E80 && $o <= 0x303E)     # CJK radicals, Kangxi, CJK punctuation
						|| ($o >= 0x3041 && $o <= 0x33FF)     # kana, Bopomofo, Hangul jamo, enclosed CJK
						|| ($o >= 0x3400 && $o <= 0x4DBF)     # CJK unified ideographs extension A
						|| ($o >= 0x4E00 && $o <= 0x9FFF)     # CJK unified ideographs
						|| ($o >= 0xA000 && $o <= 0xA4CF)     # Yi syllables and Yi radicals
						|| ($o >= 0xA960 && $o <= 0xA97F)     # Hangul Jamo extended-A
						|| ($o >= 0xAC00 && $o <= 0xD7A3)     # Hangul syllables
						|| ($o >= 0xF900 && $o <= 0xFAFF)     # CJK compatibility ideographs
						|| ($o >= 0xFE10 && $o <= 0xFE19)     # vertical forms
						|| ($o >= 0xFE30 && $o <= 0xFE6F)     # CJK compatibility and small forms
						|| ($o >= 0xFF00 && $o <= 0xFF60)     # fullwidth ASCII forms
						|| ($o >= 0xFFE0 && $o <= 0xFFE6)     # fullwidth currency and other signs
						|| ($o >= 0x20000 && $o <= 0x3FFFD))  # CJK ideographs, planes 2 and 3
						? 2 : 1;
				}
				$max = $w if $w > $max;
				$over++ if $w > $budget;
			}
			print "$max $over\n";
		' "$1" "$2"
	}

	read -r wide_max wide_over <<EOF
$(widest "$DOC_COLUMNS" "$tmp/width.out")
EOF

	if [ "${wide_over:-1}" -eq 0 ]; then
		checks=$((checks + 1))
		rules=$((rules + 1))
	else
		note "$wide_over lines of the Chinese document are wider than $DOC_COLUMNS columns, the widest at $wide_max — the fill is counting characters and a full-width glyph is two columns"
	fi

	# and the document did fill: the widest line has to come close to the budget, or the
	# assertion above passed for having laid nothing out.
	if [ "${wide_max:-0}" -ge "$MIN_FILLED" ]; then
		checks=$((checks + 1))
	else
		note "the widest line of the Chinese document is $wide_max columns and the floor is $MIN_FILLED — the paragraph was not filled, so a width assertion over it says nothing"
	fi

	# THE SPACES IN THE DOCUMENT ARE THE AUTHOR'S. Refilling a paragraph joins two source
	# lines, and what the join puts between them is a space or nothing — a decision the width
	# above cannot see, because a space dropped into the middle of a Chinese sentence is one
	# column and the line stays inside the budget. The fixture's paragraph breaks after a
	# full-width comma, which is exactly where it used to gain one.
	#
	# BOTH DIRECTIONS ARE ASSERTED, because the rule has two ways to be wrong. A space is
	# never right BETWEEN TWO FULL-WIDTH CHARACTERS — Chinese is written with none — and it is
	# always right between a Han character and a Latin word, which this tree's own zh-TW
	# documents write and the fixture breaks a line at. A rule that dropped the space whenever
	# either side was full-width would fix the first and break the second in silence.
	#
	# The property is asked of perl's own Unicode tables rather than of the block list written
	# out above, because here the block list is not the claim: `East_Asian_Width` is, and a
	# check reusing the renderer's approximation of it would pass a renderer whose
	# approximation is wrong.
	joins=$(perl -CSD -ne '
		chomp;
		print "a space that is not in the source: $_\n"
			if /(?<=[\p{Ea=W}\p{Ea=F}]) (?=[\p{Ea=W}\p{Ea=F}])/;
		print "a space that is in the source and not here: $_\n"
			if /\p{Han}[A-Za-z]|[A-Za-z]\p{Han}/;
	' "$tmp/width.out")

	if [ -z "$joins" ]; then
		checks=$((checks + 1))
		rules=$((rules + 1))
	else
		note "the line break between two source lines was joined with the wrong thing — $(printf '%s' "$joins" | head -1)"
	fi
else
	note 'no `perl` on this host, so the width of the page was not measured — §7 is the one section that cannot be skipped, its whole subject being how wide a character is'
fi

# --- 8. a name that answers nothing is refused, and says what it can see -----------------
#
# docs/tooling/doc.md's four-question table ends with one row: anything else is a refusal that
# lists what it can see, exit 1. Three commands a reader types by accident used to end
# somewhere else, and each of them printed something a reader would read as an answer:
#
#   - `zerg doc .` printed two thousand lines of the standard library as one module.
#     `module_at`'s directory arm asks whether `<root>/<name>` exists, and `<stdlib>/.` does.
#   - `zerg doc docs/` printed zero bytes and exited 0. A trailing slash left by a shell's
#     completion is the whole of how a reader gets there.
#   - `zerg doc atomic.load` said the module declares nothing called `load` and listed
#     nothing, of a module whose declarations are MISSING rather than absent.
#
# The exit code alone would pass a command that printed nothing and failed, so each case
# asserts the sentence too — and for `.` the sentence is the proof it did not answer, a stdlib
# document being the one thing that does not carry the index.
mkdir -p "$tmp/empty" "$tmp/proj/sub"

# refused <dir> <what> <name> <wanted-substring-of-the-refusal>
#
# The DIRECTORY is a parameter because `.` and `..` are answered relative to it. `..` is asked
# from inside `$tmp/proj/sub`, so the directory above it is §4's own fixture directory and
# holds sources — asked from anywhere whose parent happens to hold none, the case would pass
# on the compiler that had the bug.
refused() {
	local dir=$1 what=$2 name=$3 want=$4 out rc
	out="$(cd "$dir" && "$ZERG_ABS" doc "$name" 2>&1)"
	rc=$?
	if [ "$rc" -eq 0 ]; then
		note "$what — \`zerg doc $name\` exited 0, and a name nothing answers is a refusal"
		return
	fi
	case $out in
	*"$want"*) checks=$((checks + 1)) ;;
	*) note "$what — \`zerg doc $name\` was refused with \"$(printf '%s' "$out" | head -1)\", wanted \"$want\"" ;;
	esac
}

refused "$ROOT" 'the current directory is not a module name' '.' 'the modules it can see:'
refused "$tmp/proj/sub" 'the parent directory is not a module name' '..' 'the modules it can see:'
refused "$ROOT" 'a directory with no source in it' "$tmp/empty/" 'holds no source'
# THE FIXTURE AGAIN, and for §3's reason: this named `atomic.load` while `atomic` was a module
# that did not parse. It parses now — a generic struct is built — so `zerg doc atomic.load`
# answers, correctly, and the case was asserting a refusal of a module that had stopped being
# an example of one.
refused "$tmp" 'a declaration of a module that did not parse' 'unparseable.thing' 'does not parse under this compiler'

# and that refusal does not go on to head an empty list, which is the claim the note corrects
(cd "$tmp" && "$ZERG_ABS" doc unparseable.thing) >"$tmp/decl.out" 2>&1
grep -q '^what it does declare:' "$tmp/decl.out" &&
	note '`zerg doc` on a declaration of a module that does not parse heads a list of what the module declares and then lists nothing — the note above it has just said the module did not parse'
checks=$((checks + 1))

# A FILE WITH NOTHING TO DOCUMENT SAYS SO. `zerg doc examples` was twelve headings with
# nothing under them: no header comment, no exposed declaration, and a blank line where the
# document would be. A heading with nothing beneath it reads as a rendering that broke.
cat >"$tmp/proj/quiet.zg" <<'ZG'
fn main() {
	print 1
}
ZG
"$ZERG" doc "$tmp/proj/quiet.zg" >"$tmp/quiet.out" 2>&1
grep -qx '(nothing exposed)' "$tmp/quiet.out" ||
	note "a file with no header and nothing exposed renders as a heading and a blank line: $(cat "$tmp/quiet.out")"
checks=$((checks + 1))

# --- 9. `--check` runs every example, and still names what is undocumented ---------------
#
# The standard library first, whole: every ` ```zerg ` fence in it is built and run, and the
# run has to be clean. Then one fixture per way an example can be wrong, because a check that
# only ever sees right examples passes just as well when it runs none of them.

# bounded_check <seconds> <out-file> <args…> runs `zerg doc --check <args…>` and answers its
# status, or 124 when it was still running after <seconds>. A runner that hangs is the failure
# two of the cases below exist to catch, and a gate that hangs with it catches nothing.
bounded_check() {
	local secs=$1 outf=$2 pid dog rc
	shift 2
	"$ZERG_ABS" doc --check "$@" >"$outf" 2>&1 &
	pid=$!
	(
		sleep "$secs"
		: >"$outf.late"
		kill -9 "$pid"
	) >/dev/null 2>&1 &
	dog=$!
	wait "$pid"
	rc=$?
	kill "$dog" 2>/dev/null
	wait "$dog" 2>/dev/null
	if [ -e "$outf.late" ]; then
		rm -f "$outf.late"
		return 124
	fi
	return "$rc"
}

# THE COUNT IS DERIVED, not written down. A second opinion reads the example lines out of the
# standard library's sources with a pattern wider than the tool's own fence — any indentation,
# backticks or tildes three or more, space before the word or none, `zerg` or `zg` in any case,
# any word after it — and the tool has to report running EXACTLY that many.
# A fence the tool stopped recognising makes the two differ; a fence it reports as not one
# makes the run fail; and no number in this file has to be moved when an example is added.
derived=$(
	for src in src/stdlib/*.zg; do
		# an `if` and not a `case`: bash 3.2 cannot parse a `case` pattern's bare `)` inside `$(…)`
		if [ "${src%_test.zg}" = "$src" ]; then printf '%s\n' "$src"; fi
	done | xargs awk '
		FNR == 1 { inside = 0 }
		/^[ \t]*#+[ \t]*(```+|~~~+)[ \t]*([Zz][Ee][Rr][Gg]|[Zz][Gg])/ { inside = 1; next }
		inside && /^[ \t]*#+[ \t]*(```+|~~~+)[ \t]*$/ { inside = 0; next }
		inside { n++ }
		END { print n + 0 }
	'
)
bounded_check 600 "$tmp/check.out"
rc=$?
ran=$(sed -nE 's/.*, ([0-9]+) example line\(s\) run,.*/\1/p' "$tmp/check.out")
if [ "$rc" -ne 0 ]; then
	note "\`zerg doc --check\` over the standard library exited $rc:"
	sed 's/^/          /' "$tmp/check.out" >&2
elif [ "$derived" -eq 0 ] || [ "${ran:-0}" -ne "$derived" ]; then
	note "\`zerg doc --check\` ran ${ran:-no} example lines and the sources hold $derived — a fence was skipped, or none was found"
else
	checks=$((checks + 1))
fi

# RUNS AT ONCE, each of which must answer what a single run answers. Every run writes into a
# directory of its own; a shared path had them linking over each other's binary and failing with
# nothing wrong in any example. More than two, because that collision is a race and two runs in
# step can miss it. One module is enough to race on — the paths are per module — so the runs ask
# `json` alone rather than building the whole library three times over. Only a run that DISAGREES
# with the single run is blamed on a shared path.
bounded_check 120 "$tmp/conc0.out" json
rc=$?
concrc=""
pids=()
for i in 1 2 3; do
	bounded_check 120 "$tmp/conc$i.out" json &
	pids+=($!)
done
for i in 1 2 3; do
	wait "${pids[i - 1]}"
	r=$?
	[ "$r" -eq "$rc" ] || concrc="$concrc $i:$r"
done
if [ -n "$concrc" ]; then
	note "\`zerg doc --check json\` run at once disagreed with a single run's $rc (run:status$concrc) — a run shares a path with another:"
	for i in 1 2 3; do
		grep -v 'exposed declarations' "$tmp/conc$i.out" | head -3 | sed 's/^/          /' >&2
	done
else
	checks=$((checks + 1))
fi

mkdir -p "$tmp/ex"

# right: an expression example, a statement example, and one in the file's header — a comment
# no declaration claims, which is a claim about the code all the same
cat >"$tmp/ex/right.zg" <<'ZG'
## right is a fixture, and its header carries an example too.
##
## ```zerg
## right.twice(2)
## ```
## ```output
## 4
## ```

## twice doubles its argument.
##
## ```zerg
## right.twice(21)
## right.twice(-1)
## ```
## ```output
## 42
## -2
## ```
##
## and a statement example claims to print nothing:
##
## ```zerg
## right.twice(0)
## ```
pub fn twice(n: int) -> int {
	return n * 2
}
ZG

# ran_check <case> <file> <wanted-exit> <wanted-substring> [<args>…]
#
# Each variant below is `right.zg` with ONE line changed and the module renamed, so a finding
# can only have come from that line.
ran_check() {
	local what=$1 file=$2 want_rc=$3 want=$4 out rc
	shift 4
	bounded_check 120 "$tmp/ex/$file.out" "$@" "$tmp/ex/$file"
	rc=$?
	out="$(cat "$tmp/ex/$file.out")"
	if [ "$rc" -eq 124 ]; then
		note "$what — \`zerg doc --check $file\` was still running after 120s"
		return
	fi
	if [ "$rc" -ne "$want_rc" ]; then
		note "$what — \`zerg doc --check $file\` exited $rc, wanted $want_rc: $(printf '%s' "$out" | head -3)"
		return
	fi
	case $out in
	*"$want"*) checks=$((checks + 1)) ;;
	*) note "$what — \`zerg doc --check $file\` said \"$(printf '%s' "$out" | head -3)\", wanted \"$want\"" ;;
	esac
}

# what `right.zg`, and every copy of it that should pass, answers
RIGHT_OK=', 4 example line(s) run, 0 module(s) whose examples are wrong'

ran_check 'right examples, the statement and header ones included' right.zg 0 "$RIGHT_OK"

# a limit of no seconds is a typo, and read as one it would fail every example for being late
ran_check 'a `--timeout` below one second' right.zg 1 '`--timeout` is a whole number of seconds, one or more' --timeout 0

sed 's/^## -2$/## -3/; s/right\./wrongout./' "$tmp/ex/right.zg" >"$tmp/ex/wrongout.zg"
ran_check 'an ```output line that is not what the example prints' wrongout.zg 1 \
	"wrongout.zg:12: this example's \`\`\`output is not what it prints — line 2 says \`-3\`, and the example printed \`-2\`"

sed 's/^## 4$/## 5/; s/right\./header./' "$tmp/ex/right.zg" >"$tmp/ex/header.zg"
ran_check 'a wrong example in a comment no declaration claims' header.zg 1 \
	"header.zg:3: this example's \`\`\`output is not what it prints — line 1 says \`5\`, and the example printed \`4\`"

sed 's/^## right\.twice(21)$/## right.nothing(21)/; s/right\./broken./' "$tmp/ex/right.zg" >"$tmp/ex/broken.zg"
ran_check 'an example that does not compile' broken.zg 1 "broken.zg:3: the examples of \`broken\` do not compile"

sed 's/^## right\.twice(0)$/## print right.twice(0)/; s/right\./chatty./' "$tmp/ex/right.zg" >"$tmp/ex/chatty.zg"
ran_check 'a statement example that prints' chatty.zg 1 \
	"the examples of \`chatty\` printed more than their \`\`\`output blocks say — \`0\` is claimed by none"

# A fence spelled any other way is REPORTED, never skipped: the reader sees an example either way.
#
# fence_case <case> <name> <fence line> <wanted-finding> writes a module whose only fence-shaped
# line is <fence line>, on line 3, and holds `--check` to the finding it names. Nothing else in
# the module is an example, so the case builds nothing and asks one question.
fence_case() {
	printf '## %s is a fixture with one fence-shaped line.\n##\n## %s\n## %s.one()\n## ```\npub fn one() -> int {\n\treturn 1\n}\n' \
		"$2" "$3" "$2" >"$tmp/ex/$2.zg"
	ran_check "$1" "$2.zg" 1 "$2.zg:3: $4"
}
not_a_fence='is shaped like an example fence and is not one'
fence_case 'an indented fence' indented '  ```zerg' "\`  \`\`\`zerg\` $not_a_fence"
fence_case 'a ```zg fence' zg '```zg' "\`\`\`\`zg\` $not_a_fence"
fence_case 'a fence with a space before its word' spaced '``` zerg' "\`\`\`\` zerg\` $not_a_fence"
fence_case 'a fence whose word is capitalised' upper '```Zerg' "\`\`\`\`Zerg\` $not_a_fence"
fence_case 'a ```ZG fence' shout '```ZG' "\`\`\`\`ZG\` $not_a_fence"
fence_case 'a four-backtick fence' four '````zerg' "\`\`\`\`\`zerg\` $not_a_fence"
fence_case 'a tilde fence' tilde '~~~zerg' "\`~~~zerg\` $not_a_fence"
fence_case 'an output fence with no example above it' orphan '```output' 'an output fence with no example above it'

# A ONE-FILE MODULE IN A FOLDER OF ITS OWN NAME, and a directory module beside it. The program
# is written where `./name` reaches exactly the files the module was read from; a folder that
# happens to share the module's name must not make a one-file module look like a directory.
mkdir -p "$tmp/ex/gc" "$tmp/ex/pair"
sed 's/right\./gc./' "$tmp/ex/right.zg" >"$tmp/ex/gc/gc.zg"
ran_check 'a one-file module in a folder of its own name' gc/gc.zg 0 "$RIGHT_OK"
sed 's/right\./pair./' "$tmp/ex/right.zg" >"$tmp/ex/pair/mod.zg"
ran_check 'a directory module' pair 0 "$RIGHT_OK"

# MORE DIAGNOSTICS THAN A PIPE HOLDS, and an example that never returns. The first deadlocked
# the runner — it read the build's stdout to its end while the build waited to write stderr —
# and the second hung it with nothing to stop the wait. Both have to be REPORTED.
{
	printf '## loud has an example that does not compile, loudly.\n##\n## ```zerg\n'
	i=0
	while [ "$i" -lt 1500 ]; do
		printf '## loud.nothing%d()\n' "$i"
		i=$((i + 1))
	done
	printf '## ```\npub fn one() -> int {\n\treturn 1\n}\n'
} >"$tmp/ex/loud.zg"
ran_check 'a build whose diagnostics fill a pipe' loud.zg 1 "loud.zg:3: the examples of \`loud\` do not compile"
# and the finding carries the head of that report, saying how much it cut — half a megabyte of
# one sentence would bury every other finding in the run
if grep -q "more line(s) of the build's report cut" "$tmp/ex/loud.zg.out" &&
	[ "$(wc -l <"$tmp/ex/loud.zg.out" | tr -d ' ')" -lt 100 ]; then
	checks=$((checks + 1))
else
	note "a build report of $(wc -l <"$tmp/ex/loud.zg.out" | tr -d ' ') lines was copied into the finding without being cut"
fi

cat >"$tmp/ex/spin.zg" <<'ZG'
## spin never returns.
##
## ```zerg
## spin.spin()
## ```
pub fn spin() {
	mut i := 0
	for {
		i = i + 1
		if i > 1000 {
			i = 0
		}
	}
}
ZG
ran_check 'an example that never returns' spin.zg 1 \
	"spin.zg:3: the examples of \`spin\` did not finish within 3s" --timeout 3

# running <pattern> reports whether a process whose command line matches is alive. The listing
# goes to a file first: under `pipefail`, `ps | grep -q` fails exactly when grep FINDS the line
# and stops reading, which turns every "is it still running" into "no".
running() {
	ps -A -o args= >"$tmp/ps.out"
	grep -q "$1" "$tmp/ps.out"
}

# AND ONE THAT LEAVES A PROCESS RUNNING. Killing the example alone left its child holding the
# output and the command waiting the child out; the limit has to hold, and nothing the example
# started may outlive the run. The marker is unique to this run, and the bracket in the pattern
# keeps `grep` from finding itself.
mark="zerg-doc-grandchild-$$"
cat >"$tmp/ex/nap.zg" <<ZG
import "os"

## nap starts a child that outlives any sensible limit.
##
## \`\`\`zerg
## nap.nap()
## \`\`\`
## \`\`\`output
## 0
## \`\`\`
pub fn nap() -> int {
	return os.run(["sh", "-c", "sleep 30; echo $mark"])
}
ZG
started=$(date +%s)
ran_check 'an example whose child outlives the limit' nap.zg 1 \
	"nap.zg:5: the examples of \`nap\` did not finish within 3s" --timeout 3
took=$(($(date +%s) - started))
if [ "$took" -ge 20 ]; then
	note "an example whose child outlives a 3s limit was reported after ${took}s — the command waited for the child"
elif running "[z]erg-doc-grandchild-$$"; then
	note "an example's child was still running after the run that started it had been stopped"
else
	checks=$((checks + 1))
fi

# AN INTERRUPT CLEANS UP. Ctrl-C reaches the whole foreground process group, so the command is
# started in a group of its own (`set -m` gives a background job one in bash) and the group is
# sent INT while an endless example runs: no run directory, no generated source beside the
# module, and no process the example started may be left. The run directory is found by
# pointing `TMPDIR` at an empty one.
mkdir -p "$tmp/int/run" "$tmp/int/mod"
imark="zerg-doc-interrupted-$$"
cat >"$tmp/int/mod/intr.zg" <<ZG
import "os"

## intr never returns, and leaves a marked process running while it does not.
##
## \`\`\`zerg
## intr.intr()
## \`\`\`
pub fn intr() {
	os.run(["sh", "-c", "while :; do sleep 1; done; : $imark"])
}
ZG
set -m
TMPDIR="$tmp/int/run" "$ZERG_ABS" doc --check --timeout 60 "$tmp/int/mod/intr.zg" >"$tmp/int/out" 2>&1 &
ipid=$!
set +m
i=0
while [ "$i" -lt 30 ] && ! running "[z]erg-doc-interrupted-$$"; do
	sleep 1
	i=$((i + 1))
done
kill -INT -- -"$ipid" 2>/dev/null
wait "$ipid"
# the command dies at once and the shell under it is still cleaning up, so its cleanup is given
# a few seconds to finish before what is left is judged
j=0
while [ "$j" -lt 10 ] && [ -n "$(ls -A "$tmp/int/run")" ]; do
	sleep 1
	j=$((j + 1))
done
left=""
[ -z "$(ls -A "$tmp/int/run")" ] || left="$left the run directory,"
ls -A "$tmp/int/mod" | grep -q zerg-doc && left="$left the generated source,"
running "[z]erg-doc-interrupted-$$" && left="$left the example's process,"
if [ "$i" -ge 30 ]; then
	note "an interrupted run was never seen running its example: $(head -3 "$tmp/int/out")"
elif [ -n "$left" ]; then
	note "an interrupted \`zerg doc --check\` left${left%,} behind"
	pkill -9 -f "zerg-doc-interrupted-$$" 2>/dev/null
else
	checks=$((checks + 1))
fi

# AND THE UNDOCUMENTED HALF IS STILL THERE. The standard library has nothing undocumented, so
# §2 compares an empty list with an empty list — which a `--check` that had stopped naming
# anything would satisfy too.
{
	cat "$tmp/ex/right.zg"
	printf '\npub fn bare() -> int {\n\treturn 1\n}\n'
} | sed 's/right\./bare./' >"$tmp/ex/bare.zg"
ran_check 'an undocumented `pub fn` beside right examples' bare.zg 1 "\`bare.bare\` is exposed and carries no \`##\` comment"

# --- 10. `##` is the reader's text, and `#` the maintainer's -----------------------------
#
# #18: a comment is read twice. The reader's page — the default, and everything a listing,
# `--check` and a hover read — is the `##` lines; `--all` is every comment as written. The
# marker is per LINE, so each case below writes both kinds into one run and asks each view
# which of them it printed. A declaration with `#` notes and no `##` text is undocumented,
# which is the only way a whole forgotten `##` is ever found.
cat >"$tmp/proj/marked.zg" <<'ZG'
## marked — the header's reader text.
#
# the header's maintainer note.

## both carries reader text first.
# both's maintainer note sits between two reader paragraphs.
## both's second reader paragraph.
pub fn both() -> int {
	return 1
}

# notes_only carries a maintainer note and no reader text.
pub fn notes_only() -> int {
	return 2
}

## Holder is documented.
pub struct Holder {
	# a maintainer note on a field.
	pub n: int
}
ZG
"$ZERG" doc "$tmp/proj/marked.zg" >"$tmp/marked.out" 2>&1
"$ZERG" doc --all "$tmp/proj/marked.zg" >"$tmp/marked.all" 2>&1
grep -q '^note: ' "$tmp/marked.out" &&
	note "the marker fixture does not parse, so every view below was asserted against an empty document: $(grep -m1 '^note: ' "$tmp/marked.out")"

# shows <what> <file> <text> / hides <what> <file> <text>
shows() {
	if grep -qF "$3" "$2"; then
		checks=$((checks + 1))
		rules=$((rules + 1))
	else
		note "$1 — \`$(basename "$2")\` does not print \"$3\""
	fi
}
hides() {
	if grep -qF "$3" "$2"; then
		note "$1 — \`$(basename "$2")\` prints \"$3\""
	else
		checks=$((checks + 1))
		rules=$((rules + 1))
	fi
}

shows 'the reader page keeps the `##` header' "$tmp/marked.out" "marked — the header's reader text."
hides 'the reader page drops a `#` line of the header' "$tmp/marked.out" "the header's maintainer note"
shows 'the reader page keeps both `##` paragraphs' "$tmp/marked.out" "both's second reader paragraph."
hides 'the reader page drops a `#` line between two `##` ones' "$tmp/marked.out" "both's maintainer note"
hides "the reader page drops a field's \`#\` note" "$tmp/marked.out" 'a maintainer note on a field'

doc_out="$tmp/marked.out"
rule 'a declaration with `#` notes and no `##` text is undocumented' \
	'fn notes_only()' '(undocumented)'
doc_out="$tmp/marked.all"
rule '`--all` still marks a declaration whose only comment is a `#` note' \
	'fn notes_only()' '(undocumented)'

# THE NOTE BETWEEN TWO READER PARAGRAPHS ENDS THE FIRST. Filled together they would be one
# sentence nobody wrote; the reader page holds them a blank line apart.
if awk '/both carries reader text first\./ { f = 1; next } f { print; exit }' "$tmp/marked.out" | grep -q '^$'; then
	checks=$((checks + 1))
	rules=$((rules + 1))
else
	note 'the two `##` paragraphs of `both` are filled into one where a `#` note stood between them'
fi

shows "\`--all\` prints the header's \`#\` note" "$tmp/marked.all" "the header's maintainer note"
shows '`--all` prints a `#` line between two `##` ones' "$tmp/marked.all" "both's maintainer note"
shows "\`--all\` prints a declaration's only note" "$tmp/marked.all" 'notes_only carries a maintainer note'
shows "\`--all\` prints a field's \`#\` note" "$tmp/marked.all" 'a maintainer note on a field'

# `--check` NAMES IT, which is how a forgotten `##` on a whole comment is found
cp "$tmp/proj/marked.zg" "$tmp/ex/marked.zg"
ran_check 'a declaration with only `#` notes is named by `--check`' marked.zg 1 \
	"\`marked.notes_only\` is exposed and carries no \`##\` comment"

# AN EXAMPLE MARKED BOTH WAYS IS A FINDING. The runner reads every line and the reader page
# shows only the `##` ones, so the example that was run is not the one a reader is shown.
printf '## mixed has an example with a `#` line in it.\n##\n## ```zerg\n# mixed.one()\n## ```\npub fn one() -> int {\n\treturn 1\n}\n' >"$tmp/ex/mixed.zg"
ran_check 'an example marked `##` on some lines and `#` on others' mixed.zg 1 \
	'mixed.zg:3: an example marked `##` on some of its lines and `#` on others'

# `--all` WHERE IT WOULD CHANGE NOTHING IS REFUSED. Every listing reads the `##` text alone,
# so the flag beside one is a question the reader believes was answered.
for args in '--all --brief' '--all --check' '--all'; do
	target="$tmp/proj/marked.zg"
	[ "$args" = '--all' ] && target=''
	# shellcheck disable=SC2086
	if "$ZERG" doc $args $target >"$tmp/allref.out" 2>&1; then
		note "\`zerg doc $args\` exited 0, and \`--all\` beside a listing is refused"
	elif grep -qF "\`--all\` is the maintainer's page" "$tmp/allref.out"; then
		checks=$((checks + 1))
	else
		note "\`zerg doc $args\` was refused with \"$(head -1 "$tmp/allref.out")\""
	fi
done

# --- 11. `-s` finds a declaration by the beginning of its name ------------------------------
#
# #19: `zerg doc -s <term>` lists the exposed declarations whose name begins with the term, read
# from any segment after the module's. §1 holds the name list to every stdlib module's document;
# this fixture asks the rule's edges one at a time — a method found by its own name and by its
# type's, a private declaration and a private method never listed although their names match,
# the mark on an undocumented hit, the module's own segment and a term in the wrong case finding
# nothing, and nothing found being a refusal rather than an empty list.
cat >"$tmp/proj/finder.zg" <<'ZG'
## finder — a module to be searched.

## find_one is found by its prefix.
pub fn find_one() -> int {
	return 1
}

pub fn find_bare() -> int {
	return 2
}

## find_hidden matches every term below and is private, so no search lists it.
fn find_hidden() -> int {
	return 3
}

## Finder holds the methods.
pub struct Finder {
	pub n: int
}

impl Finder {
	## find_more is a method, found as `finder.Finder.find_more`.
	pub fn find_more() -> int {
		return this.n
	}

	## find_private is a private method, and on no page.
	fn find_private() -> int {
		return this.n
	}
}
ZG

# search <term> <scope> — `zerg doc -s` run from the fixture's directory, stdout to
# `$tmp/search.out` and stderr to `$tmp/search.err`; an empty scope is the index
search() {
	# shellcheck disable=SC2086
	(cd "$tmp/proj" && "$ZERG_ABS" doc -s "$1" $2) >"$tmp/search.out" 2>"$tmp/search.err"
}

# finds <what> <term> <scope> <the names it lists, one per line, in order>
finds() {
	local what=$1 term=$2 scope=$3 want=$4 got
	search "$term" "$scope"
	got="$(sed -nE 's/^  ([^ ]+).*/\1/p' "$tmp/search.out")"
	if [ "$got" = "$want" ]; then
		checks=$((checks + 1))
	else
		note "$what — \`zerg doc -s $term $scope\` listed [$(printf '%s' "$got" | tr '\n' ' ')], wanted [$(printf '%s' "$want" | tr '\n' ' ')] $(head -1 "$tmp/search.err")"
	fi
}

# finds_none <what> <term> <scope> — nothing found is a refusal, exit 1, that says so
finds_none() {
	local what=$1 term=$2 scope=$3 rc
	search "$term" "$scope"
	rc=$?
	if [ "$rc" -eq 0 ]; then
		note "$what — \`zerg doc -s $term $scope\` exited 0: $(head -3 "$tmp/search.out" | tr '\n' ' ')"
	elif grep -qF "has a name beginning \`$term\`" "$tmp/search.err"; then
		checks=$((checks + 1))
	else
		note "$what — \`zerg doc -s $term $scope\` was refused with \"$(head -1 "$tmp/search.err")\""
	fi
}

finds 'a prefix lists the exposed declarations it begins, and no private one' find_ finder.zg \
	"$(printf 'finder.find_one\nfinder.find_bare\nfinder.Finder.find_more')"
finds 'a method is found by its own name' find_m finder.zg 'finder.Finder.find_more'
finds "a method is found by its type's name" Finder.f finder.zg 'finder.Finder.find_more'
finds 'a term carrying a `.` is read from the module on' finder.Fi finder.zg \
	"$(printf 'finder.Finder\nfinder.Finder.find_more')"
finds_none 'a private declaration is never listed' find_h finder.zg
finds_none 'a private method is never listed' find_p finder.zg
finds_none "a bare term does not match the module's own segment" finder finder.zg
finds_none 'a search is case sensitive' FIND_ finder.zg
finds_none 'a term nothing begins is a refusal' zzz_nothing ''

# the hit's second column is the page's: the first sentence, or the mark
search find_ finder.zg
if grep -qE '^  finder\.find_one +find_one is found by its prefix\.$' "$tmp/search.out" &&
	grep -qE '^  finder\.find_bare +\(undocumented\)$' "$tmp/search.out"; then
	checks=$((checks + 1))
else
	note "a hit is not its name beside its first sentence, or beside the mark: $(cat "$tmp/search.out")"
fi

# the scope left out is the index, standard library included
finds 'with no name beside it, a search reads the standard library too' strings.spl '' 'strings.split'

# --- the module list, and the floors ----------------------------------------------------
#
# `zerg doc` with no argument is the first page a reader sees, and the only claim made about
# it here is the one that matters: every module this gate read a document out of is on it.
"$ZERG" doc >"$tmp/list.out" 2>&1
listed=$(sed -nE 's/^  ([A-Za-z_][A-Za-z0-9_]*).*/\1/p' "$tmp/list.out" | LC_ALL=C sort -u | wc -l | tr -d ' ')
for m in $modules atomic; do
	grep -qE "^  $m( |$)" "$tmp/list.out" ||
		note "\`$m\` has a document and \`zerg doc\` does not list it"
done
checks=$((checks + 1))

# AND THE LOCAL HALF, ASKED WHERE THERE IS ONE. `zerg doc` lists the modules standing beside
# the reader as well as the standard library's, and this repository's root now holds none:
# `examples/` was the sixteenth name until #57 decision 4 made a folder a module only when it
# holds a `mod.zg`, and a folder of example PROGRAMS is not one.
#
# So the floor below no longer guards that half and this does, in a directory built to have
# one. A guard that stopped guarding is worse than no guard: the number would have gone on
# passing at 15 while the walk answered nothing.
mkdir -p "$tmp/proj/greet"
printf '## A module because it holds this file.\n\nimport pub "./hello"\n' >"$tmp/proj/greet/mod.zg"
printf '## hello is the one name on the surface.\npub fn hello() -> str {\n\treturn "hi"\n}\n' >"$tmp/proj/greet/hello.zg"
printf 'fn main() {\n\tnop\n}\n' >"$tmp/proj/main.zg"
(cd "$tmp/proj" && "$ZERG_ABS" doc >"$tmp/local.out" 2>&1) || true
grep -qE "^  greet( |$)" "$tmp/local.out" ||
	note "a folder holding a \`mod.zg\` beside the reader is a module and \`zerg doc\` does not list it: $(cat "$tmp/local.out")"
checks=$((checks + 1))

if [ "$listed" -lt "$MIN_MODULES" ]; then
	note "\`zerg doc\` lists $listed modules and the floor is $MIN_MODULES — the module walk found nothing"
fi
if [ "$decls" -lt "$MIN_DECLS" ]; then
	note "$decls exposed declarations were compared and the floor is $MIN_DECLS — the extraction stopped matching, and nothing satisfies every claim above"
fi
if [ "$rules" -lt "$MIN_RULES" ]; then
	note "$rules attachment and form rules were asserted and the floor is $MIN_RULES — a fixture stopped rendering, and a rule with no case is a rule that does not exist"
fi
if [ "$checks" -lt "$MIN_CHECKS" ]; then
	note "$checks checks ran and the floor is $MIN_CHECKS — a section was skipped, and a skipped section reports nothing"
fi

[ "$fail" -eq 0 ] || {
	printf 'doc-check: the document is not what the source exposes\n' >&2
	exit 1
}

printf 'doc-check: %s exposed declarations across %s modules are each in the document, %s of them marked undocumented, %s attachment and form rules have a case of their own, colour follows the terminal while the shape does not, and a comment in Chinese lays out inside %s columns with no space its source does not have — %s checks\n' \
	"$decls" "$listed" "$undoc" "$rules" "$DOC_COLUMNS" "$checks"
