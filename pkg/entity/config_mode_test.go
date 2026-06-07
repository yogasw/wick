package entity

import "testing"

// mode tag maps to Config.Mode, but only for the two valid values; the
// bare flag form and typos fall back to "" (free, enabled toggle).
func TestStructToConfigs_ModeTag(t *testing.T) {
	type S struct {
		A string `wick:"desc=free"`
		B string `wick:"desc=locked fixed;mode=fixed"`
		C string `wick:"desc=locked expr;mode=expression"`
		D string `wick:"desc=bare flag;mode"`
		E string `wick:"desc=typo;mode=bogus"`
	}
	rows := StructToConfigs(S{})
	want := map[string]string{"a": "", "b": "fixed", "c": "expression", "d": "", "e": ""}
	got := map[string]string{}
	for _, r := range rows {
		got[r.Key] = r.Mode
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("key %q: mode = %q, want %q", k, got[k], v)
		}
	}
}
