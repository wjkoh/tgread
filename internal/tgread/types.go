package tgread

// ChatInfo represents a cloud Telegram chat, group, or channel.
type ChatInfo struct {
	ChatID             int64  `json:"chat_id"`
	Type               string `json:"type"` // "self", "user", "group", "channel"
	Title              string `json:"title"`
	Username           string `json:"username,omitempty"`
	UnreadCount        int    `json:"unread_count"`
	LastMessageID      int    `json:"last_message_id,omitempty"`
	LastMessageDate    string `json:"last_message_date,omitempty"`
	LastMessagePreview string `json:"last_message_preview,omitempty"`
}

// ExtractedURL represents a URL extracted from message text, hidden link entities, or web previews.
type ExtractedURL struct {
	URL    string `json:"url"`
	Anchor string `json:"anchor,omitempty"`
	Source string `json:"source"` // "entity_url", "entity_text_url", "web_preview"
}

// WebPreviewInfo holds Telegram's server-side link preview metadata.
type WebPreviewInfo struct {
	URL         string `json:"url"`
	DisplayURL  string `json:"display_url,omitempty"`
	SiteName    string `json:"site_name,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type,omitempty"`
}

// AttachmentInfo describes a file or photo attached to a message without downloading its bytes.
type AttachmentInfo struct {
	Kind         string `json:"kind"` // "photo", "document", "video", "audio", "voice", "sticker", "other"
	FileName     string `json:"file_name,omitempty"`
	MimeType     string `json:"mime_type,omitempty"`
	SizeBytes    int64  `json:"size_bytes,omitempty"`
	Downloadable bool   `json:"downloadable"`
}

// MessageInfo is a normalized, AI-friendly representation of a Telegram message.
type MessageInfo struct {
	ID             int             `json:"id"`
	ChatID         int64           `json:"chat_id"`
	ChatTitle      string          `json:"chat_title,omitempty"`
	Date           string          `json:"date"`
	EditDate       string          `json:"edit_date,omitempty"`
	SenderID       int64           `json:"sender_id,omitempty"`
	SenderName     string          `json:"sender_name,omitempty"`
	SenderUsername string          `json:"sender_username,omitempty"`
	Outgoing       bool            `json:"outgoing"`
	ReplyToMsgID   int             `json:"reply_to_msg_id,omitempty"`
	GroupedID      int64           `json:"grouped_id,omitempty"`
	ForwardFrom    string          `json:"forward_from,omitempty"`
	Text           string          `json:"text"`
	URLs           []ExtractedURL  `json:"urls,omitempty"`
	WebPreview     *WebPreviewInfo `json:"web_preview,omitempty"`
	Attachment     *AttachmentInfo `json:"attachment,omitempty"`
}

// DownloadedFile describes a downloaded attachment cached on local disk.
type DownloadedFile struct {
	ChatID    int64  `json:"chat_id"`
	MessageID int    `json:"message_id"`
	Kind      string `json:"kind"`
	FileName  string `json:"file_name"`
	MimeType  string `json:"mime_type,omitempty"`
	SizeBytes int64  `json:"size_bytes"`
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	Cached    bool   `json:"cached"`
}

// StatusInfo reports authentication and policy state for the current session.
type StatusInfo struct {
	Authorized    bool   `json:"authorized"`
	UserID        int64  `json:"user_id,omitempty"`
	Username      string `json:"username,omitempty"`
	DisplayName   string `json:"display_name,omitempty"`
	PolicySummary string `json:"policy_summary"`
	MediaDir      string `json:"media_dir"`
	MaxMediaBytes int64  `json:"max_media_bytes"`
}

// ListChatsOptions configures ListChats.
type ListChatsOptions struct {
	Limit      int
	UnreadOnly bool
	Query      string
}

// GetMessagesOptions configures GetMessages.
type GetMessagesOptions struct {
	Chat     string
	Limit    int
	OffsetID int
	MinDate  string // optional RFC3339 or YYYY-MM-DD
	MaxDate  string // optional RFC3339 or YYYY-MM-DD
}

// SearchMessagesOptions configures SearchMessages.
type SearchMessagesOptions struct {
	Query  string
	Chat   string // optional; empty triggers global search across allowed chats
	Limit  int
	Filter string // "all", "photos", "documents", "urls", "audio", "voice", "video"
}

// DownloadOptions configures DownloadAttachment.
type DownloadOptions struct {
	Chat         string
	MessageID    int
	IncludeAlbum bool
}
