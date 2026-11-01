package delegation

import "testing"

// A line reporting a teammate's answer, tagged with its source, is not a
// new task for that teammate; a request to it still is.
func TestParseMentionsSkipsSourceReports(t *testing.T) {
	roster := []string{"vera", "anton"}
	text := "Hasil:\n" +
		"@vera found 12 errors in the logs. [from:@vera]\n" +
		"@anton both agree. [from:@vera,@anton]\n" +
		"@anton please recheck the 500s [from:@vera]\n"
	got := ParseMentions(text, roster)
	if len(got) != 1 || got[0].Handle != "anton" || got[0].Body != "please recheck the 500s [from:@vera]" {
		t.Fatalf("mentions = %+v", got)
	}
}
