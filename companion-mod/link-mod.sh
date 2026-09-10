#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
INFO="$SCRIPT_DIR/info.json"

NAME=$(grep -o '"name": *"[^"]*"' "$INFO" | head -1 | sed 's/.*"\([^"]*\)"/\1/')
VERSION=$(grep -o '"version": *"[^"]*"' "$INFO" | head -1 | sed 's/.*"\([^"]*\)"/\1/')
LINK_NAME="${NAME}_${VERSION}"

MOD_DIRS=(
    "$HOME/factorio/mods"
    "$HOME/.factorio/mods"
)

for dir in "${MOD_DIRS[@]}"; do
    if [[ ! -d "$dir" ]]; then
        echo "Skipping $dir (not found)"
        continue
    fi

    # Remove old symlinks pointing to this mod
    for link in "$dir/${NAME}_"*; do
        if [[ -L "$link" ]]; then
            echo "Removing old link: $link"
            rm "$link"
        fi
    done

    # Remove old packaged zips of this mod
    for zip in "$dir/${NAME}_"*.zip; do
        if [[ -f "$zip" ]]; then
            echo "Removing old zip: $zip"
            rm "$zip"
        fi
    done

    ln -s "$SCRIPT_DIR" "$dir/$LINK_NAME"
    echo "Created: $dir/$LINK_NAME -> $SCRIPT_DIR"
done
