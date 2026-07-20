package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

// This file is the deterministic normalization layer — the thing the Notion MCP
// does behind the scenes and that makes "fetch" feel like one clean call. NO AI:
// every conversion here (property → value, rich_text → markdown, blocks →
// markdown, markdown → blocks) is pure code driven off Notion's type tags.

// --- search ---

type searchHit struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Type       string `json:"type"` // "page" | "database"
	URL        string `json:"url"`
	LastEdited string `json:"last_edited,omitempty"`
}

func searchHitFrom(raw json.RawMessage) (searchHit, bool) {
	var obj struct {
		Object         string                     `json:"object"`
		ID             string                     `json:"id"`
		URL            string                     `json:"url"`
		LastEditedTime string                     `json:"last_edited_time"`
		Properties     map[string]json.RawMessage `json:"properties"` // pages
		Title          json.RawMessage            `json:"title"`       // databases
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return searchHit{}, false
	}
	hit := searchHit{ID: obj.ID, Type: obj.Object, URL: obj.URL, LastEdited: obj.LastEditedTime}
	if obj.Object == "database" {
		hit.Title = richTextToPlain(obj.Title)
	} else {
		hit.Title = titleFromProperties(obj.Properties)
	}
	if hit.Title == "" {
		hit.Title = "(untitled)"
	}
	return hit, true
}

// --- page → record (flattened properties) ---

type pageRecord struct {
	ID         string         `json:"id"`
	URL        string         `json:"url"`
	Title      string         `json:"title"`
	Properties map[string]any `json:"properties"`
	EditedAt   string         `json:"edited_at,omitempty"`
}

func pageToRecord(raw json.RawMessage) (pageRecord, error) {
	var obj struct {
		ID             string                     `json:"id"`
		URL            string                     `json:"url"`
		LastEditedTime string                     `json:"last_edited_time"`
		Properties     map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return pageRecord{}, fmt.Errorf("decode page: %w", err)
	}
	rec := pageRecord{
		ID: obj.ID, URL: obj.URL, EditedAt: obj.LastEditedTime,
		Title:      titleFromProperties(obj.Properties),
		Properties: map[string]any{},
	}
	for name, praw := range obj.Properties {
		rec.Properties[name] = propToValue(praw)
	}
	return rec, nil
}

func titleFromProperties(props map[string]json.RawMessage) string {
	for _, praw := range props {
		var head struct {
			Type  string          `json:"type"`
			Title json.RawMessage `json:"title"`
		}
		if json.Unmarshal(praw, &head) == nil && head.Type == "title" {
			return richTextToPlain(head.Title)
		}
	}
	return ""
}

// propToValue maps one Notion property object to a clean scalar/list value,
// following the type-tag switch documented in the mapping table.
func propToValue(raw json.RawMessage) any {
	var head struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &head) != nil {
		return nil
	}
	switch head.Type {
	case "title":
		var p struct {
			Title json.RawMessage `json:"title"`
		}
		_ = json.Unmarshal(raw, &p)
		return richTextToPlain(p.Title)
	case "rich_text":
		var p struct {
			RichText json.RawMessage `json:"rich_text"`
		}
		_ = json.Unmarshal(raw, &p)
		return richTextToMarkdown(p.RichText)
	case "number":
		var p struct {
			Number *float64 `json:"number"`
		}
		_ = json.Unmarshal(raw, &p)
		return p.Number
	case "select":
		var p struct {
			Select *struct {
				Name string `json:"name"`
			} `json:"select"`
		}
		_ = json.Unmarshal(raw, &p)
		if p.Select == nil {
			return nil
		}
		return p.Select.Name
	case "status":
		var p struct {
			Status *struct {
				Name string `json:"name"`
			} `json:"status"`
		}
		_ = json.Unmarshal(raw, &p)
		if p.Status == nil {
			return nil
		}
		return p.Status.Name
	case "multi_select":
		var p struct {
			MultiSelect []struct {
				Name string `json:"name"`
			} `json:"multi_select"`
		}
		_ = json.Unmarshal(raw, &p)
		out := make([]string, 0, len(p.MultiSelect))
		for _, o := range p.MultiSelect {
			out = append(out, o.Name)
		}
		return out
	case "checkbox":
		var p struct {
			Checkbox bool `json:"checkbox"`
		}
		_ = json.Unmarshal(raw, &p)
		return p.Checkbox
	case "date":
		var p struct {
			Date *struct {
				Start string `json:"start"`
				End   string `json:"end"`
			} `json:"date"`
		}
		_ = json.Unmarshal(raw, &p)
		if p.Date == nil {
			return nil
		}
		if p.Date.End != "" {
			return map[string]string{"start": p.Date.Start, "end": p.Date.End}
		}
		return p.Date.Start
	case "url":
		var p struct {
			URL string `json:"url"`
		}
		_ = json.Unmarshal(raw, &p)
		return p.URL
	case "email":
		var p struct {
			Email string `json:"email"`
		}
		_ = json.Unmarshal(raw, &p)
		return p.Email
	case "phone_number":
		var p struct {
			Phone string `json:"phone_number"`
		}
		_ = json.Unmarshal(raw, &p)
		return p.Phone
	case "unique_id":
		var p struct {
			UniqueID struct {
				Prefix *string `json:"prefix"`
				Number int64   `json:"number"`
			} `json:"unique_id"`
		}
		_ = json.Unmarshal(raw, &p)
		prefix := ""
		if p.UniqueID.Prefix != nil {
			prefix = *p.UniqueID.Prefix
		}
		return prefix + strconv.FormatInt(p.UniqueID.Number, 10)
	case "people":
		var p struct {
			People []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"people"`
		}
		_ = json.Unmarshal(raw, &p)
		out := make([]string, 0, len(p.People))
		for _, u := range p.People {
			if u.Name != "" {
				out = append(out, u.Name)
			} else {
				out = append(out, u.ID)
			}
		}
		return out
	case "relation":
		var p struct {
			Relation []struct {
				ID string `json:"id"`
			} `json:"relation"`
		}
		_ = json.Unmarshal(raw, &p)
		out := make([]string, 0, len(p.Relation))
		for _, r := range p.Relation {
			out = append(out, r.ID)
		}
		return out
	case "files":
		var p struct {
			Files []struct {
				External struct {
					URL string `json:"url"`
				} `json:"external"`
				File struct {
					URL string `json:"url"`
				} `json:"file"`
			} `json:"files"`
		}
		_ = json.Unmarshal(raw, &p)
		out := make([]string, 0, len(p.Files))
		for _, f := range p.Files {
			if f.External.URL != "" {
				out = append(out, f.External.URL)
			} else if f.File.URL != "" {
				out = append(out, f.File.URL)
			}
		}
		return out
	case "created_time":
		var p struct {
			CreatedTime string `json:"created_time"`
		}
		_ = json.Unmarshal(raw, &p)
		return p.CreatedTime
	case "last_edited_time":
		var p struct {
			LastEditedTime string `json:"last_edited_time"`
		}
		_ = json.Unmarshal(raw, &p)
		return p.LastEditedTime
	default:
		// Formula/rollup and any newer type: return the type-tagged inner value raw.
		var m map[string]json.RawMessage
		if json.Unmarshal(raw, &m) == nil {
			if v, ok := m[head.Type]; ok {
				var parsed any
				if json.Unmarshal(v, &parsed) == nil {
					return parsed
				}
			}
		}
		return nil
	}
}

// --- database → record (schema) ---

func databaseToRecord(raw json.RawMessage) (any, error) {
	var obj struct {
		ID         string                     `json:"id"`
		URL        string                     `json:"url"`
		Title      json.RawMessage            `json:"title"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("decode database: %w", err)
	}
	schema := map[string]string{}
	for name, praw := range obj.Properties {
		var h struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(praw, &h) == nil {
			schema[name] = h.Type
		}
	}
	return map[string]any{
		"type": "database",
		"meta": map[string]any{
			"id":     obj.ID,
			"url":    obj.URL,
			"title":  richTextToPlain(obj.Title),
			"schema": schema,
		},
	}, nil
}

// --- rich_text → text / markdown ---

type richTextRun struct {
	PlainText   string `json:"plain_text"`
	Href        string `json:"href"`
	Annotations struct {
		Bold          bool `json:"bold"`
		Italic        bool `json:"italic"`
		Strikethrough bool `json:"strikethrough"`
		Code          bool `json:"code"`
	} `json:"annotations"`
}

func richTextToPlain(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var runs []richTextRun
	if json.Unmarshal(raw, &runs) != nil {
		return ""
	}
	var b strings.Builder
	for _, r := range runs {
		b.WriteString(r.PlainText)
	}
	return b.String()
}

func richTextToMarkdown(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var runs []richTextRun
	if json.Unmarshal(raw, &runs) != nil {
		return ""
	}
	var b strings.Builder
	for _, r := range runs {
		t := r.PlainText
		if t == "" {
			continue
		}
		if r.Annotations.Code {
			t = "`" + t + "`"
		}
		if r.Annotations.Bold {
			t = "**" + t + "**"
		}
		if r.Annotations.Italic {
			t = "*" + t + "*"
		}
		if r.Annotations.Strikethrough {
			t = "~~" + t + "~~"
		}
		if r.Href != "" {
			t = "[" + t + "](" + r.Href + ")"
		}
		b.WriteString(t)
	}
	return b.String()
}

// --- blocks → markdown (recursive walk of the page body) ---

// blockInfo is one entry in the opt-in blocks[] list: just enough for an agent
// to target a block for a comment or edit (id + type + a plain-text preview).
type blockInfo struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Text string `json:"text"`
}

// pageContent walks a page's block tree ONCE and returns the markdown render
// plus, when collectBlocks is set, a flat blocks[] list (id/type/text). Both
// come from the same walk so turning on blocks[] costs no extra API calls.
// Recurses into any block that has children (toggles, list nesting, columns…).
func pageContent(c *connector.Ctx, pageID string, collectBlocks bool) (string, []blockInfo, error) {
	var b strings.Builder
	var blocks []blockInfo
	var sink *[]blockInfo
	if collectBlocks {
		blocks = []blockInfo{}
		sink = &blocks
	}
	if err := walkBlocks(c, pageID, 0, &b, sink); err != nil {
		return "", nil, err
	}
	out := strings.TrimRight(b.String(), "\n")
	return out, blocks, nil
}

// walkBlocks renders markdown into b and, when sink != nil, appends a flat
// {id,type,text} entry per block. sink is threaded through recursion so nested
// blocks are collected too.
func walkBlocks(c *connector.Ctx, blockID string, depth int, b *strings.Builder, sink *[]blockInfo) error {
	cursor := ""
	for {
		path := "/blocks/" + blockID + "/children?page_size=100"
		if cursor != "" {
			path += "&start_cursor=" + cursor
		}
		raw, err := notionDo(c, http.MethodGet, path, nil)
		if err != nil {
			return err
		}
		var env struct {
			Results    []json.RawMessage `json:"results"`
			HasMore    bool              `json:"has_more"`
			NextCursor string            `json:"next_cursor"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			return fmt.Errorf("decode blocks: %w", err)
		}
		for _, r := range env.Results {
			blockToMarkdown(c, r, depth, b, sink)
		}
		if !env.HasMore || env.NextCursor == "" {
			break
		}
		cursor = env.NextCursor
	}
	return nil
}

func blockToMarkdown(c *connector.Ctx, raw json.RawMessage, depth int, b *strings.Builder, sink *[]blockInfo) {
	var head struct {
		ID          string `json:"id"`
		Type        string `json:"type"`
		HasChildren bool   `json:"has_children"`
	}
	if json.Unmarshal(raw, &head) != nil {
		return
	}
	indent := strings.Repeat("  ", depth)

	// rt pulls the rich_text of the block's own type key.
	rt := func() string {
		var m map[string]json.RawMessage
		if json.Unmarshal(raw, &m) != nil {
			return ""
		}
		inner, ok := m[head.Type]
		if !ok {
			return ""
		}
		var body struct {
			RichText json.RawMessage `json:"rich_text"`
		}
		_ = json.Unmarshal(inner, &body)
		return richTextToMarkdown(body.RichText)
	}

	// Collect the flat blocks[] entry (opt-in). Uses PLAIN text (no markdown
	// decoration) as a short preview — the ID is the point, not the formatting.
	if sink != nil {
		var m map[string]json.RawMessage
		text := ""
		if json.Unmarshal(raw, &m) == nil {
			if inner, ok := m[head.Type]; ok {
				var body struct {
					RichText json.RawMessage `json:"rich_text"`
				}
				_ = json.Unmarshal(inner, &body)
				text = richTextToPlain(body.RichText)
			}
		}
		*sink = append(*sink, blockInfo{ID: head.ID, Type: head.Type, Text: text})
	}

	switch head.Type {
	case "heading_1":
		b.WriteString(indent + "# " + rt() + "\n\n")
	case "heading_2":
		b.WriteString(indent + "## " + rt() + "\n\n")
	case "heading_3":
		b.WriteString(indent + "### " + rt() + "\n\n")
	case "paragraph":
		if t := rt(); t != "" {
			b.WriteString(indent + t + "\n\n")
		}
	case "bulleted_list_item":
		b.WriteString(indent + "- " + rt() + "\n")
	case "numbered_list_item":
		b.WriteString(indent + "1. " + rt() + "\n")
	case "to_do":
		var m map[string]json.RawMessage
		_ = json.Unmarshal(raw, &m)
		var td struct {
			Checked  bool            `json:"checked"`
			RichText json.RawMessage `json:"rich_text"`
		}
		_ = json.Unmarshal(m["to_do"], &td)
		box := "[ ]"
		if td.Checked {
			box = "[x]"
		}
		b.WriteString(indent + "- " + box + " " + richTextToMarkdown(td.RichText) + "\n")
	case "quote":
		b.WriteString(indent + "> " + rt() + "\n\n")
	case "callout":
		b.WriteString(indent + "> " + rt() + "\n\n")
	case "code":
		var m map[string]json.RawMessage
		_ = json.Unmarshal(raw, &m)
		var code struct {
			Language string          `json:"language"`
			RichText json.RawMessage `json:"rich_text"`
		}
		_ = json.Unmarshal(m["code"], &code)
		b.WriteString(indent + "```" + code.Language + "\n" + richTextToPlain(code.RichText) + "\n" + indent + "```\n\n")
	case "divider":
		b.WriteString(indent + "---\n\n")
	case "toggle":
		b.WriteString(indent + "- " + rt() + "\n")
	case "child_page":
		var m map[string]json.RawMessage
		_ = json.Unmarshal(raw, &m)
		var cp struct {
			Title string `json:"title"`
		}
		_ = json.Unmarshal(m["child_page"], &cp)
		b.WriteString(indent + "- 📄 " + cp.Title + "\n")
	case "child_database":
		var m map[string]json.RawMessage
		_ = json.Unmarshal(raw, &m)
		var cd struct {
			Title string `json:"title"`
		}
		_ = json.Unmarshal(m["child_database"], &cd)
		b.WriteString(indent + "- 🗃️ " + cd.Title + "\n")
	default:
		if t := rt(); t != "" {
			b.WriteString(indent + t + "\n\n")
		}
	}

	if head.HasChildren {
		_ = walkBlocks(c, head.ID, depth+1, b, sink)
	}
}

// --- markdown → blocks (create/append body) ---

// markdownToBlocks is the deterministic reverse converter: a small line-based
// markdown parser producing Notion block objects. Supports headings, bullet /
// numbered / to-do lists, quotes, fenced code, dividers, and paragraphs — the
// same block set blockToMarkdown emits.
func markdownToBlocks(md string) []any {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	blocks := make([]any, 0, len(lines))

	inCode := false
	codeLang := ""
	var codeBuf []string

	flushCode := func() {
		blocks = append(blocks, block("code", map[string]any{
			"language":  codeLangOrDefault(codeLang),
			"rich_text": textRuns(strings.Join(codeBuf, "\n")),
		}))
		codeBuf = nil
		codeLang = ""
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			if inCode {
				flushCode()
				inCode = false
			} else {
				inCode = true
				codeLang = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			}
			continue
		}
		if inCode {
			codeBuf = append(codeBuf, line)
			continue
		}

		switch {
		case trimmed == "":
			continue
		case trimmed == "---" || trimmed == "***":
			blocks = append(blocks, block("divider", map[string]any{}))
		case strings.HasPrefix(trimmed, "### "):
			blocks = append(blocks, richBlock("heading_3", trimmed[4:]))
		case strings.HasPrefix(trimmed, "## "):
			blocks = append(blocks, richBlock("heading_2", trimmed[3:]))
		case strings.HasPrefix(trimmed, "# "):
			blocks = append(blocks, richBlock("heading_1", trimmed[2:]))
		case strings.HasPrefix(trimmed, "> "):
			blocks = append(blocks, richBlock("quote", trimmed[2:]))
		case strings.HasPrefix(trimmed, "- [ ] "):
			blocks = append(blocks, block("to_do", map[string]any{"checked": false, "rich_text": textRuns(trimmed[6:])}))
		case strings.HasPrefix(trimmed, "- [x] "), strings.HasPrefix(trimmed, "- [X] "):
			blocks = append(blocks, block("to_do", map[string]any{"checked": true, "rich_text": textRuns(trimmed[6:])}))
		case strings.HasPrefix(trimmed, "- "), strings.HasPrefix(trimmed, "* "):
			blocks = append(blocks, richBlock("bulleted_list_item", trimmed[2:]))
		case isNumberedItem(trimmed):
			blocks = append(blocks, richBlock("numbered_list_item", stripNumberPrefix(trimmed)))
		default:
			blocks = append(blocks, richBlock("paragraph", trimmed))
		}
	}
	if inCode {
		flushCode()
	}
	return blocks
}

func block(typ string, inner map[string]any) map[string]any {
	return map[string]any{"object": "block", "type": typ, typ: inner}
}

func richBlock(typ, text string) map[string]any {
	return block(typ, map[string]any{"rich_text": textRuns(text)})
}

// textRuns emits a single plain rich-text run. Inline markdown (bold/links) in
// created content is left literal — deterministic and safe; the read path still
// renders any annotations Notion itself applies.
func textRuns(text string) []any {
	if text == "" {
		return []any{}
	}
	return []any{map[string]any{"type": "text", "text": map[string]any{"content": text}}}
}

func isNumberedItem(s string) bool {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return i > 0 && i+1 < len(s) && s[i] == '.' && s[i+1] == ' '
}

func stripNumberPrefix(s string) string {
	i := strings.Index(s, ". ")
	if i < 0 {
		return s
	}
	return s[i+2:]
}

func codeLangOrDefault(lang string) string {
	if lang == "" {
		return "plain text"
	}
	return lang
}

// --- helpers shared by service.go ---

func hasTitleProp(props map[string]any) bool {
	for _, v := range props {
		if m, ok := v.(map[string]any); ok {
			if _, has := m["title"]; has {
				return true
			}
		}
	}
	return false
}

func titleKeyFor(parentType string) string {
	if parentType == "database" {
		return "Name"
	}
	return "title"
}

// normalizeID accepts a dashed UUID, a bare 32-char id, or a Notion URL and
// returns the dashed UUID form the REST API expects. Falls back to the input.
func normalizeID(in string) string {
	s := strings.TrimSpace(in)
	if s == "" {
		return ""
	}
	// If it's a URL, the id is the trailing 32 hex chars (optionally after a dash).
	if i := strings.LastIndexAny(s, "/-"); i >= 0 && (strings.Contains(s, "notion.") || strings.Contains(s, "http")) {
		s = s[i+1:]
	}
	s = strings.ReplaceAll(s, "-", "")
	if len(s) != 32 || !isHex(s) {
		return strings.TrimSpace(in)
	}
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32]
}

func isHex(s string) bool {
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

// statusCard renders the connection-status widget: green when connected, red
// otherwise, with a single-line detail. Mirrors the loki connector's card so
// both plugins look the same in the manager.
func statusCard(ok bool, detail string) string {
	dot := `<span class="h-2 w-2 rounded-full bg-neg-400"></span>`
	ring := "border-neg-300 bg-neg-100 dark:bg-navy-800"
	text := "text-neg-400"
	if ok {
		dot = `<span class="h-2 w-2 rounded-full bg-pos-400"></span>`
		ring = "border-pos-300 bg-pos-100 dark:bg-navy-800"
		text = "text-pos-400"
	}
	return `<div class="flex items-center gap-2 rounded-lg border ` + ring + ` px-4 py-2.5">` +
		dot +
		`<span class="text-xs font-medium ` + text + `">` + detail + `</span>` +
		`</div>`
}
