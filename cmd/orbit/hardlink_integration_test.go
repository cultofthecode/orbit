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
// its canonical path and the account home.
func temporaryHome(t *testing.T) (string, string) {
	t.Helper()
	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"one", "two"} {
		if err := os.MkdirAll(filepath.Join(home, ".claude", "file-history", dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	return home, account.HomeDir
}

// accountHomeMachines runs every call of machines with HOME set to the
// account home: orb finds OrbStack through $HOME and reports a stopped
// OrbStack under any other HOME (#70), while the handler reads the
// temporary HOME of the test before it asks OrbStack anything. The test
// runs no other goroutine that reads HOME.
type accountHomeMachines struct {
	machines *orbstack.Client
	home     string
}

// withAccountHome runs call with HOME set to the account home and restores
// HOME afterwards.
func (m accountHomeMachines) withAccountHome(call func() error) error {
	previous := os.Getenv("HOME")
	if err := os.Setenv("HOME", m.home); err != nil {
		return err
	}
	defer func() { _ = os.Setenv("HOME", previous) }()
	return call()
}

func (m accountHomeMachines) Mounts(ctx context.Context) (map[string]map[string]string, error) {
	var mounts map[string]map[string]string
	err := m.withAccountHome(func() (err error) {
		mounts, err = m.machines.Mounts(ctx)
		return err
	})
	return mounts, err
}

func (m accountHomeMachines) ActiveMounts(ctx context.Context, name string) (map[string]string, error) {
	var mounts map[string]string
	err := m.withAccountHome(func() (err error) {
		mounts, err = m.machines.ActiveMounts(ctx, name)
		return err
	})
	return mounts, err
}

func (m accountHomeMachines) Exists(ctx context.Context, name string) (bool, error) {
	var exists bool
	err := m.withAccountHome(func() (err error) {
		exists, err = m.machines.Exists(ctx, name)
		return err
	})
	return exists, err
}

func (m accountHomeMachines) Create(ctx context.Context, name, dir string) error {
	return m.withAccountHome(func() error { return m.machines.Create(ctx, name, dir) })
}

func (m accountHomeMachines) RunAsRoot(ctx context.Context, name string, script []byte) error {
	return m.withAccountHome(func() error { return m.machines.RunAsRoot(ctx, name, script) })
}

func (m accountHomeMachines) RunAsAgent(ctx context.Context, name string, script []byte) error {
	return m.withAccountHome(func() error { return m.machines.RunAsAgent(ctx, name, script) })
}

func (m accountHomeMachines) Delete(ctx context.Context, name string) error {
	return m.withAccountHome(func() error { return m.machines.Delete(ctx, name) })
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
	client := orbstack.New(sandbox.NamePrefix)
	dir, name := newSandboxDir(ctx, t, client)
	home, accountHome := temporaryHome(t)
	machines := accountHomeMachines{machines: client, home: accountHome}
	handler := initHandler(machines, claudeRegistry(t))
	settings := filepath.Join(home, ".claude", "settings.json")
	link := filepath.Join(dir, "settings.json")
	writeHardLinkedFile(t, settings, link)

	refused, err := handler.Handle(ctx, initialize.Input{Dir: dir})
	if !errors.Is(err, sandbox.ErrHardLinkedFile) || !strings.Contains(err.Error(), settings) {
		t.Fatalf("Handle with a hard link to %s = %+v, %v, want %v naming it", settings, refused, err, sandbox.ErrHardLinkedFile)
	}
	t.Logf("refused: %v", err)
	if exists, err := machines.Exists(ctx, name); err != nil || exists {
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
