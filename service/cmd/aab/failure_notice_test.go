package main

import (
	"errors"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model/openrouter"
)

// A 402 from OpenRouter tells the asker the account is out of credit, since
// trying again cannot help; every other model error keeps the transient notice.
func TestFailureNotice(t *testing.T) {
	if got := failureNotice(&openrouter.HTTPError{Status: 402, Model: "m", Text: "add credits"}); got != outOfCreditNotice {
		t.Errorf("402 notice = %q", got)
	}
	if got := failureNotice(errors.New("dial tcp: connection refused")); got != modelFailedNotice {
		t.Errorf("generic notice = %q", got)
	}
}
