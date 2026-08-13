#!/usr/bin/env bash
#
# cloc-config — put Zerg into cloc itself, and take it out again.
#
# cloc reads `~/.config/cloc/options.txt` before every run, one switch per line, so a language
# it does not ship can be installed once and then simply BE there: `cloc .` counts Zerg with no
# flag to remember and no wrapper target to run instead. That is the whole reason this exists,
# and the reason the repository has no `make cloc` — the count belongs to cloc, and what is
# owned here is the definition.
#
# The file is SHARED. It is the user's, it may hold switches that have nothing to do with this
# repository, and an install that writes it whole would take them with it. So the line is
# carried by a marker and every edit is a filter over the lines already there: installing twice
# leaves one copy, and uninstalling leaves everything that was not put there by this script.
#
# UNINSTALL IS NOT OPTIONAL, which is not true of the editor files next door. A stale
# `ftdetect/zerg.vim` highlights nothing; a `--read-lang-def` pointing at a deleted path makes
# cloc answer
#
#     Unable to read /usr/local/lib/zerg/cloc.def
#
# and count NOTHING — in every project on the machine, for every invocation, until somebody
# works out which file is doing it. That asymmetry is why the definition is copied into
# $(PREFIX), where `make uninstall` already removes it, rather than symlinked at a checkout
# that can be moved or deleted with nothing watching.
set -u

MARKER='# zerg: cloc language definition, installed by `make install`'

usage() {
	echo "usage: cloc-config.sh install <def-path> <config-dir>" >&2
	echo "       cloc-config.sh uninstall <config-dir>" >&2
	exit 2
}

# strip removes this script's own two lines and leaves every other line untouched: the marker,
# and whatever single line FOLLOWS it, which is the pair this script writes and the only shape
# it has ever written.
#
# Following the marker rather than matching the switch is what makes it independent of
# $(PREFIX). Matching `--read-lang-def=.*/lib/zerg/cloc.def` instead reads well and is wrong
# twice: `make install PREFIX=a` then `PREFIX=b` leaves a's line behind — it is not the path b
# writes, so nothing recognises it, and the config ends up naming two definitions of the same
# language — and a user who has their OWN --read-lang-def for some other language has it
# deleted by an uninstall that was never given anything to do with it.
strip() {
	awk -v m="$MARKER" 'skip { skip = 0; next } $0 == m { skip = 1; next } { print }'
}

case "${1:-}" in
install)
	[ $# -eq 3 ] || usage
	def=$2
	dir=$3
	opts="$dir/options.txt"

	[ -f "$def" ] || {
		echo "cloc-config: $def is not there — nothing was installed" >&2
		exit 1
	}

	mkdir -p "$dir" || exit 1
	kept=""
	[ -f "$opts" ] && kept=$(strip <"$opts")
	{
		[ -n "$kept" ] && printf '%s\n' "$kept"
		printf '%s\n' "$MARKER"
		printf -- '--read-lang-def=%s\n' "$def"
	} >"$opts.new" && mv "$opts.new" "$opts" || exit 1

	printf '    cloc reads Zerg from %s\n' "$opts"
	;;
uninstall)
	[ $# -eq 2 ] || usage
	dir=$2
	opts="$dir/options.txt"

	[ -f "$opts" ] || exit 0

	kept=$(strip <"$opts")
	# Nothing of the user's left means the file was ours alone, and an empty options.txt is
	# a file cloc opens on every run to learn nothing. The directory goes only if it is
	# empty, because it is `~/.config/cloc` and nothing here put it there.
	if [ -z "$(printf '%s' "$kept" | tr -d '[:space:]')" ]; then
		rm -f "$opts"
		rmdir "$dir" 2>/dev/null || true
	else
		printf '%s\n' "$kept" >"$opts.new" && mv "$opts.new" "$opts" || exit 1
	fi

	printf '    cloc no longer reads Zerg from %s\n' "$opts"
	;;
*)
	usage
	;;
esac
