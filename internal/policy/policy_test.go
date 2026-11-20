package policy

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAccessPolicyDefaultsBlockServiceChat(t *testing.T) {
	p, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.Allowed(TelegramServiceChatID, TelegramServiceChatID, "") {
		t.Fatalf("expected Telegram service chat 777000 to be blocked by default")
	}
	if !p.Allowed(12345, 12345, "alice") {
		t.Fatalf("expected regular chat to be allowed by default")
	}
}

func TestAccessPolicyOptInServiceChat(t *testing.T) {
	p, err := New(Config{AllowTelegramServiceChat: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !p.Allowed(TelegramServiceChatID, TelegramServiceChatID, "") {
		t.Fatalf("expected service chat 777000 to be allowed when explicitly enabled")
	}
}

func TestAccessPolicyAllowAndDenyPrecedence(t *testing.T) {
	p, err := New(Config{
		AllowChats: []string{"100", "@TeamChannel", "-1002222222222"},
		DenyChats:  []string{"@teamchannel", "999"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if !p.Allowed(100, 100, "alice") {
		t.Fatalf("expected allowlisted numeric ID 100 to be allowed")
	}
	if !p.Allowed(-1002222222222, 2222222222, "ops") {
		t.Fatalf("expected allowlisted supergroup ID to be allowed")
	}
	if p.Allowed(555, 555, "TeamChannel") {
		t.Fatalf("expected denylist to override allowlist on username")
	}
	if p.Allowed(42, 42, "other") {
		t.Fatalf("expected unlisted chat to be denied when allowlist is active")
	}
}

func TestSafeMediaPathTraversalProtection(t *testing.T) {
	base := t.TempDir()
	cases := []string{
		"../../etc/passwd",
		`..\..\Windows\System32\cmd.exe`,
		"report (Q4 2026).pdf",
		"...",
		"",
	}
	for _, name := range cases {
		got, err := SafeMediaPath(base, -1001234567890, 42, name)
		if err != nil {
			t.Fatalf("SafeMediaPath(%q): %v", name, err)
		}
		rel, err := filepath.Rel(base, got)
		if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			t.Fatalf("path %q escaped base %q (rel=%q)", got, base, rel)
		}
		if strings.Contains(rel, string(filepath.Separator)) {
			t.Fatalf("expected flat filename inside cache dir, got %q", rel)
		}
	}
}
