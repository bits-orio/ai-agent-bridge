package tools

import (
	"encoding/json"
	"testing"
)

func TestDeclaresFindsAPropertyWhateverBuiltTheSchema(t *testing.T) {
	schema := ObjectSchema(map[string]any{
		"force": map[string]any{"type": "string"},
		"item":  map[string]any{"type": "string"},
	}, "item")

	if !Declares(schema, "force") {
		t.Error("force is a property of this schema")
	}
	if Declares(schema, "surface") {
		t.Error("surface is not")
	}
	if Declares(map[string]any{}, "force") || Declares(nil, "force") {
		t.Error("a schema with no properties declares nothing")
	}
}

func TestFillString(t *testing.T) {
	for _, tc := range []struct {
		name string
		args string
		want string
	}{
		{"no arguments at all", "", `{"force":"player"}`},
		{"an empty object", `{}`, `{"force":"player"}`},
		{"another argument", `{"item":"iron-plate"}`, `{"force":"player","item":"iron-plate"}`},
		{"a force already set", `{"force":"enemy"}`, `{"force":"enemy"}`},
		{"an empty force", `{"force":""}`, `{"force":"player"}`},
		{"a force of the wrong type", `{"force":7}`, `{"force":"player"}`},
		{"arguments that are not an object", `["force"]`, `["force"]`},
	} {
		got := FillString(json.RawMessage(tc.args), "force", "player")
		if !sameJSON(t, got, tc.want) {
			t.Errorf("%s: %s became %s, want %s", tc.name, tc.args, got, tc.want)
		}
	}

	if got := FillString(json.RawMessage(`{}`), "force", ""); string(got) != `{}` {
		t.Errorf("an empty value filled anyway: %s", got)
	}
}

func sameJSON(t *testing.T, got json.RawMessage, want string) bool {
	t.Helper()
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		t.Fatalf("decode %s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &b); err != nil {
		t.Fatalf("decode %s: %v", want, err)
	}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
