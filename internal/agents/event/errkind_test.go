package event

import (
	"encoding/json"
	"testing"
	"time"
)

func TestClassifyError(t *testing.T) {
	cases := []struct {
		msg  string
		want ErrKind
	}{
		{"You've hit your session limit · resets 4:40pm (Asia/Jakarta)", ErrLimit},
		{"You've hit your weekly limit · resets Oct 14, 9am (Asia/Jakarta)", ErrLimit},
		{"Claude AI usage limit reached|1760000000", ErrLimit},
		{"429 Too Many Requests", ErrLimit},
		{"You've hit your usage limit. Upgrade to Pro or try again later.", ErrLimit},
		{"Invalid API key · Please run /login", ErrAuth},
		{`API Error: 401 {"type":"error","error":{"type":"authentication_error","message":"OAuth token has expired. Please obtain a new token or refresh your existing token."}}`, ErrAuth},
		{"unexpected status 403 Forbidden", ErrAuth},
		{"Not logged in · Please run /login", ErrAuth},
		{"OAuth token revoked · Please run /login", ErrAuth},
		{`API Error: 529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, ErrUnavailable},
		{"503 Service Unavailable", ErrUnavailable},
		{"unexpected status 502 Bad Gateway", ErrUnavailable},
		{"tool failed: file not found", ErrNone},
		{"used 4010 tokens, 5000 left", ErrNone},
		{"used 4290 tokens", ErrNone},
		{"prompt is 14290 tokens long", ErrNone},
		{"status 429: slow down", ErrLimit},
		{"error_during_execution", ErrNone},
		// A status code only counts as the response's status, not as any
		// number in the body.
		{`API Error: 400 {"type":"error","error":{"message":"max_tokens: 500 > 200 allowed"}}`, ErrNone},
		{"invalid request: max_tokens: 500, retry with 401 tokens", ErrNone},
		{"context is 5030 tokens, 502 over budget", ErrNone},
		{"401 Unauthorized", ErrAuth},
		{"HTTP 401", ErrAuth},
		{"  500 internal error", ErrUnavailable},
		{"HTTP/1.1 503", ErrUnavailable},
		{"status 500", ErrUnavailable},
		{"stream error: exceeded retry limit, last status: 504", ErrUnavailable},
		{`{"statusCode":502,"message":"upstream"}`, ErrUnavailable},
		{"error code 529", ErrUnavailable},
		{"provider returned 500 error", ErrUnavailable},
		{"", ErrNone},
	}
	for _, c := range cases {
		if got := ClassifyError(c.msg); got != c.want {
			t.Errorf("ClassifyError(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}

func TestParseResetAt(t *testing.T) {
	jkt, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Skipf("no tzdata: %v", err)
	}
	at := func(y int, mo time.Month, d, h, mi int, loc *time.Location) time.Time {
		return time.Date(y, mo, d, h, mi, 0, 0, loc)
	}
	nowJKT := at(2026, 10, 13, 15, 0, jkt)
	cases := []struct {
		name string
		msg  string
		now  time.Time
		want time.Time
		ok   bool
	}{
		{"later today", "You've hit your session limit · resets 4:40pm (Asia/Jakarta)", nowJKT, at(2026, 10, 13, 16, 40, jkt), true},
		{"already past → tomorrow", "You've hit your session limit · resets 4:40pm (Asia/Jakarta)", at(2026, 10, 13, 17, 0, jkt), at(2026, 10, 14, 16, 40, jkt), true},
		{"equal to now → tomorrow", "resets 3pm (Asia/Jakarta)", nowJKT, at(2026, 10, 14, 15, 0, jkt), true},
		{"across midnight", "You've hit your session limit · resets 1am (Asia/Jakarta)", at(2026, 10, 13, 23, 30, jkt), at(2026, 10, 14, 1, 0, jkt), true},
		{"now in another zone", "resets 4:40pm (Asia/Jakarta)", nowJKT.UTC(), at(2026, 10, 13, 16, 40, jkt), true},
		{"dated weekly", "You've hit your weekly limit · resets Oct 14, 9am (Asia/Jakarta)", nowJKT, at(2026, 10, 14, 9, 0, jkt), true},
		{"full month name", "resets October 15, 9:30am (Asia/Jakarta)", nowJKT, at(2026, 10, 15, 9, 30, jkt), true},
		{"dated just passed stays passed", "resets Oct 13, 2:59pm (Asia/Jakarta)", nowJKT, at(2026, 10, 13, 14, 59, jkt), true},
		{"dated days behind stays passed", "resets Oct 10, 9am (Asia/Jakarta)", nowJKT, at(2026, 10, 10, 9, 0, jkt), true},
		{"dated Dec → Jan rollover", "resets Jan 2, 9am (Asia/Jakarta)", at(2026, 12, 30, 15, 0, jkt), at(2027, 1, 2, 9, 0, jkt), true},
		{"dated far behind → next year, clamped", "resets Aug 1, 9am (Asia/Jakarta)", nowJKT, nowJKT.Add(7 * 24 * time.Hour), true},
		{"dated far ahead clamps", "resets Dec 25, 9am (Asia/Jakarta)", nowJKT, nowJKT.Add(7 * 24 * time.Hour), true},
		{"midnight 12am", "resets 12am (Asia/Jakarta)", nowJKT, at(2026, 10, 14, 0, 0, jkt), true},
		{"noon 12pm", "resets 12pm (Asia/Jakarta)", at(2026, 10, 13, 9, 0, jkt), at(2026, 10, 13, 12, 0, jkt), true},
		{"24h clock", "resets 16:40 (Asia/Jakarta)", nowJKT, at(2026, 10, 13, 16, 40, jkt), true},
		{"no zone → server local", "resets 4:40pm", at(2026, 10, 13, 10, 0, time.Local), at(2026, 10, 13, 16, 40, time.Local), true},
		{"unknown zone → server local", "resets 4:40pm (Mars/Olympus)", at(2026, 10, 13, 10, 0, time.Local), at(2026, 10, 13, 16, 40, time.Local), true},
		{"no reset", "You've hit your session limit", nowJKT, time.Time{}, false},
		{"garbage", "resets soon, maybe", nowJKT, time.Time{}, false},
		{"bare number", "resets 5 (Asia/Jakarta)", nowJKT, time.Time{}, false},
		{"bad hour", "resets 13pm (Asia/Jakarta)", nowJKT, time.Time{}, false},
		{"bad minute", "resets 4:75pm (Asia/Jakarta)", nowJKT, time.Time{}, false},
		{"bad month", "resets Foo 14, 9am", nowJKT, time.Time{}, false},
		{"empty", "", nowJKT, time.Time{}, false},
	}
	for _, c := range cases {
		got, ok := ParseResetAt(c.msg, c.now)
		if ok != c.ok || !got.Equal(c.want) {
			t.Errorf("%s: ParseResetAt(%q) = %v, %v; want %v, %v", c.name, c.msg, got, ok, c.want, c.ok)
		}
	}
}

// pinNow fixes the clock classifyErr reads reset times against.
func pinNow(t *testing.T, now time.Time) {
	t.Helper()
	old := nowFunc
	nowFunc = func() time.Time { return now }
	t.Cleanup(func() { nowFunc = old })
}

func parseLines(t *testing.T, p Parser, lines ...string) []AgentEvent {
	t.Helper()
	var out []AgentEvent
	for _, l := range lines {
		ev, err := p.Parse(l)
		if err != nil {
			t.Fatalf("parse %q: %v", l, err)
		}
		out = append(out, ev)
	}
	return out
}

const (
	claudeLimitText   = "You've hit your session limit · resets 4:40pm (Asia/Jakarta)"
	claudeLimitFrame  = `{"type":"assistant","message":{"id":"m1","model":"<synthetic>","role":"assistant","type":"message","content":[{"type":"text","text":"You've hit your session limit · resets 4:40pm (Asia/Jakarta)"}],"usage":{"input_tokens":0,"output_tokens":0}},"session_id":"s1"}`
	claudeLimitResult = `{"type":"result","subtype":"success","is_error":true,"duration_ms":120,"num_turns":1,"result":"You've hit your session limit · resets 4:40pm (Asia/Jakarta)","session_id":"s1"}`
)

// TestClaudeLimitTwoFrames: claude reports a limit as its own assistant
// message (model "<synthetic>") and then an error result. The first is
// text the user should see but not output the model made; the second
// ends the turn as an ErrLimit with the reset time.
func TestClaudeLimitTwoFrames(t *testing.T) {
	jkt, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Skipf("no tzdata: %v", err)
	}
	pinNow(t, time.Date(2026, 10, 13, 15, 0, 0, 0, jkt))
	want := time.Date(2026, 10, 13, 16, 40, 0, 0, jkt)

	evs := parseLines(t, NewClaudeParser(), claudeLimitFrame, claudeLimitResult)
	txt, end := evs[0], evs[1]
	if txt.Type != TextDelta || txt.Text != claudeLimitText || !txt.Synthetic || txt.ErrKind != ErrLimit || !txt.ResetAt.Equal(want) {
		t.Fatalf("notice = %+v", txt)
	}
	if end.Type != Error || end.ErrKind != ErrLimit || !end.ResetAt.Equal(want) || end.Synthetic {
		t.Fatalf("result = %+v", end)
	}
}

// TestClaudeLimitAfterStreamedText: a limit hit mid-turn, after text was
// already streamed as deltas, still surfaces — the synthetic frame was
// never streamed, so the partial-text dedup must not swallow it.
func TestClaudeLimitAfterStreamedText(t *testing.T) {
	evs := parseLines(t, NewClaudeParser(),
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Working on it"}}}`,
		claudeLimitFrame,
		`{"type":"result","subtype":"error_during_execution","is_error":true,"session_id":"s1"}`,
	)
	if evs[0].Type != TextDelta || evs[0].Synthetic {
		t.Fatalf("model text = %+v", evs[0])
	}
	if evs[1].Type != TextDelta || !evs[1].Synthetic || evs[1].ErrKind != ErrLimit {
		t.Fatalf("notice = %+v", evs[1])
	}
	// The result says only "error_during_execution": the kind comes from
	// the notice before it.
	if evs[2].Type != Error || evs[2].ErrKind != ErrLimit || evs[2].ResetAt.IsZero() {
		t.Fatalf("result = %+v", evs[2])
	}
}

// TestClaudeSyntheticByText: without the "<synthetic>" model id the notice
// is still recognized by claude's own wording.
func TestClaudeSyntheticByText(t *testing.T) {
	evs := parseLines(t, NewClaudeParser(),
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Invalid API key · Please run /login"}]}}`,
		`{"type":"result","subtype":"success","is_error":true,"result":"Invalid API key · Please run /login"}`,
	)
	if !evs[0].Synthetic || evs[0].ErrKind != ErrAuth {
		t.Fatalf("notice = %+v", evs[0])
	}
	if evs[1].Type != Error || evs[1].ErrKind != ErrAuth {
		t.Fatalf("result = %+v", evs[1])
	}
}

// TestClaudeModelReplyMentioning429IsNotSynthetic: an ordinary answer that
// talks about rate limits is the model's output, and a successful turn
// carries no ErrKind.
func TestClaudeModelReplyMentioning429IsNotSynthetic(t *testing.T) {
	evs := parseLines(t, NewClaudeParser(),
		`{"type":"assistant","message":{"model":"claude-opus-4","role":"assistant","content":[{"type":"text","text":"A 429 means too many requests: you hit the rate limit."}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"A 429 means too many requests: you hit the rate limit."}`,
	)
	if evs[0].Type != TextDelta || evs[0].Synthetic || evs[0].ErrKind != ErrNone {
		t.Fatalf("reply = %+v", evs[0])
	}
	if evs[1].Type != Done || evs[1].ErrKind != ErrNone {
		t.Fatalf("done = %+v", evs[1])
	}
}

// TestClaudeErrorKindResetsPerTurn: a limit in one turn does not leak into
// the next turn's plain error.
func TestClaudeErrorKindResetsPerTurn(t *testing.T) {
	evs := parseLines(t, NewClaudeParser(),
		claudeLimitFrame, claudeLimitResult,
		`{"type":"result","subtype":"error_during_execution","is_error":true}`,
	)
	if evs[2].Type != Error || evs[2].ErrKind != ErrNone {
		t.Fatalf("second turn = %+v", evs[2])
	}
}

func TestClaudeOverloadedIsUnavailable(t *testing.T) {
	msg := `API Error: 529 {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}`
	evs := parseLines(t, NewClaudeParser(),
		`{"type":"assistant","message":{"model":"<synthetic>","role":"assistant","content":[{"type":"text","text":"`+msg+`"}]}}`,
		`{"type":"result","subtype":"success","is_error":true,"result":"`+msg+`"}`,
	)
	if !evs[0].Synthetic || evs[0].ErrKind != ErrUnavailable || evs[1].ErrKind != ErrUnavailable {
		t.Fatalf("got %+v / %+v", evs[0], evs[1])
	}
}

// TestCodexReconnectIsNotAccountError: codex's own retry notices arrive as
// top-level errors; only the terminal turn.failed says what the account
// hit.
func TestCodexReconnectIsNotAccountError(t *testing.T) {
	evs := parseLines(t, NewCodexParser(),
		`{"type":"error","message":"Reconnecting... 1/5 (stream disconnected before completion: 503 Service Unavailable)"}`,
		`{"type":"error","message":"You've hit your usage limit. Upgrade to Pro (https://chatgpt.com/explore/pro), or try again in 2 days 3 hours."}`,
		`{"type":"turn.failed","error":{"message":"You've hit your usage limit. Upgrade to Pro (https://chatgpt.com/explore/pro), or try again in 2 days 3 hours."}}`,
	)
	if evs[0].Type != Error || evs[0].ErrKind != ErrNone {
		t.Fatalf("reconnect = %+v", evs[0])
	}
	if evs[1].ErrKind != ErrLimit || evs[2].Type != Error || evs[2].ErrKind != ErrLimit {
		t.Fatalf("limit = %+v / %+v", evs[1], evs[2])
	}
}

func TestCodexTurnFailedKinds(t *testing.T) {
	cases := map[string]ErrKind{
		"unexpected status 401 Unauthorized: Missing bearer or basic authentication in header": ErrAuth,
		"unexpected status 403 Forbidden":                           ErrAuth,
		"stream error: unexpected status 500 Internal Server Error": ErrUnavailable,
		"model gpt-x does not exist":                                ErrNone,
	}
	for msg, want := range cases {
		ev, _ := NewCodexParser().Parse(`{"type":"turn.failed","error":{"message":"` + msg + `"}}`)
		if ev.Type != Error || ev.ErrKind != want {
			t.Errorf("%q: got %v/%v, want Error/%v", msg, ev.Type, ev.ErrKind, want)
		}
	}
}

func TestOMPErrorKind(t *testing.T) {
	p := NewOMPParser("omp-work")
	evs := parseLines(t, p,
		`{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"429 You have hit your usage limit"}}`,
		`{"type":"agent_end","messages":[]}`,
	)
	if last := evs[1]; last.Type != Error || last.ErrKind != ErrLimit {
		t.Fatalf("limit = %+v", last)
	}
	evs = parseLines(t, p,
		`{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"401 invalid x-api-key"}}`,
		`{"type":"agent_end","messages":[]}`,
	)
	if last := evs[1]; last.Type != Error || last.ErrKind != ErrAuth {
		t.Fatalf("auth = %+v", last)
	}
}

func TestOpencodeErrorKind(t *testing.T) {
	cases := map[string]ErrKind{
		"Rate limit exceeded":    ErrLimit,
		"Invalid API key":        ErrAuth,
		"Overloaded":             ErrUnavailable,
		"Model not found: foo/x": ErrNone,
	}
	for msg, want := range cases {
		evs, _ := NewOpencodeParser("oc").ParseAll(`{"type":"error","sessionID":"s","error":{"name":"APIError","data":{"message":"` + msg + `"}}}`)
		last := evs[len(evs)-1]
		if last.Type != Error || last.ErrKind != want {
			t.Errorf("%q: got %v/%v, want Error/%v", msg, last.Type, last.ErrKind, want)
		}
	}
}

// claudeSyntheticSamples are the nine distinct notices claude wrote itself
// in real CLI transcripts (artifacts/claude-synthetic-samples.json): code,
// isApiErrorMessage and text verbatim. The expectations are read against
// 2026-10-13 15:00 Asia/Jakarta.
var claudeSyntheticSamples = []struct {
	code  string // "" = null
	isAPI bool
	text  string
	kind  ErrKind
	reset string // "2006-01-02 15:04" Asia/Jakarta, "" = none
}{
	{"rate_limit", true, "You've hit your session limit · resets 11:40am (Asia/Jakarta)", ErrLimit, "2026-10-14 11:40"},
	{"rate_limit", true, "You've hit your session limit · resets 4:40pm (Asia/Jakarta)", ErrLimit, "2026-10-13 16:40"},
	{"rate_limit", true, "You've hit your session limit · resets 10:20pm (Asia/Jakarta)", ErrLimit, "2026-10-13 22:20"},
	{"", false, "No response requested.", ErrNone, ""},
	{"invalid_request", true, "API Error: an image in the conversation could not be processed and was removed. Re-read the file with a different approach if you still need it.", ErrNone, ""},
	{"rate_limit", true, "You've hit your individual spend limit · run /usage-credits to ask your admin for a higher limit · your session limit resets 8:50pm (Asia/Jakarta)", ErrLimit, "2026-10-13 20:50"},
	{"oauth_org_not_allowed", true, "Your organization has disabled Claude subscription access for Claude Code · Use an Anthropic API key instead, or ask your admin to enable access", ErrAuth, ""},
	{"rate_limit", true, "You've hit your session limit · resets 9:40pm (Asia/Jakarta)", ErrLimit, "2026-10-13 21:40"},
	{"authentication_failed", true, "Failed to authenticate: OAuth session expired and could not be refreshed", ErrAuth, ""},
}

func claudeSyntheticFrame(t *testing.T, code string, isAPI bool, text string, onMessage bool) string {
	t.Helper()
	var codeJSON any
	if code != "" {
		codeJSON = code
	}
	msg := map[string]any{"id": "m1", "model": "<synthetic>", "role": "assistant", "type": "message",
		"content": []any{map[string]any{"type": "text", "text": text}}}
	line := map[string]any{"type": "assistant", "session_id": "s1", "parent_tool_use_id": nil, "message": msg}
	if onMessage {
		msg["error"] = codeJSON
	} else {
		line["error"] = codeJSON
		line["isApiErrorMessage"] = isAPI
	}
	b, err := json.Marshal(line)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestClaudeSyntheticSamples: every real notice is Synthetic, its kind
// comes from claude's own code (invalid_request and "No response
// requested." are not account errors), the reset is read from the text,
// and the error result after it carries the same kind — whether the code
// sits on the line or on the message.
func TestClaudeSyntheticSamples(t *testing.T) {
	jkt, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Skipf("no tzdata: %v", err)
	}
	pinNow(t, time.Date(2026, 10, 13, 15, 0, 0, 0, jkt))
	for _, onMessage := range []bool{false, true} {
		for _, c := range claudeSyntheticSamples {
			var want time.Time
			if c.reset != "" {
				want, _ = time.ParseInLocation("2006-01-02 15:04", c.reset, jkt)
			}
			result, _ := json.Marshal(map[string]any{"type": "result", "subtype": "success", "is_error": true, "result": c.text})
			evs := parseLines(t, NewClaudeParser(), claudeSyntheticFrame(t, c.code, c.isAPI, c.text, onMessage), string(result))
			n := evs[0]
			if n.Type != TextDelta || n.Text != c.text || !n.Synthetic || n.ErrKind != c.kind || !n.ResetAt.Equal(want) {
				t.Errorf("onMessage=%v %q: notice = %v/%v/%v/%v, want TextDelta/synthetic/%v/%v", onMessage, c.text, n.Type, n.Synthetic, n.ErrKind, n.ResetAt, c.kind, want)
			}
			if e := evs[1]; e.Type != Error || e.ErrKind != c.kind || !e.ResetAt.Equal(want) {
				t.Errorf("onMessage=%v %q: result = %v/%v/%v, want Error/%v/%v", onMessage, c.text, e.Type, e.ErrKind, e.ResetAt, c.kind, want)
			}
		}
	}
}

// TestClassifyErrorSampleTexts: the same notices classified by wording
// alone — the fallback for a CLI that sends no code.
func TestClassifyErrorSampleTexts(t *testing.T) {
	for _, c := range claudeSyntheticSamples {
		if got := ClassifyError(c.text); got != c.kind {
			t.Errorf("ClassifyError(%q) = %v, want %v", c.text, got, c.kind)
		}
	}
}

func TestParseResetAtRetryIn(t *testing.T) {
	now := time.Date(2026, 10, 13, 15, 0, 0, 0, time.UTC)
	cases := []struct {
		msg  string
		want time.Duration
		ok   bool
	}{
		{"You've hit your usage limit. Upgrade to Pro (https://chatgpt.com/explore/pro), or try again in 2 days 3 hours.", 51 * time.Hour, true},
		{"You've hit your usage limit. Try again in 45 minutes.", 45 * time.Minute, true},
		{"try again in 1 hour, 30 mins", 90 * time.Minute, true},
		{"try again in 9 days", 7 * 24 * time.Hour, true},
		{"try again in a bit", 0, false},
		{"try again later", 0, false},
	}
	for _, c := range cases {
		got, ok := ParseResetAt(c.msg, now)
		if ok != c.ok || (ok && !got.Equal(now.Add(c.want))) {
			t.Errorf("ParseResetAt(%q) = %v, %v; want now+%v, %v", c.msg, got, ok, c.want, c.ok)
		}
	}
}

// TestClaudeModelReplyWithNoticeWordingIsNotSynthetic: a short, un-streamed
// reply from a real model that happens to use claude's notice wording is
// still the model's output, and the successful turn carries no ErrKind.
func TestClaudeModelReplyWithNoticeWordingIsNotSynthetic(t *testing.T) {
	for _, text := range []string{
		"API Error: 401 waktu panggil GitHub, token kamu expired",
		"You've hit your free tier quota on Vercel",
	} {
		reply, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{
			"model": "claude-opus-4", "role": "assistant",
			"content": []any{map[string]any{"type": "text", "text": text}}}})
		evs := parseLines(t, NewClaudeParser(), string(reply),
			`{"type":"result","subtype":"success","is_error":false,"result":"ok"}`)
		if evs[0].Type != TextDelta || evs[0].Synthetic || evs[0].ErrKind != ErrNone {
			t.Errorf("%q: reply = %+v", text, evs[0])
		}
		if evs[1].Type != Done || evs[1].ErrKind != ErrNone {
			t.Errorf("%q: done = %+v", text, evs[1])
		}
	}
}

// TestClaudeInvalidRequestDoesNotMaskTurnError: claude's invalid_request
// notice (an unreadable image) does not end the turn; when the turn later
// fails on an overload, the result's own wording decides.
func TestClaudeInvalidRequestDoesNotMaskTurnError(t *testing.T) {
	evs := parseLines(t, NewClaudeParser(),
		claudeSyntheticFrame(t, "invalid_request", true, claudeSyntheticSamples[4].text, false),
		`{"type":"result","subtype":"success","is_error":true,"result":"API Error: 529 {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}"}`,
	)
	if !evs[0].Synthetic || evs[0].ErrKind != ErrNone {
		t.Fatalf("notice = %+v", evs[0])
	}
	if evs[1].Type != Error || evs[1].ErrKind != ErrUnavailable || !evs[1].ResetAt.IsZero() {
		t.Fatalf("result = %+v", evs[1])
	}
}
