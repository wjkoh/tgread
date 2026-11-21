package tgread

import (
	"fmt"
	"mime"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/gotd/td/tg"
)

const channelIDOffset int64 = 1_000_000_000_000

// CanonicalUserID returns the signed canonical ID for a Telegram user.
func CanonicalUserID(userID int64) int64 {
	return userID
}

// CanonicalChatID returns the signed canonical ID for a basic Telegram group chat.
func CanonicalChatID(chatID int64) int64 {
	return -chatID
}

// CanonicalChannelID returns the Bot-API-compatible signed canonical ID (-100<id>) for a channel or supergroup.
func CanonicalChannelID(channelID int64) int64 {
	return -(channelIDOffset + channelID)
}

// CanonicalPeerID converts a tg.PeerClass into its canonical signed ID and raw MTProto ID.
func CanonicalPeerID(peer tg.PeerClass) (chatID int64, rawID int64, peerType string) {
	switch p := peer.(type) {
	case *tg.PeerUser:
		return CanonicalUserID(p.UserID), p.UserID, "user"
	case *tg.PeerChat:
		return CanonicalChatID(p.ChatID), p.ChatID, "group"
	case *tg.PeerChannel:
		return CanonicalChannelID(p.ChannelID), p.ChannelID, "channel"
	default:
		return 0, 0, "unknown"
	}
}

// entityIndex holds users and chats returned alongside a Telegram API response.
type entityIndex struct {
	users    map[int64]*tg.User
	chats    map[int64]*tg.Chat
	channels map[int64]*tg.Channel
}

func newEntityIndex(users []tg.UserClass, chats []tg.ChatClass) *entityIndex {
	idx := &entityIndex{
		users:    make(map[int64]*tg.User, len(users)),
		chats:    make(map[int64]*tg.Chat, len(chats)),
		channels: make(map[int64]*tg.Channel, len(chats)),
	}
	for _, u := range users {
		if user, ok := u.(*tg.User); ok {
			idx.users[user.ID] = user
		}
	}
	for _, c := range chats {
		switch ch := c.(type) {
		case *tg.Chat:
			idx.chats[ch.ID] = ch
		case *tg.Channel:
			idx.channels[ch.ID] = ch
		}
	}
	return idx
}

func formatUserName(u *tg.User) string {
	if u == nil {
		return ""
	}
	full := strings.TrimSpace(strings.TrimSpace(u.FirstName) + " " + strings.TrimSpace(u.LastName))
	if full != "" {
		return full
	}
	if u.Username != "" {
		return "@" + u.Username
	}
	return fmt.Sprintf("user_%d", u.ID)
}

func (idx *entityIndex) peerMeta(peer tg.PeerClass) (chatID int64, rawID int64, title string, username string, peerType string) {
	chatID, rawID, peerType = CanonicalPeerID(peer)
	if idx == nil {
		return chatID, rawID, "", "", peerType
	}
	switch p := peer.(type) {
	case *tg.PeerUser:
		if u, ok := idx.users[p.UserID]; ok {
			if u.Self {
				return chatID, rawID, "Saved Messages", u.Username, "self"
			}
			return chatID, rawID, formatUserName(u), u.Username, "user"
		}
	case *tg.PeerChat:
		if c, ok := idx.chats[p.ChatID]; ok {
			return chatID, rawID, c.Title, "", "group"
		}
	case *tg.PeerChannel:
		if ch, ok := idx.channels[p.ChannelID]; ok {
			return chatID, rawID, ch.Title, ch.Username, "channel"
		}
	}
	return chatID, rawID, "", "", peerType
}

// senderMeta resolves a message sender or forward origin using the user's actual display name
// rather than "Saved Messages" when the peer is the current user.
func (idx *entityIndex) senderMeta(peer tg.PeerClass) (senderID int64, name string, username string) {
	if p, ok := peer.(*tg.PeerUser); ok && idx != nil {
		if u, found := idx.users[p.UserID]; found {
			return CanonicalUserID(u.ID), formatUserName(u), u.Username
		}
	}
	id, _, title, uname, _ := idx.peerMeta(peer)
	return id, title, uname
}

// SliceUTF16 extracts a substring using Telegram's UTF-16 code unit offset and length.
func SliceUTF16(text string, offset, length int) string {
	if offset < 0 || length <= 0 {
		return ""
	}
	units := utf16.Encode([]rune(text))
	if offset >= len(units) {
		return ""
	}
	end := offset + length
	if end > len(units) {
		end = len(units)
	}
	return string(utf16.Decode(units[offset:end]))
}

// ExtractURLs extracts plain URLs, hidden hyperlink entities, and web preview URLs from a message.
func ExtractURLs(text string, entities []tg.MessageEntityClass, media tg.MessageMediaClass) []ExtractedURL {
	seen := make(map[string]struct{})
	var out []ExtractedURL

	add := func(u ExtractedURL) {
		u.URL = strings.TrimSpace(u.URL)
		if u.URL == "" {
			return
		}
		key := u.URL + "\x00" + u.Anchor
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		out = append(out, u)
	}

	for _, e := range entities {
		switch ent := e.(type) {
		case *tg.MessageEntityURL:
			raw := SliceUTF16(text, ent.Offset, ent.Length)
			add(ExtractedURL{URL: raw, Source: "entity_url"})
		case *tg.MessageEntityTextURL:
			anchor := SliceUTF16(text, ent.Offset, ent.Length)
			add(ExtractedURL{URL: ent.URL, Anchor: anchor, Source: "entity_text_url"})
		}
	}

	if wp := ExtractWebPreview(media); wp != nil && wp.URL != "" {
		// Avoid duplicating a URL already captured without an anchor.
		alreadyHave := false
		for _, existing := range out {
			if existing.URL == wp.URL {
				alreadyHave = true
				break
			}
		}
		if !alreadyHave {
			add(ExtractedURL{URL: wp.URL, Anchor: wp.Title, Source: "web_preview"})
		}
	}

	return out
}

// ExtractWebPreview pulls structured preview metadata from a MessageMediaWebPage.
func ExtractWebPreview(media tg.MessageMediaClass) *WebPreviewInfo {
	mwp, ok := media.(*tg.MessageMediaWebPage)
	if !ok || mwp == nil {
		return nil
	}
	wp, ok := mwp.Webpage.(*tg.WebPage)
	if !ok || wp == nil {
		return nil
	}
	info := &WebPreviewInfo{
		URL:        wp.URL,
		DisplayURL: wp.DisplayURL,
	}
	if v, ok := wp.GetSiteName(); ok {
		info.SiteName = v
	}
	if v, ok := wp.GetTitle(); ok {
		info.Title = v
	}
	if v, ok := wp.GetDescription(); ok {
		info.Description = v
	}
	if v, ok := wp.GetType(); ok {
		info.Type = v
	}
	return info
}

type downloadableTarget struct {
	info     AttachmentInfo
	location tg.InputFileLocationClass
}

// ExtractAttachment inspects message media and returns attachment metadata and its MTProto file location.
func ExtractAttachment(media tg.MessageMediaClass) (*AttachmentInfo, tg.InputFileLocationClass) {
	target := extractDownloadableTarget(media)
	if target == nil {
		return nil, nil
	}
	info := target.info
	return &info, target.location
}

func extractDownloadableTarget(media tg.MessageMediaClass) *downloadableTarget {
	switch m := media.(type) {
	case *tg.MessageMediaPhoto:
		photo, ok := m.Photo.(*tg.Photo)
		if !ok || photo == nil {
			return nil
		}
		thumbType, size := largestPhotoSize(photo.Sizes)
		if thumbType == "" {
			return nil
		}
		return &downloadableTarget{
			info: AttachmentInfo{
				Kind:         "photo",
				FileName:     fmt.Sprintf("photo_%d.jpg", photo.ID),
				MimeType:     "image/jpeg",
				SizeBytes:    size,
				Downloadable: true,
			},
			location: &tg.InputPhotoFileLocation{
				ID:            photo.ID,
				AccessHash:    photo.AccessHash,
				FileReference: photo.FileReference,
				ThumbSize:     thumbType,
			},
		}

	case *tg.MessageMediaDocument:
		doc, ok := m.Document.(*tg.Document)
		if !ok || doc == nil {
			return nil
		}
		kind, fileName := classifyDocument(doc)
		return &downloadableTarget{
			info: AttachmentInfo{
				Kind:         kind,
				FileName:     fileName,
				MimeType:     doc.MimeType,
				SizeBytes:    doc.Size,
				Downloadable: true,
			},
			location: &tg.InputDocumentFileLocation{
				ID:            doc.ID,
				AccessHash:    doc.AccessHash,
				FileReference: doc.FileReference,
			},
		}

	default:
		return nil
	}
}

func largestPhotoSize(sizes []tg.PhotoSizeClass) (thumbType string, maxBytes int64) {
	var bestArea int
	for _, s := range sizes {
		switch ps := s.(type) {
		case *tg.PhotoSize:
			area := ps.W * ps.H
			if area >= bestArea {
				bestArea = area
				thumbType = ps.Type
				maxBytes = int64(ps.Size)
			}
		case *tg.PhotoSizeProgressive:
			area := ps.W * ps.H
			var last int64
			if len(ps.Sizes) > 0 {
				last = int64(ps.Sizes[len(ps.Sizes)-1])
			}
			if area >= bestArea {
				bestArea = area
				thumbType = ps.Type
				maxBytes = last
			}
		}
	}
	return thumbType, maxBytes
}

func classifyDocument(doc *tg.Document) (kind string, fileName string) {
	kind = "document"
	for _, attr := range doc.Attributes {
		switch a := attr.(type) {
		case *tg.DocumentAttributeFilename:
			fileName = a.FileName
		case *tg.DocumentAttributeAudio:
			if a.Voice {
				kind = "voice"
			} else {
				kind = "audio"
			}
		case *tg.DocumentAttributeVideo:
			kind = "video"
		case *tg.DocumentAttributeSticker:
			kind = "sticker"
		}
	}

	if fileName == "" {
		ext := extensionForMime(doc.MimeType, kind)
		fileName = fmt.Sprintf("%s_%d%s", kind, doc.ID, ext)
	}
	return kind, fileName
}

func extensionForMime(mimeType, kind string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "application/pdf":
		return ".pdf"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "audio/ogg":
		return ".ogg"
	case "audio/mpeg":
		return ".mp3"
	case "video/mp4":
		return ".mp4"
	case "text/plain":
		return ".txt"
	}
	if mimeType != "" {
		if exts, err := mime.ExtensionsByType(mimeType); err == nil && len(exts) > 0 {
			return filepath.Clean(exts[0])
		}
	}
	if kind == "voice" {
		return ".ogg"
	}
	return ".bin"
}

// NormalizeMessage converts a tg.Message into a MessageInfo using resolved entity metadata.
func NormalizeMessage(msg *tg.Message, idx *entityIndex, fallbackChatID int64, fallbackChatTitle string) MessageInfo {
	chatID := fallbackChatID
	chatTitle := fallbackChatTitle
	if msg.PeerID != nil {
		id, _, title, _, _ := idx.peerMeta(msg.PeerID)
		if id != 0 {
			chatID = id
		}
		if title != "" {
			chatTitle = title
		}
	}

	out := MessageInfo{
		ID:        msg.ID,
		ChatID:    chatID,
		ChatTitle: chatTitle,
		Date:      time.Unix(int64(msg.Date), 0).UTC().Format(time.RFC3339),
		Outgoing:  msg.Out,
		Text:      msg.Message,
	}

	if editDate, ok := msg.GetEditDate(); ok && editDate > 0 {
		out.EditDate = time.Unix(int64(editDate), 0).UTC().Format(time.RFC3339)
	}
	if groupedID, ok := msg.GetGroupedID(); ok && groupedID != 0 {
		out.GroupedID = groupedID
	}

	// Sender resolution: FromID for groups/channels, or PeerID for incoming DMs.
	senderPeer, hasFrom := msg.GetFromID()
	if !hasFrom && !msg.Out && msg.PeerID != nil {
		senderPeer = msg.PeerID
	}
	if senderPeer != nil {
		sid, sname, suser := idx.senderMeta(senderPeer)
		out.SenderID = sid
		out.SenderName = sname
		out.SenderUsername = suser
	}

	if replyHdr, ok := msg.GetReplyTo(); ok {
		if rh, ok := replyHdr.(*tg.MessageReplyHeader); ok {
			if rid, ok := rh.GetReplyToMsgID(); ok {
				out.ReplyToMsgID = rid
			}
		}
	}

	if fwd, ok := msg.GetFwdFrom(); ok {
		if name, ok := fwd.GetFromName(); ok && name != "" {
			out.ForwardFrom = name
		} else if fpeer, ok := fwd.GetFromID(); ok && fpeer != nil {
			_, fname, fuser := idx.senderMeta(fpeer)
			if fname != "" {
				out.ForwardFrom = fname
			} else if fuser != "" {
				out.ForwardFrom = "@" + fuser
			}
		}
	}

	out.WebPreview = ExtractWebPreview(msg.Media)
	out.URLs = ExtractURLs(msg.Message, msg.Entities, msg.Media)
	att, _ := ExtractAttachment(msg.Media)
	out.Attachment = att

	return out
}

// ParseFlexibleDate parses RFC3339 or YYYY-MM-DD timestamps into UTC time.
func ParseFlexibleDate(raw string, endOfDay bool) (time.Time, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Time{}, nil
	}
	if ts, err := time.Parse(time.RFC3339, s); err == nil {
		return ts.UTC(), nil
	}
	if d, err := time.Parse("2006-01-02", s); err == nil {
		if endOfDay {
			return d.Add(24*time.Hour - time.Second).UTC(), nil
		}
		return d.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("invalid date %q (expected RFC3339 or YYYY-MM-DD)", raw)
}
