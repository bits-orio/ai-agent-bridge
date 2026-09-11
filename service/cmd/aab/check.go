// `aab check`: the preflight for a new server. Each thing the service
// needs is tried and reported on its own line, so an operator setting up
// against a host they cannot see into learns which of the three is wrong
// before the game is even running.

package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/config"
	"github.com/bits-orio/ai-agent-bridge/service/internal/rpc"
	"github.com/bits-orio/ai-agent-bridge/service/internal/transport"
)

func runCheck(ctx context.Context, cfg *config.Config, client *rpc.Client) {
	failed := false
	say := func(ok bool, what, detail string) {
		mark := "OK  "
		if !ok {
			mark = "FAIL"
			failed = true
		}
		fmt.Printf("%s %-12s %s\n", mark, what, detail)
	}
	warn := func(what, detail string) { fmt.Printf("WARN %-12s %s\n", what, detail) }

	// 1. RCON and the companion.
	rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	status, err := client.Status(rctx)
	cancel()
	if err != nil {
		detail := fmt.Sprintf("%s: %v", cfg.Factorio.RCON.Address, err)
		if strings.Contains(err.Error(), "answered with text") {
			detail += "; RCON itself works, so the companion mod is not running on that server: install ai-agent-bridge there and start the server"
		}
		say(false, "rcon", detail)
	} else {
		say(true, "rcon", fmt.Sprintf("%s answers; companion %s, protocol %d, %d player(s) on, %d question(s) pending",
			cfg.Factorio.RCON.Address, status.ModVersion, status.ProtocolVersion, status.PlayerCount, status.PendingCount))
	}

	// 2. The events file, local or over SFTP.
	var report transport.FileReport
	where := cfg.Factorio.EventsFile
	if cfg.Transport == "sftp" {
		where = fmt.Sprintf("%s on %s as %s", cfg.Factorio.EventsFile, cfg.Factorio.SFTP.Host, cfg.Factorio.SFTP.User)
		report = transport.CheckSFTP(transport.SFTPConfig{
			Host: cfg.Factorio.SFTP.Host, User: cfg.Factorio.SFTP.User,
			KeyPath: cfg.Factorio.SFTP.KeyPath, Password: cfg.Factorio.SFTP.Password,
			KnownHostsPath: cfg.Factorio.SFTP.KnownHostsPath,
		}, cfg.Factorio.EventsFile)
	} else {
		report = transport.CheckLocal(cfg.Factorio.EventsFile)
	}
	switch {
	case !report.Reachable:
		say(false, "events file", fmt.Sprintf("%s: %v", where, report.Err))
	case report.Err != nil:
		say(false, "events file", fmt.Sprintf("%s: %v", where, report.Err))
	case report.Exists:
		say(true, "events file", fmt.Sprintf("%s, %d bytes", where, report.Size))
	default:
		detail := "reachable, but the file is not there yet: the mod writes it on the first event once it runs on the server"
		if len(report.Siblings) > 0 {
			detail += "; the folder holds: " + strings.Join(report.Siblings, ", ")
		} else {
			detail += "; the folder is empty or absent, so check the path"
		}
		warn("events file", where+": "+detail)
	}

	// 3. The model key.
	switch cfg.Model.Provider {
	case "fake":
		say(true, "model", "fake model, no key needed")
	case "anthropic":
		say(cfg.Anthropic.APIKey != "", "model", keyLine(cfg.Anthropic.APIKeyEnv, cfg.Anthropic.APIKey, "anthropic", cfg.Model.ID))
	default:
		say(cfg.OpenRouter.APIKey != "", "model", keyLine(cfg.OpenRouter.APIKeyEnv, cfg.OpenRouter.APIKey, "openrouter", cfg.Model.ID))
	}

	if failed {
		fmt.Println("not ready: fix the FAIL lines above and run check again")
		os.Exit(1)
	}
	fmt.Println("ready: run `aab -config <this config> run`")
}

func keyLine(env, value, provider, model string) string {
	if value == "" {
		return fmt.Sprintf("%s is not set (put it in .env next to the config); provider %s, model %s", env, provider, model)
	}
	return fmt.Sprintf("%s set (%d chars); provider %s, model %s", env, len(value), provider, model)
}
