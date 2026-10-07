package ui

import (
	"context"
	"strings"
	"testing"
)

func TestHTMLScaleAttrs(t *testing.T) {
	if a := htmlScaleAttrs(context.Background()); len(a) != 0 {
		t.Fatalf("guest got %v, want no style (CSS default)", a)
	}
	a := htmlScaleAttrs(WithUIScale(context.Background(), 85))
	if a["style"] != "font-size: 85%" {
		t.Fatalf("style = %v", a["style"])
	}
}

func TestLayoutCarriesUIScale(t *testing.T) {
	var b strings.Builder
	if err := Layout("x").Render(WithUIScale(context.Background(), 85), &b); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `style="font-size: 85%"`) {
		t.Fatalf("<html> lacks the scale: %.300s", b.String())
	}
}
