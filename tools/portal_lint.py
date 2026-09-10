#!/usr/bin/env python3
"""Check the portal storefront against the house rules before publishing.

The mod portal renders Markdown but does not resolve relative paths, so a
link like docs/COMPAT.md becomes mods.factorio.com/mod/docs/COMPAT.md and
404s. That has bitten every mod in this family at least once, so the check
runs in CI rather than living in someone's head.

Reads docs/portal.md and tools/portal_meta.json. Exits non-zero on any
violation, printing every problem rather than stopping at the first.

Run from the repo root:  python3 tools/portal_lint.py
"""

import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
EMOJI_CAP = 3
SUMMARY_MAX = 500

# Live portal vocabulary. The wiki's list is stale (it omits planets and
# character), so this was recovered from the portal API itself.
CATEGORIES = {
    "no-category", "content", "overhaul", "tweaks", "utilities",
    "scenarios", "mod-packs", "localizations", "internal",
}
TAGS = {
    "transportation", "logistics", "trains", "combat", "armor", "enemies",
    "environment", "mining", "fluids", "logistic-network", "circuit-network",
    "manufacturing", "power", "storage", "blueprints", "cheats", "planets",
    "character",
}

EMOJI = re.compile(
    "[\U0001F1E0-\U0001F1FF\U0001F300-\U0001F5FF\U0001F600-\U0001F64F"
    "\U0001F680-\U0001F6FF\U0001F900-\U0001F9FF\U0001FA00-\U0001FAFF"
    "☀-➿⬀-⯿]",
    re.UNICODE,
)


def main():
    errors = []
    desc = (ROOT / "docs" / "portal.md").read_text()
    meta = json.loads((ROOT / "tools" / "portal_meta.json").read_text())

    for target in re.findall(r"\]\(([^)]+)\)", desc):
        if not target.startswith("http"):
            errors.append(f"relative link will 404 on the portal: {target}")

    if "![](<>)" in desc:
        errors.append("empty image tag ![](<>) present")

    # Every page in the family carries the same two badges, so a reader landing
    # on any mod finds the repo and the community in the same place.
    # An unprompted plea not to harass makes a reader assume there is a reason
    # and bounce. The AI disclosure alone does not invite that.
    if ("Developed with AI coding assistants alongside human review and in-game testing."
            not in desc):
        errors.append("the AI disclosure sentence is missing or reworded, it must be "
                      "identical across every mod in the family")

    lowered = desc.lower()
    for plea in ("keep the hate off", "human on the other side", "don't be rude",
                 "do not be rude", "please keep it kind", "anti-human"):
        if plea in lowered:
            errors.append(f"anti-harassment plea in the description: {plea!r}, "
                          "the AI disclosure stands alone")

    if "img.shields.io/badge/Discord" not in desc:
        errors.append("missing the Discord badge")
    if "img.shields.io/badge/GitHub" not in desc:
        errors.append("missing the GitHub badge")

    found = EMOJI.findall(desc)
    if len(found) > EMOJI_CAP:
        errors.append(f"{len(found)} emoji, cap is {EMOJI_CAP}: {''.join(found[:10])}")

    summary = meta.get("summary", "")
    if not summary:
        errors.append("summary is empty")
    if len(summary) > SUMMARY_MAX:
        errors.append(f"summary is {len(summary)} chars, max {SUMMARY_MAX}")
    for hedge in ("WIP", "may have bugs", "please report", "beta"):
        if hedge.lower() in summary.lower():
            errors.append(f"summary contains the hedge {hedge!r}, move it to Status")

    category = meta.get("category")
    if category not in CATEGORIES:
        errors.append(f"category {category!r} is not a valid portal category")
    if category == "no-category":
        errors.append("category is no-category, every mod must have a real one")

    tags = meta.get("tags") or []
    waiver = meta.get("tags_intentionally_empty")
    if not tags and not waiver:
        errors.append("tag list is empty, the mod is invisible to every tag filter. "
                      "If no tag in the vocabulary is honestly true, say so explicitly "
                      "in a tags_intentionally_empty field.")
    if tags and waiver:
        errors.append("tags_intentionally_empty is set but tags are present, drop one")
    for tag in tags:
        if tag not in TAGS:
            errors.append(f"tag {tag!r} is not in the live portal vocabulary")

    if not (meta.get("source_url") or "").startswith("http"):
        errors.append("source_url must be the GitHub repo URL")
    if "discord.gg/" not in (meta.get("homepage") or ""):
        errors.append("homepage must be the canonical Discord invite")

    if errors:
        print(f"portal lint FAILED ({len(errors)} problem(s)):", file=sys.stderr)
        for e in errors:
            print(f"  - {e}", file=sys.stderr)
        return 1
    print(f"portal lint OK, {len(desc)} chars, {len(found)} emoji, "
          f"summary {len(summary)} chars, {len(tags)} tags")
    return 0


if __name__ == "__main__":
    sys.exit(main())
