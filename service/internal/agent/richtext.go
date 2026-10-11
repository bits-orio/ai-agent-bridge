package agent

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// ellipsis ends every cut the service makes, so a player can tell a line
// that was cut from one that simply ends there. Every font the game draws
// chat in carries it (data/core/fonts, checked on 2.0.77).
const ellipsis = "…"

// clipRichText cuts s to at most limit runes, the ellipsis and any closing
// tags it adds included, so clipping its own output again changes nothing.
//
// The cut lands between words (wordCut) and ends with an ellipsis. Questions
// 37 and 38 on the live server, 2026-10-11, were cut at a plain rune count and
// ended "Name a couple and" and "Research: team-4 77% vs your", which reads as
// an answer that simply stopped. A word boundary is worth losing up to half
// the limit for and no more: when the nearest one would keep less, the cut
// falls at the rune instead, so one long word late in a line cannot take the
// whole line with it.
//
// The result is still something Factorio renders as rich text. Factorio draws
// a whole chat line raw, tags and all, if any tag in it is malformed.
// Question 91 on the rig, 2026-09-15, answered with every team name wrapped
// in its own [color=yellow]...[/color]; the shrinker cut the first line at
// "Team 19[" to fit the byte budget, and the player read
// "[color=yellow]Team 10[/color]" as text. Two things go wrong at a cut and
// both are repaired here: a cut inside a tag leaves a dangling "[...", which
// is dropped back to before its bracket (tidyCut), and a tag opened before
// the cut may have lost its closer, which is appended, innermost first, for
// the two kinds of tag that have one, color and font. gps, img and the like
// are single tags with no closer and need nothing.
func clipRichText(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	if limit < 1 {
		return ""
	}
	floor := (limit + 1) / 2
	for keep := limit - 1; ; {
		kept := tidyCut(wordCut(r, keep, floor))
		closers := closersFor(kept)
		n := utf8.RuneCountInString(kept)
		if n+1+utf8.RuneCountInString(closers) <= limit {
			return kept + ellipsis + closers
		}
		// The closers did not fit beside the text: cut back to an earlier
		// boundary. kept shrinks every time round, so this ends, at worst
		// with nothing kept and only the ellipsis.
		keep = n - 1
	}
}

// wordCut is the longest prefix of r, at most n runes and at least floor,
// that ends between two words: before a space or a tag, after a tag, or
// beside a CJK character, a script written without spaces where a cut between
// any two characters leaves every word whole. With no such place in reach it
// is the first n runes: a word that runs from below floor past n has nowhere
// else to be cut that keeps enough.
func wordCut(r []rune, n, floor int) string {
	if n >= len(r) {
		return string(r)
	}
	if n <= 0 {
		return ""
	}
	for k := n; k > 0 && k >= floor; k-- {
		if between(r[k-1], r[k]) {
			return string(r[:k])
		}
	}
	return string(r[:n])
}

// between reports whether a cut between prev and next leaves both words
// whole.
func between(prev, next rune) bool {
	return unicode.IsSpace(prev) || unicode.IsSpace(next) ||
		prev == ']' || next == '[' ||
		spaceless(prev) || spaceless(next)
}

// spaceless reports whether c belongs to a script written without spaces
// between its words: Chinese and Japanese, and their punctuation.
func spaceless(c rune) bool {
	return unicode.In(c, unicode.Han, unicode.Hiragana, unicode.Katakana) ||
		(c >= 0x3000 && c <= 0x303f)
}

// tidyCut makes a cut fit to end on: no tag torn open at the end, and no
// space or dangling comma left in front of the ellipsis.
func tidyCut(s string) string {
	for {
		s = strings.TrimRight(s, " ,;:")
		// A "[" after the last "]" is a tag the cut landed inside.
		open := strings.LastIndex(s, "[")
		if open < 0 || open < strings.LastIndex(s, "]") {
			return s
		}
		s = s[:open]
	}
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
