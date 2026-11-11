package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// forbiddenSubstrings are the names that no non-test Go file of the module
// may put in a string literal: the credential file of Claude Code, its
// Keychain entry and the Keychain query, and the variables that carry a
// credential of the agent (D08, R04).
var forbiddenSubstrings = []string{
	".credentials.json",
	"Claude Code-credentials",
	"find-generic-password",
	"CLAUDE_CODE_OAUTH_TOKEN",
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_AUTH_TOKEN",
}

// forbiddenIdentifiers are the names that no non-test Go file may use as an
// identifier.
var forbiddenIdentifiers = []string{
	"CLAUDE_CODE_OAUTH_TOKEN",
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_AUTH_TOKEN",
}

// forbiddenNames returns the forbidden names that the Go source src uses in
// a string literal or an identifier. A name in a comment passes. It is a
// tripwire, not a proof: a name built at run time passes it. It returns an
// error when src does not parse.
func forbiddenNames(src []byte) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), "", src, 0)
	if err != nil {
		return nil, err
	}
	var found []string
	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.BasicLit:
			if n.Kind == token.STRING {
				found = append(found, forbiddenInLiteral(n.Value)...)
			}
		case *ast.Ident:
			if slices.Contains(forbiddenIdentifiers, n.Name) {
				found = append(found, n.Name)
			}
		}
		return true
	})
	return found, nil
}

// forbiddenInLiteral returns the forbidden names in the Go string literal
// quoted, including the Keychain tool: a literal that is "security" or
// starts with "security ".
func forbiddenInLiteral(quoted string) []string {
	value, err := strconv.Unquote(quoted)
	if err != nil {
		return []string{quoted}
	}
	var found []string
	for _, name := range forbiddenSubstrings {
		if strings.Contains(value, name) {
			found = append(found, name)
		}
	}
	if value == "security" || strings.HasPrefix(value, "security ") {
		found = append(found, "security")
	}
	return found
}

func TestForbiddenNamesRefusesHostileSources(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"credential file", `package p; var f = "/home/agent/.claude/.credentials.json"`, []string{".credentials.json"}},
		{"raw string", "package p; var f = `.credentials.json`", []string{".credentials.json"}},
		{"keychain entry", `package p; var s = "Claude Code-credentials"`, []string{"Claude Code-credentials"}},
		{"keychain query", `package p; var c = "security find-generic-password -s x"`, []string{"find-generic-password", "security"}},
		{"keychain tool", `package p; import "os/exec"; var c = exec.Command("security", "dump-keychain")`, []string{"security"}},
		{"api key", `package p; import "os"; var k = os.Getenv("ANTHROPIC_API_KEY")`, []string{"ANTHROPIC_API_KEY"}},
		{"auth token", `package p; var k = "ANTHROPIC_AUTH_TOKEN=x"`, []string{"ANTHROPIC_AUTH_TOKEN"}},
		{"oauth token", `package p; var k = "CLAUDE_CODE_OAUTH_TOKEN"`, []string{"CLAUDE_CODE_OAUTH_TOKEN"}},
		{"identifier", `package p; const ANTHROPIC_API_KEY = 1`, []string{"ANTHROPIC_API_KEY"}},
		{"escaped literal", `package p; var f = "\x2ecredentials.json"`, []string{".credentials.json"}},
		{"line comment", `package p // .credentials.json and ANTHROPIC_API_KEY`, nil},
		{"block comment", "package p\n/* security find-generic-password */\nvar x = 1", nil},
		{"security as a word", `package p; var s = "security review"`, []string{"security"}},
		{"security inside a word", `package p; var s = "the security of the sandbox"`, nil},
		{"clean", `package p; var s = "claude.ai"`, nil},
	}
	for _, c := range cases {
		got, err := forbiddenNames([]byte(c.src))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		slices.Sort(got)
		want := slices.Clone(c.want)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%s: forbiddenNames = %v, want %v", c.name, got, want)
		}
	}
}

func TestForbiddenNamesReportsASourceThatDoesNotParse(t *testing.T) {
	if _, err := forbiddenNames([]byte("package p; var = ")); err == nil {
		t.Error("forbiddenNames of a broken source = nil error, want one: an unparsed file must not pass")
	}
}

// TestNoGoFileOfTheModuleNamesTheCredential runs forbiddenNames over every
// non-test Go file of the module (D08: Orbit never reads, copies, stores,
// logs, or forwards the credential of the agent). Nothing is allow-listed.
func TestNoGoFileOfTheModuleNamesTheCredential(t *testing.T) {
	root := filepath.Join("..", "..")
	checked := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "testdata") {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		names, err := forbiddenNames(src)
		if err != nil {
			t.Errorf("%s: %v", path, err)
		}
		if len(names) > 0 {
			t.Errorf("%s names %v: Orbit must not name the credential of the agent (D08)", path, names)
		}
		checked++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 10 {
		t.Errorf("checked %d Go files, want the whole module: the walk lost its root %s", checked, root)
	}
}
