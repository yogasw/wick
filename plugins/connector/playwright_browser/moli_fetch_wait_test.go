package main

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
)

func TestThinPage(t *testing.T) {
	if !thinPage("") || !thinPage("[Home](/) [Next](/b)") {
		t.Fatal("an empty or nav-only dump is thin")
	}
	if thinPage(strings.Repeat("word ", 60)) {
		t.Fatal("60 words is not thin")
	}
}

func TestMoliFetchArgsWait(t *testing.T) {
	c := connector.NewCtx(context.Background(), "inst", map[string]string{"browser": "moli"}, nil, nil, nil, nil)
	got := moliFetchArgs(c, getMarkdownInput{URL: "https://example.com", Format: "markdown", Wait: "networkidle"})
	if i := slices.Index(got, "--wait"); i < 0 || got[i+1] != "networkidle" {
		t.Fatalf("wait not passed: %v", got)
	}
	got = moliFetchArgs(c, getMarkdownInput{URL: "https://example.com", Format: "markdown"})
	if i := slices.Index(got, "--wait"); i < 0 || got[i+1] != "done" {
		t.Fatalf("empty wait should default to done: %v", got)
	}
}

func TestParseGetMarkdownWait(t *testing.T) {
	cfg := map[string]string{"browser": "moli"}
	c := connector.NewCtx(context.Background(), "inst", cfg, map[string]string{"url": "https://example.com"}, nil, nil, nil)
	in, err := parseGetMarkdown(c)
	if err != nil || in.Wait != "done" {
		t.Fatalf("default wait: %+v %v", in, err)
	}
	c = connector.NewCtx(context.Background(), "inst", cfg, map[string]string{"url": "https://example.com", "wait": "soon"}, nil, nil, nil)
	if _, err := parseGetMarkdown(c); err == nil {
		t.Fatal("an unknown wait is refused")
	}
}
