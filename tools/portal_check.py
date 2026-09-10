#!/usr/bin/env python3
"""Report where the live mod-portal page differs from what this repo tracks.

Someone edits the page by hand on the site and the repo silently stops being
the source of truth. This says so. Read-only: needs no API key, sends nothing.

Exit 0 when the live page matches, 1 when it has drifted.

Run from the repo root:  python3 tools/portal_check.py
"""

import json
import sys
import time
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
# The portal caches this endpoint, so a plain read right after a sync still
# returns the old page and everything looks like drift. The query parameter is
# ignored by the API but defeats the cache.
API = "https://mods.factorio.com/api/mods/{}/full?_={}"


def main():
    meta = json.loads((ROOT / "tools" / "portal_meta.json").read_text())
    mod = meta["mod"]
    req = urllib.request.Request(
        API.format(mod, int(time.time() * 1000)),
        headers={"Cache-Control": "no-cache", "Pragma": "no-cache"},
    )
    with urllib.request.urlopen(req, timeout=30) as r:
        live = json.load(r)

    lic = live.get("license")
    drift = []

    def compare(field, tracked, actual):
        if tracked is None:
            return
        if (tracked or "") != (actual or ""):
            drift.append((field, tracked, actual))

    compare("title", meta.get("title"), live.get("title"))
    compare("summary", meta.get("summary"), live.get("summary"))
    compare("category", meta.get("category"), live.get("category"))
    compare("homepage", meta.get("homepage"), live.get("homepage"))
    compare("source_url", meta.get("source_url"), live.get("source_url"))
    compare("license", meta.get("license"),
            lic.get("id") if isinstance(lic, dict) else lic)

    tracked_tags = sorted(meta.get("tags") or [])
    live_tags = sorted(t.get("name") if isinstance(t, dict) else t
                       for t in (live.get("tags") or []))
    if tracked_tags != live_tags:
        drift.append(("tags", tracked_tags, live_tags))

    tracked_desc = (ROOT / "docs" / "portal.md").read_text()
    if tracked_desc.strip() != (live.get("description") or "").strip():
        drift.append(("description",
                      f"{len(tracked_desc)} chars in docs/portal.md",
                      f"{len(live.get('description') or '')} chars live"))

    faq_file = ROOT / "docs" / "portal-faq.md"
    if faq_file.exists():
        if faq_file.read_text().strip() != (live.get("faq") or "").strip():
            drift.append(("faq", "docs/portal-faq.md", "live differs"))

    if not drift:
        print(f"portal check OK, {mod} matches this repo")
        return 0
    print(f"portal has DRIFTED from this repo ({len(drift)} field(s)):", file=sys.stderr)
    for field, tracked, actual in drift:
        print(f"  {field}:", file=sys.stderr)
        print(f"      repo: {tracked}", file=sys.stderr)
        print(f"      live: {actual}", file=sys.stderr)
    print("\n  run tools/sync_portal_details.sh to put the repo's version back",
          file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())
