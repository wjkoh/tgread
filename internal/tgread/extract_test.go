package tgread

import (
	"testing"
	"unicode/utf16"

	"github.com/gotd/td/tg"
)

func TestExtractURLsUTF16AndWebPreview(t *testing.T) {
	// Include surrogate-pair emoji (2 UTF-16 code units) and Korean Hangul before links.
	prefix := "📄 보고서: "
	anchor := "Barbell Deck"
	mid := " and "
	rawURL := "https://wjkoh.com/brief"
	text := prefix + anchor + mid + rawURL

	prefixLen := len(utf16.Encode([]rune(prefix)))
	anchorLen := len(utf16.Encode([]rune(anchor)))
	midLen := len(utf16.Encode([]rune(mid)))
	rawURLLen := len(utf16.Encode([]rune(rawURL)))

	entities := []tg.MessageEntityClass{
		&tg.MessageEntityTextURL{
			Offset: prefixLen,
			Length: anchorLen,
			URL:    "https://example.com/deck.pdf",
		},
		&tg.MessageEntityURL{
			Offset: prefixLen + anchorLen + midLen,
			Length: rawURLLen,
		},
	}

	media := &tg.MessageMediaWebPage{
		Webpage: &tg.WebPage{
			URL:         "https://wjkoh.com/brief",
			DisplayURL:  "wjkoh.com/brief",
			SiteName:    "wjkoh.com",
			Title:       "Brief",
			Description: "Summary",
		},
	}

	urls := ExtractURLs(text, entities, media)
	if len(urls) != 2 {
		t.Fatalf("expected 2 deduplicated URLs, got %d: %+v", len(urls), urls)
	}
	if urls[0].URL != "https://example.com/deck.pdf" || urls[0].Anchor != "Barbell Deck" || urls[0].Source != "entity_text_url" {
		t.Fatalf("unexpected hidden URL entry: %+v", urls[0])
	}
	if urls[1].URL != "https://wjkoh.com/brief" || urls[1].Source != "entity_url" {
		t.Fatalf("unexpected plain URL entry: %+v", urls[1])
	}
}

func TestExtractAttachmentPDFAndPhoto(t *testing.T) {
	docMedia := &tg.MessageMediaDocument{
		Document: &tg.Document{
			ID:            999,
			AccessHash:    111,
			FileReference: []byte("ref"),
			MimeType:      "application/pdf",
			Size:          204800,
			Attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeFilename{FileName: "Q4_Report.pdf"},
			},
		},
	}
	att, loc := ExtractAttachment(docMedia)
	if att == nil || loc == nil {
		t.Fatalf("expected downloadable PDF attachment")
	}
	if att.Kind != "document" || att.FileName != "Q4_Report.pdf" || att.MimeType != "application/pdf" || att.SizeBytes != 204800 {
		t.Fatalf("unexpected PDF metadata: %+v", att)
	}

	photoMedia := &tg.MessageMediaPhoto{
		Photo: &tg.Photo{
			ID:            555,
			AccessHash:    222,
			FileReference: []byte("pref"),
			Sizes: []tg.PhotoSizeClass{
				&tg.PhotoSize{Type: "m", W: 320, H: 240, Size: 12000},
				&tg.PhotoSizeProgressive{Type: "y", W: 1280, H: 960, Sizes: []int{20000, 95000}},
			},
		},
	}
	pAtt, pLoc := ExtractAttachment(photoMedia)
	if pAtt == nil || pLoc == nil {
		t.Fatalf("expected downloadable photo attachment")
	}
	if pAtt.Kind != "photo" || pAtt.SizeBytes != 95000 {
		t.Fatalf("unexpected photo metadata: %+v", pAtt)
	}
	photoLoc, ok := pLoc.(*tg.InputPhotoFileLocation)
	if !ok || photoLoc.ThumbSize != "y" {
		t.Fatalf("expected largest thumb size 'y', got %+v", pLoc)
	}
}

func TestCanonicalPeerIDs(t *testing.T) {
	uid, uraw, utype := CanonicalPeerID(&tg.PeerUser{UserID: 42})
	if uid != 42 || uraw != 42 || utype != "user" {
		t.Fatalf("unexpected user ID: %d %d %s", uid, uraw, utype)
	}
	cid, craw, ctype := CanonicalPeerID(&tg.PeerChat{ChatID: 77})
	if cid != -77 || craw != 77 || ctype != "group" {
		t.Fatalf("unexpected chat ID: %d %d %s", cid, craw, ctype)
	}
	chid, chraw, chtype := CanonicalPeerID(&tg.PeerChannel{ChannelID: 1234567890})
	if chid != -1001234567890 || chraw != 1234567890 || chtype != "channel" {
		t.Fatalf("unexpected channel ID: %d %d %s", chid, chraw, chtype)
	}
}
