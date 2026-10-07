package provider

import "testing"

// The bug this exists for, taken from a real session: the model wrote a
// sentence, ran a tool, then wrote the next sentence. Both pieces were
// appended into one body and arrived as "produksi.Sudah jadi."
func TestTextJoinerSeparatesMessagesAcrossATool(t *testing.T) {
	var j textJoiner
	first := "…mengikuti bentuk intent produksi."
	second := "Sudah jadi. Ada di artifacts/."

	got := j.breakBefore(first) + first
	j.toolRan()
	got += j.breakBefore(second) + second

	want := first + "\n\n" + second
	if got != want {
		t.Errorf("messages not separated:\n got: %q\nwant: %q", got, want)
	}
}

// Deltas of the SAME message must stay glued — that is how streaming works,
// and a break between them would shred every sentence.
func TestTextJoinerLeavesStreamingDeltasAlone(t *testing.T) {
	var j textJoiner
	got := ""
	for _, d := range []string{"Halo ", "dunia", ", apa kabar?"} {
		got += j.breakBefore(d) + d
	}
	if want := "Halo dunia, apa kabar?"; got != want {
		t.Errorf("streaming deltas were broken up: %q, want %q", got, want)
	}
}

// A turn that opens with a tool call has nothing to separate.
func TestTextJoinerNoBreakBeforeTheFirstText(t *testing.T) {
	var j textJoiner
	j.toolRan()
	if sep := j.breakBefore("Hasilnya:"); sep != "" {
		t.Errorf("leading break inserted: %q", sep)
	}
}

// Newlines the model already wrote count toward the blank line, so a message
// ending in "\n" does not get three of them.
func TestTextJoinerCountsExistingNewlines(t *testing.T) {
	cases := []struct {
		name, first, second, wantSep string
	}{
		{"none either side", "a", "b", "\n\n"},
		{"trailing one", "a\n", "b", "\n"},
		{"trailing two", "a\n\n", "b", ""},
		{"leading one", "a", "\nb", "\n"},
		{"leading two", "a", "\n\nb", ""},
		{"one each side", "a\n", "\nb", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var j textJoiner
			j.breakBefore(tc.first)
			j.toolRan()
			if sep := j.breakBefore(tc.second); sep != tc.wantSep {
				t.Errorf("separator = %q, want %q", sep, tc.wantSep)
			}
		})
	}
}

// Only the first text after the tool opens a new message; the deltas that
// follow it belong to that same message.
func TestTextJoinerBreaksOncePerTool(t *testing.T) {
	var j textJoiner
	j.breakBefore("satu")
	j.toolRan()
	if sep := j.breakBefore("dua"); sep != "\n\n" {
		t.Fatalf("first text after the tool got %q", sep)
	}
	if sep := j.breakBefore(" lanjutan"); sep != "" {
		t.Errorf("continuation got a break: %q", sep)
	}
}

// The next turn starts clean — no break carried over from the last one.
func TestTextJoinerResetsAtTurnEnd(t *testing.T) {
	var j textJoiner
	j.breakBefore("giliran lama")
	j.toolRan()
	j.turnEnded()
	if sep := j.breakBefore("giliran baru"); sep != "" {
		t.Errorf("break leaked across the turn boundary: %q", sep)
	}
}

func TestTextJoinerIgnoresEmptyText(t *testing.T) {
	var j textJoiner
	j.breakBefore("ada isi")
	j.toolRan()
	if sep := j.breakBefore(""); sep != "" {
		t.Errorf("empty delta got a separator: %q", sep)
	}
	// The pending break must survive an empty delta, not be eaten by it.
	if sep := j.breakBefore("teks berikutnya"); sep != "\n\n" {
		t.Errorf("pending break lost to an empty delta: %q", sep)
	}
}
