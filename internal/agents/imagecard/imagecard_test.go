package imagecard

import "testing"

func TestSplitFence(t *testing.T) {
	in := "here you go\n```imagecard\nhttps://example.com/a.gif | cat\nnot a url\nhttps://example.com/b.png\n```\nbye"
	text, imgs := Split(in)
	if text != "here you go\nbye" {
		t.Fatalf("text = %q", text)
	}
	if len(imgs) != 2 || imgs[0].URL != "https://example.com/a.gif" || imgs[0].Caption != "cat" || imgs[1].Caption != "" {
		t.Fatalf("imgs = %+v", imgs)
	}
}

func TestSplitMarkdownLine(t *testing.T) {
	text, imgs := Split("look\n  ![a dog](https://example.com/d.gif)  \nend")
	if text != "look\nend" || len(imgs) != 1 || imgs[0].Caption != "a dog" {
		t.Fatalf("text=%q imgs=%+v", text, imgs)
	}
}

func TestSplitLeavesOtherContent(t *testing.T) {
	cases := []string{
		"inline ![x](https://example.com/x.png) stays",
		"```md\n![x](https://example.com/x.png)\n```",
		"```imagecard\nnothing here\n```",
		"```imagecard\nhttps://example.com/x.png",
		"![x](ftp://example.com/x.png)",
		"plain text only",
	}
	for _, in := range cases {
		text, imgs := Split(in)
		if text != in || imgs != nil {
			t.Errorf("Split(%q) = %q, %+v; want unchanged", in, text, imgs)
		}
	}
}

func TestSplitOnlyImage(t *testing.T) {
	text, imgs := Split("```imagecard\nhttps://example.com/a.gif\n```")
	if text != "" || len(imgs) != 1 {
		t.Fatalf("text=%q imgs=%+v", text, imgs)
	}
}

func TestPlainText(t *testing.T) {
	got := PlainText([]Image{{URL: "https://example.com/a.gif", Caption: "cat"}, {URL: "https://example.com/b.gif"}})
	if got != "cat: https://example.com/a.gif\nhttps://example.com/b.gif" {
		t.Fatalf("got %q", got)
	}
}
