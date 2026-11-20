package codemode

import "testing"

func TestCompactCalls(t *testing.T) {
	calls := []Call{
		{ToolID: "file:exists a", OK: true, MS: 1},
		{ToolID: "conn:x/get", OK: true, MS: 100},
		{ToolID: "conn:x/get", OK: false, MS: 40, Error: "timed out"},
		{ToolID: "conn:x/get", OK: true, MS: 200},
		{ToolID: "conn:y/list", OK: true, MS: 5},
	}
	got := CompactCalls(calls)
	if len(got) != 4 {
		t.Fatalf("want 4 entries, got %+v", got)
	}
	if got[1].ToolID != "conn:x/get" || got[1].Count != 2 || got[1].MS != 300 {
		t.Fatalf("successful calls of one tool fold with a count: %+v", got[1])
	}
	if got[2].OK || got[2].Error != "timed out" {
		t.Fatalf("a failed call stays as it is: %+v", got[2])
	}
	if got[0].Count != 0 || got[3].Count != 0 {
		t.Fatalf("a single call keeps no count: %+v", got)
	}
	if len(calls) != 5 || calls[1].Count != 0 {
		t.Fatal("the input list is not changed")
	}
}
