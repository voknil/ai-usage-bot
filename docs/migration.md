# Migrating from the shell version

The Go version reads the same `.env` and the same state files, so the existing status message, the alert history and the refresh button on old messages keep working.

1. **Build or download** the binary and put it next to your `.env` and `state/`.
2. **Compare** the output with what the shell version shows:
   ```bash
   ./ai-usage-bot doctor
   ```
3. **Stop the old jobs** (the two launchd/systemd units: the status job and the callbacks poller). Both must be stopped — two pollers on one bot token fight over updates.
   ```bash
   launchctl bootout gui/$(id -u)/<old-status-label>
   launchctl bootout gui/$(id -u)/<old-callbacks-label>
   ```
4. **Start the new one** with the unit from `deploy/` (replace `INSTALL_DIR`).
5. Check the chat: the message should update and the button should respond.

## What changed

- One long-running process (`run`) replaces the status job and the callbacks job.
- `CHAT_ID` is the new name for `CLAUDE_STATUS_CHAT_ID` (the old name still works). `CLAUDE_STATUS_MESSAGE_ID` is no longer read; the message ID comes from `state/telegram-message.json`.
- Python and `jq` are no longer needed.
- The optional Claude Code `statusLine` capture script is gone: the OAuth API and `claude /usage` cover the same data.
- A forced refresh (the button) refetches every source; previously the CLI result always overwrote the API one. Now the first source that works wins: API, then CLI.
- Providers' failures keep the last good value instead of dropping the section.
