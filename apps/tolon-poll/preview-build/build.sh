#!/usr/bin/env bash
# Builds the real Tolon UI for the browser preview served at /preview/, with a fake Tauri layer.
# usage: build.sh [--rev <commit>] [path-to-tolon-checkout]
# Without a path it clones https://github.com/danielpsf/tolon at a pinned commit (default below,
# override with --rev). The checkout is never modified:
# ui/ is copied to a temp dir. Needs git, node and npm. Output goes to ../web/preview/.
set -euo pipefail

REV=b178e08 # tolon main used for the published preview
if [[ "${1:-}" == "--rev" ]]; then
  REV="${2:?--rev needs a commit}"
  shift 2
fi

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT="$HERE/../web/preview"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

if [[ $# -ge 1 ]]; then
  SRC="$(cd "$1" && pwd)"
else
  git clone --quiet https://github.com/danielpsf/tolon "$TMP/tolon"
  git -C "$TMP/tolon" -c advice.detachedHead=false checkout --quiet "$REV"
  SRC="$TMP/tolon"
fi
[[ -d "$SRC/ui" ]] || { echo "no ui/ in $SRC" >&2; exit 1; }
echo "source revision: $(git -C "$SRC" rev-parse --short HEAD 2>/dev/null || echo unknown)"

# Copy ui/ without build output or git data. node_modules is reused when the checkout has it.
mkdir "$TMP/ui"
cp -R "$SRC/ui/." "$TMP/ui/"
rm -rf "$TMP/ui/dist" "$TMP/ui/.git"
cd "$TMP/ui"
[[ -d node_modules ]] || npm ci --no-audit --no-fund --loglevel=error

# A second entry page for the settings UI, so index.html stays free for the shell at /preview/.
cp index.html app.html
cp "$HERE/vite.preview.config.mjs" ./vite.preview.config.mjs
npx vite build --config vite.preview.config.mjs --base /preview/ --logLevel warn

node "$HERE/postbuild.mjs" dist

# Replace the generated parts of web/preview/ and add the hand-written shell and shim.
mkdir -p "$OUT"
rm -rf "$OUT/assets"
cp -R dist/assets "$OUT/assets"
cp dist/app.html dist/interrupt.html "$OUT/"
cp "$HERE"/shell/* "$OUT/"
echo "preview written to $OUT"
