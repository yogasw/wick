// Package imagecard pulls the images an agent reply shows out of its text,
// so chat surfaces without the web UI's gallery can still display them:
// Slack posts them as image blocks (see channels/slack). Two forms count:
//
//   - an ```imagecard fence, one `url | caption | ratio | focus` per line
//     (only url and caption matter here), the same fence the web chat renders;
//   - a markdown image alone on its line: ![alt](https://…).
//
// Anything inside another code fence is left alone, and only http(s) URLs
// are taken.
package imagecard

import (
	"regexp"
	"strings"
)

// Image is one picture to show: a public URL plus an optional caption.
type Image struct {
	URL     string
	Caption string
}

// mdImage matches a line that is nothing but one markdown image.
var mdImage = regexp.MustCompile(`^!\[([^\]]*)\]\((https?://[^\s)]+)\)$`)

func isHTTP(u string) bool {
	l := strings.ToLower(u)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

// parseFence reads an imagecard fence body. Lines without an http(s) URL
// are skipped, like the web renderer does.
func parseFence(body []string) []Image {
	var out []Image
	for _, raw := range body {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "|")
		u := strings.TrimSpace(parts[0])
		if !isHTTP(u) || strings.ContainsAny(u, " \t") {
			continue
		}
		img := Image{URL: u}
		if len(parts) > 1 {
			img.Caption = strings.TrimSpace(parts[1])
		}
		out = append(out, img)
	}
	return out
}

// Split returns text with every image fence and standalone markdown image
// line removed, and the images found, in order. A fence that holds no valid
// image, or that is never closed, stays in the text as written.
func Split(text string) (string, []Image) {
	lines := strings.Split(text, "\n")
	var kept []string
	var out []Image
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "```") {
			lang := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			j := i + 1
			for ; j < len(lines) && strings.TrimSpace(lines[j]) != "```"; j++ {
			}
			if j == len(lines) {
				kept = append(kept, lines[i:]...) // unterminated: leave as is
				break
			}
			if lang == "imagecard" {
				if imgs := parseFence(lines[i+1 : j]); len(imgs) > 0 {
					out = append(out, imgs...)
					i = j
					continue
				}
			}
			kept = append(kept, lines[i:j+1]...)
			i = j
			continue
		}
		if m := mdImage.FindStringSubmatch(trimmed); m != nil {
			out = append(out, Image{URL: m[2], Caption: strings.TrimSpace(m[1])})
			continue
		}
		kept = append(kept, lines[i])
	}
	if len(out) == 0 {
		return text, nil
	}
	return strings.TrimSpace(strings.Join(kept, "\n")), out
}

// PlainText is the fallback text for a set of images: caption and URL per
// line, for notifications and clients that do not render blocks.
func PlainText(imgs []Image) string {
	var b strings.Builder
	for i, img := range imgs {
		if i > 0 {
			b.WriteString("\n")
		}
		if img.Caption != "" {
			b.WriteString(img.Caption + ": ")
		}
		b.WriteString(img.URL)
	}
	return b.String()
}
