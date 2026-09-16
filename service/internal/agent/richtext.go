package agent

import "strings"

// clipRichText trims s to at most limit runes the way clipRunes does, and
// then makes the result something Factorio will still render as rich text.
//
// Factorio draws a whole chat line raw, tags and all, if any tag in it is
// malformed. Question 91 on the rig, 2026-09-15, answered with every team
// name wrapped in its own [color=yellow]...[/color]; the shrinker cut the
// first line at "Team 19[" to fit the byte budget, and the player read
// "[color=yellow]Team 10[/color]" as text. Two things go wrong at a plain
// rune cut and both are repaired here: a cut inside a tag leaves a dangling
// "[...", which is dropped back to before its bracket, and a tag opened
// before the cut may have lost its closer, which is appended, innermost
// first, for the two kinds of tag that have one, color and font. gps, img
// and the like are single tags with no closer and need nothing.
func clipRichText(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	kept := string(r[:limit])
	// A "[" after the last "]" is a tag the cut landed inside.
	if open := strings.LastIndex(kept, "["); open >= 0 && open > strings.LastIndex(kept, "]") {
		kept = strings.TrimRight(kept[:open], " ")
	}
	return kept + closersFor(kept)
}

// closersFor returns the closing tags the text still owes, innermost first.
// Only color and font open a span in Factorio's rich text; every other tag
// stands alone. A closer met with nothing open is left alone: the model
// wrote it, and a stray "[/color]" renders as nothing rather than raw.
func closersFor(text string) string {
	var open []string
	for i := 0; i < len(text); i++ {
		if text[i] != '[' {
			continue
		}
		end := strings.IndexByte(text[i:], ']')
		if end < 0 {
			break
		}
		tag := text[i+1 : i+end]
		switch {
		case strings.HasPrefix(tag, "color="):
			open = append(open, "color")
		case strings.HasPrefix(tag, "font="):
			open = append(open, "font")
		case tag == "/color" || tag == "/font":
			want := tag[1:]
			for j := len(open) - 1; j >= 0; j-- {
				if open[j] == want {
					open = append(open[:j], open[j+1:]...)
					break
				}
			}
		}
		i += end
	}
	var b strings.Builder
	for j := len(open) - 1; j >= 0; j-- {
		b.WriteString("[/" + open[j] + "]")
	}
	return b.String()
}
