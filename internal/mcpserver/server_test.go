package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wjkoh/tgread/internal/tgread"
)

type fakeReader struct{}

func (fakeReader) ListChats(_ context.Context, _ tgread.ListChatsOptions) ([]tgread.ChatInfo, error) {
	return []tgread.ChatInfo{
		{ChatID: 42, Type: "user", Title: "Alice", Username: "alice", UnreadCount: 2},
	}, nil
}

func (fakeReader) GetMessages(_ context.Context, opts tgread.GetMessagesOptions) ([]tgread.MessageInfo, error) {
	return []tgread.MessageInfo{
		{
			ID:     101,
			ChatID: 42,
			Text:   "Attached PDF for " + opts.Chat,
			URLs:   []tgread.ExtractedURL{{URL: "https://example.com", Source: "entity_url"}},
			Attachment: &tgread.AttachmentInfo{
				Kind:         "document",
				FileName:     "memo.pdf",
				MimeType:     "application/pdf",
				SizeBytes:    1024,
				Downloadable: true,
			},
		},
	}, nil
}

func (fakeReader) SearchMessages(_ context.Context, opts tgread.SearchMessagesOptions) ([]tgread.MessageInfo, error) {
	return []tgread.MessageInfo{
		{ID: 202, ChatID: 42, Text: "Match for " + opts.Query},
	}, nil
}

func (fakeReader) DownloadAttachment(_ context.Context, opts tgread.DownloadOptions) ([]tgread.DownloadedFile, error) {
	return []tgread.DownloadedFile{
		{
			ChatID:    42,
			MessageID: opts.MessageID,
			Kind:      "document",
			FileName:  "memo.pdf",
			MimeType:  "application/pdf",
			SizeBytes: 1024,
			Path:      "/tmp/chat_42_msg_101_memo.pdf",
			SHA256:    "deadbeef",
		},
	}, nil
}

func TestMCPServerReadOnlyToolsEndToEnd(t *testing.T) {
	ctx := context.Background()

	srv, err := New(fakeReader{}, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server.Connect: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer session.Close()

	toolsRes, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(toolsRes.Tools) != 4 {
		t.Fatalf("expected 4 read-only tools, got %d", len(toolsRes.Tools))
	}
	for _, tool := range toolsRes.Tools {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatalf("tool %q missing ReadOnlyHint=true annotation", tool.Name)
		}
	}

	callRes, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "list_chats",
		Arguments: map[string]any{"unread_only": true},
	})
	if err != nil {
		t.Fatalf("CallTool(list_chats): %v", err)
	}
	if callRes.IsError {
		t.Fatalf("expected list_chats to succeed")
	}

	dlRes, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "download_attachment",
		Arguments: map[string]any{"chat": "42", "message_id": 101},
	})
	if err != nil {
		t.Fatalf("CallTool(download_attachment): %v", err)
	}
	rawBytes, err := json.Marshal(dlRes.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var out downloadAttachmentOutput
	if err := json.Unmarshal(rawBytes, &out); err != nil {
		t.Fatalf("unmarshal download output: %v", err)
	}
	if out.Count != 1 || out.Files[0].FileName != "memo.pdf" {
		t.Fatalf("unexpected download response: %+v", out)
	}
}
