// Adapted from open-discord-bridge/bridge/cmd/bridge/logfile_test.go: same test logic
// against resolveLogPath, with the import path and default file name ("bridge.log" ->
// "aab.log") swapped for AAB.

package main

import (
	"path/filepath"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/config"
)

func TestResolveLogPath(t *testing.T) {
	mk := func(transport, eventsFile, logFile string) *config.Config {
		c := &config.Config{Transport: transport, LogFile: logFile}
		c.Factorio.EventsFile = eventsFile
		return c
	}
	eventsDir := filepath.Join("/data", "script-output", "ai-agent-bridge")

	cases := []struct {
		name string
		cfg  *config.Config
		want string
	}{
		{"explicit path wins", mk("local", filepath.Join(eventsDir, "events.jsonl"), "/var/log/aab.log"), "/var/log/aab.log"},
		{"default next to events (local)", mk("local", filepath.Join(eventsDir, "events.jsonl"), ""), filepath.Join(eventsDir, "aab.log")},
		{"dash disables the file", mk("local", filepath.Join(eventsDir, "events.jsonl"), "-"), ""},
		{"sftp default is stderr-only (remote events path)", mk("sftp", "script-output/ai-agent-bridge/events.jsonl", ""), ""},
		{"sftp honors an explicit local path", mk("sftp", "remote/events.jsonl", "/local/aab.log"), "/local/aab.log"},
		{"no events file, no default", mk("local", "", ""), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveLogPath(tc.cfg); got != tc.want {
				t.Errorf("resolveLogPath() = %q, want %q", got, tc.want)
			}
		})
	}
}
