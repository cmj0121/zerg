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
# reason.
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
# WHAT IS NOT HERE: `zerg doc --check` — compiling a doc example and diffing its output —
# is the second half of issue #17 and is not built. scripts/doc-examples-check.sh is still
# the thing that runs the examples, it still runs on the board under `stdlib-test`, and
# nothing here overlaps it.
#
# It needs no corpus: the standard library ships in this repository and the fixture is
# written by the script, so it runs the same everywhere.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

ZERG="${ZERG:-./bin/zerg}"

# The counts this gate would report success for having measured nothing. 183 exposed
# declarations across the 14 stdlib modules that parse, 15 modules in the list, and 18
# declarations that carry no comment.
MIN_DECLS="${MIN_DECLS:-183}"
MIN_MODULES="${MIN_MODULES:-15}"
UNDOCUMENTED="${UNDOCUMENTED:-18}"
MIN_CHECKS="${MIN_CHECKS:-45}"
MIN_RULES="${MIN_RULES:-25}"

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
	sed -nE 's/^[[:space:]]*pub[[:space:]]+(unsafe[[:space:]]+)?(mut[[:space:]]+)?(fn|struct|enum|const|type|spec)[[:space:]]+([A-Za-z_][A-Za-z0-9_]*).*/\4/p' "$1" |
		LC_ALL=C sort
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
	' | LC_ALL=C sort
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

	decls=$((decls + $(wc -l <"$tmp/doc.names" | tr -d ' ')))
	checks=$((checks + 1))
done

# --- 2. the undocumented ones are counted, and the number is pinned -------------------
#
# They are SHOWN by design (issue #16, point 4). What a gate can add is that the number
# moves deliberately: a tool that stopped marking them would look like a library somebody
# had finished documenting, and a `pub` added with no comment would otherwise arrive in
# silence. Both directions are the same assertion, so it is an equality and not a bound.
undoc=0
for m in $modules; do
	n=$("$ZERG" doc "$m" 2>/dev/null | grep -c '(undocumented)')
	undoc=$((undoc + n))
done
if [ "$undoc" -ne "$UNDOCUMENTED" ]; then
	note "$undoc declarations are marked (undocumented) and the pinned number is $UNDOCUMENTED — if a comment was written or a bare \`pub\` added, move UNDOCUMENTED in this script with it"
else
	checks=$((checks + 1))
fi

# --- 3. a module that does not parse says so ------------------------------------------
#
# `atomic` declares a generic struct this compiler refuses (E9004), so its declarations
# cannot be listed. The honest answer is the one thing that must not go missing: the header
# still reads, and a `note:` line says the rest is absent. Silence here would be a module
# that documents itself as empty.
"$ZERG" doc atomic >"$tmp/atomic.out" 2>&1
grep -q '^note: ' "$tmp/atomic.out" ||
	note "\`zerg doc atomic\` lists no declarations and says nothing about why — a module that does not parse must be reported, not rendered empty"
grep -q '^atomic — ' "$tmp/atomic.out" ||
	note "\`zerg doc atomic\` lost the module header of a file it could not parse; the header comes from the trivia and does not need the AST"
checks=$((checks + 2))

# --- 4. the comment-attachment rules, one case each ------------------------------------
#
# Which comment documents which declaration is decided by line geometry over the token
# stream, and every rule below is a line in that algorithm. They are written into one
# fixture rather than one file each so that the rules sit next to each other in the order
# a reader meets them, and so the whole set is one parse.
mkdir -p "$tmp/proj"
cat >"$tmp/proj/attach.zg" <<'ZG'
# attach — the module header, which belongs to the MODULE and to no declaration below it.
#
# A second paragraph of it, so that a header stolen by the first declaration would be
# visible as a declaration carrying two paragraphs it never had.

# MAX is documented by this comment and by nothing above it.
pub const MAX: int = 64

# --- a section banner, which documents nothing ---------------------------------------
pub fn after_banner() -> int {
	return 1
}

# this comment is separated from the declaration by a blank line, so it attaches to nothing

pub fn detached() -> int {
	return 2
}

# Derived carries a decorator between its comment and its declaration.
#[derive(Eq)]
pub struct Derived {
	pub n: int
}

impl Derived {
	# doubled is documented by a comment indented inside the impl block.
	pub fn doubled() -> int {
		return this.n * 2
	}
}

# adjacent is documented; the declaration under it has no comment of its own.
pub fn adjacent() -> int {
	return 3
}
pub fn crowded() -> int {
	return 4
}

# hashy is documented by this comment, and the `#` in the string below is not one.
pub fn hashy() -> str {
	return "# not a comment"
}
# tail_commented is documented by this comment and not by the one at the end of the line
# above it.
pub fn tail_commented() -> int {
	return 5
}

# ```zerg
# attach.only_example()
# ```
# ```output
# 6
# ```
pub fn only_example() -> int {
	return 6
}

# tag is the free function, and Widget has a method of the same name.
pub fn tag() -> str {
	return "free"
}

# Widget is a struct.
pub struct Widget {
	pub n: int
}

impl Widget {
	# tag is the method, whose answer is not the free function's.
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
sed -i.bak '/^	return "# not a comment"$/{n;s/^}$/}  # this trailing comment documents nothing/;}' \
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
# asked once and every rule below is one call.
doc_of() {
	awk -v want="$1" '
		index($0, want) && !seen { seen = 1; next }
		seen && NF { print; exit }
	' "$tmp/attach.out"
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
# forms — the exposed forms the standard library happens not to declare.

# LIMIT is a public constant.
pub const LIMIT: int = 64

# Name is a public type.
pub type Name = str

# Named is a public spec.
pub spec Named {
	# label is what a Named answers to.
	fn label() -> str
}

# Counter is a struct whose method mutates it.
pub struct Counter {
	pub n: int
}

impl Counter {
	# bump adds one to the receiver, in place.
	pub mut fn bump() {
		this.n = this.n + 1
	}
}

unsafe {
	# COUNTER is the one mutable global the language has.
	pub mut COUNTER := 0

	# peek reads a raw address, and is an `unsafe fn` because the group says so.
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
	grep -q "$esc\[33m(undocumented)$esc\[0m" "$tmp/pty.raw" ||
		note 'the (undocumented) mark at a terminal is not coloured'
	checks=$((checks + 2))

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
# width — 一份用中文寫成的 module,它量的是文件的欄寬。
#
# 全形字在終端機上佔兩欄。一段以「字數」折行的文字會折到八十個字,印出來卻是一百六十欄,
# 整份文件因此跑出終端機的右緣;以顯示欄寬折行的則每一行都落在八十欄以內。這一段刻意寫得
# 夠長,長到只要折行是以字數計算就一定有一行超出,而 zerg doc 的排版正是這裡在量的東西。

# LIMIT 是這份文件量出來的欄數。
pub const LIMIT: int = 80

# label 回答一個標籤。它的說明也是中文的,而且夠長:縮排六欄之後的內文仍然必須折行,折在
# 哪一個字上才是這道 gate 真正在看的東西,而 terminal 與 column 這類西文詞夾在其中,讓貪
# 心填字有得選。
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
refused "$ROOT" 'a declaration of a module that did not parse' 'atomic.load' 'does not parse under this compiler'

# and that refusal does not go on to head an empty list, which is the claim the note corrects
"$ZERG" doc atomic.load >"$tmp/decl.out" 2>&1
grep -q '^what it does declare:' "$tmp/decl.out" &&
	note '`zerg doc atomic.load` heads a list of what the module declares and then lists nothing — the module did not parse, which the note above it has just said'
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

printf 'doc-check: %s exposed declarations across %s modules are each in the document, %s of them marked undocumented, %s attachment and form rules have a case of their own, colour follows the terminal while the shape does not, and a comment in Chinese lays out inside %s columns — %s checks\n' \
	"$decls" "$listed" "$undoc" "$rules" "$DOC_COLUMNS" "$checks"
