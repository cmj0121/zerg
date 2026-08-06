# diag.sh — how a diagnostic from THIS compiler is told from one from cc.
#
# Sourced by refuse-check, reject-check and reject-fuzz. It is shared because it is one
# fact about the compiler's output layout, not a list of cases: the argument for keeping
# those three scripts apart is that their case lists have different lifetimes, and these
# two predicates are not case lists.
#
# Both are NEGATIVE assertions, which is exactly why a copy is dangerous — a stale one
# fails OPEN. When `#line` made cc name the `.zg`, the older test (looking for a
# `.zerg-cache` path) stopped matching anything and would have gone on passing forever.

# is_cc_diag <text> — cc opens a line with `path:line:col: error:`. This compiler opens
# with `error:` and puts the place on an indented `-->` line beneath it, so the two are
# told apart by SHAPE rather than by the path inside them.
is_cc_diag() {
	printf '%s\n' "$1" | grep -qE '^[^ ].*:[0-9]+:[0-9]+: (error|warning):'
}

# has_place <text> — every diagnostic owes a `--> file:line:col`.
has_place() {
	printf '%s\n' "$1" | grep -qE '^  --> .*:[0-9]+:[0-9]+$'
}

# place_is <text> <suffix-regex> — the positive half of has_place: some `-->` line's place
# ENDS with the given regex (e.g. "case\.zg:1:1"), for a case that pins WHICH line a rule
# points at rather than only that one exists. It lives here beside has_place because the
# indented-arrow layout is one fact about this compiler's output, not a per-script detail.
place_is() {
	printf '%s\n' "$1" | grep -qE "^  --> .*$2\$"
}
