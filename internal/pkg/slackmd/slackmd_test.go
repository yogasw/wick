package slackmd

import (
	"strings"
	"testing"
)

const table = "| Area | Bisa apa | Contoh |\n|---|---|---|\n| Logs | cek error | Loki |\n| Tiket | buat tiket | Notion |"

func TestNeedsBlock(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"table", "Ringkasan:\n\n" + table, true},
		{"aligned table", "a | b\n:--|--:\n1 | 2", true},
		{"heading", "## Hasil\nsemua aman", true},
		{"bold", "ini **penting** ya", true},
		{"link", "lihat [dashboard](https://example.com/x)", true},
		{"nested list", "- satu\n  - dua", true},
		{"fence", "```\ngo test ./...\n```", true},
		{"plain", "Halo, deploy sudah selesai.", false},
		{"mrkdwn bold", "ini *penting* dan _miring_", false},
		{"mrkdwn link", "lihat <https://example.com|dashboard>", false},
		{"flat list", "- satu\n- dua", false},
		{"pipe in prose", "pilih a | b saja", false},
		{"empty", "  \n", false},
	}
	for _, c := range cases {
		if got := NeedsBlock(c.text); got != c.want {
			t.Errorf("%s: NeedsBlock = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestChunksFitsUnchanged(t *testing.T) {
	got := Chunks(table, MaxBlockChars)
	if len(got) != 1 || got[0] != table {
		t.Fatalf("short text must stay one chunk, got %q", got)
	}
}

func TestChunksCutBetweenParagraphsAndTables(t *testing.T) {
	para := strings.Repeat("kalimat panjang ", 40) // ~640 bytes
	text := para + "\n\n" + table + "\n\n" + para
	max := len(para) + 10
	got := Chunks(text, max)
	if len(got) != 3 {
		t.Fatalf("want 3 chunks, got %d: %q", len(got), got)
	}
	if got[1] != table {
		t.Fatalf("table must move whole into its own chunk, got %q", got[1])
	}
	for _, c := range got {
		if len(c) > max {
			t.Fatalf("chunk over limit: %d > %d", len(c), max)
		}
	}
}

func TestChunksSplitBigTableRepeatsHeader(t *testing.T) {
	header := "| No | Nama |\n|---|---|\n"
	var b strings.Builder
	b.WriteString(header)
	for i := 0; i < 400; i++ {
		b.WriteString("| 1 | baris tabel yang cukup panjang |\n")
	}
	text := strings.TrimRight(b.String(), "\n")
	got := Chunks(text, 2000)
	if len(got) < 2 {
		t.Fatalf("want several chunks, got %d", len(got))
	}
	rows := 0
	for i, c := range got {
		if len(c) > 2000 {
			t.Fatalf("chunk %d over limit: %d", i, len(c))
		}
		if !strings.HasPrefix(c, header) {
			t.Fatalf("chunk %d does not start with the table header: %q", i, c[:40])
		}
		for _, line := range strings.Split(c, "\n")[2:] {
			if line != "| 1 | baris tabel yang cukup panjang |" {
				t.Fatalf("chunk %d has a broken row %q", i, line)
			}
			rows++
		}
		if !NeedsBlock(c) {
			t.Fatalf("chunk %d no longer reads as a table", i)
		}
	}
	if rows != 400 {
		t.Fatalf("rows lost or duplicated: %d", rows)
	}
}

func TestChunksSplitBigFenceClosesAndReopens(t *testing.T) {
	var b strings.Builder
	b.WriteString("```\n")
	for i := 0; i < 200; i++ {
		b.WriteString("log line yang lumayan panjang di sini\n")
	}
	b.WriteString("```")
	got := Chunks(b.String(), 1500)
	if len(got) < 2 {
		t.Fatalf("want several chunks, got %d", len(got))
	}
	for i, c := range got {
		if !strings.HasPrefix(c, "```\n") || !strings.HasSuffix(c, "```") {
			t.Fatalf("chunk %d is not a closed fence: %q…%q", i, c[:10], c[len(c)-10:])
		}
	}
}

func TestChunksHugeLineStillFits(t *testing.T) {
	text := "## Judul\n\n" + strings.Repeat("x", 5000)
	for _, c := range Chunks(text, 1000) {
		if len(c) > 1000 {
			t.Fatalf("chunk over limit: %d", len(c))
		}
	}
}

func TestFallback(t *testing.T) {
	got := Fallback("## Hasil\n\nLihat **ini** dan [dashboard](https://example.com/x)\n\n" + table)
	want := "Hasil Lihat ini dan dashboard Area · Bisa apa · Contoh Logs · cek error · Loki Tiket · buat tiket · Notion"
	if got != want {
		t.Fatalf("Fallback = %q\nwant      %q", got, want)
	}
	long := Fallback("# a\n" + strings.Repeat("kata ", 200))
	if n := len([]rune(long)); n > maxFallbackRunes+1 || !strings.HasSuffix(long, "…") {
		t.Fatalf("long fallback not clipped: %d runes", n)
	}
}
