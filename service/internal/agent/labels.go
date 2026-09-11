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
		for _, from := range aliases(l) {
			rules = append(rules, rule{from, l.Name})
		}
	}
	sort.SliceStable(rules, func(i, j int) bool { return len(rules[i].from) > len(rules[j].from) })
	for _, r := range rules {
		re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(r.from) + `\b`)
		text = re.ReplaceAllLiteralString(text, r.to)
	}
	return text
}

// aliases is every spelling a player may use for one force: the label, the
// X of a "Team X" label, the label and the force name with spaces for
// hyphens, and the label with the zeros stripped from a trailing number, so
// "Team 02", "team 2" and "team-2" all reach the model as team-2.
func aliases(l ForceLabel) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		key := strings.ToLower(s)
		if s == "" || seen[key] {
			return
		}
		seen[key] = true
		out = append(out, s)
	}
	add(l.Label)
	if rest, ok := teamRemainder(l.Label); ok {
		add(rest)
	}
	add(strings.ReplaceAll(l.Label, "-", " "))
	add(strings.ReplaceAll(l.Name, "-", " "))
	if m := trailingNumber.FindStringSubmatch(l.Label); m != nil && m[2] != m[3] {
		add(m[1] + m[3])
	}
	return out
}

// trailingNumber splits "Team 02" into "Team ", "02" and "2".
var trailingNumber = regexp.MustCompile(`^(.*?\s)0*((?:0*)(\d+))$`)

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
