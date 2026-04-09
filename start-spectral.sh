#!/usr/bin/env bash
# Start Spectral Cloud server if not running, then open the web UI.

BINARY="/home/ghostdev/.local/bin/spectral-cloud"
DATA_DIR="/home/ghostdev/.spectral-cloud"
PORT=8080
URL="http://localhost:$PORT/ui/"

mkdir -p "$DATA_DIR"

if ! nc -z 127.0.0.1 "$PORT" 2>/dev/null; then
  nohup "$BINARY" \
    --data-dir "$DATA_DIR" \
    --port "$PORT" \
    >> "$DATA_DIR/server.log" 2>&1 &

  for i in $(seq 1 10); do
    sleep 0.5
    nc -z 127.0.0.1 "$PORT" 2>/dev/null && break
  done
fi

# Open as a standalone app window (no browser toolbar).
if command -v google-chrome &>/dev/null; then
  google-chrome --app="$URL" &
else
  xdg-open "$URL"
fi
