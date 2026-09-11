package agent

import "testing"

func TestPreferImgRewritesNamedTags(t *testing.T) {
	for in, want := range map[string]string{
		"[item=iron-plate] 15/min":                                "[img=item.iron-plate] 15/min",
		"[fluid=crude-oil] and [entity=assembling-machine-2]":     "[img=fluid.crude-oil] and [img=entity.assembling-machine-2]",
		"[technology=logistics-2] [planet=nauvis] [tile=grass-3]": "[img=technology.logistics-2] [img=planet.nauvis] [img=tile.grass-3]",
		"[virtual-signal=signal-A] [recipe=basic-oil-processing]": "[img=virtual-signal.signal-A] [img=recipe.basic-oil-processing]",
		"[item=iron-plate,quality=rare]":                          "[img=item.iron-plate][img=quality.rare]",
		// Already the img form, a colour, a gps and plain text stay as they are.
		"[img=item.iron-plate] [color=red]low[/color] [gps=1,2] iron-plate": "[img=item.iron-plate] [color=red]low[/color] [gps=1,2] iron-plate",
		"no tags here": "no tags here",
	} {
		if got := preferImg(in); got != want {
			t.Errorf("preferImg(%q) = %q, want %q", in, got, want)
		}
	}
}
