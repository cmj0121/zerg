# shellcheck shell=bash
# ledger.sh — a list of names with a reason each, and the three questions asked of one.
#
# Four gates keep one, and every one of them is the same arrangement: a set this repository
# tolerates, written down so that a tolerated thing cannot become an unread one.
#
#   scripts/oracle-skips.txt    the programs the SEED cannot build
#   scripts/sanitize-leaks.txt  the corpus cases that still report under the sanitizers
#   scripts/corpus-skips.txt    the corpus cases `zerg` cannot build yet
#   gates-check.sh's own lists  the make targets that are not gates, the gates that tolerate
#                               an absent corpus, the gates that owe no floor
#
# A ledger is only worth having if it is held in BOTH directions, and that is three clauses,
# not one:
#
#   UNLISTED  something is in the tolerated state and the ledger does not name it — nobody
#             decided to tolerate it, it simply arrived
#   STALE     the ledger names something that is no longer in that state — the line has
#             rotted, and the next arrival under that name passes unread
#   MOVED     the ledger names it and the REASON is different — the same name over a new
#             defect, which is the clause worth having and the one a count can never show
#
# All four wrote those clauses out for themselves, in four codes, with four sets of wording
# and four sets of gaps: the seed's skips grew a scope rule nobody else has, the leak list
# grew a qualifier nobody else has, `CORPUS_SKIP` had one clause of the three and checked an
# exit status for it, and the board's lists had no STALE clause at all, so a name for a target
# that no longer exists would have sat there for as long as anyone kept typing it. The doctrine
# is here once now, and each gate keeps its own voice for reporting what this found.
#
# FORMAT is `<key>TAB<reason>`. A `#` line and a blank one are skipped. The key is whatever the
# gate names its members by — a path, a case name, a target — and the reason is a sentence a
# person reads, not a code this file parses.
#
# THE QUALIFIER. A reason opening with `?` is one the ledger cannot promise about EVERY host:
# `assert_claim` reports under the leak checker on linux/arm64 and not on linux/amd64, so a
# STALE clause that held it would turn a true line into a red board on whichever architecture
# it was not measured on. A `?` line is exempt from STALE and from nothing else — UNLISTED
# still covers it, so nothing new may arrive anywhere. What is given up is only the claim that
# this particular line is still earning its place.
#
# THE SCOPE. A ledger may cover more than one run looked at: `make oracle` passes two skip
# inventories and a developer sweeping `examples/` alone must not be told that thirty test-data
# cases have started building. A scope file is a list of keys THIS RUN examined, and STALE is
# asked only about entries inside it.

# ledger_read <file>... — the lines of a ledger, `<key>TAB<reason>`, comments and blanks gone.
# A file that is not there contributes nothing: the private half of a ledger lives in the
# test-data submodule, and a checkout without it is not handed those members either.
ledger_read() {
	local f
	for f in "$@"; do
		[ -f "$f" ] || continue
		grep -v '^[[:space:]]*#' "$f" | grep -v '^[[:space:]]*$'
	done
}

# ledger_lookup <key> <file>... — the reason this ledger gives for <key>, with the qualifier
# stripped, and a non-zero status when the key is not listed at all. The qualifier is the
# STALE clause's business and no caller's.
ledger_lookup() {
	local key=$1
	shift
	ledger_read "$@" | awk -F'\t' -v k="$key" '
		function rest(  i, s) { s = $2; for (i = 3; i <= NF; i++) s = s "\t" $i; return s }
		$1 == k { s = rest(); sub(/^\?/, "", s); print s; found = 1; exit }
		END { exit !found }
	'
}

# ledger_unlisted <observed> <file>... — the observed members the ledger does not name, as
# `<key>TAB<reason>` lines taken from the OBSERVATION, since that is the sentence a reader
# needs in order to decide whether to add the line or fix the thing.
ledger_unlisted() {
	local observed=$1
	shift
	{
		ledger_read "$@" | awk '{ print "L\t" $0 }'
		awk '{ print "O\t" $0 }' "$observed"
	} | awk -F'\t' '
		function rest(  i, s) { s = $3; for (i = 4; i <= NF; i++) s = s "\t" $i; return s }
		$1 == "L" { listed[$2] = 1; next }
		$1 == "O" && !($2 in listed) { print $2 "\t" rest() }
	'
}

# ledger_stale <observed> <scope> <file>... — the listed members that are no longer in the
# state the ledger is about. <scope> is a file of keys this run examined, or `-` for a run
# that examined everything the ledger covers.
ledger_stale() {
	local observed=$1 scope=$2 have_scope=0
	shift 2
	[ "$scope" != "-" ] && [ -f "$scope" ] && have_scope=1
	{
		[ "$have_scope" -eq 1 ] && awk '{ print "S\t" $0 }' "$scope"
		awk '{ print "O\t" $0 }' "$observed"
		ledger_read "$@" | awk '{ print "L\t" $0 }'
	} | awk -F'\t' -v have_scope="$have_scope" '
		function rest(  i, s) { s = $3; for (i = 4; i <= NF; i++) s = s "\t" $i; return s }
		$1 == "S" { inscope[$2] = 1; next }
		$1 == "O" { seen[$2] = 1; next }
		$1 == "L" {
			if ($2 in seen) next
			if (have_scope && !($2 in inscope)) next
			if ($3 ~ /^\?/) next
			print $2 "\t" rest()
		}
	'
}

# ledger_moved <observed> <scope> <file>... — the listed members observed for a DIFFERENT
# reason, as `<key>TAB<was>TAB<now>`. The qualifier says nothing about the reason, so a `?`
# line is held here like any other.
ledger_moved() {
	local observed=$1 scope=$2 have_scope=0
	shift 2
	[ "$scope" != "-" ] && [ -f "$scope" ] && have_scope=1
	{
		[ "$have_scope" -eq 1 ] && awk '{ print "S\t" $0 }' "$scope"
		awk '{ print "O\t" $0 }' "$observed"
		ledger_read "$@" | awk '{ print "L\t" $0 }'
	} | awk -F'\t' -v have_scope="$have_scope" '
		function rest(  i, s) { s = $3; for (i = 4; i <= NF; i++) s = s "\t" $i; return s }
		$1 == "S" { inscope[$2] = 1; next }
		$1 == "O" { seen[$2] = 1; now[$2] = rest(); next }
		$1 == "L" {
			if (!($2 in seen)) next
			if (have_scope && !($2 in inscope)) next
			was = rest(); sub(/^\?/, "", was)
			if (was == now[$2]) next
			print $2 "\t" was "\t" now[$2]
		}
	'
}

# ledger_self_test — the three clauses still behave as this file documents. Every one of them
# is a NEGATIVE assertion — an empty answer is the shape of success — so a rewrite that breaks
# the extraction reports four clean ledgers instead of failing. Each gate runs this before it
# measures anything, which is what grammar.sh's own self-test is for and for the same reason.
ledger_self_test() {
	local dir bad=0 got
	dir=$(mktemp -d) || return 1

	printf '# a comment\n\nkept\tthe reason it is kept\nmoved\tthe old reason\nfixed\tthe reason it had\nqualified\t?not on every host\nout-of-scope\tnobody looked\n' >"$dir/ledger"
	printf 'kept\tthe reason it is kept\nmoved\tthe new reason\narrived\tnothing said why\n' >"$dir/observed"
	printf 'kept\nmoved\nfixed\nqualified\narrived\n' >"$dir/scope"

	want() {
		if [ "$2" != "$3" ]; then
			printf 'SELFTEST  %s: wanted [%s], got [%s]\n' "$1" "$3" "$2"
			bad=1
		fi
	}

	got=$(ledger_unlisted "$dir/observed" "$dir/ledger" | tr '\t' '=' | tr '\n' ' ')
	want "unlisted names what arrived" "$got" "arrived=nothing said why "

	got=$(ledger_stale "$dir/observed" "$dir/scope" "$dir/ledger" | tr '\t' '=' | tr '\n' ' ')
	want "stale names the line whose member is gone, and skips the qualified one" "$got" "fixed=the reason it had "

	got=$(ledger_stale "$dir/observed" - "$dir/ledger" | cut -f1 | tr '\n' ' ')
	want "an unscoped run asks about every line but the qualified" "$got" "fixed out-of-scope "

	got=$(ledger_moved "$dir/observed" "$dir/scope" "$dir/ledger" | tr '\t' '=' | tr '\n' ' ')
	want "moved names the old reason and the new" "$got" "moved=the old reason=the new reason "

	got=$(ledger_lookup qualified "$dir/ledger")
	want "lookup strips the qualifier" "$got" "not on every host"

	if ledger_lookup nosuch "$dir/ledger" >/dev/null; then
		printf 'SELFTEST  lookup answered for a key the ledger does not have\n'
		bad=1
	fi

	rm -rf "$dir"
	[ $bad -eq 0 ] && return 0
	echo "the ledger clauses no longer behave as scripts/lib/ledger.sh documents,"
	echo "so every tolerated set below would have been held to nothing"
	return 1
}
