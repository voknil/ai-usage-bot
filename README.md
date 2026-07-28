# Claude Usage Telegram Bot

A tiny Telegram status bot for Claude Code subscription usage.

It keeps one Telegram message updated instead of sending a stream of new messages. The first two lines are optimized for Telegram chat previews:

```text
5h: 13% used · rst 18:20
Week: 53% used · rst Fri 08:00
```

The message also includes progress bars, updated time, and an inline refresh button.

## Features

- One persistent Telegram message, edited in place.
- `↻ Обновить сейчас ✅` refresh button.
- Button switches to `⏳ Обновляю...` while refresh is running.
- Auto-refresh via launchd on macOS or systemd user timers on Linux.
- Reads Claude Code usage from `claude /usage`.
- Optional official `rate_limits` capture via Claude Code `statusLine`.
- Optional Anthropic OAuth usage API fallback if your local credentials work.
- Optional OpenAI section: Codex (ChatGPT) subscription usage via CodexBar, plus
  pay-as-you-go API billing for the current day via the OpenAI Usage/Costs API.
- Optional 85% / 95% threshold alerts as standalone messages for each tracked
  window (Claude 5h, Claude week, Codex week), auto-deleted once that window
  actually resets.
- Public service health summary for Anthropic and OpenAI, with concrete
  components and incidents shown only when there is a problem.
- No credentials are committed; `.env`, `state/`, and `logs/` are gitignored.

## Requirements

- macOS or Linux.
- `bash`, `curl`, `jq`, `python3`.
- Claude Code CLI available as `claude`.
- A Telegram bot token from BotFather.

## Setup

Clone the repo and create your env file:

```bash
cp .env.example .env
nano .env
```

Set:

```bash
TELEGRAM_BOT_TOKEN='your-bot-token'
```

Open the bot in Telegram and send `/start`.

Run once:

```bash
./scripts/telegram-claude-usage-status.sh
```

This creates the first status message and stores its `chat_id` and `message_id` in `state/telegram-message.json`.

Start callback polling for the refresh button:

```bash
./scripts/telegram-claude-usage-callbacks.sh
```

## Linux systemd

The systemd units are user services. Copy the templates and replace `REPO_DIR` with the absolute path to this repo:

```bash
mkdir -p ~/.config/systemd/user
cp systemd/claude-usage-status.service ~/.config/systemd/user/
cp systemd/claude-usage-status.timer ~/.config/systemd/user/
cp systemd/claude-usage-callbacks.service ~/.config/systemd/user/

repo_dir="$PWD"
sed -i "s|REPO_DIR|$repo_dir|g" ~/.config/systemd/user/claude-usage-status.service
sed -i "s|REPO_DIR|$repo_dir|g" ~/.config/systemd/user/claude-usage-callbacks.service
```

Enable:

```bash
systemctl --user daemon-reload
systemctl --user enable --now claude-usage-status.timer
systemctl --user enable --now claude-usage-callbacks.service
```

The status timer runs every 60 seconds.

If your user services stop when you log out, enable lingering:

```bash
loginctl enable-linger "$USER"
```

## macOS launchd

Copy the templates and replace `REPO_DIR` with the absolute path to this repo:

```bash
cp launchd/com.example.claude-usage-status.plist ~/Library/LaunchAgents/
cp launchd/com.example.claude-usage-callbacks.plist ~/Library/LaunchAgents/
```

Then edit both copied files:

```bash
plutil -replace ProgramArguments.0 -string "$PWD/scripts/telegram-claude-usage-status.sh" ~/Library/LaunchAgents/com.example.claude-usage-status.plist
plutil -replace WorkingDirectory -string "$PWD" ~/Library/LaunchAgents/com.example.claude-usage-status.plist
plutil -replace StandardOutPath -string "$PWD/logs/status.log" ~/Library/LaunchAgents/com.example.claude-usage-status.plist
plutil -replace StandardErrorPath -string "$PWD/logs/status.err.log" ~/Library/LaunchAgents/com.example.claude-usage-status.plist

plutil -replace ProgramArguments.0 -string "$PWD/scripts/telegram-claude-usage-callbacks.sh" ~/Library/LaunchAgents/com.example.claude-usage-callbacks.plist
plutil -replace WorkingDirectory -string "$PWD" ~/Library/LaunchAgents/com.example.claude-usage-callbacks.plist
plutil -replace StandardOutPath -string "$PWD/logs/callbacks.log" ~/Library/LaunchAgents/com.example.claude-usage-callbacks.plist
plutil -replace StandardErrorPath -string "$PWD/logs/callbacks.err.log" ~/Library/LaunchAgents/com.example.claude-usage-callbacks.plist
```

Load:

```bash
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.example.claude-usage-status.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.example.claude-usage-callbacks.plist
```

The status job runs every 60 seconds.

## Optional Claude Code statusLine

If Claude Code provides `rate_limits` in the statusLine payload, you can capture it:

```json
{
  "statusLine": {
    "type": "command",
    "command": "/absolute/path/to/claude-usage-telegram-bot/scripts/claude-usage-statusline-capture.sh"
  }
}
```

When `rate_limits` are present, they are stored in `state/status.json` with source `official_rate_limits`.

## Optional OpenAI section (Codex subscription + API billing)

Adds a `🤖 OpenAI` block to the same status message:

```text
🤖 OpenAI
Codex (subscription): 41% used · rst Mon 08:00
  ↳ free rate-limit resets available: 2
API billing today: $2.19 (gpt-5.6-sol $2.15, gpt-5.6-luna $0.03)
```

- **Codex subscription usage** comes from the [CodexBar](https://codexbar.app)
  CLI (`codexbar usage --provider codex --format json`). Install and log it in
  separately; if `codexbar` is not on `PATH`, this part is skipped silently.
- **API billing** comes from OpenAI's Usage/Costs API and needs an **Admin API
  key** (Settings -> Organization -> Admin keys on platform.openai.com),
  scoped to `api.usage.read` only. A regular `sk-proj-` key cannot read this
  endpoint (403). Set `OPENAI_ADMIN_KEY` in `.env` to enable it.

Both halves are independent — you can enable just one, or neither (the
section is simply omitted if `openai-usage-capture.sh` never produces data).

## Threshold alerts (85% / 95%)

Once any tracked window (Claude 5h, Claude week, Codex week) crosses 85% or
95% used, the bot sends a separate one-off Telegram message for that
threshold. It will not repeat the same threshold again until the window's
`resets_at` actually advances — at which point the earlier alert message(s)
for that window are deleted automatically and the cycle starts fresh.

State for this lives in `state/usage-alerts.json`; delete it to reset alert
history without affecting the main status message.

## Public service status

The status message also includes compact overall health lines from the public
status pages:

```text
🌐 Anthropic: ✅ All Systems Operational
🌐 OpenAI: ✅ All Systems Operational
```

Healthy components are not listed individually. If a provider reports a
degraded component or an active incident, the affected source is shown below
the provider line. The latest successful response is cached so a temporary
status-page timeout does not break the bot.

## Files

- `scripts/claude-usage-cli-capture.sh` - runs `claude /usage` and writes normalized JSON.
- `scripts/claude-usage-api-capture.sh` - optional OAuth API capture.
- `scripts/claude-usage-statusline-capture.sh` - optional statusLine capture.
- `scripts/openai-usage-capture.sh` - optional Codex subscription + OpenAI API billing capture.
- `scripts/claude-service-status-capture.sh` - public Anthropic service status capture.
- `scripts/openai-service-status-capture.sh` - public OpenAI service status capture.
- `scripts/telegram-claude-usage-status.sh` - creates/edits the Telegram status message, and sends/prunes threshold alerts.
- `scripts/telegram-claude-usage-callbacks.sh` - handles the inline refresh button.

## Security

Do not commit `.env`, `state/`, `logs/`, or Claude credentials. The repo ignores them by default.
The `OPENAI_ADMIN_KEY`, if you set one, should be scoped to `api.usage.read`
only — it cannot spend money or change account settings with that scope, but
treat it like any other secret and never commit it.
