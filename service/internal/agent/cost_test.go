package agent

import (
	"math"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
)

func TestCostUSD(t *testing.T) {
	million := model.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}
	for _, tc := range []struct {
		name  string
		usage model.Usage
		want  float64
	}{
		{"claude-opus-5", million, 30},
		{"claude-sonnet-5", million, 12},
		{"claude-haiku-4-5", million, 6},
		// A dated id prices as its family.
		{"claude-haiku-4-5-20251001", million, 6},
		// An unknown model prices at zero rather than at a guess.
		{"some-other-model", million, 0},
		{"fake", million, 0},
		// A reported cost is the cost, whatever the model id.
		{"deepseek/deepseek-v4-pro-0813", model.Usage{InputTokens: 5000, OutputTokens: 200, Cost: 0.0031}, 0.0031},
		{"claude-opus-5", model.Usage{InputTokens: 1_000_000, Cost: 4.2}, 4.2},
		// The everyday case: a few thousand tokens, in fractions of a cent.
		{"claude-opus-5", model.Usage{InputTokens: 4000, OutputTokens: 300}, 0.0275},
		// A cache read is a tenth of the input price, a cache write a
		// quarter more than it.
		{"claude-opus-5", model.Usage{CacheReadTokens: 1_000_000}, 0.5},
		{"claude-opus-5", model.Usage{CacheWriteTokens: 1_000_000}, 6.25},
		// A one-hour write is twice the input price; the hour counter is a
		// part of the write total, not an addition to it.
		{"claude-opus-5", model.Usage{CacheWriteTokens: 1_000_000, CacheWriteHourTokens: 1_000_000}, 10},
		{"claude-opus-5", model.Usage{CacheWriteTokens: 1_000_000, CacheWriteHourTokens: 400_000}, 7.75},
		// An hour counter past the total (a malformed reply) is clamped.
		{"claude-opus-5", model.Usage{CacheWriteTokens: 100, CacheWriteHourTokens: 500}, 0.001},
		// The everyday cached case: the fixed prompt read back from the
		// cache, a few hundred fresh tokens, a short answer.
		{"claude-sonnet-5", model.Usage{InputTokens: 300, CacheReadTokens: 6000, OutputTokens: 200}, 0.0038},
	} {
		got := CostUSD(tc.name, tc.usage)
		if math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("CostUSD(%q, %+v) = %v, want %v", tc.name, tc.usage, got, tc.want)
		}
	}
}

// Prefix matching must not let a shorter family swallow a longer id that has
// its own price.
func TestPriceForPrefersTheLongestMatch(t *testing.T) {
	prices["claude-opus-5-mini"] = price{in: 1, out: 2}
	defer delete(prices, "claude-opus-5-mini")

	p, ok := priceFor("claude-opus-5-mini-20260101")
	if !ok || p.in != 1 {
		t.Fatalf("priceFor picked %+v (found %v), want the mini price", p, ok)
	}
}
