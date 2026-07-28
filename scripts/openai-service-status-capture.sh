#!/usr/bin/env bash
set -euo pipefail

export PATH="$HOME/.local/bin:/opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:${PATH:-}"

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
project_dir="$(cd -- "$script_dir/.." && pwd)"
state_file="${OPENAI_SERVICE_STATUS_STATE:-$project_dir/state/openai-service-status.json}"
log_file="${CLAUDE_STATUS_LOG:-$project_dir/logs/status.log}"
mkdir -p "$(dirname "$state_file")" "$(dirname "$log_file")"

captured_at="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
tmp="$(mktemp)"
state_tmp="$(mktemp "${state_file}.tmp.XXXXXX")"
trap 'rm -f "$tmp" "$state_tmp"' EXIT

http_status="$(curl -sS -L --max-time 8 -w '%{http_code}' -o "$tmp" \
  'https://status.openai.com/api/v2/summary.json' 2>/dev/null || printf '000')"

if [[ "$http_status" != "200" ]] || ! jq -e '.status.indicator? and (.components? | type == "array")' "$tmp" >/dev/null 2>&1; then
  printf '%s openai_status_failed http=%s\n' "$captured_at" "$http_status" >> "$log_file"
  exit 1
fi

jq --arg captured_at "$captured_at" '
  {
    captured_at: $captured_at,
    source: "openai_statuspage",
    status: .status,
    components: [.components[] | {name, status, description}],
    incidents: [(.incidents // [])[] | {name, status, impact, shortlink}]
  }
' "$tmp" > "$state_tmp"

mv "$state_tmp" "$state_file"
printf '%s openai_statuspage\n' "$captured_at" >> "$log_file"
