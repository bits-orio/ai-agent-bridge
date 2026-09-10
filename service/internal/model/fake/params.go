// The small text readers that turn one keyword match into a tool argument.
// Each pulls one value out of the question text near the word that matched
// in keywords.go; none of them decide which tool to call.

package fake

import "strings"

// itemOf reads the item name out of "rate of iron-plate" or "rate for
// copper-plate", skipping an article on the way.
func itemOf(text string) string {
	fields := strings.Fields(strings.ToLower(text))
	for i, word := range fields {
		if word != "of" && word != "for" {
			continue
		}
		for _, candidate := range fields[i+1:] {
			candidate = strings.Trim(candidate, ".,?!\"'")
			switch candidate {
			case "", "the", "a", "an", "my", "our", "your":
				continue
			}
			return candidate
		}
	}
	return "iron-plate"
}

// techOf reads the technology name out of "technology automation-2", "tech
// automation-2" or "tech status of automation-2": the first field after
// whichever trigger word matched that is not one of the words a player drops
// between the trigger and the name they mean.
func techOf(text string) string {
	fields := strings.Fields(strings.ToLower(text))
	for i, word := range fields {
		trimmed := strings.Trim(word, ".,?!\"'")
		if trimmed != "technology" && trimmed != "tech" {
			continue
		}
		for _, candidate := range fields[i+1:] {
			candidate = strings.Trim(candidate, ".,?!\"'")
			if !techFiller(candidate) {
				return candidate
			}
		}
	}
	return ""
}

// techFiller is the small set of words that sit between "tech" and the
// technology name without naming anything: "tech status of automation-2"
// means the same technology as "tech automation-2".
func techFiller(word string) bool {
	switch word {
	case "", "status", "of", "for", "the", "a", "an", "is":
		return true
	}
	return false
}

// entityNameOf reads the entity name out of "how many biter-spawner are
// there": the field right after the two-word trigger "how many".
func entityNameOf(text string) string {
	fields := strings.Fields(strings.ToLower(text))
	for i := 0; i+2 < len(fields); i++ {
		if fields[i] == "how" && fields[i+1] == "many" {
			return strings.Trim(fields[i+2], ".,?!\"'")
		}
	}
	return ""
}
