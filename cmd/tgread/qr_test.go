package main

import (
	"strings"
	"testing"

	"github.com/gotd/td/telegram/auth/qrlogin"
)

func TestRenderQRToTerminal(t *testing.T) {
	tok := qrlogin.NewToken([]byte("0123456789abcdef0123456789abcdef"), 0)
	img, err := tok.Image(0)
	if err != nil {
		t.Fatalf("tok.Image: %v", err)
	}
	rendered := renderQRToTerminal(img)
	lines := strings.Split(strings.TrimSuffix(rendered, "\n"), "\n")
	if len(lines) < 10 || len(lines) > 40 {
		t.Fatalf("expected compact QR terminal output (10..40 rows), got %d rows", len(lines))
	}
	if !strings.Contains(rendered, "█") || !strings.Contains(rendered, "▀") {
		t.Fatalf("expected Unicode half-block glyphs in rendered QR")
	}
}
