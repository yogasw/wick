//go:build windows

package main

import "testing"

// Windows hands us one flat command-line string instead of argv, so the
// user-data dir has to be parsed out by hand — quoted when the path contains
// spaces, bare otherwise, and always followed by more flags.
func TestExtractUserDataDir(t *testing.T) {
	const root = `C:\wick`
	cases := []struct {
		name    string
		cmdline string
		want    string
		wantOK  bool
	}{
		{
			name:    "bare path followed by another flag",
			cmdline: `chrome.exe --user-data-dir=C:\wick\prof-1 --headless=new`,
			want:    `C:\wick\prof-1`,
			wantOK:  true,
		},
		{
			name:    "quoted path containing spaces",
			cmdline: `chrome.exe --user-data-dir="C:\wick\my prof" --no-sandbox`,
			want:    `C:\wick\my prof`,
			wantOK:  true,
		},
		{
			name:    "flag at end of line",
			cmdline: `chrome.exe --user-data-dir=C:\wick\prof-2`,
			want:    `C:\wick\prof-2`,
			wantOK:  true,
		},
		{
			name:    "no matching flag",
			cmdline: `chrome.exe --headless=new`,
			wantOK:  false,
		},
		{
			// The safety property: a browser the user launched themselves has
			// its profile elsewhere and must never be claimed as ours.
			name:    "user's own browser outside the session dir",
			cmdline: `chrome.exe --user-data-dir=C:\Users\me\AppData\Local\Chrome`,
			wantOK:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := extractUserDataDir(tc.cmdline, root)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Fatalf("dir = %q, want %q", got, tc.want)
			}
		})
	}
}
