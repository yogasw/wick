package channels

import "testing"

func TestRecoverPanicSwallowsPanic(t *testing.T) {
	ran := false
	func() {
		defer RecoverPanic("test", "unit")
		ran = true
		var m map[string]int
		m["boom"] = 1 // nil map write panics
	}()
	if !ran {
		t.Fatal("body did not run")
	}
}
