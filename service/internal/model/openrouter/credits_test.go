package openrouter

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The balance endpoint sits beside the chat one, a 402 on a round is
// recognisable as "no credit", and a key OpenRouter rejects is a 401 the
// preflight can tell apart from a network problem.
func TestCreditsAndOutOfCredit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/credits":
			if r.Header.Get("Authorization") != "Bearer test-key" {
				w.WriteHeader(401)
				_, _ = io.WriteString(w, `{"error":{"message":"No auth credentials found","code":401}}`)
				return
			}
			_, _ = io.WriteString(w, `{"data":{"total_credits":0,"total_usage":0.163953002}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/chat/completions":
			w.WriteHeader(402)
			_, _ = io.WriteString(w, `{"error":{"message":"This request would exceed your available credits given your current in-flight requests. Retry after in-flight requests settle, or add credits.","code":402}}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	c := New("test-key", Options{Model: "deepseek/deepseek-v4-pro-0813", Endpoint: srv.URL + "/chat/completions"})
	credits, err := c.Credits(context.Background())
	if err != nil {
		t.Fatalf("credits: %v", err)
	}
	if credits.Total != 0 || credits.Used < 0.16 || credits.Remaining() >= 0 {
		t.Errorf("credits = %+v, remaining %.4f; want nothing bought, some used, negative remaining", credits, credits.Remaining())
	}

	_, err = c.Step(context.Background(), "rules", question, oneTool)
	if !IsOutOfCredit(err) || !strings.Contains(err.Error(), "HTTP 402") || !strings.Contains(err.Error(), "add credits") {
		t.Errorf("402 round = %v; want an out-of-credit HTTPError carrying OpenRouter's text", err)
	}
	if IsOutOfCredit(io.EOF) {
		t.Error("an unrelated error is not out of credit")
	}

	bad := New("wrong-key", Options{Model: "m", Endpoint: srv.URL + "/chat/completions"})
	_, err = bad.Credits(context.Background())
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != 401 || !strings.Contains(err.Error(), "No auth credentials") {
		t.Errorf("rejected key = %v; want HTTP 401 with OpenRouter's text", err)
	}
}
