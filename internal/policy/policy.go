package policy

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

// TelegramServiceChatID is Telegram's official service notification user ID
// where login codes and security alerts arrive. It is blocked by default.
const TelegramServiceChatID int64 = 777000

// Config controls which Telegram chats are readable by the server.
type Config struct {
	AllowChats               []string
	DenyChats                []string
	AllowTelegramServiceChat bool
}

// AccessPolicy enforces chat-level read permissions.
type AccessPolicy struct {
	allowIDs                 map[int64]struct{}
	allowUsernames           map[string]struct{}
	denyIDs                  map[int64]struct{}
	denyUsernames            map[string]struct{}
	allowTelegramServiceChat bool
}

// New constructs an AccessPolicy from string lists of numeric IDs or @usernames.
func New(cfg Config) (*AccessPolicy, error) {
	p := &AccessPolicy{
		allowIDs:                 make(map[int64]struct{}),
		allowUsernames:           make(map[string]struct{}),
		denyIDs:                  make(map[int64]struct{}),
		denyUsernames:            make(map[string]struct{}),
		allowTelegramServiceChat: cfg.AllowTelegramServiceChat,
	}

	if err := addEntries(cfg.AllowChats, p.allowIDs, p.allowUsernames); err != nil {
		return nil, fmt.Errorf("invalid allow-chats entry: %w", err)
	}
	if err := addEntries(cfg.DenyChats, p.denyIDs, p.denyUsernames); err != nil {
		return nil, fmt.Errorf("invalid deny-chats entry: %w", err)
	}
	return p, nil
}

// ParseCSV splits a comma-separated list of chat identifiers, trimming empty values.
func ParseCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func addEntries(entries []string, ids map[int64]struct{}, usernames map[string]struct{}) error {
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if id, err := strconv.ParseInt(entry, 10, 64); err == nil {
			ids[id] = struct{}{}
			continue
		}
		u := normalizeUsername(entry)
		if u == "" {
			return fmt.Errorf("%q is neither a numeric chat ID nor a valid username", raw)
		}
		usernames[u] = struct{}{}
	}
	return nil
}

func normalizeUsername(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "@")
	s = strings.ToLower(s)
	return s
}

// HasAllowlist reports whether strict allowlist mode is active.
func (p *AccessPolicy) HasAllowlist() bool {
	return len(p.allowIDs) > 0 || len(p.allowUsernames) > 0
}

// Allowed reports whether a chat with the given canonical signed ID, raw MTProto ID,
// and optional username may be read.
func (p *AccessPolicy) Allowed(chatID int64, rawID int64, username string) bool {
	if !p.allowTelegramServiceChat && (chatID == TelegramServiceChatID || rawID == TelegramServiceChatID) {
		return false
	}

	normUser := normalizeUsername(username)

	// Explicit denylist always wins.
	if _, denied := p.denyIDs[chatID]; denied {
		return false
	}
	if rawID != 0 {
		if _, denied := p.denyIDs[rawID]; denied {
			return false
		}
	}
	if normUser != "" {
		if _, denied := p.denyUsernames[normUser]; denied {
			return false
		}
	}

	// If no allowlist is configured, everything not denied is allowed.
	if !p.HasAllowlist() {
		return true
	}

	if _, ok := p.allowIDs[chatID]; ok {
		return true
	}
	if rawID != 0 {
		if _, ok := p.allowIDs[rawID]; ok {
			return true
		}
	}
	if normUser != "" {
		if _, ok := p.allowUsernames[normUser]; ok {
			return true
		}
	}
	return false
}

// Summary returns a human-readable summary of the active access policy.
func (p *AccessPolicy) Summary() string {
	var parts []string
	if p.HasAllowlist() {
		parts = append(parts, fmt.Sprintf("allowlist(%d ids, %d usernames)", len(p.allowIDs), len(p.allowUsernames)))
	} else {
		parts = append(parts, "all-chats-default")
	}
	if len(p.denyIDs) > 0 || len(p.denyUsernames) > 0 {
		parts = append(parts, fmt.Sprintf("denylist(%d ids, %d usernames)", len(p.denyIDs), len(p.denyUsernames)))
	}
	if p.allowTelegramServiceChat {
		parts = append(parts, "service-chat-777000=allowed")
	} else {
		parts = append(parts, "service-chat-777000=blocked")
	}
	return strings.Join(parts, ", ")
}

// SafeMediaPath builds a deterministic, traversal-proof file path inside mediaDir.
func SafeMediaPath(mediaDir string, chatID int64, msgID int, origName string) (string, error) {
	if strings.TrimSpace(mediaDir) == "" {
		return "", fmt.Errorf("media directory must not be empty")
	}
	absDir, err := filepath.Abs(mediaDir)
	if err != nil {
		return "", fmt.Errorf("resolve media directory: %w", err)
	}
	cleanName := sanitizeFilename(origName)
	fileName := fmt.Sprintf("chat_%d_msg_%d_%s", chatID, msgID, cleanName)
	target := filepath.Join(absDir, fileName)

	rel, err := filepath.Rel(absDir, target)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("computed media path escapes cache directory")
	}
	return target, nil
}

func sanitizeFilename(raw string) string {
	base := filepath.Base(strings.ReplaceAll(raw, "\\", "/"))
	base = strings.TrimSpace(base)
	if base == "" || base == "." || base == ".." || base == "/" {
		return "attachment.bin"
	}

	var b strings.Builder
	b.Grow(len(base))
	for _, r := range base {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	cleaned := strings.Trim(b.String(), "._")
	if cleaned == "" {
		return "attachment.bin"
	}
	if len(cleaned) > 120 {
		ext := filepath.Ext(cleaned)
		if len(ext) > 16 {
			ext = ""
		}
		stem := cleaned[:120-len(ext)]
		cleaned = stem + ext
	}
	return cleaned
}
