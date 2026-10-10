package scm

import "testing"

func TestReadOnlyIndexCmd(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"status", "--porcelain=v2"}, true},
		{[]string{"diff", "--cached"}, true},
		{[]string{"-c", "core.quotepath=off", "status"}, true},
		{[]string{"-C", "/repo", "diff"}, true},
		{[]string{"--no-pager", "diff", "HEAD"}, true},
		{[]string{"-c", "status", "add", "."}, false},
		{[]string{"add", "--", "status"}, false},
		{[]string{"commit", "-m", "diff"}, false},
		{nil, false},
	}
	for _, c := range cases {
		if got := readOnlyIndexCmd(c.args); got != c.want {
			t.Errorf("readOnlyIndexCmd(%q) = %v, want %v", c.args, got, c.want)
		}
	}
}
