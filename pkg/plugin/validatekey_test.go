package plugin

import "testing"

func TestValidateKey(t *testing.T) {
	ok := []string{"httpbin", "google_workspace", "slack", "x", "a1_b2", "my-connector", "convert-text-alt", "wa-message-inbound-extractor"}
	for _, k := range ok {
		if err := ValidateKey(k); err != nil {
			t.Errorf("ValidateKey(%q) should pass, got %v", k, err)
		}
	}
	bad := []string{"", "Google", "-x", "x-", "a/b", "../etc", "a b", "a.b", "foo!"}
	for _, k := range bad {
		if err := ValidateKey(k); err == nil {
			t.Errorf("ValidateKey(%q) should fail", k)
		}
	}
}
