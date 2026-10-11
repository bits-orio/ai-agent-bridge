package main

import (
	"strconv"
	"strings"

	"github.com/bits-orio/ai-agent-bridge/service/internal/agent"
)

// artSince is the first companion that renders the art shape. An older one
// prints "(unrecognised answer shape)" for it, so art bound there goes out
// as a list of its lines instead: the drawing loses its spacing, the answer
// still arrives.
var artSince = [3]int{1, 0, 9}

// artFor is a, unless a is art and the companion predates it.
func artFor(companion string, a agent.Artifact) agent.Artifact {
	if a.Shape != agent.ShapeArt || atLeast(companion, artSince) {
		return a
	}
	items := make([]string, 0, agent.MaxListItems)
	for _, l := range a.Lines {
		if l = strings.Join(strings.Fields(l), " "); l != "" && len(items) < agent.MaxListItems {
			items = append(items, l)
		}
	}
	return agent.Artifact{Shape: agent.ShapeList, Title: a.Title, Items: items, Session: a.Session, ToAsker: a.ToAsker}
}

// atLeast reports whether version, "major.minor.patch", is want or later. A
// version it cannot read is older than everything.
func atLeast(version string, want [3]int) bool {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return false
		}
		if n != want[i] {
			return n > want[i]
		}
	}
	return true
}
