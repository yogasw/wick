package main

import "testing"

func TestDeployOpIsTracked(t *testing.T) {
	mod := moduleFor(New(t.TempDir()))
	for _, op := range mod.AllOps() {
		if op.Key != "deploy" {
			continue
		}
		if op.Track == nil {
			t.Fatal("deploy op has no track spec")
		}
		if err := op.Track.Validate(); err != nil {
			t.Fatal(err)
		}
		if op.Track.Params["run_id"] != "out.run_id" || op.Track.StatusOp != "status" {
			t.Fatalf("unexpected track: %+v", op.Track)
		}
		return
	}
	t.Fatal("deploy op not found")
}
