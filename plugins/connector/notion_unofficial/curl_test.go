package main

import "testing"

// sampleCurl is a real DevTools "Copy as cURL" of a notion.so/api/v3 request
// (token redacted-ish — this is test data, not a live secret). It exercises the
// bash single-quoted form with a big cookie jar and many headers.
const sampleCurl = `curl 'https://app.notion.com/api/v3/getPublicSpaceData' \
  -H 'accept: */*' \
  -H 'content-type: application/json' \
  -b '_ga=GA1.1.x; notion_user_id=c7162a50-5cf4-4957-96f4-ec2c18528dbd; NEXT_LOCALE=en-US; token_v2=v03%3Asample%3Aabc.def; __cf_bm=zzz' \
  -H 'notion-client-version: 23.13.20260716.0207' \
  -H 'user-agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36' \
  -H 'x-notion-active-user-header: c7162a50-5cf4-4957-96f4-ec2c18528dbd' \
  -H 'x-notion-space-id: 827e9fd8-c1e6-4684-b62d-274773e6cb75' \
  --data-raw '{"type":"space-ids"}'`

func TestParseCurl_Sample(t *testing.T) {
	got := parseCurl(sampleCurl)

	// token_v2 must be URL-decoded (%3A → :).
	if got.TokenV2 != "v03:sample:abc.def" {
		t.Errorf("TokenV2 = %q, want %q", got.TokenV2, "v03:sample:abc.def")
	}
	if want := "23.13.20260716.0207"; got.ClientVersion != want {
		t.Errorf("ClientVersion = %q, want %q", got.ClientVersion, want)
	}
	if want := "c7162a50-5cf4-4957-96f4-ec2c18528dbd"; got.ActiveUser != want {
		t.Errorf("ActiveUser = %q, want %q", got.ActiveUser, want)
	}
	if want := "827e9fd8-c1e6-4684-b62d-274773e6cb75"; got.SpaceID != want {
		t.Errorf("SpaceID = %q, want %q", got.SpaceID, want)
	}
	// The User-Agent contains ';' — the very thing that can't live in a wick tag.
	if got.UserAgent == "" || !contains(got.UserAgent, "Chrome/150") {
		t.Errorf("UserAgent = %q, want it to contain Chrome/150", got.UserAgent)
	}
}

func TestParseCurl_Empty(t *testing.T) {
	got := parseCurl("")
	if got != (curlCreds{}) {
		t.Errorf("parseCurl(\"\") = %+v, want zero value", got)
	}
}

func TestParseCurl_DoubleQuotes(t *testing.T) {
	// The Windows "Copy as cURL (cmd)" form uses double quotes.
	in := `curl "https://app.notion.com/api/v3/x" -H "user-agent: UA1" -b "token_v2=v03%3Aabc"`
	got := parseCurl(in)
	if got.UserAgent != "UA1" {
		t.Errorf("UserAgent = %q, want UA1", got.UserAgent)
	}
	if got.TokenV2 != "v03:abc" {
		t.Errorf("TokenV2 = %q, want v03:abc", got.TokenV2)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
