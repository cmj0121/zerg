# shellcheck shell=bash
# sha256.sh — the digest the release path writes, and the one it reads back.
#
# TWO SPELLINGS OF ONE TOOL. GNU coreutils has `sha256sum`; macOS ships `shasum -a 256`.
# Both print `<hash>  <name>` and both read that format back with `-c`, so the only thing
# that differs is which name is on the machine — and three sites in this tree had each
# decided that for themselves. A release WRITES a line (`release-tarball.sh`), the publish
# job READS every line back (`release-sums.sh`), and the notes tell a downloader to do the
# same; a fallback that existed at one of those and not the others is a release path that
# works on the runner it was written on.
#
# `sha256-check.sh` is deliberately NOT a caller. It is allowed to find neither tool and
# still pass, because it is asking whether `src/stdlib/sha256.zg` agrees with an outside
# authority — and a machine with no such authority has not failed that question, it has
# only left half of it unasked. Here, a missing tool means the artifact ships unverifiable,
# which is a failure.

# sha256_sum_file <dir> <name> — the `<hash>  <name>` line for one file, computed with the
# directory as the working directory so the NAME in the line is relative, which is what a
# downloader who unpacked a tarball beside it needs it to be.
sha256_sum_file() {
	local dir=$1 name=$2
	if command -v sha256sum >/dev/null 2>&1; then
		(cd "$dir" && sha256sum "$name")
	elif command -v shasum >/dev/null 2>&1; then
		(cd "$dir" && shasum -a 256 "$name")
	else
		echo "sha256: neither sha256sum nor shasum is on this machine" >&2
		return 1
	fi
}

# sha256_verify <dir> <sums> — every line of <sums> checked against the files beside it,
# which is the command the release notes tell a downloader to run.
sha256_verify() {
	local dir=$1 sums=$2
	if command -v sha256sum >/dev/null 2>&1; then
		(cd "$dir" && sha256sum -c "$sums")
	elif command -v shasum >/dev/null 2>&1; then
		(cd "$dir" && shasum -a 256 -c "$sums")
	else
		echo "sha256: neither sha256sum nor shasum is on this machine" >&2
		return 1
	fi
}
