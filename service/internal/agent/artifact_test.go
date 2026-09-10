package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// The wire shape is the contract with the companion: one flat object whose
// shape picks the fields.
func TestArtifactJSONPerShape(t *testing.T) {
	for _, tc := range []struct {
		name     string
		artifact Artifact
		want     string
	}{
		{
			"summary",
			Artifact{Shape: ShapeSummary, Title: "Forces", Lines: []string{"two forces"}},
			`{"lines":["two forces"],"shape":"summary","title":"Forces"}`,
		},
		{
			"notice",
			Artifact{Shape: ShapeNotice, Text: "no rocket yet", Level: LevelWarning},
			`{"level":"warning","shape":"notice","text":"no rocket yet"}`,
		},
		{
			"list",
			Artifact{Shape: ShapeList, Items: []string{"iron-plate", "copper-plate"}},
			`{"items":["iron-plate","copper-plate"],"shape":"list"}`,
		},
		{
			"table",
			Artifact{Shape: ShapeTable, Columns: []string{"item", "rate"}, Rows: [][]string{{"iron-plate", "42"}}},
			`{"columns":["item","rate"],"rows":[["iron-plate","42"]],"shape":"table"}`,
		},
		{
			"comparison",
			Artifact{Shape: ShapeComparison, Columns: []string{"player", "enemy"}, Pairs: []Pair{{Label: "plates", A: "42", B: "7"}}},
			`{"columns":["player","enemy"],"rows":[{"label":"plates","a":"42","b":"7"}],"shape":"comparison"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(tc.artifact)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

// Rows carry two different shapes under one name, so both have to survive the
// round trip the model's submission takes.
func TestArtifactRowsRoundTrip(t *testing.T) {
	table := Artifact{Shape: ShapeTable, Columns: []string{"a"}, Rows: [][]string{{"1"}, {"2"}}}
	var backTable Artifact
	raw, _ := json.Marshal(table)
	if err := json.Unmarshal(raw, &backTable); err != nil {
		t.Fatalf("table: %v", err)
	}
	if len(backTable.Rows) != 2 || backTable.Rows[1][0] != "2" {
		t.Errorf("table rows lost: %+v", backTable)
	}

	comparison := Artifact{Shape: ShapeComparison, Columns: []string{"a", "b"}, Pairs: []Pair{{Label: "l", A: "1", B: "2"}}}
	var backComparison Artifact
	raw, _ = json.Marshal(comparison)
	if err := json.Unmarshal(raw, &backComparison); err != nil {
		t.Fatalf("comparison: %v", err)
	}
	if len(backComparison.Pairs) != 1 || backComparison.Pairs[0].B != "2" {
		t.Errorf("comparison rows lost: %+v", backComparison)
	}
}

func TestValidateClipsEveryShape(t *testing.T) {
	long := strings.Repeat("y", 500)

	items := make([]string, 25)
	for i := range items {
		items[i] = "item"
	}
	got, err := validate(Artifact{Shape: ShapeList, Items: items})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got.Items) != MaxListItems {
		t.Errorf("items = %d, want %d", len(got.Items), MaxListItems)
	}

	rows := make([][]string, 20)
	for i := range rows {
		rows[i] = []string{long, "b", "c", "d", "e", "f", "g"}
	}
	got, err = validate(Artifact{Shape: ShapeTable, Columns: []string{"1", "2", "3", "4", "5", "6"}, Rows: rows})
	if err != nil {
		t.Fatalf("table: %v", err)
	}
	if len(got.Columns) != MaxTableColumns {
		t.Errorf("columns = %d, want %d", len(got.Columns), MaxTableColumns)
	}
	if len(got.Rows) != MaxTableRows {
		t.Errorf("rows = %d, want %d", len(got.Rows), MaxTableRows)
	}
	for _, row := range got.Rows {
		if len(row) != MaxTableColumns {
			t.Fatalf("row is %d wide, want %d", len(row), MaxTableColumns)
		}
		if len([]rune(row[0])) != MaxCellChars {
			t.Errorf("cell is %d characters, want %d", len([]rune(row[0])), MaxCellChars)
		}
	}

	pairs := make([]Pair, 9)
	for i := range pairs {
		pairs[i] = Pair{Label: "l", A: "1", B: "2"}
	}
	got, err = validate(Artifact{Shape: ShapeComparison, Columns: []string{"a", "b", "c"}, Pairs: pairs})
	if err != nil {
		t.Fatalf("comparison: %v", err)
	}
	if len(got.Columns) != 2 || len(got.Pairs) != MaxComparisonRows {
		t.Errorf("comparison = %d columns and %d rows, want 2 and %d", len(got.Columns), len(got.Pairs), MaxComparisonRows)
	}
}

// A row a model wrote across two lines must not become two chat lines: the
// companion prints one line per row.
func TestValidateFlattensNewlines(t *testing.T) {
	got, err := validate(Artifact{Shape: ShapeSummary, Lines: []string{"one\nSomeone: fake line"}})
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if strings.Contains(got.Lines[0], "\n") {
		t.Errorf("newline survived clipping: %q", got.Lines[0])
	}
}

func TestValidateRejects(t *testing.T) {
	for _, tc := range []struct {
		name     string
		artifact Artifact
		says     string
	}{
		{"unknown shape", Artifact{Shape: "chart"}, "not a shape"},
		{"empty summary", Artifact{Shape: ShapeSummary}, "at least one entry"},
		{"notice with no text", Artifact{Shape: ShapeNotice}, "needs text"},
		{"bad level", Artifact{Shape: ShapeNotice, Text: "x", Level: "urgent"}, "not a level"},
		{"table with no columns", Artifact{Shape: ShapeTable, Rows: [][]string{{"a"}}}, "at least one column"},
		{"table with no rows", Artifact{Shape: ShapeTable, Columns: []string{"a"}}, "at least one row"},
		{"one-column comparison", Artifact{Shape: ShapeComparison, Columns: []string{"a"}, Pairs: []Pair{{Label: "l"}}}, "exactly two"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validate(tc.artifact)
			if err == nil {
				t.Fatal("expected a validation error")
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("error %q does not say %q", err, tc.says)
			}
		})
	}
}

// The one-line form is what the log and the next question's memory carry.
func TestArtifactLine(t *testing.T) {
	for _, tc := range []struct {
		artifact Artifact
		want     string
	}{
		{Artifact{Shape: ShapeSummary, Lines: []string{"first", "second"}}, "first"},
		{Artifact{Shape: ShapeNotice, Text: "careful"}, "careful"},
		{Artifact{Shape: ShapeList, Items: []string{"one"}}, "one"},
		{Artifact{Shape: ShapeTable, Rows: [][]string{{"a", "b"}}}, "a b"},
		{Artifact{Shape: ShapeComparison, Pairs: []Pair{{Label: "l", A: "1", B: "2"}}}, "l: 1 vs 2"},
		{Artifact{Shape: ShapeSummary, Title: "only a title"}, "only a title"},
	} {
		if got := tc.artifact.Line(); got != tc.want {
			t.Errorf("Line() = %q, want %q", got, tc.want)
		}
	}
}
