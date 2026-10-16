package logfiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDailyFileRollsOnDateChange(t *testing.T) {
	dir := t.TempDir()
	d, err := newDailyFile(dir, "app")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.date = "2000-01-01" // pretend the process started on an older day
	if _, err := d.Write([]byte("x\n")); err != nil {
		t.Fatal(err)
	}
	today := filepath.Join(dir, "app-"+time.Now().Format(dateLayout)+logSuffix)
	if _, err := os.Stat(today); err != nil {
		t.Fatalf("expected today's file after the date changed: %v", err)
	}
}

func TestCapUndatedLogsKeepsTail(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "daemon-stderr.log")
	dated := filepath.Join(dir, "app-"+time.Now().Format(dateLayout)+logSuffix)
	line := strings.Repeat("a", 99) + "\n"
	content := strings.Repeat(line, 1000) // 100 KB
	if err := os.WriteFile(big, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dated, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	capUndatedLogs(dir, 50<<10, 10<<10)
	got, _ := os.ReadFile(big)
	if len(got) == 0 || len(got) > 10<<10 || !strings.HasSuffix(string(got), "\n") {
		t.Fatalf("undated log not trimmed to its tail: %d bytes", len(got))
	}
	if string(got[:99]) != strings.Repeat("a", 99) {
		t.Fatal("trimmed file must start on a whole line")
	}
	if kept, _ := os.ReadFile(dated); len(kept) != len(content) {
		t.Fatal("dated log must be left to the retention pass, not capped")
	}
}

func TestPruneOldLogsOneDay(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "app-"+time.Now().AddDate(0, 0, -3).Format(dateLayout)+logSuffix)
	cur := filepath.Join(dir, "app-"+time.Now().Format(dateLayout)+logSuffix)
	for _, p := range []string{old, cur} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pruneOldLogs(dir, defaultRetentionDays)
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("3-day-old log should be removed at 1-day retention")
	}
	if _, err := os.Stat(cur); err != nil {
		t.Fatal("today's log must stay")
	}
}
