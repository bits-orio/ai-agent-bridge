package ledger

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// S10: the ledger holds player names and question text, so its directory and
// files are the owner's alone, and a file an older service made world
// readable is narrowed when it is opened.
func TestLedgerFilesAreOwnerOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ledger")
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	w := Open(dir, true)
	w.now = func() time.Time { return now }
	w.WriteQuestion(NewQuestionRecord())
	w.Close()
	checkMode(t, dir, 0o700)
	checkMode(t, filepath.Join(dir, fileName(now)), 0o600)

	older := filepath.Join(dir, fileName(now.Add(24*time.Hour)))
	if err := os.WriteFile(older, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	w = Open(dir, true)
	w.now = func() time.Time { return now.Add(24 * time.Hour) }
	w.WriteQuestion(NewQuestionRecord())
	w.Close()
	checkMode(t, older, 0o600)
}

func checkMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != want {
		t.Errorf("%s mode = %v, want %v", path, got, want)
	}
}
