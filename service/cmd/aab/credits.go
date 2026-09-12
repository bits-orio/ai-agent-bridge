// The OpenRouter balance, read at preflight and at startup. An account with
// no credit fails every question with HTTP 402, which in game looks like the
// service is broken when nothing is; the balance is visible before the first
// question is asked, so both places say it.

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/config"
	"github.com/bits-orio/ai-agent-bridge/service/internal/model/openrouter"
)

// lowBalanceUSD is where the balance line turns into a warning: a few
// hundred cheap questions, or a few dozen expensive ones.
const lowBalanceUSD = 1.0

// balanceVerdict is one line about the balance: ok, warn (low, or unreadable
// for a reason other than the key), or fail (no credit, or a key OpenRouter
// rejects).
type balanceVerdict struct {
	level string // "ok", "warn" or "fail"
	text  string
}

func checkBalance(ctx context.Context, cfg *config.Config) balanceVerdict {
	c := openrouter.New(cfg.OpenRouter.APIKey, openrouter.Options{Model: cfg.Model.ID})
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	credits, err := c.Credits(ctx)
	if err != nil {
		var he *openrouter.HTTPError
		if errors.As(err, &he) && (he.Status == http.StatusUnauthorized || he.Status == http.StatusForbidden) {
			return balanceVerdict{"fail", fmt.Sprintf("OpenRouter rejected the key in %s (HTTP %d: %s)", cfg.OpenRouter.APIKeyEnv, he.Status, he.Text)}
		}
		return balanceVerdict{"warn", fmt.Sprintf("could not read the OpenRouter balance: %v", err)}
	}
	left := credits.Remaining()
	switch {
	case left <= 0:
		return balanceVerdict{"fail", fmt.Sprintf("OpenRouter balance is $%.2f: every question is refused with HTTP 402 until credits are added at %s", left, creditsURL)}
	case left < lowBalanceUSD:
		return balanceVerdict{"warn", fmt.Sprintf("OpenRouter balance is $%.2f, which is running low; top up at %s", left, creditsURL)}
	}
	return balanceVerdict{"ok", fmt.Sprintf("OpenRouter balance $%.2f", left)}
}
