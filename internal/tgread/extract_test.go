package tgread

import (
	"testing"
	"unicode/utf16"

	"github.com/gotd/td/tg"
)

func TestExtractURLsUTF16AndWebPreview(t *testing.T) {
	// Include surrogate-pair emoji (2 UTF-16 code units) and Korean Hangul before links.
	prefix := "📄 보고서: "
	anchor := "Quarterly Deck"
	mid := " and "
	rawURL := "https://example.com/brief"
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
			URL:         "https://example.com/brief",
			DisplayURL:  "example.com/brief",
			SiteName:    "example.com",
			Title:       "Brief",
			Description: "Summary",
		},
	}

	urls := ExtractURLs(text, entities, media)
	if len(urls) != 2 {
		t.Fatalf("expected 2 deduplicated URLs, got %d: %+v", len(urls), urls)
	}
	if urls[0].URL != "https://example.com/deck.pdf" || urls[0].Anchor != "Quarterly Deck" || urls[0].Source != "entity_text_url" {
		t.Fatalf("unexpected hidden URL entry: %+v", urls[0])
	}
	if urls[1].URL != "https://example.com/brief" || urls[1].Source != "entity_url" {
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

func TestNormalizeMessageSelfSenderUsesDisplayName(t *testing.T) {
	idx := newEntityIndex([]tg.UserClass{
		&tg.User{ID: 100, Self: true, FirstName: "Alice", LastName: "Smith", Username: "alice"},
		&tg.User{ID: 200, FirstName: "Bob", LastName: "Lee"},
	}, nil)

	dmMsg := &tg.Message{
		ID:      10,
		Out:     true,
		PeerID:  &tg.PeerUser{UserID: 200},
		FromID:  &tg.PeerUser{UserID: 100},
		Date:    1700000000,
		Message: "Hello",
	}
	dmMsg.SetFlags()
	gotDM := NormalizeMessage(dmMsg, idx, 200, "Bob Lee")
	if gotDM.SenderName != "Alice Smith" {
		t.Fatalf("expected outgoing DM sender_name 'Alice Smith', got %q", gotDM.SenderName)
	}
	if gotDM.ChatTitle != "Bob Lee" {
		t.Fatalf("expected chat_title 'Bob Lee', got %q", gotDM.ChatTitle)
	}

	savedMsg := &tg.Message{
		ID:      11,
		Out:     true,
		PeerID:  &tg.PeerUser{UserID: 100},
		FromID:  &tg.PeerUser{UserID: 100},
		Date:    1700000001,
		Message: "Note to self",
	}
	savedMsg.SetFlags()
	gotSaved := NormalizeMessage(savedMsg, idx, 100, "Saved Messages")
	if gotSaved.ChatTitle != "Saved Messages" {
		t.Fatalf("expected Saved Messages chat_title, got %q", gotSaved.ChatTitle)
	}
	if gotSaved.SenderName != "Alice Smith" {
		t.Fatalf("expected Saved Messages sender_name 'Alice Smith', got %q", gotSaved.SenderName)
	}
}
