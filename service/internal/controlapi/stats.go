// What the control API reports. The operator brought the key and picked the
// model (ADR 0005), so the running total of what that has cost is a first
// class number here, never buried in a log line.

package controlapi

import (
	"sync"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
)

// Stats is the live counter set behind /v1/status. Every field is written by
// the poll loop and read by HTTP handlers, so all access goes through the
// mutex.
type Stats struct {
	mu sync.Mutex

	started    time.Time
	modelName  string
	connected  bool
	modVersion string
	answered   int
	tokensIn   int
	tokensOut  int
	costUSD    float64
}

func NewStats(modelName string, started time.Time) *Stats {
	return &Stats{started: started, modelName: modelName}
}

// SetConnected records whether the last RCON round trip worked.
func (s *Stats) SetConnected(connected bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connected = connected
}

// SetModVersion records the companion version the server reported.
func (s *Stats) SetModVersion(version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modVersion = version
}

// RecordAnswer adds one answered question to the totals.
func (s *Stats) RecordAnswer(usage model.Usage, costUSD float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.answered++
	s.tokensIn += usage.InputTokens
	s.tokensOut += usage.OutputTokens
	s.costUSD += costUSD
}

// status is the JSON body of /v1/status.
type status struct {
	Connected         bool    `json:"connected"`
	ModVersion        string  `json:"mod_version"`
	QuestionsAnswered int     `json:"questions_answered"`
	TokensIn          int     `json:"tokens_in"`
	TokensOut         int     `json:"tokens_out"`
	CostUSD           float64 `json:"cost_usd"`
	Model             string  `json:"model"`
	Uptime            string  `json:"uptime"`
}

func (s *Stats) snapshot(now time.Time) status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return status{
		Connected:         s.connected,
		ModVersion:        s.modVersion,
		QuestionsAnswered: s.answered,
		TokensIn:          s.tokensIn,
		TokensOut:         s.tokensOut,
		CostUSD:           s.costUSD,
		Model:             s.modelName,
		Uptime:            now.Sub(s.started).Round(time.Second).String(),
	}
}
