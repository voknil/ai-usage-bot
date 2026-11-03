# Security policy

The bot handles a Telegram bot token and, optionally, an OpenAI admin key and
access to your local Claude Code credentials, so security reports are taken
seriously.

## Supported versions

Only the latest released minor version receives security fixes.

## Reporting a vulnerability

Please **do not open a public issue**. Use GitHub's private reporting instead:
**Security → Report a vulnerability** on this repository
(<https://github.com/voknil/ai-usage-bot/security/advisories/new>).

Include, when you can: the affected version, how you run the bot, a minimal
reproduction and the impact you expect (token disclosure, a command run with
attacker-controlled input, a message sent to the wrong chat, …).

You can expect an acknowledgement within **3 working days** and a fix or a
mitigation plan within **14 days** for confirmed issues.

## Scope

In scope: handling of secrets (bot token, admin key, credentials file), which
chats the bot talks to, parsing of external input (CLI output, API responses),
and the files the bot writes.

Out of scope: vulnerabilities in Telegram, Claude Code, CodexBar or the
providers' APIs themselves.

## Hardening notes

See *Security* in the [README](README.md).
