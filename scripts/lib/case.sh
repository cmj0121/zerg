# case.sh — how a gate case's text becomes the files the compiler reads.
#
# Sourced by reject-check and refuse-check, which both take a case on stdin. It is one copy on
# purpose: the split is a byte pipe, and a second copy that drifted would hand the compiler a
# different program from the one the case wrote, with nothing to say so.

# write_case <dir> <entry> — splits stdin into files under <dir>. A line `--- <relative path>`
# starts the next file; everything before the first one is <entry>, which always exists
# afterwards, so a module case can sit in a directory of its own and `import "./lib"` reaches
# that case's module and no other case's.
#
# LC_ALL=C, because this is a byte pipe and not a text one. A case is a PROGRAM, and one
# class of ill-formed program is ill-formed exactly because its bytes are not text: under
# the ambient UTF-8 locale macOS awk stops on `towc: multibyte conversion failure` and
# writes a TRUNCATED file, so the case that reaches the compiler is not the case that was
# written and the gate reports a mismatch on a rule it never exercised. In the C locale a
# record is bytes, `/^--- /` matches bytes, and every case — UTF-8, ASCII or neither —
# arrives as it was typed.
write_case() {
	rm -rf "$1"
	mkdir -p "$1"
	LC_ALL=C awk -v dir="$1" -v entry="$2" '
		/^--- / {
			out = dir "/" $2
			d = out
			sub(/\/[^\/]*$/, "", d)
			system("mkdir -p \"" d "\"")
			next
		}
		{ print > (out ? out : entry) }
	' >/dev/null
	touch "$2"
}
