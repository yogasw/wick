package team

import (
	"errors"
	"testing"
)

func TestNormalizeSuggestedPrompts(t *testing.T) {
	got, err := NormalizeSuggestedPrompts([]SuggestedPrompt{
		{Title: "  Recap  ", Message: ""}, {Title: "", Message: ""}, {Title: "", Message: "Check logs"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (SuggestedPrompt{"Recap", "Recap"}) || got[1] != (SuggestedPrompt{"Check logs", "Check logs"}) {
		t.Fatalf("got %+v", got)
	}
	five := make([]SuggestedPrompt, 5)
	for i := range five {
		five[i] = SuggestedPrompt{Title: "t", Message: "m"}
	}
	if _, err := NormalizeSuggestedPrompts(five); !errors.Is(err, ErrTooManyPrompts) {
		t.Fatalf("err = %v, want ErrTooManyPrompts", err)
	}
	if out := DecodeSuggestedPrompts(EncodeSuggestedPrompts(got)); len(out) != 2 {
		t.Fatalf("round trip = %+v", out)
	}
	if out := DecodeSuggestedPrompts("garbage"); len(out) != 0 {
		t.Fatal("garbage must decode to none")
	}
}
