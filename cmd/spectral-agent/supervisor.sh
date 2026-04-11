#!/usr/bin/env bash
# spectral-agent supervisor - launches a local pool of agents and restarts any
# that crash.
#
# Usage:
#   SPECTRAL_API_KEY=tenant-key ./supervisor.sh [server_url] [tenant]
#
# Optional environment:
#   SPECTRAL_AGENT_BIN - path to a built spectral-agent binary
#   SPECTRAL_API_KEY   - API key or tenant-scoped key passed to each agent
#   SPECTRAL_LOG_DIR   - directory for per-agent log files
set -euo pipefail

SERVER="${1:-${SPECTRAL_SERVER:-http://localhost:8080}}"
TENANT="${2:-${SPECTRAL_TENANT:-default}}"
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/../.." && pwd)"
LOG_DIR="${SPECTRAL_LOG_DIR:-/tmp/spectral-agents}"
AGENT_BIN="${SPECTRAL_AGENT_BIN:-$REPO_ROOT/spectral-agent}"

mkdir -p "$LOG_DIR"

declare -a AGENT_CMD=()
declare -a LAUNCH_PREFIX=()

resolve_agent_cmd() {
  if [[ -x "$AGENT_BIN" ]]; then
    AGENT_CMD=("$AGENT_BIN")
    return
  fi
  if command -v go >/dev/null 2>&1; then
    AGENT_CMD=(go run ./cmd/spectral-agent)
    return
  fi
  echo "[supervisor] unable to find spectral-agent binary at $AGENT_BIN and 'go' is not on PATH" >&2
  exit 1
}

resolve_launch_prefix() {
  if command -v setsid >/dev/null 2>&1; then
    LAUNCH_PREFIX=(setsid)
    return
  fi
  echo "[supervisor] 'setsid' not found; starting agents without session detachment" >&2
}

declare -A AGENT_DEFS=(
  ["echo-agent"]="echo,reverse,timestamp"
  ["hash-agent"]="hash,multi-hash"
  ["fetch-agent"]="http-fetch,ping,dns-lookup"
  ["math-agent"]="math"
  ["text-agent"]="summarize,count-words"
  ["task-agent"]="sleep,timestamp"
  ["data-agent"]="base64-encode,base64-decode,regex-match,generate,csv-parse,json-query,sort-data,compress,template"
  ["system-agent"]="sys-info,disk-usage,process-count,file-stat"
  ["pipeline-agent"]="pipeline"
)

declare -A AGENT_TAGS=(
  ["echo-agent"]="type=utility,region=local"
  ["hash-agent"]="type=compute,region=local"
  ["fetch-agent"]="type=network,region=local"
  ["math-agent"]="type=compute,region=local"
  ["text-agent"]="type=nlp,region=local"
  ["task-agent"]="type=utility,region=local"
  ["data-agent"]="type=data,region=local"
  ["system-agent"]="type=system,region=local"
  ["pipeline-agent"]="type=orchestration,region=local"
)

declare -A PIDS=()

shutdown() {
  trap - EXIT INT TERM
  echo "[supervisor] stopping agents"
  for pid in "${PIDS[@]:-}"; do
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
    fi
  done
  wait || true
}

launch_agent() {
  local id="$1"
  local caps="${AGENT_DEFS[$id]}"
  local tags="${AGENT_TAGS[$id]}"
  local log="$LOG_DIR/${id}.log"
  local extra=()

  if [[ -n "${SPECTRAL_API_KEY:-}" ]]; then
    extra=(-api-key "$SPECTRAL_API_KEY")
  fi

  echo "[supervisor] starting $id  caps=$caps"
  (
    cd "$REPO_ROOT"
    exec "${LAUNCH_PREFIX[@]}" "${AGENT_CMD[@]}" \
      -id "$id" \
      -capabilities "$caps" \
      -tags "$tags" \
      -server "$SERVER" \
      -tenant "$TENANT" \
      -poll 2s \
      -heartbeat 20s \
      "${extra[@]}"
  ) >> "$log" 2>&1 &
  PIDS[$id]=$!
}

resolve_agent_cmd
resolve_launch_prefix
trap shutdown EXIT INT TERM

# Initial launch
for id in "${!AGENT_DEFS[@]}"; do
  launch_agent "$id"
done

echo "[supervisor] all agents started; watching for crashes (Ctrl+C to stop all)"

# Restart loop
while true; do
  sleep 5
  for id in "${!AGENT_DEFS[@]}"; do
    pid="${PIDS[$id]:-0}"
    if ! kill -0 "$pid" 2>/dev/null; then
      echo "[supervisor] $id (pid=$pid) died — restarting"
      launch_agent "$id"
    fi
  done
done
