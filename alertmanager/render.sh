#!/usr/bin/env sh
set -euo pipefail

TEMPLATE="/templates/alertmanager.tmpl.yml"
OUT="/config/alertmanager.yml"

if [ ! -f "$TEMPLATE" ]; then
  echo "template not found: $TEMPLATE" >&2
  exit 1
fi

mkdir -p /config
envsubst < "$TEMPLATE" > "$OUT"
echo "rendered $OUT"
