// What a question cost. The operator brings the key and picks the model
// (ADR 0005), so the price of every answer is reported rather than hidden.
//
// Prices are USD per million tokens, from the published list. A model this
// table does not know prices at zero: a made-up number would be worse than an
// obvious blank, and "fake" costs nothing by definition. PriceKnown lets the
// startup check refuse that blank where a daily budget depends on it.

package agent

import (
	"strings"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
)

type price struct {
	in  float64 // USD per million input tokens
	out float64 // USD per million output tokens
}

// Cache prices are the published multiples of the input price: writing a
// prefix into the cache costs a quarter more than sending it for the
// five-minute lifetime and twice as much for the one-hour lifetime; reading
// it back costs a tenth. Every model on the list uses the same multiples.
const (
	cacheWriteMultiple     = 1.25
	cacheWriteHourMultiple = 2.0
	cacheReadMultiple      = 0.1
)

var prices = map[string]price{
	"claude-opus-5":    {in: 5, out: 25},
	"claude-sonnet-5":  {in: 2, out: 10},
	"claude-haiku-4-5": {in: 1, out: 5},
}

// CostUSD prices one question's usage for one model id. Ids carrying a date
// suffix, claude-haiku-4-5-20251001 for instance, match their family; any
// other extension of a key (claude-opus-50) does not.
//
// The four counters are disjoint: the API reports a cached prefix only in
// the cache counters, never in InputTokens as well, so each is priced once.
func CostUSD(name string, u model.Usage) float64 {
	// A route that reports what it charged is believed over the table: the
	// table knows three models, the route knows every one it sells.
	if u.Cost > 0 {
		return u.Cost
	}
	p, known := priceFor(name)
	if !known {
		return 0
	}
	in := float64(u.InputTokens) * p.in
	out := float64(u.OutputTokens) * p.out
	hour := u.CacheWriteHourTokens
	if hour > u.CacheWriteTokens {
		hour = u.CacheWriteTokens
	}
	fiveMin := u.CacheWriteTokens - hour
	cached := float64(u.CacheReadTokens)*p.in*cacheReadMultiple +
		float64(fiveMin)*p.in*cacheWriteMultiple +
		float64(hour)*p.in*cacheWriteHourMultiple
	return (in + out + cached) / 1e6
}

// PriceKnown says whether CostUSD can price this provider's model. A
// direct Anthropic id missing from the table costs zero, which blinds the
// daily budget, so the startup check asks. OpenRouter reports what it
// charged, and Ollama and the fake model cost nothing.
func PriceKnown(provider, modelID string) bool {
	if provider != "anthropic" {
		return true
	}
	_, ok := priceFor(modelID)
	return ok
}

func priceFor(name string) (price, bool) {
	if p, ok := prices[name]; ok {
		return p, true
	}
	for k, p := range prices {
		if rest, found := strings.CutPrefix(name, k+"-"); found && isDate(rest) {
			return p, true
		}
	}
	return price{}, false
}

// isDate is the snapshot suffix of a dated model id: eight digits.
func isDate(s string) bool {
	if len(s) != 8 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
