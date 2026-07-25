package parser

import "testing"

// A '{' block re-enables automatic ';' insertion even when it sits inside an unclosed
// '(' or '[': the block's own statements are newline-separated, so a multi-line closure
// or block passed as a call argument parses without explicit ';'. Before the fix the
// enclosing '(' suppressed ASI throughout, so the second statement ran into the first.

// TestASIMultilineClosureInCall parses a multi-line closure literal passed directly as
// a call argument — the canonical higher-order-function idiom.
func TestASIMultilineClosureInCall(t *testing.T) {
	parseOK(t, "fn apply(f: fn(int) -> int, x: int) -> int { return f(x) }\n"+
		"fn main() {\n"+
		"\tprint apply(fn(n: int) -> int {\n"+
		"\t\tmut acc := n\n"+
		"\t\tacc = acc * 2\n"+
		"\t\treturn acc\n"+
		"\t}, 10)\n"+
		"}\n")
}

// TestASIMultilineBlockInCall parses a multi-line block-expression passed as a call
// argument — the same ASI condition without a closure.
func TestASIMultilineBlockInCall(t *testing.T) {
	parseOK(t, "fn take(x: int) -> int { return x }\n"+
		"fn main() {\n"+
		"\tprint take({\n"+
		"\t\ta := 1\n"+
		"\t\ta + 2\n"+
		"\t})\n"+
		"}\n")
}

// TestASICommaListStillSuppressed keeps the original rule intact: inside an unclosed
// '(' with no brace, a line break does NOT separate — a call's arguments still span
// lines.
func TestASICommaListStillSuppressed(t *testing.T) {
	parseOK(t, "fn add(a: int, b: int) -> int { return a + b }\n"+
		"fn main() {\n"+
		"\tprint add(\n"+
		"\t\t1,\n"+
		"\t\t2)\n"+
		"}\n")
}
