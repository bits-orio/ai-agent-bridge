// Copied from open-discord-bridge/bridge/cmd/bridge/logfile.go, unchanged beyond this
// header, the import path, and the "bridge"/"ODB_LOG_FILE"/"bridge.log" wording below
// (now "service"/"AAB_LOG_FILE"/"aab.log").

package main

import (
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/bits-orio/ai-agent-bridge/service/internal/config"
)

// setupLogFile tees the standard logger to a file in addition to stderr, so the service's
// output is visible where stdout isn't, e.g. a hosting panel that backgrounds the service
// and only surfaces files. The path comes from log_file / AAB_LOG_FILE; when unset it
// defaults to aab.log next to the events file (local transport only: an SFTP events path
// is remote, not a local target). A log_file of "-" disables the file (stderr only). Open
// failures fall back to stderr with a warning rather than stopping the service.
func setupLogFile(cfg *config.Config) {
	path := resolveLogPath(cfg)
	if path == "" {
		return
	}
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Printf("log: cannot write to %s: %v (logging to stderr only)", path, err)
		return
	}
	// f intentionally stays open for the lifetime of the process.
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	log.Printf("log: also writing to %s", path)
}

// resolveLogPath returns the log-file path, or "" for stderr-only.
func resolveLogPath(cfg *config.Config) string {
	switch lf := cfg.LogFile; lf {
	case "-": // explicitly disabled
		return ""
	case "": // default: next to the events file, but only for local transport
		if cfg.Transport == "local" && cfg.Factorio.EventsFile != "" {
			return filepath.Join(filepath.Dir(cfg.Factorio.EventsFile), "aab.log")
		}
		return ""
	default:
		return lf
	}
}
