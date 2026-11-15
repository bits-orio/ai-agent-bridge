package agent

import (
	"strings"
	"testing"
)

// A8 (review findings). The companion refuses an artifact that would print
// nothing (artifact_check.lua, says_nothing and visible): blank cells and
// cells of nothing but tags that draw nothing. The service must refuse the
// same while the model still has a round, not hand the companion an answer
// it will bounce as bad_artifact after the rounds were paid for.
func TestValidateRefusesWhatTheCompanionWouldPrintAsNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		a    Artifact
		want string
	}{
		{"summary of spaces", Artifact{Shape: ShapeSummary, Lines: []string{"   ", "\t"}}, "needs at least one entry in lines"},
		{"summary of empty colour", Artifact{Shape: ShapeSummary, Lines: []string{"[color=red][/color]"}}, "needs at least one entry in lines"},
		{"summary of an empty font span", Artifact{Shape: ShapeSummary, Lines: []string{"[font=default-bold] [/font]"}}, "needs at least one entry in lines"},
		{"list of a tag naming nothing", Artifact{Shape: ShapeList, Items: []string{"[img=]", " "}}, "needs at least one entry in items"},
		{"notice of an empty colour", Artifact{Shape: ShapeNotice, Text: "[color=yellow] [/color]"}, "needs text"},
		{"table of blank rows", Artifact{Shape: ShapeTable, Columns: []string{"force"}, Rows: [][]string{{" "}, {"[color=red][/color]"}}}, "at least one row"},
		{"table with a column of only a tag", Artifact{Shape: ShapeTable, Columns: []string{"[color=red][/color]"}, Rows: [][]string{{"x"}}}, "needs a name"},
		{"comparison of blank rows", Artifact{Shape: ShapeComparison, Columns: []string{"a", "b"}, Pairs: []Pair{{Label: " ", A: "", B: "[font=default]"}}}, "at least one row"},
		{"art of spaces", Artifact{Shape: ShapeArt, Lines: []string{"    ", "  "}}, "at least one line"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validate(tc.a)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("validate(%+v) = %v, want an error saying %q", tc.a, err, tc.want)
			}
		})
	}
}

// What does draw is kept: a sprite on its own is an answer, and blank cells
// between real ones are dropped rather than printed as empty lines.
func TestValidateKeepsWhatDrawsAndDropsTheBlanks(t *testing.T) {
	got, err := validate(Artifact{Shape: ShapeSummary, Lines: []string{"[img=item.iron-plate]", "  ", "[color=red][/color]", "42 a minute"}})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if strings.Join(got.Lines, "|") != "[img=item.iron-plate]|42 a minute" {
		t.Errorf("lines = %q, want the sprite and the text, blanks dropped", got.Lines)
	}
	tbl, err := validate(Artifact{Shape: ShapeTable, Columns: []string{"force", "plates"}, Rows: [][]string{{" ", ""}, {"team-1", ""}}})
	if err != nil {
		t.Fatalf("validate table: %v", err)
	}
	if len(tbl.Rows) != 1 || tbl.Rows[0][0] != "team-1" {
		t.Errorf("rows = %q, want the one row with something in it, its blank cell kept", tbl.Rows)
	}
	for _, s := range []string{"x", "[img=item.iron-plate]", "[gps=1,2,nauvis]", "[color=red]!", "0"} {
		if !visible(s) {
			t.Errorf("visible(%q) = false", s)
		}
	}
	for _, s := range []string{"", " \t\n", "[color=red][/color]", "[font=default-bold][/font]", "[img=]", "[item= ]"} {
		if visible(s) {
			t.Errorf("visible(%q) = true", s)
		}
	}
}
