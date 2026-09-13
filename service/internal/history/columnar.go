package history

import "strings"

// tsvRow joins fields with a tab, the delimiter docs/design/phase4-spec.md
// section 13 picks because it cannot appear in a Factorio force or player
// display name: no value here ever needs quoting. Per that section's
// sanitising rule, any tab already inside a value (a chat message can carry
// one; nothing else read by this package can) is replaced with a single
// space first, so a row never holds more tabs than there are fields minus
// one.
func tsvRow(fields ...string) string {
	clean := make([]string, len(fields))
	for i, f := range fields {
		clean[i] = strings.ReplaceAll(f, "\t", " ")
	}
	return strings.Join(clean, "\t")
}

// columnar builds the shape every one of this package's tools returns
// (section 13): a header line naming the columns, then one already
// tab-joined line per row, nothing wrapped around either. None of these
// tools sweeps an axis, so none carries the sweep envelope: no axis, no
// shown, no total. A header with no rows after it is a valid, empty
// result, not an error.
func columnar(header string, rows []string) string {
	if len(rows) == 0 {
		return header
	}
	return header + "\n" + strings.Join(rows, "\n")
}
