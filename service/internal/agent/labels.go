// Force labels as a substitution, not a prompt paragraph. Players say
// "Team Ace"; tools and the model say "team-1". The label is swapped for
// the force name in the question before the model reads it, and the
// companion swaps names back into labels when it renders an answer, so the
// model never spends a token learning the mapping.
//
// Only force names that are not plain words take part: "team-1" does,
// "player" does not, or "the player" in every answer would come out as a
// label. The companion applies the same rule on the way out.

package agent

import (
	"regexp"
	"sort"
	"strings"
)

// substitutable says whether a force name may take part in substitution:
// it has to carry a digit, a hyphen or an underscore.
func substitutable(name string) bool {
	return strings.ContainsAny(name, "0123456789-_")
}

// substituteLabels replaces every label (and, for a "Team X" label, the
// bare X when it is three characters or longer) with its force name,
// whole words only, case-insensitively, longest label first.
func substituteLabels(text string, labels []ForceLabel) string {
	if len(labels) == 0 || text == "" {
		return text
	}
	type rule struct{ from, to string }
	var rules []rule
	for _, l := range labels {
		if l.Label == "" || !substitutable(l.Name) {
			continue
		}
		rules = append(rules, rule{l.Label, l.Name})
		if rest, ok := teamRemainder(l.Label); ok {
			rules = append(rules, rule{rest, l.Name})
		}
	}
	sort.SliceStable(rules, func(i, j int) bool { return len(rules[i].from) > len(rules[j].from) })
	for _, r := range rules {
		re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(r.from) + `\b`)
		text = re.ReplaceAllLiteralString(text, r.to)
	}
	return text
}

// teamRemainder is the X of a "Team X" label when X is long enough to be a
// name on its own.
func teamRemainder(label string) (string, bool) {
	rest := strings.TrimSpace(label)
	if len(rest) < 5 || !strings.EqualFold(rest[:5], "team ") {
		return "", false
	}
	rest = strings.TrimSpace(rest[5:])
	if len(rest) < 3 {
		return "", false
	}
	return rest, true
}
