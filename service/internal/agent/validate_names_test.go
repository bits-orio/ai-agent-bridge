package agent

import (
	"strings"
	"testing"
)

// A column with no name is refused, so the model names it instead of the
// game printing "  vs  Team 01" as a live answer once did.
func TestValidateRefusesEmptyColumnNames(t *testing.T) {
	_, err := validate(Artifact{Shape: ShapeComparison, Columns: []string{"", "team-1"}, Pairs: []Pair{{Label: "x", A: "1", B: "2"}}})
	if err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Errorf("blank comparison column accepted: %v", err)
	}
	_, err = validate(Artifact{Shape: ShapeComparison, Columns: []string{"team-2", "  "}, Pairs: []Pair{{Label: "x", A: "1", B: "2"}}})
	if err == nil {
		t.Error("whitespace comparison column accepted")
	}
	_, err = validate(Artifact{Shape: ShapeTable, Columns: []string{"a", ""}, Rows: [][]string{{"1", "2"}}})
	if err == nil || !strings.Contains(err.Error(), "needs a name") {
		t.Errorf("blank table column accepted: %v", err)
	}
	if _, err := validate(Artifact{Shape: ShapeComparison, Columns: []string{"team-1", "team-2"}, Pairs: []Pair{{Label: "x", A: "1", B: "2"}}}); err != nil {
		t.Errorf("a named comparison must pass: %v", err)
	}
}
