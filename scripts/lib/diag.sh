# diag.sh — the SHAPE of a diagnostic: how one from THIS compiler is told from one from cc,
# and how a refusal by name is told from the message a typo gets.
#
# Sourced by every gate that judges a diagnostic rather than a program — refuse-check,
# reject-check, reject-fuzz, conformance-check, productions-check, counterexample-check. It
# is shared because each of these is one fact about the compiler's OUTPUT LAYOUT, not a list
# of cases: the argument for keeping those scripts apart is that their case lists have
# different lifetimes, and none of these predicates is a case list.
#
# Every one of them is a NEGATIVE assertion, which is exactly why a copy is dangerous — a
# stale one fails OPEN and goes on reporting success forever. It has happened twice already,
# once per predicate: when `#line` made cc name the `.zg`, the older cc test (looking for a
# `.zerg-cache` path) stopped matching anything; and when rules gained codes, both copies of
# `is_typo_msg` stopped matching the very messages they were written for.

# is_crash <status> — the compiler died of a signal. A crash is a NON-ZERO EXIT too, so
# every wording assertion in both scripts reports it as a message that drifted: a SIGSEGV
# writes nothing to stderr, and the case fails as `wanted X, got:` with an empty tail. It
# is the one outcome the standing rule never allows (docs/conformance.md), so it is named
# — and it lives here because both `reject` and `expect` run the same sequence and both
# had the same blind spot.
is_crash() {
	[ "$1" -ge 128 ]
}

# is_cc_diag <text> — cc opens a line with `path:line:col: error:`. This compiler opens
# with `error:` and puts the place on an indented `-->` line beneath it, so the two are
# told apart by SHAPE rather than by the path inside them.
is_cc_diag() {
	printf '%s\n' "$1" | grep -qE '^[^ ].*:[0-9]+:[0-9]+: (error|warning):'
}

# cc_answered <text> — the diagnostic came from cc rather than from this compiler, by either
# of the two tells. The standing rule is that `zerg` refuses and cc never speaks about a
# program's source (docs/conformance.md), and a case whose finding moved from one to the
# other still FAILS either way, so no exit status separates them.
#
# The two tells are not one. The SHAPE is `is_cc_diag` above: cc opens a line with the place
# and this compiler puts the place on an indented `-->` beneath. The PATH is the second, and
# it survived the first: `#line` directives point cc at the `.zg` the programmer wrote, so a
# cc error can carry no `.zerg-cache` at all — but a build given no `-o` still leaves its
# intermediate C in the cache, and a message naming that path is one nobody can open.
#
# All three gates that judge a refusal ask this — refuse, reject, counterexample — and each
# had its own arrangement of the same two lines. That is the failure this file's header
# recounts, one level up: not a stale predicate, but three call sites free to drift into
# asking two questions, or one.
cc_answered() {
	is_cc_diag "$1" && return 0
	printf '%s\n' "$1" | grep -q '\.zerg-cache'
}

# is_typo_msg <text> — the message a form gets when the compiler did not RECOGNISE it and
# reported the token it was standing on instead. It is what the conformance and production
# corpora assert against: a form GRAMMAR derives must be built or refused BY NAME, and "
# expected `=>`, found `|`" is neither — it is the answer a misspelling gets, handed to a
# derivation the language plainly has.
#
# It is a NEGATIVE test and it fails open, which is the risk the list is worth taking: the
# alternative is a per-file expected-message inventory, and a case name in those corpora is
# private content that may not be written down in this repo.
#
# The CODE is optional in the pattern, and it was not — a fail-open that had already
# happened once. A checked rule opens its message with `E2004 …` now, so the day the codes
# landed this predicate stopped matching the very messages it was written for, and nothing
# went red, because a stale negative test reports nothing. Both corpora's gates carried
# their own copy of the pattern and were "kept in step by hand", which is the arrangement
# that let it happen; there is one copy now.
is_typo_msg() {
	printf '%s\n' "$1" | grep -qE "^(error: )?(E[0-9]{4} )?(expected |undefined |no type named |no field |unexpected )"
}

# has_place <text> — every diagnostic owes a `--> file:line:col`.
has_place() {
	printf '%s\n' "$1" | grep -qE '^  --> .*:[0-9]+:[0-9]+$'
}

# names_a_temp <text> — the diagnostic names something the COMPILER invented rather than
# something the reader wrote. A message may only speak of names that are in the file: told
# about a binding they cannot find, a reader has nowhere to go and nothing to fix.
#
# Every name this compiler mints for itself is spelled `zg…_` — `zg_` for a mangled user
# name, `zgt_` for an expression temporary, `zga_` for the operands `assert` hoists so that
# a failure can report their values, `zgd_` for what `zerg desugar` writes out. None of
# those is source, and no rule has any business quoting one.
#
# It is here rather than in one gate because it is the same kind of fact as `cc_answered`:
# a statement about whose vocabulary a diagnostic is allowed to use. `assert` is what made
# it worth writing down — its temporaries are bindings in the ordinary environment, so any
# rule that reports a NAME could reach one, and two already had: a closure capturing an
# assert was refused as _E4069 a closure captures `zga_l3c10`_, and an operand the checker
# turned away left _E3069 undefined name `zga_l3c9`_ behind it, one per conjunct of an `and`.
# Both were found by hand. This is what finds the third.
#
# `temp_named` answers WHICH one, so the gate that catches it can put the name in its own
# message rather than leaving a reader to search the output for it, and the two are one
# function and a test of it: a second copy of the pattern is the failure this file's header
# recounts, and a negative assertion is where that failure is invisible.
temp_named() {
	printf '%s\n' "$1" | grep -oE '`zg[a-z]*_[A-Za-z0-9_]*' | head -1 | tr -d '`'
}

names_a_temp() {
	[ -n "$(temp_named "$1")" ]
}

# opens_with_code <text> <code> — the diagnostic's FIRST line starts with the code. Where a
# rule reports a place the renderer's `error:` opens the line ahead of it (`error: E3006 …`),
# and a rule that raises before there is a place to report prints the message alone — so the
# code is what the line starts with either way, and that is the whole fact this encodes.
#
# It is here rather than in each script because it is the same fact as `has_place`: one
# statement about where the compiler puts things in a line. Both gates asserted it with their
# own copy, which is the shape of failure this file's header is about — a renderer that grew
# an `error[E3006]:` form would leave one copy fixed and the other passing while asserting
# nothing.
opens_with_code() {
	case $(printf '%s\n' "$1" | head -1) in
	"$2 "* | "error: $2 "*) return 0 ;;
	esac
	return 1
}

# place_is <text> <suffix-regex> — the positive half of has_place: some `-->` line's place
# ENDS with the given regex (e.g. "case\.zg:1:1"), for a case that pins WHICH line a rule
# points at rather than only that one exists. It lives here beside has_place because the
# indented-arrow layout is one fact about this compiler's output, not a per-script detail.
place_is() {
	printf '%s\n' "$1" | grep -qE "^  --> .*$2\$"
}
