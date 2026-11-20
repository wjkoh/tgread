package mcpserver

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wjkoh/tgread/internal/tgread"
)

// Reader defines the strictly read-only Telegram operations exposed over MCP.
type Reader interface {
	ListChats(ctx context.Context, opts tgread.ListChatsOptions) ([]tgread.ChatInfo, error)
	GetMessages(ctx context.Context, opts tgread.GetMessagesOptions) ([]tgread.MessageInfo, error)
	SearchMessages(ctx context.Context, opts tgread.SearchMessagesOptions) ([]tgread.MessageInfo, error)
	DownloadAttachment(ctx context.Context, opts tgread.DownloadOptions) ([]tgread.DownloadedFile, error)
}

type listChatsInput struct {
	Limit      int    `json:"limit,omitempty" jsonschema:"Maximum number of chats to return (default 30, max 100)"`
	UnreadOnly bool   `json:"unread_only,omitempty" jsonschema:"If true, return only chats with unread messages"`
	Query      string `json:"query,omitempty" jsonschema:"Optional case-insensitive substring filter on chat title or username"`
}

type listChatsOutput struct {
	Count int               `json:"count"`
	Chats []tgread.ChatInfo `json:"chats"`
}

type getMessagesInput struct {
	Chat     string `json:"chat" jsonschema:"Chat identifier: numeric chat_id from list_chats, @username, or 'me' for Saved Messages"`
	Limit    int    `json:"limit,omitempty" jsonschema:"Maximum number of messages to return (default 20, max 100)"`
	OffsetID int    `json:"offset_id,omitempty" jsonschema:"Optional message ID to paginate older messages before this ID"`
	MinDate  string `json:"min_date,omitempty" jsonschema:"Optional earliest date filter (RFC3339 or YYYY-MM-DD)"`
	MaxDate  string `json:"max_date,omitempty" jsonschema:"Optional latest date filter (RFC3339 or YYYY-MM-DD)"`
}

type messagesOutput struct {
	Count    int                  `json:"count"`
	Messages []tgread.MessageInfo `json:"messages"`
}

type searchMessagesInput struct {
	Query  string `json:"query,omitempty" jsonschema:"Text search query"`
	Chat   string `json:"chat,omitempty" jsonschema:"Optional chat identifier (numeric chat_id, @username, or 'me'); omit to search globally across allowed chats"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum number of matching messages to return (default 20, max 100)"`
	Filter string `json:"filter,omitempty" jsonschema:"Optional media filter: all, photos, documents, urls, audio, voice, video"`
}

type downloadAttachmentInput struct {
	Chat         string `json:"chat" jsonschema:"Chat identifier: numeric chat_id, @username, or 'me'"`
	MessageID    int    `json:"message_id" jsonschema:"Message ID containing the photo, PDF, or file attachment"`
	IncludeAlbum bool   `json:"include_album,omitempty" jsonschema:"If true and the message belongs to a multi-item album (grouped_id), download all attachments in the album"`
}

type downloadAttachmentOutput struct {
	Count int                     `json:"count"`
	Files []tgread.DownloadedFile `json:"files"`
}

// New constructs an MCP server wired with strictly read-only Telegram tools.
func New(reader Reader, version string) (*mcp.Server, error) {
	if reader == nil {
		return nil, errors.New("reader is required")
	}
	if version == "" {
		version = "0.1.0"
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "tgread",
		Version: version,
	}, &mcp.ServerOptions{
		Instructions: "Read-only access to the user's Telegram cloud messages, links, and attachments. Never sends, edits, deletes, or marks messages as read. Call list_chats to find chat IDs, get_messages or search_messages to inspect messages and URLs, and download_attachment to save a PDF or image locally for reading.",
	})

	readOnlyAnn := func(title string) *mcp.ToolAnnotations {
		destructive := false
		openWorld := true
		return &mcp.ToolAnnotations{
			Title:           title,
			ReadOnlyHint:    true,
			DestructiveHint: &destructive,
			IdempotentHint:  true,
			OpenWorldHint:   &openWorld,
		}
	}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_chats",
		Description: "List recent Telegram cloud chats, groups, channels, and Saved Messages with unread counts and latest message previews. Read-only; does not mark anything read.",
		Annotations: readOnlyAnn("List Telegram Chats"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listChatsInput) (*mcp.CallToolResult, listChatsOutput, error) {
		chats, err := reader.ListChats(ctx, tgread.ListChatsOptions{
			Limit:      in.Limit,
			UnreadOnly: in.UnreadOnly,
			Query:      in.Query,
		})
		if err != nil {
			return nil, listChatsOutput{}, err
		}
		if chats == nil {
			chats = []tgread.ChatInfo{}
		}
		return nil, listChatsOutput{Count: len(chats), Chats: chats}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_messages",
		Description: "Fetch recent messages from a Telegram chat with resolved sender names, full text/captions, extracted URLs (including hidden hyperlinks), web preview cards, and attachment metadata. Read-only; does not mark messages read.",
		Annotations: readOnlyAnn("Get Telegram Messages"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getMessagesInput) (*mcp.CallToolResult, messagesOutput, error) {
		msgs, err := reader.GetMessages(ctx, tgread.GetMessagesOptions{
			Chat:     in.Chat,
			Limit:    in.Limit,
			OffsetID: in.OffsetID,
			MinDate:  in.MinDate,
			MaxDate:  in.MaxDate,
		})
		if err != nil {
			return nil, messagesOutput{}, err
		}
		if msgs == nil {
			msgs = []tgread.MessageInfo{}
		}
		return nil, messagesOutput{Count: len(msgs), Messages: msgs}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_messages",
		Description: "Search Telegram messages within a specific chat or globally across all allowed chats, optionally filtering by media type (photos, documents/PDFs, urls, audio, voice, video).",
		Annotations: readOnlyAnn("Search Telegram Messages"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in searchMessagesInput) (*mcp.CallToolResult, messagesOutput, error) {
		msgs, err := reader.SearchMessages(ctx, tgread.SearchMessagesOptions{
			Query:  in.Query,
			Chat:   in.Chat,
			Limit:  in.Limit,
			Filter: in.Filter,
		})
		if err != nil {
			return nil, messagesOutput{}, err
		}
		if msgs == nil {
			msgs = []tgread.MessageInfo{}
		}
		return nil, messagesOutput{Count: len(msgs), Messages: msgs}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "download_attachment",
		Description: "Download an attached PDF, image, audio note, or document from a Telegram message into the local cache directory and return its file path, MIME type, size, and SHA-256 digest.",
		Annotations: readOnlyAnn("Download Telegram Attachment"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in downloadAttachmentInput) (*mcp.CallToolResult, downloadAttachmentOutput, error) {
		files, err := reader.DownloadAttachment(ctx, tgread.DownloadOptions{
			Chat:         in.Chat,
			MessageID:    in.MessageID,
			IncludeAlbum: in.IncludeAlbum,
		})
		if err != nil {
			return nil, downloadAttachmentOutput{}, err
		}
		if files == nil {
			files = []tgread.DownloadedFile{}
		}
		return nil, downloadAttachmentOutput{Count: len(files), Files: files}, nil
	})

	return server, nil
}
