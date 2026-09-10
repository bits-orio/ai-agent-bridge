#!/usr/bin/env bash
# Deploy the whole mod-portal page from files tracked in this repo, so the page
# is reproducible: if anyone edits it by hand on the site, re-running this puts
# it back exactly as the repo describes it.
#
#   docs/portal.md          -> description   (the storefront; README keeps the depth)
#   docs/portal-faq.md      -> faq           (optional; skipped if absent)
#   tools/portal_meta.json  -> title, summary, category, tags, license,
#                              homepage, source_url, deprecated
#
# The gallery images and the thumbnail are NOT settable through the API and so
# cannot be tracked here -- they stay manual on the site.
#
# Usage: tools/sync_portal_details.sh [--dry-run|--check]
#   --dry-run  print what would be sent, send nothing
#   --check    diff the live page against these files, send nothing (no key needed)
# Env: FACTORIO_API_KEY -- needs the "ModPortal: Edit Mods" scope, which is
#      separate from the "Upload Mods" scope used for releases.
#
# API: https://wiki.factorio.com/Mod_details_API  (POST /api/v2/mods/edit_details)
set -euo pipefail
cd "$(dirname "$0")/.."

command -v jq >/dev/null || { echo "jq required" >&2; exit 1; }
META=tools/portal_meta.json
MOD=$(jq -r '.mod // empty' "$META")
[[ -n "$MOD" ]] || { echo "portal_meta.json has no \"mod\" field" >&2; exit 1; }

if [[ "${1:-}" == "--check" ]]; then
    exec python3 tools/portal_check.py
fi

# House rules are enforced here, not in review: a relative link 404s silently on
# the portal and nobody notices for weeks.
python3 tools/portal_lint.py

ARGS=( -F "mod=${MOD}" -F "description=<docs/portal.md" )
[[ -f docs/portal-faq.md ]] && ARGS+=( -F "faq=<docs/portal-faq.md" )

for field in title summary category license homepage source_url; do
    value=$(jq -r --arg f "$field" '.[$f] // empty' "$META")
    [[ -n "$value" ]] && ARGS+=( --form-string "${field}=${value}" )
done
# deprecated is a bool: only send it when explicitly set.
dep=$(jq -r '.deprecated // empty' "$META")
[[ -n "$dep" ]] && ARGS+=( --form-string "deprecated=${dep}" )
# tags is multi-valued: one repeated form field per tag.
while read -r tag; do
    [[ -n "$tag" ]] && ARGS+=( --form-string "tags=${tag}" )
done < <(jq -r '.tags[]? // empty' "$META")

if [[ "${1:-}" == "--dry-run" ]]; then
    printf 'would send:\n'; printf '  %s\n' "${ARGS[@]}"; exit 0
fi

: "${FACTORIO_API_KEY:?FACTORIO_API_KEY env var not set}"
RESP=$(curl -sS -w $'\nHTTP_CODE:%{http_code}' \
    -H "Authorization: Bearer ${FACTORIO_API_KEY}" \
    "${ARGS[@]}" \
    "https://mods.factorio.com/api/v2/mods/edit_details")

HTTP=$(echo "$RESP" | tail -n1 | cut -d: -f2)
BODY=$(echo "$RESP" | sed '$d')
echo "edit_details HTTP ${HTTP}"
echo "$BODY"

if [[ "$HTTP" -lt 200 || "$HTTP" -ge 300 ]]; then
    echo "edit_details failed -- a 403 usually means the API key lacks the" >&2
    echo "'ModPortal: Edit Mods' scope (the release key only has Upload)." >&2
    exit 1
fi
[[ "$(echo "$BODY" | jq -r '.success // false')" == "true" ]] \
    || { echo "edit_details returned non-success" >&2; exit 1; }
echo "portal details synced for ${MOD}"
