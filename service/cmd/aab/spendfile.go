// The daily spend kept across restarts (agent.daily_spend_path). Without it
// the max_cost_per_day cap starts from zero every time the process does, so
// a question that crashes the service could be billed again and again with
// the cap never reached.

package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/bits-orio/ai-agent-bridge/service/internal/agent"
)

// loadSpend reads the spend an earlier process saved. A file that is not
// there is a first start; one that does not parse is logged and read as no
// spend, since refusing to start over it would take the server's answers
// away for a bookkeeping file.
func loadSpend(path string) []agent.SpendEntry {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		log.Printf("budget: could not read %s, the day's spend starts from zero: %v", path, err)
		return nil
	}
	var out []agent.SpendEntry
	if err := json.Unmarshal(b, &out); err != nil {
		log.Printf("budget: %s does not parse, the day's spend starts from zero: %v", path, err)
		return nil
	}
	return out
}

// saveSpend writes the spend whole or not at all: to a temporary file
// beside it, then renamed over it, so a crash mid-write leaves the last
// good copy. The file is the owner's alone.
func saveSpend(path string, entries []agent.SpendEntry) error {
	b, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// keepSpend restores the agent's spend from path and saves it there after
// every spend. A failed save is logged once per streak.
func keepSpend(a *agent.Agent, path string) {
	a.RestoreDailySpend(loadSpend(path))
	failing := false
	a.OnDailySpend(func(entries []agent.SpendEntry) {
		if err := saveSpend(path, entries); err != nil {
			if !failing {
				log.Printf("budget: could not save the day's spend to %s: %v", path, err)
			}
			failing = true
			return
		}
		failing = false
	})
}
