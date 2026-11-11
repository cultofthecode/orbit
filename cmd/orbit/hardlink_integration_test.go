//go:build integration

package main

import (
	"context"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cultofthecode/orbit/internal/orbstack"
	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/initialize"
)

// temporaryHome makes a new home directory with a .claude directory in it,
// makes it the home directory of the environment of the test, and returns
// its canonical path. orb finds OrbStack through $HOME/.orbstack (#70), so
// that path is a symlink to the one of the account home.
func temporaryHome(t *testing.T) string {
	t.Helper()
	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(account.HomeDir, ".orbstack"), filepath.Join(home, ".orbstack")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude", "file-history", "one"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude", "file-history", "two"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	return home
}

// writeHardLinkedFile writes the empty file path, with a second hard link at
// link.
func writeHardLinkedFile(t *testing.T, path, link string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, link); err != nil {
		t.Fatal(err)
	}
}

// TestInitRefusesAHardLinkedCredentialFile runs init against the real
// OrbStack while the settings of Claude Code in the home directory have a
// hard link in the project directory (#69): init refuses and creates no
// machine. After the link is removed, init creates the machine, and links
// between the snapshots of Claude Code in .claude/file-history keep it
// accepted.
func TestInitRefusesAHardLinkedCredentialFile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), provisionTimeout)
	t.Cleanup(cancel)
	home := temporaryHome(t)
	client := orbstack.New(sandbox.NamePrefix)
	dir, name := newSandboxDir(ctx, t, client)
	handler := initHandler(client, claudeRegistry(t))
	settings := filepath.Join(home, ".claude", "settings.json")
	link := filepath.Join(dir, "settings.json")
	writeHardLinkedFile(t, settings, link)

	refused, err := handler.Handle(ctx, initialize.Input{Dir: dir})
	if !errors.Is(err, sandbox.ErrHardLinkedFile) || !strings.Contains(err.Error(), settings) {
		t.Fatalf("Handle with a hard link to %s = %+v, %v, want %v naming it", settings, refused, err, sandbox.ErrHardLinkedFile)
	}
	if exists, err := client.Exists(ctx, name); err != nil || exists {
		t.Fatalf("Exists(%q) after the refusal = %v, %v, want false", name, exists, err)
	}

	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	created, err := handler.Handle(ctx, initialize.Input{Dir: dir})
	if err != nil || !created.Created {
		t.Fatalf("Handle after the link was removed = %+v, %v, want a created sandbox", created, err)
	}

	writeHardLinkedFile(t, filepath.Join(home, ".claude", "file-history", "one", "snapshot"), filepath.Join(home, ".claude", "file-history", "two", "snapshot"))
	kept, err := handler.Handle(ctx, initialize.Input{Dir: dir})
	if err != nil || kept.Created {
		t.Fatalf("Handle with links in .claude/file-history = %+v, %v, want the existing sandbox provisioned again", kept, err)
	}
}
