# tgread Architecture & Security Design

Created: Friday, November 20, 2026 — 14:48 PST
Module: github.com/wjkoh/tgread
License: Apache-2.0

## 1. Purpose

tgread is a minimal, strictly read-only Telegram MTProto MCP server and CLI written in pure Go. It lets local AI agents such as Antigravity CLI, Claude Code, or Cursor inspect received Telegram messages, hidden hyperlinks, link previews, and attached PDFs or images without trusting opaque third-party MCP wrappers with a personal Telegram session.

## 2. Threat Model & Security Controls

A personal Telegram MTProto session carries the same authority as a logged-in phone or desktop client. The server minimizes supply-chain and prompt-injection risk through five hard controls:

| Control | Mechanism | Verification |
| --- | --- | --- |
| Minimal dependency graph | Only two direct external modules: gotd/td for MTProto 2.0 and modelcontextprotocol/go-sdk for MCP stdio framing. Zero CGO. | go.mod and go.sum checksums |
| Compile-time read-only boundary | No Telegram write, send, edit, delete, or read-receipt RPC methods exist anywhere in the codebase. Unread badges stay intact. | Automated Go AST audit test across all packages |
| Login-code protection | Telegram's official service account (ID 777000, which receives OTP login codes) is denied by default across every query, search, and download path unless explicitly opted in. | Unit tests on AccessPolicy |
| Optional chat allowlist and denylist | Open to cloud chats by default, with optional allowlist and denylist filters by numeric chat ID or username. | Unit tests on AccessPolicy |
| Bounded, traversal-safe downloads | Attachments download on demand into a 0700 cache directory using sanitized deterministic filenames, atomic temp-file rename, and a default 50 MB size cap. | Unit tests on SafeCachePath and size guard |

## 3. Architecture

The binary uses a single shared read-only service layer exposed through two frontends:

1. MCP stdio server (serve subcommand): Keeps one authenticated gotd/td MTProto session alive for the lifetime of the stdio stream. All diagnostic logs write strictly to stderr so stdout remains pure JSON-RPC.
2. Direct CLI (login, status, chats, messages, search, download subcommands): Reuses the same service and AccessPolicy so shell workflows and interactive login require no extra tooling.

### Package Layout

| Package | Responsibility |
| --- | --- |
| cmd/tgread | CLI entrypoint, flag and env parsing, interactive login flow, and subcommand dispatch |
| internal/policy | AccessPolicy enforcing allowlist, denylist, and default blocking of Telegram service chat 777000 |
| internal/tgread | Pure-Go gotd/td read-only client, in-memory peer AccessHash resolver, URL/entity/preview extractor, album grouping, and atomic media downloader |
| internal/mcpserver | Official Go MCP SDK registration for the four read-only tools |

## 4. MCP Tool Surface

| Tool | Purpose | Key Parameters |
| --- | --- | --- |
| list_chats | List recent cloud chats, groups, and channels with unread counts and previews | limit, unread_only, query |
| get_messages | Fetch recent messages from a chat with resolved senders, extracted URLs, web previews, and attachment metadata | chat, limit, offset_id, min_date, max_date |
| search_messages | Search messages in one chat or globally across all allowed chats, with optional media type filter | query, chat, limit, filter |
| download_attachment | Download a specific attached PDF, image, audio file, or full grouped album to local cache and return file paths and SHA-256 digests | chat, message_id, include_album |

## 5. Known Telegram Protocol Boundaries

Telegram Secret Chats use device-bound end-to-end encryption and cannot be read from any secondary MTProto session. Standard cloud direct messages, Saved Messages, groups, supergroups, forum topics, and channels are all readable.
