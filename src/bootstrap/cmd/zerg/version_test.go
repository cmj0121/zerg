package main

import "testing"

// TestScanRequires exercises the comment scanner against the layouts examples
// in the repo actually use (shebang + marker on line 2, marker on line 1
// without shebang, no marker, malformed marker, marker after first code line).
func TestScanRequires(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		wantOK    bool
		wantMajor int
		wantMinor int
	}{
		{
			name:      "shebang then marker",
			src:       "#! /usr/bin/env zerg\n# requires: v0.2\nprint 1\n",
			wantOK:    true,
			wantMajor: 0,
			wantMinor: 2,
		},
		{
			name:      "marker on line 1",
			src:       "# requires: v0.5\nprint 1\n",
			wantOK:    true,
			wantMajor: 0,
			wantMinor: 5,
		},
		{
			name:      "marker after blank line",
			src:       "\n# requires: v0.10\nprint 1\n",
			wantOK:    true,
			wantMajor: 0,
			wantMinor: 10,
		},
		{
			name:      "marker after a plain comment",
			src:       "# header\n# requires: v0.4\nprint 1\n",
			wantOK:    true,
			wantMajor: 0,
			wantMinor: 4,
		},
		{
			name:   "no marker",
			src:    "#! /usr/bin/env zerg\n# header\nprint 1\n",
			wantOK: false,
		},
		{
			name:   "marker buried after code is ignored",
			src:    "print 1\n# requires: v0.9\n",
			wantOK: false,
		},
		{
			name:   "malformed marker (no v prefix)",
			src:    "# requires: 0.2\nprint 1\n",
			wantOK: false,
		},
		{
			name:   "empty source",
			src:    "",
			wantOK: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			gotMaj, gotMin, gotOK := scanRequires([]byte(tc.src))
			if gotOK != tc.wantOK {
				t.Fatalf("ok = %v, want %v", gotOK, tc.wantOK)
			}
			if !gotOK {
				return
			}
			if gotMaj != tc.wantMajor || gotMin != tc.wantMinor {
				t.Fatalf("got v%d.%d, want v%d.%d",
					gotMaj, gotMin, tc.wantMajor, tc.wantMinor)
			}
		})
	}
}

// TestVersionLess pins the (major, minor) ordering used by the gate.
func TestVersionLess(t *testing.T) {
	cases := []struct {
		am, an, bm, bn int
		want           bool
	}{
		{0, 1, 0, 2, true},  // file requires future minor
		{0, 1, 1, 0, true},  // file requires future major
		{0, 2, 0, 1, false}, // file requires past minor
		{0, 1, 0, 1, false}, // exact match → not less
		{1, 0, 0, 9, false}, // major dominates minor
		{0, 9, 0, 10, true}, // minor compares numerically, not lexically
	}
	for _, tc := range cases {
		got := versionLess(tc.am, tc.an, tc.bm, tc.bn)
		if got != tc.want {
			t.Errorf("versionLess(%d.%d, %d.%d) = %v, want %v",
				tc.am, tc.an, tc.bm, tc.bn, got, tc.want)
		}
	}
}
