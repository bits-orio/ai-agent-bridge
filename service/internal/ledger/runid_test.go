package ledger

import (
	"testing"
	"time"
)

// A question id repeats: a re-run after an outage, a new map. The writer
// gives every run its own run_id, shared by its rounds and its question
// record, so a reader joins them without mixing two runs of one id.
func TestEveryRunOfAQuestionGetsItsOwnRunID(t *testing.T) {
	dir := t.TempDir()
	w := Open(dir, true)
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	w.now = func() time.Time { return now }
	for run := 0; run < 2; run++ {
		w.WriteRound(RoundRecord{QuestionID: 7, Round: 1})
		w.WriteRound(RoundRecord{QuestionID: 7, Round: 2})
		w.WriteQuestion(QuestionRecord{QuestionID: 7})
		now = now.Add(time.Second)
	}
	w.WriteQuestion(QuestionRecord{QuestionID: 7}) // refused: no rounds at all
	w.Close()

	recs, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs.Rounds) != 4 || len(recs.Questions) != 3 {
		t.Fatalf("read %d rounds and %d questions", len(recs.Rounds), len(recs.Questions))
	}
	ids := map[string]bool{}
	for i, q := range recs.Questions {
		if q.RunID == "" {
			t.Fatalf("question record %d has no run_id", i)
		}
		ids[q.RunID] = true
	}
	if len(ids) != 3 {
		t.Errorf("%d distinct run_ids over three runs", len(ids))
	}
	for i, r := range recs.Rounds {
		if r.RunID != recs.Questions[i/2].RunID {
			t.Errorf("round %d run_id %q, want its question's %q", i, r.RunID, recs.Questions[i/2].RunID)
		}
	}
}
