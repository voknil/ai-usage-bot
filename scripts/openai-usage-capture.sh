#!/usr/bin/env bash
set -euo pipefail

export PATH="$HOME/.local/bin:/opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:${PATH:-}"

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
project_dir="$(cd -- "$script_dir/.." && pwd)"

env_file="${CLAUDE_STATUS_BOT_ENV:-$project_dir/.env}"
state_file="${OPENAI_USAGE_STATE:-$project_dir/state/openai-status.json}"
log_file="${OPENAI_USAGE_CAPTURE_LOG:-$project_dir/logs/capture.log}"
mkdir -p "$(dirname "$state_file")" "$(dirname "$log_file")"

captured_at="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"

if [[ -f "$env_file" ]]; then
  # shellcheck disable=SC1090
  source "$env_file"
fi

codex_tmp="$(mktemp)"
costs_tmp="$(mktemp)"
state_tmp="$(mktemp "${state_file}.tmp.XXXXXX")"
trap 'rm -f "$codex_tmp" "$costs_tmp" "$state_tmp"' EXIT

# --- Codex (ChatGPT) subscription usage, via CodexBar (https://codexbar.app) ---
codex_ok=0
if command -v codexbar >/dev/null 2>&1; then
  if codexbar usage --provider codex --format json > "$codex_tmp" 2>>"$log_file"; then
    codex_ok=1
  else
    printf '%s codex_usage_failed\n' "$captured_at" >> "$log_file"
  fi
else
  printf '%s codexbar_not_found\n' "$captured_at" >> "$log_file"
fi

# --- OpenAI API pay-as-you-go billing (today), via the Usage/Costs API.
# Requires an Admin API key (platform.openai.com -> Settings -> Organization ->
# Admin keys) with the api.usage.read scope -- a regular project sk-proj- key
# cannot read this endpoint (403 insufficient permissions).
billing_ok=0
if [[ -n "${OPENAI_ADMIN_KEY:-}" ]]; then
  start_of_day="$(date -u -v0H -v0M -v0S '+%s' 2>/dev/null || date -u -d 'today 00:00' '+%s')"
  http_status="$(
    curl -sS --max-time 10 -w '%{http_code}' -o "$costs_tmp" \
      "https://api.openai.com/v1/organization/costs?start_time=${start_of_day}&limit=31&group_by=line_item" \
      -H "Authorization: Bearer ${OPENAI_ADMIN_KEY}" 2>/dev/null || printf '000'
  )"
  if [[ "$http_status" == "200" ]]; then
    billing_ok=1
  else
    printf '%s billing_costs_failed http=%s\n' "$captured_at" "$http_status" >> "$log_file"
  fi
else
  printf '%s billing_admin_key_missing\n' "$captured_at" >> "$log_file"
fi

if [[ "$codex_ok" == "0" && "$billing_ok" == "0" ]]; then
  printf '%s nothing_captured\n' "$captured_at" >> "$log_file"
  exit 1
fi

python3 - "$captured_at" "$codex_tmp" "$codex_ok" "$costs_tmp" "$billing_ok" "$state_tmp" <<'PY'
import json, sys

captured_at, codex_path, codex_ok, costs_path, billing_ok, state_tmp_path = sys.argv[1:7]
codex_ok = codex_ok == "1"
billing_ok = billing_ok == "1"

payload = {"captured_at": captured_at, "source": "codexbar+openai_costs_api"}

if codex_ok:
    try:
        data = json.load(open(codex_path))
        entry = data[0] if isinstance(data, list) and data else {}
        usage = entry.get("usage") or {}
        window = usage.get("secondary") or usage.get("primary") or usage.get("tertiary") or {}
        credits = (entry.get("credits") or {}).get("remaining")
        reset_credits = ((usage.get("codexResetCredits") or {}).get("availableCount"))
        payload["codex"] = {
            "account_email": usage.get("accountEmail"),
            "used_percentage": window.get("usedPercent"),
            "resets_at": window.get("resetsAt"),
            "reset_description": window.get("resetDescription"),
            "window_minutes": window.get("windowMinutes"),
            "credits_remaining": credits,
            "free_reset_credits_available": reset_credits,
        }
    except Exception as exc:
        payload["codex_error"] = str(exc)

if billing_ok:
    try:
        costs = json.load(open(costs_path))
        total = 0.0
        by_model = {}
        for bucket in costs.get("data", []):
            for result in bucket.get("results", []):
                amount = float((result.get("amount") or {}).get("value") or 0)
                total += amount
                line_item = result.get("line_item") or "other"
                model = line_item.split(",")[0].strip()
                by_model[model] = by_model.get(model, 0.0) + amount
        payload["billing"] = {
            "today_usd": round(total, 4),
            "by_model_usd": {k: round(v, 4) for k, v in sorted(by_model.items(), key=lambda kv: -kv[1])},
        }
    except Exception as exc:
        payload["billing_error"] = str(exc)

with open(state_tmp_path, "w", encoding="utf-8") as f:
    json.dump(payload, f, ensure_ascii=False, indent=2)
    f.write("\n")
PY

mv "$state_tmp" "$state_file"
printf '%s captured codex_ok=%s billing_ok=%s\n' "$captured_at" "$codex_ok" "$billing_ok" >> "$log_file"
