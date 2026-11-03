# Changelog

All notable changes to this project are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
the project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Fixed
- Providers are optional: with no Claude Code installed the bot no longer shows "waiting for Claude Code data"
  and `doctor` no longer fails. The chat preview shows Codex when Claude is absent, and a message with no usage
  provider shows the public status pages (or a hint to run `doctor`).

### Added
- `PROVIDERS` setting to choose providers explicitly instead of detecting them.

### Changed
- **Rewritten in Go.** One static binary (`run`, `once`, `doctor`, `version`) replaces the shell scripts,
  Python and `jq`; the two background jobs become a single process.
- The first source that works wins for Claude usage (OAuth API, then `claude /usage`).
- A failing source keeps its last good value instead of dropping its section.
- The `.env` file is parsed, not executed.

### Added
- `doctor` command: reads every source and prints a preview without sending anything.
- Tests for parsers, rendering, alert logic and the whole Telegram flow against a fake Bot API.
- CI (lint, tests on Linux and macOS, `govulncheck`), release automation, Dependabot, `SECURITY.md`.
- `ALERT_THRESHOLDS`, `REFRESH_INTERVAL`, `CACHE_TTL`, `STATE_DIR` settings.
- `/status` command; commands and buttons are accepted from the owner's chat only.

### Removed
- Claude Code `statusLine` capture script.

State files keep their names and layout, so existing installations keep their message and alert history
(see `docs/migration.md`).
