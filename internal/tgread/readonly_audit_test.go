package tgread_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadOnlyASTAudit parses every non-test Go source file in the repository
// and verifies that no Telegram write, send, edit, delete, or read-receipt RPC
// method is referenced anywhere in the binary.
func TestReadOnlyASTAudit(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	// Exhaustive list of forbidden MTProto write/mutation method prefixes or names.
	forbiddenPrefixes := []string{
		"MessagesSend",
		"MessagesEdit",
		"MessagesDelete",
		"MessagesForward",
		"MessagesRead",
		"MessagesSaveDraft",
		"MessagesSetTyping",
		"MessagesClear",
		"MessagesUpdate",
		"MessagesToggle",
		"MessagesCreate",
		"MessagesAdd",
		"MessagesKick",
		"MessagesPin",
		"MessagesUnpin",
		"MessagesImport",
		"MessagesReport",
		"MessagesMark",
		"ChannelsRead",
		"ChannelsEdit",
		"ChannelsDelete",
		"ChannelsJoin",
		"ChannelsLeave",
		"ChannelsCreate",
		"ChannelsInvite",
		"ChannelsUpdate",
		"ChannelsToggle",
		"ChannelsSet",
		"AccountUpdate",
		"AccountDelete",
		"AccountSet",
		"ContactsBlock",
		"ContactsDelete",
		"ContactsImport",
		"ContactsAdd",
		"UploadSave",
		"FoldersEdit",
		"FoldersDelete",
	}

	// Explicit allowlist of tg.Client RPC methods permitted in this codebase.
	allowedTelegramRPCs := map[string]bool{
		"UsersGetUsers":           true,
		"MessagesGetDialogs":      true,
		"MessagesGetHistory":      true,
		"MessagesSearch":          true,
		"MessagesSearchGlobal":    true,
		"MessagesGetMessages":     true,
		"ChannelsGetMessages":     true,
		"ContactsResolveUsername": true,
	}

	fset := token.NewFileSet()
	var auditedFiles int

	err = filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == ".direnv" || name == "vendor" || name == "bin" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			t.Errorf("parse %s: %v", path, parseErr)
			return nil
		}
		auditedFiles++

		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			name := sel.Sel.Name
			pos := fset.Position(sel.Pos())

			for _, prefix := range forbiddenPrefixes {
				if strings.HasPrefix(name, prefix) {
					t.Errorf("FORBIDDEN Telegram mutation RPC %q referenced at %s:%d", name, pos.Filename, pos.Line)
				}
			}

			// If this selector is called on an identifier named "api", enforce the strict RPC allowlist.
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "api" {
				if !allowedTelegramRPCs[name] {
					t.Errorf("unapproved api.%s call at %s:%d (update allowlist only after security review)", name, pos.Filename, pos.Line)
				}
			}
			if parentSel, ok := sel.X.(*ast.SelectorExpr); ok && parentSel.Sel.Name == "api" {
				if !allowedTelegramRPCs[name] {
					t.Errorf("unapproved s.api.%s call at %s:%d (update allowlist only after security review)", name, pos.Filename, pos.Line)
				}
			}
			return true
		})

		return nil
	})
	if err != nil {
		t.Fatalf("walk repo: %v", err)
	}
	if auditedFiles == 0 {
		t.Fatalf("expected to audit at least one non-test Go file")
	}
}
