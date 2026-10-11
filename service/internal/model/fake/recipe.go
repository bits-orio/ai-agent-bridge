// Pirion's question answered the way a real model reads recipe_needs: the
// iron and copper plate a minute, and the belts of each, straight off the
// tool's own rows. The rest of the fake quotes the first 300 runes of a
// result, which for recipe_needs is the targets and never reaches a row.

package fake

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// scienceBelts is the phrase that asks Pirion's question.
const scienceBelts = "science belts"

// plateItems are the rows the answer quotes, in this order.
var plateItems = []string{"iron-plate", "copper-plate"}

// plateTable turns a recipe_needs result into a table with one row per
// plate it lists, or nil when it lists neither, so the caller quotes the
// raw result instead and a harness sees what came back.
func plateTable(result string) map[string]any {
	var reply struct {
		Rows []struct {
			Item   string `json:"item"`
			PerMin any    `json:"per_min"`
			Belts  any    `json:"belts"`
		} `json:"rows"`
	}
	if json.Unmarshal([]byte(result), &reply) != nil {
		return nil
	}
	var rows [][]string
	for _, want := range plateItems {
		for _, row := range reply.Rows {
			if row.Item == want {
				rows = append(rows, []string{row.Item, figure(row.PerMin), figure(row.Belts)})
			}
		}
	}
	if len(rows) == 0 {
		return nil
	}
	return map[string]any{"shape": "table", "columns": []string{"item", "per_min", "belts"}, "rows": rows}
}

// figure is one wire figure as text: the companion sends a whole number as a
// number and a fraction as a short decimal string, "6.63".
func figure(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}
