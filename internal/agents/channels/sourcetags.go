package channels

import (
	"regexp"
	"strings"
)

// SourceNamesFn maps the handles of a session's teammates (lower case,
// no "@") to their display names. Wired by the server at boot; nil
// leaves every handle as its own name.
type SourceNamesFn func(sessionID string) map[string]string

// SourceNames resolves the names RewriteSourceTags writes.
var SourceNames SourceNamesFn

// sourceTagRe is a source tag a Team agent ends a point with: [from:@a]
// or [from:@a,@b], plus the space before it.
var sourceTagRe = regexp.MustCompile(`[ \t]*\[from:\s*(@[A-Za-z0-9][A-Za-z0-9._-]*(?:\s*,\s*@[A-Za-z0-9][A-Za-z0-9._-]*)*)\s*\]`)

// openSourceTagRe is a tag still streaming in at the end of a live reply.
var openSourceTagRe = regexp.MustCompile(`[ \t]*\[(?:f(?:r(?:o(?:m(?::[^\]\n]*)?)?)?)?)?$`)

// RewriteSourceTags turns the source tags of a reply going out to a chat
// (Slack, Telegram) into plain text: "[from:@a,@b]" becomes " (from A, B)".
// The web UI renders the tag itself; a chat must never show the raw tag.
// It resolves the roster on every call; a channel that rewrites the same
// turn many times (live edits) resolves once with SourceNamesFor and
// calls RewriteSourceTagsWith.
func RewriteSourceTags(sessionID, text string, escape func(string) string) string {
	if !strings.Contains(text, "[from:") {
		return text
	}
	return RewriteSourceTagsWith(text, SourceNamesFor(sessionID), escape)
}

// SourceNamesFor is the session's handle → name map, nil without a
// resolver.
func SourceNamesFor(sessionID string) map[string]string {
	if SourceNames == nil {
		return nil
	}
	return SourceNames(sessionID)
}

// RewriteSourceTagsWith rewrites with names already resolved. Like the
// web, a handle that is not a teammate is dropped, and a tag naming no
// teammate at all is removed whole, so a reply cannot claim a source
// that is not on the Team. escape makes a name safe for the channel's
// markup (Slack: & < > so a name cannot ping); nil leaves it as is.
// Code spans and fences are left as written.
func RewriteSourceTagsWith(text string, names map[string]string, escape func(string) string) string {
	if !strings.Contains(text, "[from:") {
		return text
	}
	return rewriteSourceTags(text, names, escape)
}

// TrimOpenSourceTag drops a source tag that has not finished streaming
// from the end of a live reply, so the raw "[from:@al" never flashes in
// the thread before the closing bracket arrives.
func TrimOpenSourceTag(text string) string {
	loc := openSourceTagRe.FindStringIndex(text)
	if loc == nil || inCode(text[:loc[0]]) {
		return text
	}
	return text[:loc[0]]
}

func rewriteSourceTags(text string, names map[string]string, escape func(string) string) string {
	return outsideCode(text, func(s string) string {
		return sourceTagRe.ReplaceAllStringFunc(s, func(tag string) string {
			m := sourceTagRe.FindStringSubmatch(tag)
			var out []string
			seen := map[string]bool{}
			for _, h := range strings.Split(m[1], ",") {
				key := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(h), "@"))
				n := strings.TrimSpace(names[key])
				if n == "" || seen[key] {
					continue
				}
				seen[key] = true
				if escape != nil {
					n = escape(n)
				}
				out = append(out, n)
			}
			if len(out) == 0 {
				return ""
			}
			return " (from " + strings.Join(out, ", ") + ")"
		})
	})
}

// fenceRe is a fence line as the web renderer reads one (common/md
// renderMarkdown): exactly ``` plus an optional language. Each one opens
// or closes a block, so both sides agree on what is code.
var fenceRe = regexp.MustCompile("^\\s*```\\w*\\s*$")

// outsideCode applies fn to the parts of a markdown text that are not
// inside a fenced block or an inline code span.
func outsideCode(text string, fn func(string) string) string {
	var b strings.Builder
	inFence := false
	for _, line := range strings.SplitAfter(text, "\n") {
		if fenceRe.MatchString(line) {
			inFence = !inFence
			b.WriteString(line)
			continue
		}
		if inFence {
			b.WriteString(line)
			continue
		}
		b.WriteString(outsideSpans(line, fn))
	}
	return b.String()
}

// outsideSpans applies fn to the parts of one line between inline code
// spans. An unclosed backtick leaves the rest of the line as text.
func outsideSpans(line string, fn func(string) string) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(line, '`')
		if i < 0 {
			b.WriteString(fn(line))
			return b.String()
		}
		n := 0
		for i+n < len(line) && line[i+n] == '`' {
			n++
		}
		ticks := line[i : i+n]
		j := strings.Index(line[i+n:], ticks)
		if j < 0 {
			b.WriteString(fn(line))
			return b.String()
		}
		b.WriteString(fn(line[:i]))
		end := i + n + j + n
		b.WriteString(line[i:end])
		line = line[end:]
	}
}

// inCode reports whether text ends inside an open fence or code span.
func inCode(text string) bool {
	const mark = "\x00"
	return strings.Contains(outsideCode(text+mark, func(s string) string { return strings.ReplaceAll(s, mark, "") }), mark)
}
