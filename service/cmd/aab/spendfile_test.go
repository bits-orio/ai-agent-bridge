package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/agent"
)

// S3: the spend file is written whole or not at all, readable by the owner
// alone, and read back as written; a missing or broken file is no spend.
func TestTheSpendFileRoundTripsPrivately(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daily-spend.json")
	if got := loadSpend(path); len(got) != 0 {
		t.Fatalf("a missing file read as %v", got)
	}
	want := []agent.SpendEntry{{At: time.Now().UTC().Truncate(time.Second), USD: 0.25}}
	if err := saveSpend(path, want); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
	got := loadSpend(path)
	if len(got) != 1 || !got[0].At.Equal(want[0].At) || got[0].USD != 0.25 {
		t.Errorf("read back %v, want %v", got, want)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "*.tmp*")); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadSpend(path); len(got) != 0 {
		t.Errorf("a broken file read as %v", got)
	}
}
