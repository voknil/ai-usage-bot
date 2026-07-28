#!/usr/bin/env bash
set -euo pipefail

export PATH="$HOME/.local/bin:/opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:${PATH:-}"

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
project_dir="$(cd -- "$script_dir/.." && pwd)"

env_file="${CLAUDE_STATUS_BOT_ENV:-$project_dir/.env}"
usage_file="${CLAUDE_USAGE_STATE:-$project_dir/state/status.json}"
state_file="${CLAUDE_STATUS_MESSAGE_STATE:-$project_dir/state/telegram-message.json}"
api_capture_script="$script_dir/claude-usage-api-capture.sh"
cli_capture_script="$script_dir/claude-usage-cli-capture.sh"
openai_capture_script="$script_dir/openai-usage-capture.sh"
openai_usage_file="${OPENAI_USAGE_STATE:-$project_dir/state/openai-status.json}"
anthropic_status_capture_script="$script_dir/claude-service-status-capture.sh"
openai_status_capture_script="$script_dir/openai-service-status-capture.sh"
anthropic_status_file="${CLAUDE_SERVICE_STATUS_STATE:-$project_dir/state/anthropic-service-status.json}"
openai_status_file="${OPENAI_SERVICE_STATUS_STATE:-$project_dir/state/openai-service-status.json}"
alert_state_file="${CLAUDE_USAGE_ALERT_STATE:-$project_dir/state/usage-alerts.json}"
status_log="${CLAUDE_STATUS_LOG:-$project_dir/logs/status.log}"

if [[ ! -f "$env_file" ]]; then
  echo "Missing $env_file" >&2
  exit 2
fi

# shellcheck disable=SC1090
source "$env_file"

if [[ -z "${TELEGRAM_BOT_TOKEN:-}" ]]; then
  echo "TELEGRAM_BOT_TOKEN is empty" >&2
  exit 2
fi

api="https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}"
mkdir -p "$(dirname "$state_file")"
mkdir -p "$(dirname "$status_log")"

telegram() {
  local method="$1"
  shift
  curl -fsS -X POST "$api/$method" "$@"
}

telegram_raw() {
  local method="$1"
  shift
  curl -sS -X POST "$api/$method" "$@"
}

mtime_epoch() {
  local file="$1"
  stat -f '%m' "$file" 2>/dev/null || stat -c '%Y' "$file" 2>/dev/null || echo 0
}

state_chat_id="$(jq -r '.chat_id // empty' "$state_file" 2>/dev/null || true)"
state_message_id="$(jq -r '.message_id // empty' "$state_file" 2>/dev/null || true)"
chat_id="${CLAUDE_STATUS_CHAT_ID:-$state_chat_id}"
message_id="${CLAUDE_STATUS_MESSAGE_ID:-$state_message_id}"

if [[ -z "$chat_id" ]]; then
  updates="$(curl -fsS "$api/getUpdates")"
  chat_id="$(
    python3 - "$updates" <<'PY'
import json, sys
data = json.loads(sys.argv[1])
matches = []
for upd in data.get("result", []):
    msg = upd.get("message") or {}
    chat = msg.get("chat") or {}
    text = (msg.get("text") or "").strip()
    if chat.get("type") == "private" and text.startswith("/start"):
        matches.append(chat.get("id"))
if matches:
    print(matches[-1])
PY
  )"
fi

if [[ -z "$chat_id" ]]; then
  echo "No private /start update found for the status bot yet." >&2
  exit 3
fi

usage_age_seconds=999999
if [[ -f "$usage_file" ]]; then
  usage_mtime="$(mtime_epoch "$usage_file")"
  usage_age_seconds="$(( $(date +%s) - usage_mtime ))"
fi

if [[ "${CLAUDE_USAGE_SKIP_API_CAPTURE:-}" != "1" && -x "$api_capture_script" && ( "${CLAUDE_USAGE_FORCE_API_CAPTURE:-${CLAUDE_USAGE_FORCE_CLI_CAPTURE:-}}" == "1" || "$usage_age_seconds" -gt 240 ) ]]; then
  "$api_capture_script" >/dev/null 2>>"$status_log" || printf '%s api_capture_failed\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" >> "$status_log"
fi

usage_age_seconds=999999
if [[ -f "$usage_file" ]]; then
  usage_mtime="$(mtime_epoch "$usage_file")"
  usage_age_seconds="$(( $(date +%s) - usage_mtime ))"
fi

if [[ "${CLAUDE_USAGE_SKIP_CLI_CAPTURE:-}" != "1" && -x "$cli_capture_script" && ( "${CLAUDE_USAGE_FORCE_CLI_CAPTURE:-}" == "1" || "$usage_age_seconds" -gt 240 ) ]]; then
  if command -v timeout >/dev/null 2>&1; then
    timeout 30 "$cli_capture_script" >/dev/null 2>>"$status_log" || printf '%s cli_capture_failed\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" >> "$status_log"
  else
    "$cli_capture_script" >/dev/null 2>>"$status_log" || printf '%s cli_capture_failed\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" >> "$status_log"
  fi
fi

openai_age_seconds=999999
if [[ -f "$openai_usage_file" ]]; then
  openai_mtime="$(mtime_epoch "$openai_usage_file")"
  openai_age_seconds="$(( $(date +%s) - openai_mtime ))"
fi

if [[ "${OPENAI_USAGE_SKIP_CAPTURE:-}" != "1" && -x "$openai_capture_script" && ( "${OPENAI_USAGE_FORCE_CAPTURE:-}" == "1" || "$openai_age_seconds" -gt 240 ) ]]; then
  "$openai_capture_script" >/dev/null 2>>"$status_log" || printf '%s openai_capture_failed\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" >> "$status_log"
fi

anthropic_status_age_seconds=999999
if [[ -f "$anthropic_status_file" ]]; then
  anthropic_status_age_seconds="$(( $(date +%s) - $(mtime_epoch "$anthropic_status_file") ))"
fi
if [[ "${CLAUDE_SERVICE_STATUS_SKIP_CAPTURE:-}" != "1" && -x "$anthropic_status_capture_script" && ( "${CLAUDE_SERVICE_STATUS_FORCE_CAPTURE:-}" == "1" || "$anthropic_status_age_seconds" -gt 240 ) ]]; then
  "$anthropic_status_capture_script" >/dev/null 2>>"$status_log" || printf '%s anthropic_status_capture_failed\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" >> "$status_log"
fi

openai_status_age_seconds=999999
if [[ -f "$openai_status_file" ]]; then
  openai_status_age_seconds="$(( $(date +%s) - $(mtime_epoch "$openai_status_file") ))"
fi
if [[ "${OPENAI_SERVICE_STATUS_SKIP_CAPTURE:-}" != "1" && -x "$openai_status_capture_script" && ( "${OPENAI_SERVICE_STATUS_FORCE_CAPTURE:-}" == "1" || "$openai_status_age_seconds" -gt 240 ) ]]; then
  "$openai_status_capture_script" >/dev/null 2>>"$status_log" || printf '%s openai_status_capture_failed\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" >> "$status_log"
fi

# --- Optional threshold alerts (85% / 95%) for Claude 5h, Claude week, Codex week.
# Sends a standalone Telegram message per threshold crossed, and deletes those
# messages once the corresponding window actually resets (resets_at moves forward).
mkdir -p "$(dirname "$alert_state_file")"
[[ -f "$alert_state_file" ]] || echo '{"windows":{}}' > "$alert_state_file"

alert_plan="$(python3 - "$usage_file" "$openai_usage_file" "$alert_state_file" <<'PY'
import json, sys

claude_path, openai_path, alert_path = sys.argv[1:4]

def load(path):
    try:
        return json.load(open(path))
    except Exception:
        return {}

claude = load(claude_path)
openai = load(openai_path)
alerts = load(alert_path) or {}
windows_state = alerts.get("windows") or {}

rl = claude.get("rate_limits") or {}
five = rl.get("five_hour") or rl.get("fiveHour") or rl.get("session") or rl.get("current_session") or {}
week = rl.get("seven_day") or rl.get("sevenDay") or rl.get("weekly") or rl.get("week") or {}
codex = openai.get("codex") or {}

targets = [
    ("claude_5h", "Claude 5h", five.get("used_percentage"), five.get("resets_at")),
    ("claude_week", "Claude week", week.get("used_percentage"), week.get("resets_at")),
    ("codex_week", "Codex subscription", codex.get("used_percentage"), codex.get("resets_at")),
]

plan = []
new_windows_state = dict(windows_state)
for key, label, pct, resets_at in targets:
    if pct is None:
        continue
    stored = windows_state.get(key) or {}
    last_pct = stored.get("last_pct")
    notified = list(stored.get("notified") or [])
    message_ids = dict(stored.get("message_ids") or {})
    # A real reset shows up as usage dropping meaningfully, not as resets_at
    # changing (that field drifts every poll for rolling-window sources).
    reset_detected = last_pct is not None and pct < last_pct - 5
    delete_ids = []
    if reset_detected:
        delete_ids = list(message_ids.values())
        notified = []
        message_ids = {}
    notify = [th for th in (85, 95) if pct >= th and th not in notified]
    plan.append({
        "key": key, "label": label, "pct": pct, "resets_at": resets_at,
        "delete_message_ids": delete_ids, "notify_thresholds": notify,
    })
    new_windows_state[key] = {"resets_at": resets_at, "last_pct": pct, "notified": notified, "message_ids": message_ids}

alerts["windows"] = new_windows_state
with open(alert_path, "w", encoding="utf-8") as f:
    json.dump(alerts, f, ensure_ascii=False, indent=2)
    f.write("\n")

print(json.dumps(plan))
PY
)"

if [[ -n "$chat_id" ]]; then
  echo "$alert_plan" | jq -c '.[]' 2>/dev/null | while read -r item; do
    key="$(jq -r '.key' <<<"$item")"
    label="$(jq -r '.label' <<<"$item")"
    resets_at="$(jq -r '.resets_at // empty' <<<"$item")"

    jq -r '.delete_message_ids[]?' <<<"$item" | while read -r del_id; do
      [[ -n "$del_id" ]] && telegram_raw deleteMessage --data-urlencode "chat_id=$chat_id" --data-urlencode "message_id=$del_id" >/dev/null
    done

    jq -r '.notify_thresholds[]?' <<<"$item" | while read -r th; do
      [[ -z "$th" ]] && continue
      alert_text="⚠️ ${label}: ${th}% used"
      if [[ -n "$resets_at" ]]; then
        alert_text="${alert_text}, resets: ${resets_at}"
      fi
      resp="$(telegram sendMessage --data-urlencode "chat_id=$chat_id" --data-urlencode "text=$alert_text" --data-urlencode "disable_notification=false")"
      mid="$(jq -r '.result.message_id // empty' <<<"$resp")"
      if [[ -n "$mid" ]]; then
        tmp_alert="$(mktemp)"
        jq --arg key "$key" --arg th "$th" --argjson mid "$mid" \
          '.windows[$key].message_ids[$th] = $mid | .windows[$key].notified = ((.windows[$key].notified // []) + [($th|tonumber)] | unique)' \
          "$alert_state_file" > "$tmp_alert" && mv "$tmp_alert" "$alert_state_file"
      fi
    done
  done
fi

text="$(
  python3 - "$usage_file" "$openai_usage_file" "$anthropic_status_file" "$openai_status_file" <<'PY'
import json, sys
from datetime import datetime, timezone

path = sys.argv[1]
openai_path = sys.argv[2] if len(sys.argv) > 2 else None
anthropic_status_path = sys.argv[3] if len(sys.argv) > 3 else None
openai_status_path = sys.argv[4] if len(sys.argv) > 4 else None
now = datetime.now(timezone.utc)

def parse_dt(value):
    if not value:
        return None
    if isinstance(value, (int, float)):
        # Accept seconds or milliseconds.
        if value > 10_000_000_000:
            value /= 1000
        return datetime.fromtimestamp(value, timezone.utc)
    if isinstance(value, str):
        try:
            return datetime.fromisoformat(value.replace("Z", "+00:00"))
        except ValueError:
            return None
    return None

def find_percent(obj):
    if not isinstance(obj, dict):
        return None
    keys = (
        "percent_used", "used_percent", "usage_percent", "percent",
        "percentage", "usedPercentage", "usagePercentage", "used_pct",
        "usage_pct", "used_percentage", "usage_percentage"
    )
    for key in keys:
        val = obj.get(key)
        if isinstance(val, (int, float)):
            pct = float(val)
            return pct * 100 if 0 <= pct <= 1 else pct
        if isinstance(val, str):
            s = val.strip().rstrip("%")
            try:
                pct = float(s)
            except ValueError:
                continue
            return pct * 100 if 0 <= pct <= 1 else pct
    used = obj.get("used")
    limit = obj.get("limit") or obj.get("total")
    if isinstance(used, (int, float)) and isinstance(limit, (int, float)) and limit:
        return used / limit * 100
    return None

def find_reset(obj):
    if not isinstance(obj, dict):
        return None
    for key in ("reset_at", "resets_at", "resetAt", "resetsAt", "reset_time", "resetTime"):
        dt = parse_dt(obj.get(key))
        if dt:
            return dt
    return None

def fmt_pct(p):
    if p is None:
        return "unknown"
    left = max(0, 100 - p)
    return f"{p:.0f}% used / {left:.0f}% left"

def fmt_used(p):
    if p is None:
        return "unknown"
    return f"{p:.0f}% used"

def fmt_left_short(p):
    if p is None:
        return "?"
    return f"{max(0, 100 - p):.0f}%"

def fmt_reset(dt):
    if not dt:
        return "unknown"
    delta = dt - now
    mins = max(0, int(delta.total_seconds() // 60))
    h, m = divmod(mins, 60)
    local = dt.astimezone().strftime("%H:%M")
    if h:
        return f"{local} (in {h}h {m}m)"
    return f"{local} (in {m}m)"

def fmt_reset_short(dt):
    if not dt:
        return "?"
    local_dt = dt.astimezone()
    local_now = now.astimezone(local_dt.tzinfo)
    if local_dt.date() == local_now.date():
        return local_dt.strftime("%H:%M")
    return local_dt.strftime("%a %H:%M")

def top_line(label, item):
    if not item:
        return f"{label}: ? used · rst ?"
    percent = item.get("percent")
    used = "?" if percent is None else f"{percent:.0f}%"
    return f"{label}: {used} used · rst {fmt_reset_short(item.get('reset'))}"

def progress_bar(p, width=18):
    if p is None:
        return "[" + "?" * width + "]"
    p = max(0, min(100, p))
    filled = round(width * p / 100)
    return "[" + "#" * filled + "-" * (width - filled) + "]"

def human_label(key):
    raw = str(key or "").replace("-", "_")
    labels = {
        "five_hour": "Current session",
        "fivehour": "Current session",
        "session": "Current session",
        "current_session": "Current session",
        "seven_day": "All models",
        "sevenday": "All models",
        "weekly": "All models",
        "week": "All models",
        "all_models": "All models",
        "allmodels": "All models",
        "fable": "Fable",
        "opus": "Opus",
        "sonnet": "Sonnet",
        "haiku": "Haiku",
    }
    lowered = raw.lower()
    if lowered in labels:
        return labels[lowered]
    return raw.replace("_", " ").strip().title() or "Limit"

def extract_limit(obj, label):
    pct = find_percent(obj)
    reset = find_reset(obj)
    if pct is None and reset is None:
        return None
    return {
        "label": label,
        "percent": pct,
        "reset": reset,
    }

def child_dicts(obj):
    if not isinstance(obj, dict):
        return []
    out = []
    for key, val in obj.items():
        if isinstance(val, dict):
            out.append((key, val))
    return out

def collect_nested_limits(obj, parent_label=None, depth=0):
    if not isinstance(obj, dict) or depth > 4:
        return []
    found = []
    for key, val in child_dicts(obj):
        label = human_label(key)
        item = extract_limit(val, label)
        children = collect_nested_limits(val, label, depth + 1)
        if item:
            found.append(item)
        found.extend(children)
    return found

def dedupe_limits(items):
    seen = set()
    out = []
    for item in items:
        pct = item.get("percent")
        reset = item.get("reset")
        sig = (
            item.get("label"),
            None if pct is None else round(pct, 3),
            reset.isoformat() if reset else None,
        )
        if sig in seen:
            continue
        seen.add(sig)
        out.append(item)
    return out

def append_limit(lines, item):
    lines.append(item["label"])
    lines.append(f"{progress_bar(item.get('percent'))} {fmt_used(item.get('percent'))}")
    reset = item.get("reset")
    if reset:
        lines.append(f"Resets: {fmt_reset(reset)}")

try:
    data = json.load(open(path))
except FileNotFoundError:
    print("5h: ? used · rst ?\nWeek: ? used · rst ?\n\n⏳ Status: waiting for Claude Code data\n✅ Updated: never")
    raise SystemExit

captured = parse_dt(data.get("captured_at"))
age = None
if captured:
    age = int((now - captured).total_seconds() // 60)

source = data.get("source") or "unknown"
rl = data.get("rate_limits") or {}

five = rl.get("five_hour") or rl.get("fiveHour") or rl.get("session") or rl.get("current_session") or {}
week = rl.get("seven_day") or rl.get("sevenDay") or rl.get("weekly") or rl.get("week") or {}

five_item = extract_limit(five, "Current session")
week_item = extract_limit(week, "All models")
all_items = dedupe_limits(collect_nested_limits(rl))

weekly_items = []
for item in all_items:
    if item["label"] == "Current session":
        continue
    if week_item and item["label"] == "All models":
        continue
    weekly_items.append(item)

trusted_sources = {"official_rate_limits", "claude_usage_command", "anthropic_oauth_usage_api"}
if source in trusted_sources:
    lines = [
        top_line("5h", five_item),
        top_line("Week", week_item),
    ]

    if five_item:
        lines.append("")
        append_limit(lines, five_item)
    else:
        lines.append("")
        lines.append("Current session: unknown")

    lines.append("")
    lines.append("Weekly limits")
    if week_item:
        append_limit(lines, week_item)
    if weekly_items:
        for item in weekly_items:
            lines.append("")
            append_limit(lines, item)
    elif not week_item:
        lines.append("unknown")
else:
    lines = ["5h: ? used · rst ?", "Week: ? used · rst ?"]
    lines.append("")
    lines.append("⏳ Status: waiting for official rate_limits")

def fmt_reset_dt_short(iso_str):
    dt = parse_dt(iso_str)
    return fmt_reset_short(dt) if dt else "?"

if openai_path:
    try:
        oai = json.load(open(openai_path))
    except FileNotFoundError:
        oai = None
    if oai:
        lines.append("")
        lines.append("🤖 OpenAI")
        codex = oai.get("codex")
        if codex and codex.get("used_percentage") is not None:
            pct = codex.get("used_percentage")
            rst = fmt_reset_dt_short(codex.get("resets_at"))
            lines.append(f"Codex (subscription): {pct:.0f}% used · rst {rst}")
            credits = codex.get("free_reset_credits_available")
            if credits:
                lines.append(f"  ↳ free rate-limit resets available: {credits}")
        elif oai.get("codex_error"):
            lines.append("Codex (subscription): read error")
        billing = oai.get("billing")
        if billing and billing.get("today_usd") is not None:
            total = billing["today_usd"]
            by_model = billing.get("by_model_usd") or {}
            top = ", ".join(f"{k} ${v:.2f}" for k, v in list(by_model.items())[:3])
            suffix = f" ({top})" if top else ""
            lines.append(f"API billing today: ${total:.2f}{suffix}")
        elif oai.get("billing_error"):
            lines.append("API billing: read error")

def append_public_status(lines, label, path):
    try:
        service = json.load(open(path))
    except (FileNotFoundError, json.JSONDecodeError):
        service = None
    if not service:
        lines.append(f"🌐 {label}: ❔ status temporarily unavailable")
        return

    status = service.get("status") or {}
    indicator = str(status.get("indicator") or "unknown").lower()
    description = status.get("description") or "Unknown"
    status_icons = {"none": "✅", "minor": "⚠️", "major": "🟠", "critical": "🔴"}
    lines.append(f"🌐 {label}: {status_icons.get(indicator, '❔')} {description}")

    # Healthy components stay silent; report only the concrete source of an issue.
    component_icons = {
        "degraded_performance": "⚠️",
        "partial_outage": "🟠",
        "major_outage": "🔴",
        "under_maintenance": "🔧",
    }
    for component in service.get("components") or []:
        comp_status = str(component.get("status") or "unknown")
        if comp_status in component_icons:
            lines.append(f"  {component_icons[comp_status]} {component.get('name')}: {comp_status.replace('_', ' ')}")

    incidents = [item for item in (service.get("incidents") or []) if item.get("status") not in ("resolved", "postmortem")]
    for incident in incidents[:3]:
        lines.append(f"  ⚠️ Incident: {incident.get('name') or 'unknown'}")

lines.append("")
append_public_status(lines, "Anthropic", anthropic_status_path)
append_public_status(lines, "OpenAI", openai_status_path)

if captured:
    local = captured.astimezone().strftime("%H:%M")
    stale = " stale" if age is not None and age > 20 else ""
    lines.append(f"✅ Updated: {local} ({age}m ago){stale}")
else:
    lines.append("✅ Updated: unknown")

print("\n".join(lines))
PY
	)"

reply_markup='{"inline_keyboard":[[{"text":"↻ Обновить сейчас ✅","callback_data":"refresh_claude_usage"}]]}'

if [[ -z "$message_id" ]]; then
  response="$(telegram sendMessage --data-urlencode "chat_id=$chat_id" --data-urlencode "text=$text" --data-urlencode "reply_markup=$reply_markup" --data-urlencode "disable_notification=true")"
  message_id="$(jq -r '.result.message_id' <<<"$response")"
  jq -n --argjson chat_id "$chat_id" --argjson message_id "$message_id" \
    '{chat_id: $chat_id, message_id: $message_id}' > "$state_file"
  echo "Created status message $message_id in chat $chat_id"
  exit 0
fi

edit_response="$(telegram_raw editMessageText --data-urlencode "chat_id=$chat_id" --data-urlencode "message_id=$message_id" --data-urlencode "text=$text" --data-urlencode "reply_markup=$reply_markup")"
edit_ok="$(jq -r '.ok // false' <<<"$edit_response" 2>/dev/null || echo false)"
edit_description="$(jq -r '.description // empty' <<<"$edit_response" 2>/dev/null || true)"

if [[ "$edit_ok" == "true" ]]; then
  echo "Updated status message $message_id in chat $chat_id"
elif [[ "$edit_description" == *"message is not modified"* ]]; then
  echo "Status message $message_id unchanged in chat $chat_id"
elif [[ "$edit_description" == *"message to edit not found"* ]]; then
  response="$(telegram sendMessage --data-urlencode "chat_id=$chat_id" --data-urlencode "text=$text" --data-urlencode "reply_markup=$reply_markup" --data-urlencode "disable_notification=true")"
  message_id="$(jq -r '.result.message_id' <<<"$response")"
  jq -n --argjson chat_id "$chat_id" --argjson message_id "$message_id" \
    '{chat_id: $chat_id, message_id: $message_id}' > "$state_file"
  echo "Recreated missing status message $message_id in chat $chat_id"
else
  echo "Failed to edit status message $message_id in chat $chat_id: $edit_description" >&2
  exit 4
fi
