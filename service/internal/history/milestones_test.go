package history

import (
	"path/filepath"
	"testing"
)

func ingestRow(t *testing.T, s *Store, line string) Ingested {
	t.Helper()
	got, err := s.IngestRow([]byte(line))
	if err != nil {
		t.Fatalf("IngestRow(%s): %v", line, err)
	}
	return got
}

// IngestRow says what it stored: the row's id, its epoch and whether every
// question may read it, which is what a toast is allowed to speak from.
func TestIngestRowReportsTheRowItStored(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "h.sqlite"))
	defer s.Close()
	ingest(t, s, `{"event":"session","tick":10,"data":{"map_id":"M1"}}`)
	got := ingestRow(t, s, `{"event":"rocket_launched","tick":100,"data":{"force":"team-1","surface":"nauvis"}}`)
	if got.ID == 0 || got.Event != "rocket_launched" || got.Tick != 100 || got.Force != "team-1" || !got.Public {
		t.Errorf("IngestRow = %+v, want a public rocket row of team-1 at tick 100", got)
	}
	if got.Epoch != s.CurrentEpoch() || got.Epoch == 0 {
		t.Errorf("row epoch %d, current %d", got.Epoch, s.CurrentEpoch())
	}
	chat := ingestRow(t, s, `{"event":"console_chat","tick":101,"data":{"player":"A","force":"team-1","message":"x","scope":"team-1","private":true}}`)
	if chat.Public {
		t.Error("a private chat row read as public")
	}
	if got, _ := s.IngestRow([]byte("   ")); got.ID != 0 {
		t.Errorf("a blank line stored row %d", got.ID)
	}
}

// A row quarantined behind a status (guard.go) is not public, so no toast
// can speak from a line that may be the map before.
func TestIngestRowQuarantinedIsNotPublic(t *testing.T) {
	s := oldMapStore(t)
	noteMap(t, s, "M2", 900, 0)
	got := ingestRow(t, s, `{"event":"rocket_launched","tick":4000800,"data":{"force":"team-4","surface":"nauvis"}}`)
	if got.Public {
		t.Errorf("a quarantined row read as public: %+v", got)
	}
}

// The counts a milestone is decided from: one force's rockets in one epoch
// up to and including a row, the forces with a rocket, and whether a
// technology was finished before a row. Another epoch's rows never count.
func TestMilestoneCounts(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "h.sqlite"))
	defer s.Close()
	ingest(t, s, `{"event":"session","tick":10,"data":{"map_id":"OLD"}}`)
	ingest(t, s, `{"event":"rocket_launched","tick":20000,"data":{"force":"team-1","surface":"nauvis"}}`)
	ingest(t, s, `{"event":"research_finished","tick":20001,"data":{"force":"team-1","tech":"rocket-silo","level":1}}`)
	ingest(t, s, `{"event":"session","tick":10,"data":{"map_id":"NEW"}}`)
	a := ingestRow(t, s, `{"event":"rocket_launched","tick":20000,"data":{"force":"team-1","surface":"nauvis"}}`)
	b := ingestRow(t, s, `{"event":"rocket_launched","tick":20001,"data":{"force":"team-2","surface":"nauvis"}}`)
	c := ingestRow(t, s, `{"event":"rocket_launched","tick":20002,"data":{"force":"team-1","surface":"nauvis"}}`)

	if n, err := s.RocketsUpTo(a.Epoch, "team-1", a.ID); err != nil || n != 1 {
		t.Errorf("team-1 rockets up to its first row = %d, %v; want 1, the old map's never counted", n, err)
	}
	if n, _ := s.RocketsUpTo(c.Epoch, "team-1", c.ID); n != 2 {
		t.Errorf("team-1 rockets up to its second row = %d, want 2", n)
	}
	if n, _ := s.RocketForcesUpTo(b.Epoch, b.ID); n != 2 {
		t.Errorf("forces with a rocket up to team-2's = %d, want 2", n)
	}
	if n, _ := s.RocketForcesUpTo(a.Epoch, a.ID); n != 1 {
		t.Errorf("forces with a rocket up to team-1's first = %d, want 1", n)
	}

	r1 := ingestRow(t, s, `{"event":"research_finished","tick":20003,"data":{"force":"team-2","tech":"rocket-silo","level":1}}`)
	if earlier, err := s.ResearchedBefore(r1.Epoch, "rocket-silo", r1.ID); err != nil || earlier {
		t.Errorf("rocket-silo before the new map's first = %v, %v; want false, the old map's never counted", earlier, err)
	}
	r2 := ingestRow(t, s, `{"event":"research_finished","tick":20004,"data":{"force":"team-1","tech":"rocket-silo","level":1}}`)
	if earlier, _ := s.ResearchedBefore(r2.Epoch, "rocket-silo", r2.ID); !earlier {
		t.Error("rocket-silo before team-1's = false, want true: team-2 finished it first")
	}
}
