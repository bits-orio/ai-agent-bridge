package controlapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
)

func TestHealthzIsAlwaysOpen(t *testing.T) {
	s := New("127.0.0.1:0", "secret", NewStats("claude-opus-5", time.Now()))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz = %d, want 200 even with a token configured", rec.Code)
	}
}

func TestStatusReportsTheTotals(t *testing.T) {
	started := time.Now()
	stats := NewStats("claude-opus-5", started)
	stats.SetConnected(true)
	stats.SetModVersion("0.2.0")
	stats.RecordAnswer(model.Usage{InputTokens: 4000, OutputTokens: 300}, 0.0275)
	stats.RecordAnswer(model.Usage{InputTokens: 1000, OutputTokens: 100}, 0.0075)

	s := New("127.0.0.1:0", "", stats)
	s.now = func() time.Time { return started.Add(90 * time.Second) }

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with no token configured", rec.Code)
	}
	var got status
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("status body: %v", err)
	}
	want := status{
		Connected: true, ModVersion: "0.2.0", QuestionsAnswered: 2,
		TokensIn: 5000, TokensOut: 400, CostUSD: 0.035,
		Model: "claude-opus-5", Uptime: "1m30s",
	}
	if got != want {
		t.Errorf("status = %+v\nwant       %+v", got, want)
	}
}

func TestStatusChecksTheBearerToken(t *testing.T) {
	s := New("127.0.0.1:0", "secret", NewStats("fake", time.Now()))
	for _, tc := range []struct {
		name   string
		header string
		want   int
	}{
		{"no header", "", http.StatusUnauthorized},
		{"wrong token", "Bearer nope", http.StatusUnauthorized},
		{"no scheme", "secret", http.StatusUnauthorized},
		{"right token", "Bearer secret", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}
