# tgread

Created: Friday, November 20, 2026 — 14:53 PST
License: Apache-2.0

A minimal, compile-time read-only Telegram MTProto MCP server and CLI written in pure Go.

Built for local AI agents (Antigravity CLI, Claude Code, Cursor) to read received Telegram messages, hidden hyperlinks, link previews, and attached PDFs or images without giving a third-party MCP wrapper write access to your personal account.

## Security Guarantees

1. **Minimal dependency tree:** Only two direct external Go modules (`github.com/gotd/td` and `github.com/modelcontextprotocol/go-sdk`), zero CGO, static binary.
2. **Compile-time read-only:** Zero write, send, edit, delete, or read-receipt Telegram RPCs exist anywhere in the binary. Unread badges on your phone and desktop remain untouched. An automated Go AST audit test (`TestReadOnlyASTAudit`) fails the build if any Telegram mutation RPC is introduced.
3. **Login-code protection by default:** Telegram's official service account (`777000`, where OTP login codes arrive) is blocked across all listing, history, search, and download operations unless explicitly enabled via `--allow-telegram-service-chat`.
4. **Optional allowlist and denylist:** Reads all standard cloud chats by default, with optional `--allow-chats` and `--deny-chats` filtering by numeric chat ID or `@username`.
5. **Safe attachment downloads:** Attachments download on demand into a `0700` local cache directory with sanitized filenames, path-traversal checks, atomic temp-file writes, SHA-256 digests, and a default 50 MB size limit.

> **Note on Secret Chats:** Telegram Secret Chats use device-bound end-to-end encryption and cannot be read from any secondary MTProto session. Standard cloud DMs, Saved Messages (`me`), groups, supergroups, and channels all work.

## Build & Install

With Nix + Go-Task:

```bash
direnv allow
direnv exec . task check
direnv exec . task install
```

Or with standard Go:

```bash
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ~/.local/bin/tgread ./cmd/tgread
```

## Quickstart

### 1. Get Telegram API credentials

Create an app at https://my.telegram.org/apps to obtain `API_ID` and `API_HASH`.

### 2. One-time interactive login

Scan a QR code directly in Telegram (**Settings → Devices → Link Desktop Device**):

```bash
tgread login --qr \
  --api-id-file /run/secrets/telegram/api_id \
  --api-hash-file /run/secrets/telegram/api_hash
```

Or use phone number + login code:

```bash
export TGREAD_API_ID=12345678
export TGREAD_API_HASH=your_api_hash
tgread login
```

This authenticates your session and stores it at `~/.config/tgread/session.json` with `0600` permissions.

Verify authorization:

```bash
tgread status
tgread chats --unread
```

### 3. Register with Antigravity CLI (`~/.gemini/config/mcp_config.json`)

Standard env-var configuration:

```json
{
  "mcpServers": {
    "tgread": {
      "command": "/Users/you/.local/bin/tgread",
      "args": ["serve"],
      "env": {
        "TGREAD_API_ID": "12345678",
        "TGREAD_API_HASH": "your_api_hash"
      }
    }
  }
}
```

Or declaratively with Nix-Darwin + SOPS-Nix (zero raw credentials in `mcp_config.json` or process env):

```json
{
  "mcpServers": {
    "tgread": {
      "command": "/nix/store/...-tgread-0.1.0/bin/tgread",
      "args": [
        "serve",
        "--api-id-file", "/run/secrets/telegram/api_id",
        "--api-hash-file", "/run/secrets/telegram/api_hash"
      ]
    }
  }
}
```

## Exposed MCP Tools (All `readOnlyHint: true`)

| Tool | Description |
| --- | --- |
| `list_chats` | List recent cloud chats, groups, channels, and Saved Messages with unread counts and previews |
| `get_messages` | Fetch recent messages with resolved sender names, UTF-16-accurate hidden URLs, web preview cards, and attachment metadata |
| `search_messages` | Search inside one chat or globally across allowed chats with optional media filters (`photos`, `documents`, `urls`, `audio`, `voice`, `video`) |
| `download_attachment` | Download a PDF, photo, audio note, or grouped album (`include_album: true`) into the local cache and return paths + SHA-256 hashes |

## Direct CLI Usage

```bash
tgread chats --limit 20 --unread
tgread messages --chat me --limit 10
tgread search --query "invoice" --filter documents
tgread download --chat -1001234567890 --message-id 42 --album
```
