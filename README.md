# ai-usage-bot

A small Telegram bot that keeps **one message** up to date with your AI subscription limits — Claude Code, Codex — plus today's OpenAI API spend and the providers' public status, and warns you before a limit runs out.

[![CI](https://github.com/voknil/ai-usage-bot/actions/workflows/ci.yml/badge.svg)](https://github.com/voknil/ai-usage-bot/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

The first two lines are written for the chat list, so you can read the numbers without opening the chat:

```text
5h: 24% used · rst 11:30
Week: 34% used · rst Fri 08:00

Current session
[####--------------] 24% used
Resets: 11:30 (in 3h 13m)

Weekly limits
All models
[######------------] 34% used
Resets: 08:00 (in 71h 43m)

🤖 OpenAI
Codex 5h: 1% used · rst 13:00
Codex week: 17% used · rst Wed 11:00
API billing today: $2.19 (gpt-5 $2.15, gpt-5-mini $0.03)

🌐 Anthropic: ✅ All Systems Operational
🌐 OpenAI: ✅ All Systems Operational
✅ Updated: 08:16 (0m ago)
```

An inline **↻ Обновить сейчас ✅** button refreshes everything on demand.

## Features

- **One message, edited in place** — no stream of notifications; it is only edited when something changed.
- **Claude Code** limits (session, week, per-model) from the OAuth usage API, falling back to `claude /usage`.
- **Codex** 5-hour and weekly limits through [CodexBar](https://codexbar.app), and **OpenAI API spend** for today through the organization costs API. Each part is optional and independent.
- **Threshold alerts** (85 % and 95 % by default) as separate messages, retracted automatically when the window resets.
- **Public status** of Anthropic and OpenAI; components and incidents are listed only when something is wrong.
- **One static binary**, no dependencies, no runtime other than the tools it reads from. A failing source keeps its last good value instead of blanking the message.
- **Safe by default**: the bot token is never logged, only the owner's chat can press the button, state files are private (`0600`), the `.env` file is parsed, not executed.
- **`doctor`** command to see which source works without sending anything.

## Quick start

1. Create a bot with [@BotFather](https://t.me/BotFather) and copy the token.
2. Get the binary — download from [Releases](https://github.com/voknil/ai-usage-bot/releases) or build it:

   ```bash
   git clone https://github.com/voknil/ai-usage-bot && cd ai-usage-bot
   make build            # -> bin/ai-usage-bot
   ```

3. Configure:

   ```bash
   cp .env.example .env
   $EDITOR .env          # set TELEGRAM_BOT_TOKEN
   ```

4. Check that the sources work (sends nothing):

   ```bash
   ./bin/ai-usage-bot doctor
   ```

5. Run it, then open your bot in Telegram and send `/start`:

   ```bash
   ./bin/ai-usage-bot run
   ```

The first private chat that sends `/start` becomes the owner. To avoid depending on who writes first, set `CHAT_ID` in `.env` (your ID is stored in `state/telegram-message.json` after the first `/start`).

### Run it permanently

**macOS (launchd)** — copy `deploy/launchd/ai-usage-bot.plist`, replace `INSTALL_DIR` with the folder holding the binary, `.env` and `state/`, then:

```bash
cp deploy/launchd/ai-usage-bot.plist ~/Library/LaunchAgents/com.example.ai-usage-bot.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.example.ai-usage-bot.plist
```

**Linux (systemd, user service)** — copy `deploy/systemd/ai-usage-bot.service`, replace `INSTALL_DIR`, then:

```bash
mkdir -p ~/.config/systemd/user
cp deploy/systemd/ai-usage-bot.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now ai-usage-bot
loginctl enable-linger "$USER"      # keep it running after logout
```

Coming from the shell version? See [docs/migration.md](docs/migration.md) — your message, alerts and button keep working.

## Commands

| Command | Does |
|---|---|
| `ai-usage-bot run` | Refresh on a schedule and answer the button. The long-running mode. |
| `ai-usage-bot once` | Refresh the message one time and exit. |
| `ai-usage-bot doctor` | Read every source, print the result and a preview. Sends nothing. |
| `ai-usage-bot version` | Print the version. |

In the chat: `/start` (re)creates the status message, `/status` does the same.

## Configuration

Settings come from the environment and an optional `.env` file (`BOT_ENV` points elsewhere); the environment wins.

| Variable | Default | Meaning |
|---|---|---|
| `TELEGRAM_BOT_TOKEN` | — *(required)* | Token from BotFather |
| `CHAT_ID` | *(first `/start`)* | Pin the bot to one chat. Alias: `CLAUDE_STATUS_CHAT_ID` |
| `STATE_DIR` | `state` | Where the small state files live |
| `REFRESH_INTERVAL` | `1m` | How often to refresh |
| `CACHE_TTL` | `4m` | How long a fetched value is reused |
| `ALERT_THRESHOLDS` | `85,95` | Percentages that trigger a warning |
| `CLAUDE_BIN` | *(from `PATH`)* | The `claude` executable |
| `CLAUDE_CREDENTIALS_FILE` | `~/.claude/.credentials.json` | Where the OAuth token is read from, if present |
| `CODEXBAR_BIN` | *(from `PATH`)* | The `codexbar` executable; without it the Codex lines are skipped |
| `OPENAI_ADMIN_KEY` | *(empty)* | Enables API spend. Needs an **Admin** key with only `api.usage.read` |
| `LOG_LEVEL` / `LOG_FORMAT` | `info` / `text` | `debug…error`; `text` or `json` |

## How it works

```
 claude (API → CLI)  ─┐
 codexbar / costs API ├─▶ cache ─▶ render ─▶ one Telegram message (edited in place)
 status pages ────────┘      │
                             └─▶ alerts ─▶ warning messages (deleted on reset)
 Telegram updates ─▶ /start, refresh button
```

One process does everything: a ticker refreshes stale sources, the update loop long-polls Telegram, and a mutex keeps the two from racing. Details and design decisions are in [docs/architecture.md](docs/architecture.md).

## Security

- The bot token is only ever part of the request URL to `api.telegram.org` (Telegram's design) and is scrubbed from every error message.
- Button presses and commands are accepted from the owner's chat only.
- `OPENAI_ADMIN_KEY` should be scoped to `api.usage.read`: it can read usage, not spend money or change settings.
- `.env`, `state/` and `logs/` are git-ignored. Do not commit them.
- Report vulnerabilities as described in [SECURITY.md](SECURITY.md).

## Development

```bash
make check     # vet + golangci-lint + tests with the race detector
make cover     # coverage summary
make doctor    # build and run the doctor against your real sources
```

Go 1.22 or newer. Every provider is a small package with its own parser tests; the Telegram flow is tested end to end against a fake Bot API. See [CONTRIBUTING.md](CONTRIBUTING.md) to add a provider.

## License

MIT. See [LICENSE](LICENSE).

---

## По-русски

`ai-usage-bot` держит в Telegram **одно сообщение** с лимитами ваших AI-подписок (Claude Code, Codex), расходом OpenAI API за сегодня и статусом сервисов, а при приближении к лимиту присылает предупреждение (по умолчанию на 85 % и 95 %). Кнопка «↻ Обновить сейчас» обновляет всё немедленно.

Быстрый старт: создайте бота у @BotFather, положите токен в `.env` (`TELEGRAM_BOT_TOKEN`), проверьте источники командой `ai-usage-bot doctor`, запустите `ai-usage-bot run` и отправьте боту `/start`. Для постоянной работы — шаблоны в `deploy/` (launchd на macOS, systemd на Linux). С версии на shell можно перейти без потери сообщения и истории алертов: см. [docs/migration.md](docs/migration.md).
