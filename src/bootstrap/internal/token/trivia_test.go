package token

import "testing"

func TestTriviaKindString(t *testing.T) {
	cases := []struct {
		kind TriviaKind
		want string
	}{
		{LineComment, "LineComment"},
		{DocComment, "DocComment"},
		{BlankLine, "BlankLine"},
		{TriviaKind(99), "TriviaKind(?)"},
	}
	for _, tc := range cases {
		if got := tc.kind.String(); got != tc.want {
			t.Errorf("TriviaKind(%d).String() = %q, want %q", int(tc.kind), got, tc.want)
		}
	}
}
