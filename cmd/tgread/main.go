package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
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
	"github.com/gotd/td/telegram/auth/qrlogin"
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

func (c *commonConfig) newClient(updateHandler telegram.UpdateHandler) (*telegram.Client, error) {
	if err := c.validateCredentials(); err != nil {
		return nil, err
	}
	sessDir := filepath.Dir(c.SessionFile)
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		return nil, fmt.Errorf("create session dir: %w", err)
	}
	opts := telegram.Options{
		NoUpdates:      updateHandler == nil,
		UpdateHandler:  updateHandler,
		AllowCDN:       true,
		SessionStorage: &session.FileStorage{Path: c.SessionFile},
		Device: telegram.DeviceConfig{
			DeviceModel: "tgread",
			AppVersion:  appVersion,
		},
	}
	return telegram.NewClient(c.APIID, c.APIHash, opts), nil
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
	useQR := fs.Bool("qr", false, "Log in by scanning a QR code in Telegram (Settings -> Devices -> Link Desktop Device)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var (
		dispatcher    tg.UpdateDispatcher
		updateHandler telegram.UpdateHandler
	)
	if *useQR {
		dispatcher = tg.NewUpdateDispatcher()
		updateHandler = dispatcher
	}

	client, err := cfg.newClient(updateHandler)
	if err != nil {
		return err
	}
	return client.Run(ctx, func(ctx context.Context) error {
		ta := terminalAuth{reader: bufio.NewReader(os.Stdin)}
		if *useQR {
			status, err := client.Auth().Status(ctx)
			if err != nil {
				return fmt.Errorf("check auth status: %w", err)
			}
			if !status.Authorized {
				loggedIn := qrlogin.OnLoginToken(dispatcher)
				qrFlow := qrlogin.NewQR(client.API(), cfg.APIID, cfg.APIHash, qrlogin.Options{
					Migrate:  client.MigrateTo,
					Password: ta.Password,
				})
				if _, err := qrFlow.Auth(ctx, loggedIn, func(_ context.Context, token qrlogin.Token) error {
					img, err := token.Image(0)
					if err != nil {
						return fmt.Errorf("render QR image: %w", err)
					}
					fmt.Fprintln(os.Stderr, "\nScan this QR code in Telegram (Settings -> Devices -> Link Desktop Device):")
					fmt.Fprintln(os.Stderr, renderQRToTerminal(img))
					fmt.Fprintf(os.Stderr, "Token URL: %s\nWaiting for scan...\n", token.URL())
					return nil
				}); err != nil {
					return err
				}
			}
		} else {
			flow := auth.NewFlow(ta, auth.SendCodeOptions{})
			if err := client.Auth().IfNecessary(ctx, flow); err != nil {
				return err
			}
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
	client, err := cfg.newClient(nil)
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

func (a terminalAuth) Code(_ context.Context, sentCode *tg.AuthSentCode) (string, error) {
	fmt.Fprintf(os.Stderr, "Login code sent via %s.\n", describeSentCodeType(sentCode))
	fmt.Fprint(os.Stderr, "Telegram login code: ")
	line, err := a.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func describeSentCodeType(sent *tg.AuthSentCode) string {
	if sent == nil || sent.Type == nil {
		return "unknown delivery channel"
	}
	switch t := sent.Type.(type) {
	case *tg.AuthSentCodeTypeApp:
		return fmt.Sprintf("Telegram app (%d-digit code in the official 'Telegram' chat — check Archived Chats if muted)", t.Length)
	case *tg.AuthSentCodeTypeSMS:
		return fmt.Sprintf("SMS (%d-digit code)", t.Length)
	case *tg.AuthSentCodeTypeEmailCode:
		return fmt.Sprintf("email to %s (%d-digit code)", t.EmailPattern, t.Length)
	case *tg.AuthSentCodeTypeFragmentSMS:
		return fmt.Sprintf("Fragment SMS (%s)", t.URL)
	case *tg.AuthSentCodeTypeCall:
		return fmt.Sprintf("phone call (%d-digit code)", t.Length)
	case *tg.AuthSentCodeTypeFlashCall:
		return "flash phone call"
	case *tg.AuthSentCodeTypeMissedCall:
		return "missed phone call"
	default:
		return fmt.Sprintf("%T", sent.Type)
	}
}

// renderQRToTerminal converts a QR code image into a compact ANSI half-block string
// with black foreground on white background so it scans reliably on dark and light terminals.
func renderQRToTerminal(img image.Image) string {
	b := img.Bounds()
	isDark := func(x, y int) bool {
		r, g, bl, _ := img.At(x, y).RGBA()
		return (r+g+bl)/3 < 0x8000
	}

	// Find tight bounding box of dark modules and exact pixel scale per module.
	minX, minY := b.Max.X, b.Max.Y
	maxX, maxY := b.Min.X-1, b.Min.Y-1
	scale := b.Dx()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		run := 0
		prevDark := false
		rowHasDark := false
		for x := b.Min.X; x < b.Max.X; x++ {
			d := isDark(x, y)
			if d {
				rowHasDark = true
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
			if x == b.Min.X {
				prevDark = d
				run = 1
			} else if d == prevDark {
				run++
			} else {
				if prevDark && run > 0 && run < scale {
					scale = run
				}
				prevDark = d
				run = 1
			}
		}
		if rowHasDark && prevDark && run > 0 && run < scale {
			scale = run
		}
	}
	if maxX < minX || maxY < minY || scale <= 0 {
		return ""
	}

	cols := (maxX-minX+1)/scale + 4 // 2-module quiet zone on left and right
	rows := (maxY-minY+1)/scale + 4 // 2-module quiet zone on top and bottom

	moduleDark := func(mx, my int) bool {
		if mx < 2 || my < 2 || mx >= cols-2 || my >= rows-2 {
			return false
		}
		px := minX + (mx-2)*scale + scale/2
		py := minY + (my-2)*scale + scale/2
		if px < b.Min.X || px >= b.Max.X || py < b.Min.Y || py >= b.Max.Y {
			return false
		}
		return isDark(px, py)
	}

	var sb strings.Builder
	for my := 0; my < rows; my += 2 {
		sb.WriteString("\x1b[30;47m")
		for mx := 0; mx < cols; mx++ {
			top := moduleDark(mx, my)
			bot := false
			if my+1 < rows {
				bot = moduleDark(mx, my+1)
			}
			switch {
			case top && bot:
				sb.WriteRune('█')
			case top && !bot:
				sb.WriteRune('▀')
			case !top && bot:
				sb.WriteRune('▄')
			default:
				sb.WriteRune(' ')
			}
		}
		sb.WriteString("\x1b[0m\n")
	}
	return sb.String()
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
