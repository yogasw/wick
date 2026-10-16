package connector

import "testing"

func TestParseFileFormats(t *testing.T) {
	set, denied, invalid := ParseFileFormats(" PNG, .pdf,,exe, sh ,../x,toolongextension,html")
	for _, f := range []string{"png", "pdf", "html"} {
		if !set[f] {
			t.Fatalf("%s missing from %v", f, set)
		}
	}
	if len(set) != 3 || len(denied) != 2 || len(invalid) != 2 {
		t.Fatalf("set=%v denied=%v invalid=%v", set, denied, invalid)
	}
	if ParseMaxBinaryMB("") != DefaultMaxBinaryMB || ParseMaxBinaryMB("x") != DefaultMaxBinaryMB || ParseMaxBinaryMB("500") != MaxBinaryMBCeiling || ParseMaxBinaryMB(" 7 ") != 7 {
		t.Fatal("ParseMaxBinaryMB")
	}
	if !FileFormatDenied("EXE") || FileFormatDenied("png") {
		t.Fatal("FileFormatDenied")
	}
	if def, _, _ := ParseFileFormats(DefaultFileFormats); len(def) != 21 {
		t.Fatalf("default list has %d formats", len(def))
	}
}
