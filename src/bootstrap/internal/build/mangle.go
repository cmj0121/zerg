package build

import (
	"fmt"
	"hash/fnv"
)

// mangleModule turns a module's canonical resolution path into a stable C
// identifier suitable for prefixing type / fn / vtable symbols. Algorithm
// per PLAN.md §Mangling for codegen:
//
//  1. Strip the `.zg` extension if present.
//  2. Replace every byte not matching [A-Za-z0-9] with `_`.
//  3. If the result starts with a digit, prepend `m_`.
//  4. Append `_h<hex8>` where <hex8> is the first 8 hex chars of the FNV-1a
//     hash of the canonical input path.
//
// The hash suffix defeats collisions on basename collisions across
// directories ("/a/util.zg" vs "/b/util.zg") and makes the identifier
// deterministic per absolute path.
//
// Inputs:
//   - The entry module's canonical name is the literal string "main"; the
//     entry file's source filename does NOT enter the mangle. Pass "main".
//   - Every sibling module's canonical name is its absolute filesystem path.
//
// Examples:
//
//	mangleModule("main")                    → "main_h<hex8>"
//	mangleModule("/abs/path/util.zg")       → "_abs_path_util_h<hex8>"
//	mangleModule("/abs/path/2d-math.zg")    → "m_abs_path_2d_math_h<hex8>"
//	mangleModule("/中文.zg")                → "_____h<hex8>" (3 invalid bytes per char × 1 char)
func mangleModule(canonicalPath string) string {
	// Step 1: hash the ORIGINAL canonical path so two paths with the same
	// post-replace shape (e.g. "/util" and "_util") still produce different
	// suffixes. Compute this BEFORE stripping the extension so two files
	// "util" and "util.zg" get distinct suffixes too.
	h := fnv.New32a()
	_, _ = h.Write([]byte(canonicalPath))
	hex8 := fmt.Sprintf("%08x", h.Sum32())

	// Step 2: strip the .zg extension if present.
	stripped := canonicalPath
	if len(stripped) >= 3 && stripped[len(stripped)-3:] == ".zg" {
		stripped = stripped[:len(stripped)-3]
	}

	// Step 3: replace non-alphanumeric bytes with `_`. We walk byte-by-byte
	// so non-ASCII (multi-byte UTF-8) characters expand to the right number
	// of underscores per PLAN.md (every byte not matching [A-Za-z0-9] is
	// replaced — `中` is three bytes, all replaced).
	out := make([]byte, 0, len(stripped)+12)
	for i := 0; i < len(stripped); i++ {
		c := stripped[i]
		switch {
		case c >= 'A' && c <= 'Z':
			out = append(out, c)
		case c >= 'a' && c <= 'z':
			out = append(out, c)
		case c >= '0' && c <= '9':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}

	// Step 4: leading-digit prepend. After replacement the first byte may be
	// a digit (e.g. "2d-math" → "2d_math"). C identifiers must start with a
	// non-digit; prepend `m_` to make the result a valid leading character.
	if len(out) > 0 && out[0] >= '0' && out[0] <= '9' {
		out = append([]byte("m_"), out...)
	}

	// Step 5: append `_h<hex8>`.
	out = append(out, '_', 'h')
	out = append(out, hex8...)
	return string(out)
}
