package slack

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
)

const (
	replyTestOwner   = "U0OWNER1234"
	replyTestChannel = "D0OWNER1234"
)

func replyRequest(t *testing.T, method, channel string) *http.Request {
	t.Helper()
	form := url.Values{}
	if channel != "" {
		if method == "assistant.threads.setStatus" {
			form.Set("channel_id", channel)
			form.Set("thread_ts", "1793385566.861559")
			form.Set("status", "Thinking…")
		} else {
			form.Set("channel", channel)
		}
	}
	form.Set("text", "reply")
	req, err := http.NewRequest(http.MethodPost, "https://slack.com/api/"+method, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

type bodyRecordingRT struct {
	calls       int
	lastMethod  string
	lastChannel string
}

func (f *bodyRecordingRT) RoundTrip(req *http.Request) (*http.Response, error) {
	f.calls++
	f.lastMethod = strings.TrimPrefix(req.URL.Path, "/api/")
	body, _ := io.ReadAll(req.Body)
	_ = req.Body.Close()
	values, _ := url.ParseQuery(string(body))
	f.lastChannel = values.Get("channel")
	if f.lastChannel == "" {
		f.lastChannel = values.Get("channel_id")
	}
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

func replyRestrictedEnv(t *testing.T) {
	t.Helper()
	t.Setenv(dmOnlyUserEnv, replyTestOwner)
	t.Setenv(dmOnlyChannelEnv, replyTestChannel)
	t.Setenv(connectOnlyEnv, "false")
}

func TestSlackReplyRestrictedAllowsOwnerDMTarget(t *testing.T) {
	replyRestrictedEnv(t)
	for _, method := range []string{"chat.postMessage", "chat.update", "assistant.threads.setStatus"} {
		t.Run(method, func(t *testing.T) {
			fake := &bodyRecordingRT{}
			transport := restrictedSlackTransport{next: fake}
			resp, err := transport.RoundTrip(replyRequest(t, method, replyTestChannel))
			if resp != nil {
				resp.Body.Close()
			}
			if err != nil {
				t.Fatalf("%s blocked: %v", method, err)
			}
			if fake.calls != 1 || fake.lastMethod != method {
				t.Fatalf("calls=%d method=%q", fake.calls, fake.lastMethod)
			}
			// The wrapped transport must still receive the original form body.
			if fake.lastChannel != replyTestChannel {
				t.Fatalf("body not restored, channel=%q", fake.lastChannel)
			}
		})
	}
}

func TestSlackReplyRestrictedBlocksOtherTargetsAndWrites(t *testing.T) {
	replyRestrictedEnv(t)
	for _, tc := range []struct{ name, method, channel string }{
		{"other public channel", "chat.postMessage", "C0123456789"},
		{"other DM", "chat.postMessage", "D9876543210"},
		{"owner id as channel", "chat.postMessage", replyTestOwner},
		{"missing channel", "chat.postMessage", ""},
		{"update other channel", "chat.update", "C0123456789"},
		{"open DM", "conversations.open", ""},
		{"react", "reactions.add", replyTestChannel},
		{"assistant status other channel", "assistant.threads.setStatus", "C0123456789"},
		{"upload", "files.getUploadURLExternal", ""},
		{"history", "conversations.history", replyTestChannel},
		{"unknown method", "brand.new.method", replyTestChannel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &bodyRecordingRT{}
			transport := restrictedSlackTransport{next: fake}
			resp, err := transport.RoundTrip(replyRequest(t, tc.method, tc.channel))
			if resp != nil {
				resp.Body.Close()
			}
			if err == nil || fake.calls != 0 {
				t.Fatalf("allowed: calls=%d err=%v", fake.calls, err)
			}
		})
	}
}

func TestSlackReplyRestrictedFailsClosedOnMissingEnv(t *testing.T) {
	os.Unsetenv(dmOnlyUserEnv)
	os.Unsetenv(dmOnlyChannelEnv)
	os.Unsetenv(connectOnlyEnv)
	fake := &bodyRecordingRT{}
	transport := restrictedSlackTransport{next: fake}
	resp, err := transport.RoundTrip(replyRequest(t, "chat.postMessage", replyTestChannel))
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil || fake.calls != 0 {
		t.Fatalf("missing env must block: calls=%d err=%v", fake.calls, err)
	}
	if restrictedSlackHTTPClient() != http.DefaultClient {
		t.Fatal("absent restriction envs must keep the default client")
	}
}

func TestSlackReplyRestrictedKeepsReadMethods(t *testing.T) {
	replyRestrictedEnv(t)
	for _, method := range []string{"auth.test", "users.list", "conversations.info", "users.info", "team.info"} {
		t.Run(method, func(t *testing.T) {
			fake := &bodyRecordingRT{}
			transport := restrictedSlackTransport{next: fake}
			resp, err := transport.RoundTrip(replyRequest(t, method, replyTestChannel))
			if resp != nil {
				resp.Body.Close()
			}
			if err != nil || fake.calls != 1 {
				t.Fatalf("read blocked: calls=%d err=%v", fake.calls, err)
			}
		})
	}
}

func TestSlackReplyRestrictedConnectOnlyStillBlocksReplies(t *testing.T) {
	t.Setenv(dmOnlyUserEnv, replyTestOwner)
	t.Setenv(dmOnlyChannelEnv, replyTestChannel)
	t.Setenv(connectOnlyEnv, "true")
	fake := &bodyRecordingRT{}
	transport := restrictedSlackTransport{next: fake}
	resp, err := transport.RoundTrip(replyRequest(t, "chat.postMessage", replyTestChannel))
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil || fake.calls != 0 {
		t.Fatalf("connect-only must block replies: calls=%d err=%v", fake.calls, err)
	}
}

func TestSlackReplyRestrictedWrapsClientWithoutConnectOnly(t *testing.T) {
	replyRestrictedEnv(t)
	client := restrictedSlackHTTPClient()
	if client == http.DefaultClient {
		t.Fatal("reply restriction must wrap the SDK client")
	}
	if _, ok := client.Transport.(restrictedSlackTransport); !ok {
		t.Fatal("reply restriction guard missing from transport")
	}
	if client.Timeout != http.DefaultClient.Timeout {
		t.Fatal("default client settings changed")
	}
}
