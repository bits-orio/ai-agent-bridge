// Salvage: the slips a weaker host makes in submit_answer, repaired instead
// of refused. submit_answer has nine optional fields and only shape is
// required, so a host that puts a summary's prose in text, or a notice's
// line in lines, sends an answer the game cannot show although every word
// of it arrived. Refusing it sent one such host round the loop until its
// rounds ran out, the same refusal six times, and the player got a notice.
//
// Only the obvious cases are repaired: the shape is the model's own, and
// the text is moved into the field that shape reads. A submission with
// nothing visible anywhere is still refused, since there is nothing to move.

package agent

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// salvage returns a, repaired, with a note saying what was moved where, and
// ok true; or a unchanged and ok false when the shape's own field already
// holds something or nothing anywhere can stand in for it.
func salvage(a Artifact) (Artifact, string, bool) {
	switch a.Shape {
	case ShapeSummary:
		if len(shown(a.Lines)) > 0 {
			return a, "", false
		}
		var note string
		switch {
		case visible(a.Text):
			a.Lines, note = proseLines(a.Text), "summary: lines from text"
		case len(shown(a.Items)) > 0:
			a.Lines, note = shown(a.Items), "summary: lines from items"
		case visible(a.Title):
			// The title alone was the answer; printed as both it would say
			// the same thing twice.
			a.Lines, a.Title, note = []string{a.Title}, "", "summary: lines from title"
		default:
			return a, "", false
		}
		a.Text, a.Items = "", nil
		return a, note, true
	case ShapeNotice:
		if visible(a.Text) {
			return a, "", false
		}
		var note string
		switch {
		case len(shown(a.Lines)) > 0:
			a.Text, note = clipRichText(flat(strings.Join(shown(a.Lines), " ")), MaxCellChars), "notice: text from lines"
		case visible(a.Title):
			a.Text, a.Title, note = a.Title, "", "notice: text from title"
		default:
			return a, "", false
		}
		a.Lines = nil
		return a, note, true
	case ShapeList:
		if len(shown(a.Items)) > 0 || len(shown(a.Lines)) == 0 {
			return a, "", false
		}
		a.Items, a.Lines = shown(a.Lines), nil
		return a, "list: items from lines", true
	}
	return a, "", false
}

// sentence is one sentence with the space after it, or the tail of a text
// that does not end in one.
var sentence = regexp.MustCompile(`[^.!?]*[.!?]+(\s+|$)|[^.!?]+$`)

// proseLines splits a summary's prose into lines that fit the summary's
// caps, since the service made the split and the model cannot be asked to
// fix it: each paragraph its own line, a paragraph over the cell cap packed
// sentence by sentence, and whatever does not fit in the last line cut
// there with the ellipsis.
func proseLines(text string) []string {
	var lines []string
	for _, para := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		para = flat(para)
		if !visible(para) {
			continue
		}
		lines = append(lines, pack(sentence.FindAllString(para, -1), MaxCellChars)...)
	}
	if len(lines) > MaxSummaryLines {
		rest := strings.Join(lines[MaxSummaryLines-1:], " ")
		lines = append(lines[:MaxSummaryLines-1], rest)
	}
	for i, line := range lines {
		lines[i] = clipRichText(line, MaxCellChars)
	}
	return lines
}

// pack joins pieces into lines of at most limit runes, a piece too long for
// a line of its own taking one by itself (clipped later).
func pack(pieces []string, limit int) []string {
	var lines []string
	var cur strings.Builder
	for _, p := range pieces {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if cur.Len() > 0 && utf8.RuneCountInString(cur.String())+1+utf8.RuneCountInString(p) > limit {
			lines = append(lines, cur.String())
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteByte(' ')
		}
		cur.WriteString(p)
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}
	return lines
}
