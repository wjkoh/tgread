package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wjkoh/tgread/internal/mcpserver"
	"github.com/wjkoh/tgread/internal/policy"
	"github.com/wjkoh/tgread/internal/tgread"
)

const appVersion = "0.1.0"

type commonConfig struct {
	APIID                    int
	APIHash                  string
	APIIDFile                string
	APIHashFile              string
	SessionFile              string
	MediaDir                 string
	MaxMediaBytes            int64
	AllowChats               string
	DenyChats                string
	AllowTelegramServiceChat bool
}

func defaultPaths() (sessionFile, mediaDir string) {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		cfgDir = "."
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		cacheDir = os.TempDir()
	}
	return filepath.Join(cfgDir, "tgread", "session.json"),
		filepath.Join(cacheDir, "tgread", "media")
}

func registerCommonFlags(fs *flag.FlagSet, cfg *commonConfig) {
	defSession, defMedia := defaultPaths()

	envAPIID, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("TGREAD_API_ID")))
	envMaxBytes, _ := strconv.ParseInt(strings.TrimSpace(os.Getenv("TGREAD_MAX_MEDIA_BYTES")), 10, 64)
	if envMaxBytes <= 0 {
		envMaxBytes = 50 * 1024 * 1024
	}
	envSession := strings.TrimSpace(os.Getenv("TGREAD_SESSION_FILE"))
	if envSession == "" {
		envSession = defSession
	}
	envMedia := strings.TrimSpace(os.Getenv("TGREAD_MEDIA_DIR"))
	if envMedia == "" {
		envMedia = defMedia
	}
	envAllowService := strings.EqualFold(strings.TrimSpace(os.Getenv("TGREAD_ALLOW_TELEGRAM_SERVICE_CHAT")), "true") ||
		strings.TrimSpace(os.Getenv("TGREAD_ALLOW_TELEGRAM_SERVICE_CHAT")) == "1"

	fs.IntVar(&cfg.APIID, "api-id", envAPIID, "Telegram API ID (or TGREAD_API_ID)")
	fs.StringVar(&cfg.APIHash, "api-hash", strings.TrimSpace(os.Getenv("TGREAD_API_HASH")), "Telegram API Hash (or TGREAD_API_HASH)")
	fs.StringVar(&cfg.APIIDFile, "api-id-file", strings.TrimSpace(os.Getenv("TGREAD_API_ID_FILE")), "Path to file containing Telegram API ID (or TGREAD_API_ID_FILE)")
	fs.StringVar(&cfg.APIHashFile, "api-hash-file", strings.TrimSpace(os.Getenv("TGREAD_API_HASH_FILE")), "Path to file containing Telegram API Hash (or TGREAD_API_HASH_FILE)")
	fs.StringVar(&cfg.SessionFile, "session", envSession, "Path to 0600 Telegram session file (or TGREAD_SESSION_FILE)")
	fs.StringVar(&cfg.MediaDir, "media-dir", envMedia, "Local directory for downloaded attachments (or TGREAD_MEDIA_DIR)")
	fs.Int64Var(&cfg.MaxMediaBytes, "max-media-bytes", envMaxBytes, "Maximum downloadable attachment size in bytes (or TGREAD_MAX_MEDIA_BYTES)")
	fs.StringVar(&cfg.AllowChats, "allow-chats", os.Getenv("TGREAD_ALLOW_CHATS"), "Optional comma-separated allowlist of chat IDs or @usernames (or TGREAD_ALLOW_CHATS)")
	fs.StringVar(&cfg.DenyChats, "deny-chats", os.Getenv("TGREAD_DENY_CHATS"), "Optional comma-separated denylist of chat IDs or @usernames (or TGREAD_DENY_CHATS)")
	fs.BoolVar(&cfg.AllowTelegramServiceChat, "allow-telegram-service-chat", envAllowService, "Allow reading Telegram official service chat 777000 (blocked by default)")
}

func (c *commonConfig) validateCredentials() error {
	if c.APIID <= 0 && strings.TrimSpace(c.APIIDFile) != "" {
		raw, err := os.ReadFile(c.APIIDFile)
		if err != nil {
			return fmt.Errorf("read api-id-file %q: %w", c.APIIDFile, err)
		}
		id, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil {
			return fmt.Errorf("parse api-id-file %q: %w", c.APIIDFile, err)
		}
		c.APIID = id
	}
	if strings.TrimSpace(c.APIHash) == "" && strings.TrimSpace(c.APIHashFile) != "" {
		raw, err := os.ReadFile(c.APIHashFile)
		if err != nil {
			return fmt.Errorf("read api-hash-file %q: %w", c.APIHashFile, err)
		}
		c.APIHash = strings.TrimSpace(string(raw))
	}
	if c.APIID <= 0 || strings.TrimSpace(c.APIHash) == "" {
		return errors.New("missing Telegram API credentials: set TGREAD_API_ID/TGREAD_API_HASH or --api-id-file/--api-hash-file (from https://my.telegram.org/apps)")
	}
	return nil
}

func (c *commonConfig) buildPolicy() (*policy.AccessPolicy, error) {
	return policy.New(policy.Config{
		AllowChats:               policy.ParseCSV(c.AllowChats),
		DenyChats:                policy.ParseCSV(c.DenyChats),
		AllowTelegramServiceChat: c.AllowTelegramServiceChat,
	})
}

func (c *commonConfig) newClient() (*telegram.Client, error) {
	if err := c.validateCredentials(); err != nil {
		return nil, err
	}
	sessDir := filepath.Dir(c.SessionFile)
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		return nil, fmt.Errorf("create session dir: %w", err)
	}
	return telegram.NewClient(c.APIID, c.APIHash, telegram.Options{
		NoUpdates:      true,
		AllowCDN:       true,
		SessionStorage: &session.FileStorage{Path: c.SessionFile},
		Device: telegram.DeviceConfig{
			DeviceModel: "tgread",
			AppVersion:  appVersion,
		},
	}), nil
}

func ensureSessionPermissions(path string) {
	_ = os.Chmod(path, 0o600)
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var err error
	switch os.Args[1] {
	case "login":
		err = runLogin(ctx, os.Args[2:])
	case "status":
		err = runStatus(ctx, os.Args[2:])
	case "serve":
		err = runServe(ctx, os.Args[2:])
	case "chats":
		err = runChats(ctx, os.Args[2:])
	case "messages":
		err = runMessages(ctx, os.Args[2:])
	case "search":
		err = runSearch(ctx, os.Args[2:])
	case "download":
		err = runDownload(ctx, os.Args[2:])
	case "version", "-v", "--version":
		fmt.Printf("tgread %s\n", appVersion)
		return
	case "help", "-h", "--help":
		printUsage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `tgread %s — compile-time read-only Telegram MTProto MCP server & CLI

Usage:
  tgread <subcommand> [flags]

Subcommands:
  login      Interactive one-time Telegram login (saves 0600 session file)
  status     Verify session authorization and print account/policy summary
  serve      Run MCP stdio server (for Antigravity CLI, Claude Code, Cursor)
  chats      List recent cloud chats as JSON
  messages   Fetch recent messages from a chat as JSON
  search     Search messages in a chat or globally as JSON
  download   Download an attachment (PDF/image/file) to local cache as JSON
  version    Print version

Environment variables:
  TGREAD_API_ID, TGREAD_API_HASH     Telegram app credentials from https://my.telegram.org/apps
  TGREAD_API_ID_FILE                 Path to file containing API ID (SOPS-Nix friendly)
  TGREAD_API_HASH_FILE               Path to file containing API Hash (SOPS-Nix friendly)
  TGREAD_SESSION_FILE                Session path (default: ~/.config/tgread/session.json)
  TGREAD_MEDIA_DIR                   Attachment cache dir (default: ~/.cache/tgread/media)
  TGREAD_MAX_MEDIA_BYTES             Max attachment bytes (default: 52428800)
  TGREAD_ALLOW_CHATS                 Optional comma-separated chat ID / @username allowlist
  TGREAD_DENY_CHATS                  Optional comma-separated chat ID / @username denylist
  TGREAD_ALLOW_TELEGRAM_SERVICE_CHAT Set to 1/true to allow Telegram service chat 777000
`, appVersion)
}

func runLogin(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	var cfg commonConfig
	registerCommonFlags(fs, &cfg)
	if err := fs.Parse(args); err != nil {
		return err
	}

	client, err := cfg.newClient()
	if err != nil {
		return err
	}
	return client.Run(ctx, func(ctx context.Context) error {
		flow := auth.NewFlow(terminalAuth{reader: bufio.NewReader(os.Stdin)}, auth.SendCodeOptions{})
		if err := client.Auth().IfNecessary(ctx, flow); err != nil {
			return err
		}
		ensureSessionPermissions(cfg.SessionFile)

		accessPolicy, err := cfg.buildPolicy()
		if err != nil {
			return err
		}
		svc, err := tgread.NewService(client.API(), accessPolicy, cfg.MediaDir, cfg.MaxMediaBytes)
		if err != nil {
			return err
		}
		st, err := svc.Status(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Logged in as %s (@%s, id=%d). Session saved to %s (0600).\n",
			st.DisplayName, st.Username, st.UserID, cfg.SessionFile)
		return nil
	})
}

func runWithService(ctx context.Context, cfg *commonConfig, fn func(ctx context.Context, svc *tgread.Service) error) error {
	accessPolicy, err := cfg.buildPolicy()
	if err != nil {
		return err
	}
	client, err := cfg.newClient()
	if err != nil {
		return err
	}
	return client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return fmt.Errorf("check auth status: %w", err)
		}
		if !status.Authorized {
			return fmt.Errorf("not logged in (session %s); run 'tgread login' first", cfg.SessionFile)
		}
		ensureSessionPermissions(cfg.SessionFile)

		svc, err := tgread.NewService(client.API(), accessPolicy, cfg.MediaDir, cfg.MaxMediaBytes)
		if err != nil {
			return err
		}
		return fn(ctx, svc)
	})
}

func runStatus(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	var cfg commonConfig
	registerCommonFlags(fs, &cfg)
	if err := fs.Parse(args); err != nil {
		return err
	}
	return runWithService(ctx, &cfg, func(ctx context.Context, svc *tgread.Service) error {
		st, err := svc.Status(ctx)
		if err != nil {
			return err
		}
		return writeJSON(st)
	})
}

func runServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	var cfg commonConfig
	registerCommonFlags(fs, &cfg)
	if err := fs.Parse(args); err != nil {
		return err
	}
	return runWithService(ctx, &cfg, func(ctx context.Context, svc *tgread.Service) error {
		srv, err := mcpserver.New(svc, appVersion)
		if err != nil {
			return err
		}
		return srv.Run(ctx, &mcp.StdioTransport{})
	})
}

func runChats(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("chats", flag.ExitOnError)
	var cfg commonConfig
	registerCommonFlags(fs, &cfg)
	limit := fs.Int("limit", 30, "Maximum number of chats to return")
	unreadOnly := fs.Bool("unread", false, "Only show chats with unread messages")
	query := fs.String("query", "", "Filter by title or username substring")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return runWithService(ctx, &cfg, func(ctx context.Context, svc *tgread.Service) error {
		chats, err := svc.ListChats(ctx, tgread.ListChatsOptions{
			Limit:      *limit,
			UnreadOnly: *unreadOnly,
			Query:      *query,
		})
		if err != nil {
			return err
		}
		return writeJSON(chats)
	})
}

func runMessages(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("messages", flag.ExitOnError)
	var cfg commonConfig
	registerCommonFlags(fs, &cfg)
	chat := fs.String("chat", "", "Chat ID, @username, or 'me'")
	limit := fs.Int("limit", 20, "Maximum number of messages to return")
	offsetID := fs.Int("offset-id", 0, "Paginate older messages before this message ID")
	minDate := fs.String("min-date", "", "Earliest date (RFC3339 or YYYY-MM-DD)")
	maxDate := fs.String("max-date", "", "Latest date (RFC3339 or YYYY-MM-DD)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return runWithService(ctx, &cfg, func(ctx context.Context, svc *tgread.Service) error {
		msgs, err := svc.GetMessages(ctx, tgread.GetMessagesOptions{
			Chat:     *chat,
			Limit:    *limit,
			OffsetID: *offsetID,
			MinDate:  *minDate,
			MaxDate:  *maxDate,
		})
		if err != nil {
			return err
		}
		return writeJSON(msgs)
	})
}

func runSearch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	var cfg commonConfig
	registerCommonFlags(fs, &cfg)
	query := fs.String("query", "", "Search query")
	chat := fs.String("chat", "", "Optional chat ID, @username, or 'me' (omit for global search)")
	limit := fs.Int("limit", 20, "Maximum number of messages to return")
	filter := fs.String("filter", "all", "Filter: all, photos, documents, urls, audio, voice, video")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return runWithService(ctx, &cfg, func(ctx context.Context, svc *tgread.Service) error {
		msgs, err := svc.SearchMessages(ctx, tgread.SearchMessagesOptions{
			Query:  *query,
			Chat:   *chat,
			Limit:  *limit,
			Filter: *filter,
		})
		if err != nil {
			return err
		}
		return writeJSON(msgs)
	})
}

func runDownload(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("download", flag.ExitOnError)
	var cfg commonConfig
	registerCommonFlags(fs, &cfg)
	chat := fs.String("chat", "", "Chat ID, @username, or 'me'")
	msgID := fs.Int("message-id", 0, "Message ID containing attachment")
	includeAlbum := fs.Bool("album", false, "Download all items in a grouped album")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return runWithService(ctx, &cfg, func(ctx context.Context, svc *tgread.Service) error {
		files, err := svc.DownloadAttachment(ctx, tgread.DownloadOptions{
			Chat:         *chat,
			MessageID:    *msgID,
			IncludeAlbum: *includeAlbum,
		})
		if err != nil {
			return err
		}
		return writeJSON(files)
	})
}

func writeJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// terminalAuth implements gotd/td auth.UserAuthenticator using stdin/stderr prompts.
type terminalAuth struct {
	reader *bufio.Reader
}

func (a terminalAuth) Phone(_ context.Context) (string, error) {
	fmt.Fprint(os.Stderr, "Telegram phone number (international format, e.g. +14155551234): ")
	line, err := a.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func (a terminalAuth) Code(_ context.Context, _ *tg.AuthSentCode) (string, error) {
	fmt.Fprint(os.Stderr, "Telegram login code: ")
	line, err := a.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func (a terminalAuth) Password(_ context.Context) (string, error) {
	fmt.Fprint(os.Stderr, "Telegram 2FA password (input hidden if TTY): ")
	restored := disableTTYEcho()
	line, err := a.reader.ReadString('\n')
	if restored {
		enableTTYEcho()
		fmt.Fprintln(os.Stderr)
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func (a terminalAuth) AcceptTermsOfService(_ context.Context, tos tg.HelpTermsOfService) error {
	return fmt.Errorf("account signup is not supported; log in with an existing Telegram account (TOS: %s)", tos.Text)
}

func (a terminalAuth) SignUp(_ context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, errors.New("account signup is not supported; use an existing Telegram account")
}

func disableTTYEcho() bool {
	fi, err := os.Stdin.Stat()
	if err != nil || (fi.Mode()&os.ModeCharDevice) == 0 {
		return false
	}
	cmd := exec.Command("stty", "-echo")
	cmd.Stdin = os.Stdin
	return cmd.Run() == nil
}

func enableTTYEcho() {
	cmd := exec.Command("stty", "echo")
	cmd.Stdin = os.Stdin
	_ = cmd.Run()
}
