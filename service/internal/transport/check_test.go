package transport

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckLocalReportsExistsMissingAndUnreachable(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(file, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := CheckLocal(file); !r.Reachable || !r.Exists || r.Size != 3 || r.Err != nil {
		t.Errorf("existing file: %+v", r)
	}
	if r := CheckLocal(filepath.Join(dir, "missing.jsonl")); !r.Reachable || r.Exists || r.Err != nil || len(r.Siblings) != 1 || r.Siblings[0] != "events.jsonl" {
		t.Errorf("missing file: %+v", r)
	}
}

// An SFTP host that does not answer is reported as unreachable, not as a
// missing file, with the dial error kept.
func TestCheckSFTPUnreachableHost(t *testing.T) {
	r := CheckSFTP(SFTPConfig{Host: "127.0.0.1:1", User: "x", Password: "y"}, "events.jsonl")
	if r.Reachable || r.Exists || r.Err == nil {
		t.Errorf("unreachable host: %+v", r)
	}
}
