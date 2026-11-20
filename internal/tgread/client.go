package tgread

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"

	"github.com/wjkoh/tgread/internal/policy"
)

const (
	defaultListLimit    = 30
	defaultMessageLimit = 20
	maxQueryLimit       = 100
	defaultMaxMediaSize = 50 * 1024 * 1024 // 50 MB
)

// resolvedPeer stores an MTProto InputPeer together with its canonical metadata.
type resolvedPeer struct {
	chatID    int64
	rawID     int64
	peerType  string
	title     string
	username  string
	inputPeer tg.InputPeerClass
	channel   *tg.InputChannel
}

// Service implements strictly read-only Telegram operations on top of gotd/td.
type Service struct {
	api           *tg.Client
	dl            *downloader.Downloader
	policy        *policy.AccessPolicy
	mediaDir      string
	maxMediaBytes int64

	mu           sync.RWMutex
	peersByID    map[int64]resolvedPeer
	peersByRawID map[int64]resolvedPeer
	peersByUser  map[string]resolvedPeer
	selfPeer     *resolvedPeer
}

// NewService constructs a read-only Telegram service.
func NewService(api *tg.Client, accessPolicy *policy.AccessPolicy, mediaDir string, maxMediaBytes int64) (*Service, error) {
	if api == nil {
		return nil, errors.New("tg client is required")
	}
	if accessPolicy == nil {
		var err error
		accessPolicy, err = policy.New(policy.Config{})
		if err != nil {
			return nil, err
		}
	}
	if maxMediaBytes <= 0 {
		maxMediaBytes = defaultMaxMediaSize
	}
	if strings.TrimSpace(mediaDir) == "" {
		cacheBase, err := os.UserCacheDir()
		if err != nil {
			cacheBase = os.TempDir()
		}
		mediaDir = filepath.Join(cacheBase, "tgread", "media")
	}
	return &Service{
		api:           api,
		dl:            downloader.NewDownloader(),
		policy:        accessPolicy,
		mediaDir:      mediaDir,
		maxMediaBytes: maxMediaBytes,
		peersByID:     make(map[int64]resolvedPeer),
		peersByRawID:  make(map[int64]resolvedPeer),
		peersByUser:   make(map[string]resolvedPeer),
	}, nil
}

// Status verifies the active session and returns account and policy details.
func (s *Service) Status(ctx context.Context) (*StatusInfo, error) {
	users, err := s.api.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUserSelf{}})
	if err != nil {
		return nil, fmt.Errorf("fetch current user: %w", err)
	}
	info := &StatusInfo{
		Authorized:    true,
		PolicySummary: s.policy.Summary(),
		MediaDir:      s.mediaDir,
		MaxMediaBytes: s.maxMediaBytes,
	}
	for _, uc := range users {
		if u, ok := uc.(*tg.User); ok {
			info.UserID = u.ID
			info.Username = u.Username
			info.DisplayName = formatUserName(u)
			s.cacheUser(u)
			break
		}
	}
	return info, nil
}

// ListChats returns recent Telegram chats filtered by AccessPolicy and optional criteria.
func (s *Service) ListChats(ctx context.Context, opts ListChatsOptions) ([]ChatInfo, error) {
	limit := clampLimit(opts.Limit, defaultListLimit)
	queryLower := strings.ToLower(strings.TrimSpace(opts.Query))

	// Over-fetch slightly when filtering so callers still get up to limit results.
	fetchBatch := limit
	if opts.UnreadOnly || queryLower != "" || s.policy.HasAllowlist() {
		fetchBatch = maxQueryLimit
	}

	dialogs, idx, msgMap, err := s.fetchDialogs(ctx, fetchBatch)
	if err != nil {
		return nil, err
	}

	out := make([]ChatInfo, 0, limit)
	for _, dc := range dialogs {
		d, ok := dc.(*tg.Dialog)
		if !ok {
			continue
		}
		chatID, rawID, title, username, peerType := idx.peerMeta(d.Peer)
		if !s.policy.Allowed(chatID, rawID, username) {
			continue
		}
		if opts.UnreadOnly && d.UnreadCount == 0 {
			continue
		}
		if queryLower != "" {
			if !strings.Contains(strings.ToLower(title), queryLower) &&
				!strings.Contains(strings.ToLower(username), queryLower) {
				continue
			}
		}

		ci := ChatInfo{
			ChatID:        chatID,
			Type:          peerType,
			Title:         title,
			Username:      username,
			UnreadCount:   d.UnreadCount,
			LastMessageID: d.TopMessage,
		}
		if msg, ok := msgMap[peerMsgKey{chatID: chatID, msgID: d.TopMessage}]; ok {
			ci.LastMessageDate = time.Unix(int64(msg.Date), 0).UTC().Format(time.RFC3339)
			ci.LastMessagePreview = previewMessageText(msg)
		}
		out = append(out, ci)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

type peerMsgKey struct {
	chatID int64
	msgID  int
}

func (s *Service) fetchDialogs(ctx context.Context, limit int) ([]tg.DialogClass, *entityIndex, map[peerMsgKey]*tg.Message, error) {
	res, err := s.api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
		OffsetPeer: &tg.InputPeerEmpty{},
		Limit:      limit,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("messages.getDialogs: %w", err)
	}

	var (
		dialogs  []tg.DialogClass
		users    []tg.UserClass
		chats    []tg.ChatClass
		messages []tg.MessageClass
	)
	switch r := res.(type) {
	case *tg.MessagesDialogs:
		dialogs, users, chats, messages = r.Dialogs, r.Users, r.Chats, r.Messages
	case *tg.MessagesDialogsSlice:
		dialogs, users, chats, messages = r.Dialogs, r.Users, r.Chats, r.Messages
	case *tg.MessagesDialogsNotModified:
		return nil, newEntityIndex(nil, nil), map[peerMsgKey]*tg.Message{}, nil
	default:
		return nil, nil, nil, fmt.Errorf("unexpected dialogs response type %T", res)
	}

	idx := newEntityIndex(users, chats)
	s.cacheEntities(users, chats)

	msgMap := make(map[peerMsgKey]*tg.Message, len(messages))
	for _, mc := range messages {
		if m, ok := mc.(*tg.Message); ok && m.PeerID != nil {
			cid, _, _ := CanonicalPeerID(m.PeerID)
			msgMap[peerMsgKey{chatID: cid, msgID: m.ID}] = m
		}
	}
	return dialogs, idx, msgMap, nil
}

// GetMessages returns recent messages from a specific chat without marking anything read.
func (s *Service) GetMessages(ctx context.Context, opts GetMessagesOptions) ([]MessageInfo, error) {
	if strings.TrimSpace(opts.Chat) == "" {
		return nil, errors.New("chat is required (numeric chat_id, @username, or 'me')")
	}
	peer, err := s.resolveAndAuthorizeChat(ctx, opts.Chat)
	if err != nil {
		return nil, err
	}

	minTime, err := ParseFlexibleDate(opts.MinDate, false)
	if err != nil {
		return nil, fmt.Errorf("min_date: %w", err)
	}
	maxTime, err := ParseFlexibleDate(opts.MaxDate, true)
	if err != nil {
		return nil, fmt.Errorf("max_date: %w", err)
	}

	limit := clampLimit(opts.Limit, defaultMessageLimit)
	req := &tg.MessagesGetHistoryRequest{
		Peer:     peer.inputPeer,
		OffsetID: opts.OffsetID,
		Limit:    limit,
	}
	if !maxTime.IsZero() {
		req.OffsetDate = int(maxTime.Unix()) + 1
	}

	res, err := s.api.MessagesGetHistory(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("messages.getHistory: %w", err)
	}

	rawMsgs, users, chats, err := unpackMessages(res)
	if err != nil {
		return nil, err
	}
	s.cacheEntities(users, chats)
	idx := newEntityIndex(users, chats)

	out := make([]MessageInfo, 0, len(rawMsgs))
	for _, mc := range rawMsgs {
		msg, ok := mc.(*tg.Message)
		if !ok {
			continue
		}
		msgTime := time.Unix(int64(msg.Date), 0).UTC()
		if !minTime.IsZero() && msgTime.Before(minTime) {
			continue
		}
		if !maxTime.IsZero() && msgTime.After(maxTime) {
			continue
		}
		out = append(out, NormalizeMessage(msg, idx, peer.chatID, peer.title))
	}
	return out, nil
}

// SearchMessages searches within a single chat or globally across all allowed chats.
func (s *Service) SearchMessages(ctx context.Context, opts SearchMessagesOptions) ([]MessageInfo, error) {
	query := strings.TrimSpace(opts.Query)
	filterObj, err := parseMessagesFilter(opts.Filter)
	if err != nil {
		return nil, err
	}
	if query == "" && opts.Filter == "" {
		return nil, errors.New("query or filter is required")
	}
	limit := clampLimit(opts.Limit, defaultMessageLimit)

	if strings.TrimSpace(opts.Chat) != "" {
		peer, err := s.resolveAndAuthorizeChat(ctx, opts.Chat)
		if err != nil {
			return nil, err
		}
		res, err := s.api.MessagesSearch(ctx, &tg.MessagesSearchRequest{
			Peer:   peer.inputPeer,
			Q:      query,
			Filter: filterObj,
			Limit:  limit,
		})
		if err != nil {
			return nil, fmt.Errorf("messages.search: %w", err)
		}
		rawMsgs, users, chats, err := unpackMessages(res)
		if err != nil {
			return nil, err
		}
		s.cacheEntities(users, chats)
		idx := newEntityIndex(users, chats)

		out := make([]MessageInfo, 0, len(rawMsgs))
		for _, mc := range rawMsgs {
			if msg, ok := mc.(*tg.Message); ok {
				out = append(out, NormalizeMessage(msg, idx, peer.chatID, peer.title))
			}
		}
		return out, nil
	}

	// Global search across chats; strictly filter every result through AccessPolicy.
	res, err := s.api.MessagesSearchGlobal(ctx, &tg.MessagesSearchGlobalRequest{
		Q:          query,
		Filter:     filterObj,
		OffsetPeer: &tg.InputPeerEmpty{},
		Limit:      maxQueryLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("messages.searchGlobal: %w", err)
	}
	rawMsgs, users, chats, err := unpackMessages(res)
	if err != nil {
		return nil, err
	}
	s.cacheEntities(users, chats)
	idx := newEntityIndex(users, chats)

	out := make([]MessageInfo, 0, limit)
	for _, mc := range rawMsgs {
		msg, ok := mc.(*tg.Message)
		if !ok || msg.PeerID == nil {
			continue
		}
		chatID, rawID, title, username, _ := idx.peerMeta(msg.PeerID)
		if !s.policy.Allowed(chatID, rawID, username) {
			continue
		}
		out = append(out, NormalizeMessage(msg, idx, chatID, title))
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// DownloadAttachment downloads a message attachment (or full grouped album) into the local cache.
func (s *Service) DownloadAttachment(ctx context.Context, opts DownloadOptions) ([]DownloadedFile, error) {
	if strings.TrimSpace(opts.Chat) == "" {
		return nil, errors.New("chat is required")
	}
	if opts.MessageID <= 0 {
		return nil, errors.New("message_id must be positive")
	}
	peer, err := s.resolveAndAuthorizeChat(ctx, opts.Chat)
	if err != nil {
		return nil, err
	}

	targetMsg, err := s.fetchSingleMessage(ctx, peer, opts.MessageID)
	if err != nil {
		return nil, err
	}

	msgsToDownload := []*tg.Message{targetMsg}
	if opts.IncludeAlbum {
		if gid, ok := targetMsg.GetGroupedID(); ok && gid != 0 {
			albumMsgs, err := s.fetchAlbumMessages(ctx, peer, targetMsg.ID, gid)
			if err == nil && len(albumMsgs) > 0 {
				msgsToDownload = albumMsgs
			}
		}
	}

	if err := os.MkdirAll(s.mediaDir, 0o700); err != nil {
		return nil, fmt.Errorf("create media cache dir: %w", err)
	}

	results := make([]DownloadedFile, 0, len(msgsToDownload))
	for _, msg := range msgsToDownload {
		att, loc := ExtractAttachment(msg.Media)
		if att == nil || loc == nil {
			continue
		}
		if att.SizeBytes > s.maxMediaBytes {
			return nil, fmt.Errorf("message %d attachment %q (%d bytes) exceeds max-media-bytes limit (%d bytes)",
				msg.ID, att.FileName, att.SizeBytes, s.maxMediaBytes)
		}

		dstPath, err := policy.SafeMediaPath(s.mediaDir, peer.chatID, msg.ID, att.FileName)
		if err != nil {
			return nil, err
		}

		if fi, statErr := os.Stat(dstPath); statErr == nil && fi.Mode().IsRegular() && fi.Size() > 0 {
			if att.SizeBytes == 0 || fi.Size() == att.SizeBytes {
				digest, hashErr := sha256File(dstPath)
				if hashErr == nil {
					results = append(results, DownloadedFile{
						ChatID:    peer.chatID,
						MessageID: msg.ID,
						Kind:      att.Kind,
						FileName:  att.FileName,
						MimeType:  att.MimeType,
						SizeBytes: fi.Size(),
						Path:      dstPath,
						SHA256:    digest,
						Cached:    true,
					})
					continue
				}
			}
		}

		downloaded, err := s.downloadAtomic(ctx, loc, dstPath)
		if err != nil {
			return nil, fmt.Errorf("download attachment for message %d: %w", msg.ID, err)
		}
		results = append(results, DownloadedFile{
			ChatID:    peer.chatID,
			MessageID: msg.ID,
			Kind:      att.Kind,
			FileName:  att.FileName,
			MimeType:  att.MimeType,
			SizeBytes: downloaded.size,
			Path:      dstPath,
			SHA256:    downloaded.sha256,
			Cached:    false,
		})
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("message %d has no downloadable photo or document attachment", opts.MessageID)
	}
	return results, nil
}

type downloadMetrics struct {
	size   int64
	sha256 string
}

func (s *Service) downloadAtomic(ctx context.Context, loc tg.InputFileLocationClass, dstPath string) (downloadMetrics, error) {
	tmpFile, err := os.CreateTemp(s.mediaDir, ".tgread-dl-*")
	if err != nil {
		return downloadMetrics{}, err
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}()

	if err := tmpFile.Chmod(0o600); err != nil {
		return downloadMetrics{}, err
	}

	lw := &limitedHashWriter{
		w:   tmpFile,
		h:   sha256.New(),
		max: s.maxMediaBytes,
	}
	if _, err := s.dl.Download(s.api, loc).Stream(ctx, lw); err != nil {
		return downloadMetrics{}, err
	}
	if err := tmpFile.Sync(); err != nil {
		return downloadMetrics{}, err
	}
	if err := tmpFile.Close(); err != nil {
		return downloadMetrics{}, err
	}
	if err := os.Rename(tmpPath, dstPath); err != nil {
		return downloadMetrics{}, err
	}
	return downloadMetrics{
		size:   lw.n,
		sha256: hex.EncodeToString(lw.h.Sum(nil)),
	}, nil
}

type limitedHashWriter struct {
	w   io.Writer
	h   hash.Hash
	n   int64
	max int64
}

func (l *limitedHashWriter) Write(p []byte) (int, error) {
	if l.n+int64(len(p)) > l.max {
		return 0, fmt.Errorf("download exceeded max-media-bytes limit (%d bytes)", l.max)
	}
	n, err := l.w.Write(p)
	if n > 0 {
		_, _ = l.h.Write(p[:n])
		l.n += int64(n)
	}
	return n, err
}

func (s *Service) fetchSingleMessage(ctx context.Context, peer resolvedPeer, msgID int) (*tg.Message, error) {
	ids := []tg.InputMessageClass{&tg.InputMessageID{ID: msgID}}
	var (
		res tg.MessagesMessagesClass
		err error
	)
	if peer.channel != nil {
		res, err = s.api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: peer.channel,
			ID:      ids,
		})
	} else {
		res, err = s.api.MessagesGetMessages(ctx, ids)
	}
	if err != nil {
		return nil, fmt.Errorf("fetch message %d: %w", msgID, err)
	}
	rawMsgs, users, chats, err := unpackMessages(res)
	if err != nil {
		return nil, err
	}
	s.cacheEntities(users, chats)

	for _, mc := range rawMsgs {
		if msg, ok := mc.(*tg.Message); ok && msg.ID == msgID {
			return msg, nil
		}
	}
	return nil, fmt.Errorf("message %d not found in chat %d", msgID, peer.chatID)
}

func (s *Service) fetchAlbumMessages(ctx context.Context, peer resolvedPeer, centerMsgID int, groupedID int64) ([]*tg.Message, error) {
	// Telegram albums contain at most 10 items; inspect a narrow window around centerMsgID.
	res, err := s.api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
		Peer:      peer.inputPeer,
		OffsetID:  centerMsgID + 11,
		AddOffset: 0,
		Limit:     25,
	})
	if err != nil {
		return nil, err
	}
	rawMsgs, users, chats, err := unpackMessages(res)
	if err != nil {
		return nil, err
	}
	s.cacheEntities(users, chats)

	var album []*tg.Message
	for _, mc := range rawMsgs {
		msg, ok := mc.(*tg.Message)
		if !ok {
			continue
		}
		if gid, ok := msg.GetGroupedID(); ok && gid == groupedID {
			album = append(album, msg)
		}
	}
	sort.Slice(album, func(i, j int) bool { return album[i].ID < album[j].ID })
	return album, nil
}

func (s *Service) resolveAndAuthorizeChat(ctx context.Context, ref string) (resolvedPeer, error) {
	peer, err := s.resolveChat(ctx, ref)
	if err != nil {
		return resolvedPeer{}, err
	}
	if !s.policy.Allowed(peer.chatID, peer.rawID, peer.username) {
		return resolvedPeer{}, fmt.Errorf("access denied by policy for chat %q (id=%d)", ref, peer.chatID)
	}
	return peer, nil
}

func (s *Service) resolveChat(ctx context.Context, ref string) (resolvedPeer, error) {
	trimmed := strings.TrimSpace(ref)
	lower := strings.ToLower(trimmed)

	if lower == "me" || lower == "self" || lower == "saved" {
		s.mu.RLock()
		sp := s.selfPeer
		s.mu.RUnlock()
		if sp != nil {
			return *sp, nil
		}
		if _, err := s.Status(ctx); err == nil {
			s.mu.RLock()
			sp = s.selfPeer
			s.mu.RUnlock()
			if sp != nil {
				return *sp, nil
			}
		}
		return resolvedPeer{
			peerType:  "self",
			title:     "Saved Messages",
			inputPeer: &tg.InputPeerSelf{},
		}, nil
	}

	// Numeric ID resolution.
	if id, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		if peer, ok := s.lookupPeerByID(id); ok {
			return peer, nil
		}
		// Warm cache from recent dialogs once, then retry.
		if _, _, _, err := s.fetchDialogs(ctx, maxQueryLimit); err != nil {
			return resolvedPeer{}, err
		}
		if peer, ok := s.lookupPeerByID(id); ok {
			return peer, nil
		}
		// Basic group chats do not need an access hash.
		if id < 0 && id > -channelIDOffset {
			rawChatID := -id
			return resolvedPeer{
				chatID:    id,
				rawID:     rawChatID,
				peerType:  "group",
				inputPeer: &tg.InputPeerChat{ChatID: rawChatID},
			}, nil
		}
		return resolvedPeer{}, fmt.Errorf("chat ID %d not found in recent dialogs; call list_chats or pass @username", id)
	}

	// Username resolution.
	uname := strings.TrimPrefix(lower, "@")
	if uname == "" {
		return resolvedPeer{}, fmt.Errorf("invalid chat identifier %q", ref)
	}
	s.mu.RLock()
	if peer, ok := s.peersByUser[uname]; ok {
		s.mu.RUnlock()
		return peer, nil
	}
	s.mu.RUnlock()

	resolved, err := s.api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{
		Username: uname,
	})
	if err != nil {
		return resolvedPeer{}, fmt.Errorf("resolve username @%s: %w", uname, err)
	}
	s.cacheEntities(resolved.Users, resolved.Chats)

	cid, _, _ := CanonicalPeerID(resolved.Peer)
	if peer, ok := s.lookupPeerByID(cid); ok {
		return peer, nil
	}
	return resolvedPeer{}, fmt.Errorf("could not resolve peer for @%s", uname)
}

func (s *Service) lookupPeerByID(id int64) (resolvedPeer, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if p, ok := s.peersByID[id]; ok {
		return p, true
	}
	if p, ok := s.peersByRawID[id]; ok {
		return p, true
	}
	return resolvedPeer{}, false
}

func (s *Service) cacheEntities(users []tg.UserClass, chats []tg.ChatClass) {
	for _, uc := range users {
		if u, ok := uc.(*tg.User); ok {
			s.cacheUser(u)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, cc := range chats {
		switch c := cc.(type) {
		case *tg.Chat:
			rp := resolvedPeer{
				chatID:    CanonicalChatID(c.ID),
				rawID:     c.ID,
				peerType:  "group",
				title:     c.Title,
				inputPeer: &tg.InputPeerChat{ChatID: c.ID},
			}
			s.peersByID[rp.chatID] = rp
			s.peersByRawID[rp.rawID] = rp
		case *tg.Channel:
			rp := resolvedPeer{
				chatID:    CanonicalChannelID(c.ID),
				rawID:     c.ID,
				peerType:  "channel",
				title:     c.Title,
				username:  c.Username,
				inputPeer: &tg.InputPeerChannel{ChannelID: c.ID, AccessHash: c.AccessHash},
				channel:   &tg.InputChannel{ChannelID: c.ID, AccessHash: c.AccessHash},
			}
			s.peersByID[rp.chatID] = rp
			s.peersByRawID[rp.rawID] = rp
			if c.Username != "" {
				s.peersByUser[strings.ToLower(c.Username)] = rp
			}
		}
	}
}

func (s *Service) cacheUser(u *tg.User) {
	if u == nil {
		return
	}
	peerType := "user"
	title := formatUserName(u)
	var inputPeer tg.InputPeerClass = &tg.InputPeerUser{UserID: u.ID, AccessHash: u.AccessHash}
	if u.Self {
		peerType = "self"
		title = "Saved Messages"
		inputPeer = &tg.InputPeerSelf{}
	}
	rp := resolvedPeer{
		chatID:    CanonicalUserID(u.ID),
		rawID:     u.ID,
		peerType:  peerType,
		title:     title,
		username:  u.Username,
		inputPeer: inputPeer,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.peersByID[rp.chatID] = rp
	s.peersByRawID[rp.rawID] = rp
	if u.Username != "" {
		s.peersByUser[strings.ToLower(u.Username)] = rp
	}
	if u.Self {
		cp := rp
		s.selfPeer = &cp
	}
}

func unpackMessages(res tg.MessagesMessagesClass) ([]tg.MessageClass, []tg.UserClass, []tg.ChatClass, error) {
	switch r := res.(type) {
	case *tg.MessagesMessages:
		return r.Messages, r.Users, r.Chats, nil
	case *tg.MessagesMessagesSlice:
		return r.Messages, r.Users, r.Chats, nil
	case *tg.MessagesChannelMessages:
		return r.Messages, r.Users, r.Chats, nil
	case *tg.MessagesMessagesNotModified:
		return nil, nil, nil, nil
	default:
		return nil, nil, nil, fmt.Errorf("unexpected messages response type %T", res)
	}
}

func parseMessagesFilter(raw string) (tg.MessagesFilterClass, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "all":
		return &tg.InputMessagesFilterEmpty{}, nil
	case "photos", "photo", "images":
		return &tg.InputMessagesFilterPhotos{}, nil
	case "documents", "document", "files", "pdf":
		return &tg.InputMessagesFilterDocument{}, nil
	case "urls", "url", "links":
		return &tg.InputMessagesFilterURL{}, nil
	case "audio", "music":
		return &tg.InputMessagesFilterMusic{}, nil
	case "voice":
		return &tg.InputMessagesFilterVoice{}, nil
	case "video", "videos":
		return &tg.InputMessagesFilterVideo{}, nil
	default:
		return nil, fmt.Errorf("unsupported filter %q (allowed: all, photos, documents, urls, audio, voice, video)", raw)
	}
}

func clampLimit(v, fallback int) int {
	if v <= 0 {
		return fallback
	}
	if v > maxQueryLimit {
		return maxQueryLimit
	}
	return v
}

func previewMessageText(msg *tg.Message) string {
	text := strings.TrimSpace(msg.Message)
	if text == "" {
		if att, _ := ExtractAttachment(msg.Media); att != nil {
			text = fmt.Sprintf("[%s: %s]", att.Kind, att.FileName)
		}
	}
	runes := []rune(text)
	if len(runes) > 120 {
		return string(runes[:117]) + "..."
	}
	return text
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
