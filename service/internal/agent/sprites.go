// Sprite-only rich text. In chat, [item=iron-plate] draws the icon and then
// "Item: Iron plate"; [img=item.iron-plate] draws the icon alone, which is
// what the owner wants every answer to use (https://wiki.factorio.com/rich_text).
// The prompt asks the model for the img form; this pass turns any named form
// it writes anyway into it, so the convention holds whatever model answers.

package agent

import (
	"regexp"
	"strings"
)

// namedTag matches the object tags that have an img equivalent. The class
// list is the wiki's: item, fluid, entity, technology, recipe, tile,
// virtual-signal, planet. gps, train, armor and the like have no sprite
// form and pass through.
var namedTag = regexp.MustCompile(`\[(item|fluid|entity|technology|recipe|tile|virtual-signal|planet)=([A-Za-z0-9_.-]+)(?:,quality=([A-Za-z0-9_-]+))?\]`)

// preferImg rewrites [class=name] into [img=class.name], and a quality
// suffix into its own quality icon beside it.
func preferImg(s string) string {
	if !strings.Contains(s, "[") {
		return s
	}
	return namedTag.ReplaceAllStringFunc(s, func(tag string) string {
		m := namedTag.FindStringSubmatch(tag)
		out := "[img=" + m[1] + "." + m[2] + "]"
		if m[3] != "" {
			out += "[img=quality." + m[3] + "]"
		}
		return out
	})
}
