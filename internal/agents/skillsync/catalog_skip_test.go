package skillsync

import "testing"

func TestIsRequiredSkill(t *testing.T) {
	if !IsRequiredSkill("wick-agent-cards") || IsRequiredSkill("wick-notes") {
		t.Fatal("required skill set")
	}
}
