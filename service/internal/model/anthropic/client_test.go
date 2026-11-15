package anthropic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
)

// model.timeout must reach the direct client: a server that never answers
// ends the call at the configured time, and the SDK's own retry is limited to
// one more try, as with the OpenRouter client.
func TestTimeoutBoundsAHungRequest(t *testing.T) {
	var calls int32
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		select {
		case <-r.Context().Done():
		case <-done:
		}
	}))
	defer srv.Close()
	defer close(done)

	c := New("test-key", Options{Model: "claude-x", Endpoint: srv.URL, Timeout: 100 * time.Millisecond})
	start := time.Now()
	_, err := c.Step(context.Background(), "rules", []model.Message{{Role: model.RoleUser, Blocks: []model.Block{{Type: model.BlockText, Text: "q"}}}}, nil)
	if err == nil {
		t.Fatal("a hung server must be an error")
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("took %s, the 100ms timeout did not apply", took)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Errorf("calls = %d, want 2 (the hung request and one retry)", n)
	}
}
