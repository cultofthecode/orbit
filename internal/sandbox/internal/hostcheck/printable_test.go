package hostcheck

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/cultofthecode/orbit/internal/sandbox"
)

func TestPrintableErrorEscapesWhatIsNotPrintable(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want string
	}{
		{name: "printable text stays", msg: `/Users/dev/héllo wörld "x": no such file`, want: `/Users/dev/héllo wörld "x": no such file`},
		{name: "escape and newline", msg: "/home\n\x1b[2J/.ssh", want: `/home\n\x1b[2J/.ssh`},
		{name: "carriage return, bell, and tab", msg: "a\rb\x07c\td", want: `a\rb\ac\td`},
		{name: "invalid UTF-8", msg: "a\xffb", want: `a\xffb`},
		{name: "unicode line separator", msg: "a\u2028b", want: `a\u2028b`},
		{name: "C1 control", msg: "a\u009bb", want: `a\u009bb`},
		{name: "backslash", msg: `a\nb`, want: `a\\nb`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := printable(errors.New(test.msg)).Error(); got != test.want {
				t.Errorf("printable(%q) = %q, want %q", test.msg, got, test.want)
			}
		})
	}
}

func TestPrintableKeepsNilAndTheWrappedError(t *testing.T) {
	if err := printable(nil); err != nil {
		t.Errorf("printable(nil) = %v, want nil", err)
	}
	cause := fmt.Errorf("wrapped: %w", syscall.ELOOP)
	if err := printable(cause); !errors.Is(err, syscall.ELOOP) {
		t.Errorf("printable(%v) = %v, want it to wrap %v", cause, err, syscall.ELOOP)
	}
}

// hostileHome is a home directory whose name holds an escape sequence and a
// line break, with the directory work/api in it.
type hostileHome struct {
	layout
	home string
}

func newHostileHome(t *testing.T) hostileHome {
	t.Helper()
	l := newLayout(t)
	home := filepath.Join(l.root, "home\n\x1b[2J")
	if err := os.MkdirAll(filepath.Join(home, "work", "api"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	return hostileHome{layout: l, home: home}
}

// projectDir runs ProjectDir on the work/api directory of the home.
func (h hostileHome) projectDir() error {
	entry := &user.User{Uid: strconv.Itoa(os.Getuid()), HomeDir: h.home}
	_, err := ProjectDir(filepath.Join(h.home, "work", "api"), func() (*user.User, error) { return entry, nil })
	return err
}

func (h hostileHome) symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// requirePrintable fails unless err wraps want and its message names the
// hostile directory in escaped form, with no raw control character.
func requirePrintable(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	if !strings.Contains(err.Error(), `home\n\x1b[2J`) {
		t.Errorf("error = %q, want the escaped directory name", err)
	}
	for _, raw := range []string{"\n", "\x1b", "\x07", "\r", "\t"} {
		if strings.Contains(err.Error(), raw) {
			t.Errorf("error = %q, contains the raw character %q", err, raw)
		}
	}
}

// TestErrorsQuoteTheHostilePathOfEveryKind covers each error of hostcheck
// that holds a path derived from the home directory (#111).
func TestErrorsQuoteTheHostilePathOfEveryKind(t *testing.T) {
	t.Run("dangling target with .. after a name", func(t *testing.T) {
		h := newHostileHome(t)
		h.symlink(t, "nowhere/../x", filepath.Join(h.home, ".ssh"))

		requirePrintable(t, h.projectDir(), ErrUnresolvableLink)
	})
	t.Run("dangling target that is too long a chain", func(t *testing.T) {
		h := newHostileHome(t)
		h.symlink(t, "links/l0", filepath.Join(h.home, ".ssh"))
		const hops = maxLinkHops + 4
		for i := range hops - 1 {
			h.symlink(t, "l"+strconv.Itoa(i+1), filepath.Join(h.home, "links", "l"+strconv.Itoa(i)))
		}
		h.symlink(t, "gone", filepath.Join(h.home, "links", "l"+strconv.Itoa(hops-1)))

		requirePrintable(t, h.projectDir(), syscall.ELOOP)
	})
	t.Run("symlink loop in a protected directory", func(t *testing.T) {
		h := newHostileHome(t)
		h.symlink(t, "loop", filepath.Join(h.home, ".ssh", "loop"))

		requirePrintable(t, h.projectDir(), syscall.ELOOP)
	})
	t.Run("protected directory that cannot be read", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root reads every directory")
		}
		h := newHostileHome(t)
		locked := filepath.Join(h.home, ".ssh", "locked")
		if err := os.MkdirAll(locked, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(locked, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

		requirePrintable(t, h.projectDir(), os.ErrPermission)
	})
	t.Run("stored mount path through a symlink", func(t *testing.T) {
		h := newHostileHome(t)
		target := filepath.Join(h.home, "real")
		link := filepath.Join(h.home, "link")
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		h.symlink(t, target, link)
		machines := map[string]map[string]string{other: {link: link}}

		requirePrintable(t, Unnested(machines, own, h.proj, false), sandbox.ErrLinkedSandboxDir)
	})
}
