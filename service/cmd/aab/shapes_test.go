package main

import (
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/agent"
)

func TestArtFor(t *testing.T) {
	art := agent.Artifact{Shape: agent.ShapeArt, Title: "Bot", Lines: []string{"  [o_x]  ", "", " /|_|\\"}}
	for _, v := range []string{"1.0.9", "1.1.0", "2.0.0", "1.0.10"} {
		if got := artFor(v, art); got.Shape != agent.ShapeArt {
			t.Errorf("companion %s renders art, got %s", v, got.Shape)
		}
	}
	for _, v := range []string{"1.0.8", "1.0.0", "0.9.9", "", "x"} {
		got := artFor(v, art)
		if got.Shape != agent.ShapeList || got.Title != "Bot" || len(got.Items) != 2 || got.Items[0] != "[o_x]" || got.Items[1] != "/|_|\\" {
			t.Errorf("companion %q gets art as a list of its lines, got %+v", v, got)
		}
	}
	plain := agent.Summary("hi")
	if got := artFor("1.0.0", plain); got.Shape != agent.ShapeSummary {
		t.Errorf("a shape every companion renders is left alone, got %s", got.Shape)
	}
}
