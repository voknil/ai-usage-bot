#!/usr/bin/env bash
set -euo pipefail

export PATH="$HOME/.local/bin:/opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:${PATH:-}"

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
project_dir="$(cd -- "$script_dir/.." && pwd)"

env_file="${CLAUDE_STATUS_BOT_ENV:-$project_dir/.env}"
state_file="${CLAUDE_STATUS_MESSAGE_STATE:-$project_dir/state/telegram-message.json}"
offset_file="${CLAUDE_STATUS_UPDATES_OFFSET:-$project_dir/state/telegram-updates.offset}"
status_script="$script_dir/telegram-claude-usage-status.sh"

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
mkdir -p "$(dirname "$offset_file")"

telegram() {
  local method="$1"
  shift
  curl -fsS -X POST "$api/$method" "$@"
}

set_button_text() {
  local chat_id="$1"
  local message_id="$2"
  local button_text="$3"
  local markup
  markup="$(jq -cn --arg text "$button_text" '{inline_keyboard:[[{"text":$text,"callback_data":"refresh_claude_usage"}]]}')"
  telegram editMessageReplyMarkup \
    --data-urlencode "chat_id=$chat_id" \
    --data-urlencode "message_id=$message_id" \
    --data-urlencode "reply_markup=$markup" >/dev/null || true
}

answer_callback() {
  local callback_id="$1"
  local text="$2"
  telegram answerCallbackQuery \
    --data-urlencode "callback_query_id=$callback_id" \
    --data-urlencode "text=$text" \
    --data-urlencode "show_alert=false" >/dev/null || true
}

offset="$(cat "$offset_file" 2>/dev/null || echo 0)"

while true; do
  response="$(
    curl -fsS -G "$api/getUpdates" \
      --data-urlencode "timeout=50" \
      --data-urlencode "offset=$offset" \
      --data-urlencode 'allowed_updates=["callback_query"]'
  )"

  while IFS= read -r update; do
    [[ -z "$update" ]] && continue

    update_id="$(jq -r '.update_id' <<<"$update")"
    offset="$((update_id + 1))"
    printf '%s\n' "$offset" > "$offset_file"

    callback_id="$(jq -r '.callback_query.id // empty' <<<"$update")"
    data="$(jq -r '.callback_query.data // empty' <<<"$update")"
    chat_id="$(jq -r '.callback_query.message.chat.id // empty' <<<"$update")"
    message_id="$(jq -r '.callback_query.message.message_id // empty' <<<"$update")"
    expected_chat_id="$(jq -r '.chat_id // empty' "$state_file" 2>/dev/null || true)"

    [[ -z "$callback_id" ]] && continue

    if [[ "$data" != "refresh_claude_usage" ]]; then
      answer_callback "$callback_id" "Unknown action"
      continue
    fi

    if [[ -n "$expected_chat_id" && "$chat_id" != "$expected_chat_id" ]]; then
      answer_callback "$callback_id" "Not allowed here"
      continue
    fi

    if [[ -n "$chat_id" && -n "$message_id" ]]; then
      set_button_text "$chat_id" "$message_id" "⏳ Обновляю..."
    fi

    if CLAUDE_USAGE_FORCE_CLI_CAPTURE=1 CLAUDE_SERVICE_STATUS_FORCE_CAPTURE=1 OPENAI_SERVICE_STATUS_FORCE_CAPTURE=1 "$status_script" >/dev/null 2>&1; then
      answer_callback "$callback_id" "✅ Обновлено"
    else
      if [[ -n "$chat_id" && -n "$message_id" ]]; then
        set_button_text "$chat_id" "$message_id" "↻ Обновить сейчас ✅"
      fi
      answer_callback "$callback_id" "⚠️ Не смог обновить"
    fi
  done < <(jq -c '.result[]?' <<<"$response")
done
