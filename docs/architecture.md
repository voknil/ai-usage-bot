# Architecture

```
main.go                  commands (run, once, doctor, version), wiring
internal/config          environment + .env → Config
internal/claude          Claude Code usage: OAuth API, `claude /usage` fallback
internal/openai          Codex via CodexBar, API spend via the costs API
internal/statuspage      Atlassian Statuspage summaries (Anthropic, OpenAI)
internal/usage           the data types providers produce
internal/render          data → message text (pure functions)
internal/alerts          threshold bookkeeping (pure, no I/O)
internal/telegram        minimal Bot API client
internal/state           atomic JSON/int files
internal/bot             orchestration: cache, publish, updates
```

The dependency direction is one-way: providers know nothing about Telegram, `render` and `alerts` know nothing about I/O, and only `bot` and `main` connect them. That is what makes almost everything testable without a network.

## Refresh cycle

1. **Fetch.** Each source has a cached value and a timestamp. A source is refetched when its value is older than `CACHE_TTL` or when the user pressed the button. A failure is logged and the previous value is kept, so a hiccup turns into a "stale" marker in the footer instead of an empty message.
2. **Alerts.** The latest percentages go through `alerts.Evaluate`, which returns what to send and what to delete. The bot performs those Telegram calls and reports the new message IDs back with `Sent`, then persists the state.
3. **Publish.** The text is rendered; if it equals the last published text nothing is sent. Otherwise the message is edited. "Message is not modified" is success; "message to edit not found" creates a new message and stores its ID.

## Decisions

**One process instead of cron plus a poller.** The shell version ran a status job every minute and a separate long-polling loop, which shared state through files. One process with a mutex removes the races and halves the moving parts.

**Claude: API first, CLI second.** The OAuth usage endpoint is cheap and structured, but its token is not available everywhere (on macOS it often lives in the keychain). `claude /usage` always works where Claude Code is installed, at the cost of parsing text, which the parser tests pin down. The CLI's reset times carry no year; the parser takes it from the clock and rolls into the next year for a date that would otherwise be far in the past.

**Reset detection by usage drop, not by timestamp.** Some sources report a reset time that drifts on every poll, so "the reset time changed" would retract alerts constantly. A drop of more than five points between readings is a real reset.

**Alert state is compatible with the shell version.** `usage-alerts.json`, `telegram-message.json` and `telegram-updates.offset` keep their names and layout, so switching versions keeps the status message and the alert history.

**Owner chat.** The first private `/start` claims the bot unless `CHAT_ID` pins it. After that, other chats are ignored: the bot answers only its owner.

**No dependencies.** The standard library covers HTTP, JSON, logging and process execution. Fewer moving parts, a trustworthy `govulncheck`, a tiny binary.

## Adding a provider

1. Add a package under `internal/` with a client and a `Fetch(ctx)` method returning a type from `internal/usage` (add one if needed). Put the parsing in its own function and test it against a real sample.
2. Wire it into `sources()` in `main.go` and into `bot.Sources`.
3. Add a section to `render` (with a golden test) and, if it has rate-limit windows, a line in `Bot.targets` so alerts cover it.
