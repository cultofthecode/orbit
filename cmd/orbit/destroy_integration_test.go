//go:build integration

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/cultofthecode/orbit/internal/agent/claude"
	"github.com/cultofthecode/orbit/internal/orbstack"
	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/initialize"
)

// destroyTimeout bounds the destroy test when the test binary has no
// deadline: three runs of init, two with the install of Claude Code, and the
// deletes.
const destroyTimeout = 30 * time.Minute

// Names in the sandbox that destroy must remove.
const (
	destroyFile      = "/home/agent/orbit-it-destroy-file"
	destroyImage     = "orbit-it/empty:1"
	destroyReadyWait = `for _ in $(seq 90); do docker info >/dev/null 2>&1 && break; sleep 1; done
docker info >/dev/null 2>&1 || { echo "the daemon did not answer within 90 s"; exit 1; }
`
)

// writeAgentState writes a file in the home of the agent and imports an
// empty image into the rootless engine, which needs no network.
const writeAgentState = "set -eu\n" + destroyReadyWait + `
printf 'login\n' > ` + destroyFile + `
tar -cf - -T /dev/null | docker import - ` + destroyImage + ` >/dev/null
docker image inspect ` + destroyImage + ` >/dev/null
`

// expectNoAgentState fails unless the file and the image are gone.
const expectNoAgentState = "set -eu\n" + destroyReadyWait + `
if [ -e ` + destroyFile + ` ]; then echo "` + destroyFile + ` exists"; exit 1; fi
images=$(docker images -q)
[ -z "$images" ] || { echo "the engine lists images: $images"; exit 1; }
`

// treeOf returns one line per entry of dir: its path, mode, modification
// time, and content hash.
func treeOf(t *testing.T, dir string) []string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		hash := ""
		if info.Mode().IsRegular() {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			hash = fmt.Sprintf("%x", sha256.Sum256(content))
		}
		lines = append(lines, fmt.Sprintf("%s %v %d %s", path, info.Mode(), info.ModTime().UnixNano(), hash))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

// orbit runs the command orbit with args in dir, with stdin as standard
// input, and returns the exit code, standard output, and standard error.
func orbit(ctx context.Context, t *testing.T, client *orbstack.Client, dir string, stdin *os.File, args ...string) (int, string, string) {
	t.Helper()
	t.Chdir(dir)
	root := newRoot(destroyCommand(client), stopCommand(client))
	root.SetIn(stdin)
	var stdout, stderr bytes.Buffer
	code := run(ctx, root, args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// poller reads the state of a machine until it is stopped, and records
// every state that it saw.
type poller struct {
	mu     sync.Mutex
	seen   map[string]bool
	cancel context.CancelFunc
	done   chan struct{}
}

// startPolling starts a poller of the machine name.
func startPolling(ctx context.Context, client *orbstack.Client, name string) *poller {
	ctx, cancel := context.WithCancel(ctx)
	p := &poller{seen: map[string]bool{}, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(p.done)
		for ctx.Err() == nil {
			if state, err := client.State(ctx, name); err == nil {
				p.mu.Lock()
				p.seen[state] = true
				p.mu.Unlock()
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	return p
}

// stop ends the poller and returns the states that it saw.
func (p *poller) stop() []string {
	p.cancel()
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	var states []string
	for state := range p.seen {
		states = append(states, state)
	}
	slices.Sort(states)
	return states
}

// TestDestroyDeletesTheSandboxAndNothingElse runs orbit destroy on two
// sandboxes: it deletes the running sandbox of the current directory with
// the agent state on its disk, keeps the project files and the second
// sandbox, refuses without a terminal and without a sandbox, and deletes a
// stopped sandbox without ever starting it. After the first destroy,
// orbit ls no longer lists the destroyed sandbox and still lists the second
// one as running.
func TestDestroyDeletesTheSandboxAndNothingElse(t *testing.T) {
	lockInit(t, syscall.LOCK_SH)
	ctx := boundedContext(t, destroyTimeout)
	client := orbstack.New(sandbox.NamePrefix)
	setUp := initHandler(client, claudeRegistry(t))
	dirA, nameA := newSandboxDir(ctx, t, client)
	dirB, nameB := newSandboxDir(ctx, t, client)
	for _, dir := range []string{dirA, dirB} {
		if _, err := setUp.Handle(ctx, initialize.Input{Dir: dir, Distro: integrationDistro(t), Agent: claude.Definition.Name}); err != nil {
			t.Fatalf("init --agent claude-code in %s = %v; machines that this test did not create: %s", dir, err, otherMachines(ctx, client, nameA))
		}
	}
	expectAgent(ctx, t, client, nameA, dirA, "write the agent state", writeAgentState)
	if err := os.Mkdir(filepath.Join(dirA, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	before := treeOf(t, dirA)
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = devNull.Close() })

	checkDestroyRefusals(ctx, t, client, devNull, dirA, nameA, nameB)
	checkDestroyOfARunningSandbox(ctx, t, client, devNull, dirA, nameA, nameB, before)
	checkLsAfterDestroy(ctx, t, client, dirB, nameA, nameB)
	checkReinitHasNoAgentState(ctx, t, client, setUp, dirA, nameA)
	checkDestroyOfAStoppedSandbox(ctx, t, client, devNull, dirA, nameA, nameB)
	expectOrbStackRunning(t)
}

// checkDestroyRefusals checks that destroy without --yes and without a
// terminal, and in a directory without a sandbox, deletes nothing.
func checkDestroyRefusals(ctx context.Context, t *testing.T, client *orbstack.Client, devNull *os.File, dirA, nameA, nameB string) {
	t.Helper()
	code, stdout, stderr := orbit(ctx, t, client, dirA, devNull, "destroy")
	if code != exitFailure || stdout != "" || !strings.Contains(stderr, "--yes") {
		t.Errorf("destroy with /dev/null and no --yes = %d, %q, %q, want a refusal that names --yes", code, stdout, stderr)
	}
	for _, dir := range []string{projectDir(t), filepath.Join(dirA, "sub")} {
		code, stdout, stderr := orbit(ctx, t, client, dir, devNull, "destroy", "--yes")
		if code != exitFailure || stdout != "" || !strings.Contains(stderr, `"orbit init"`) {
			t.Errorf("destroy --yes in %s = %d, %q, %q, want no sandbox", dir, code, stdout, stderr)
		}
	}
	expectState(ctx, t, client, nameA, "running", "after the refusals")
	expectState(ctx, t, client, nameB, "running", "after the refusals")
}

// checkDestroyOfARunningSandbox destroys the running sandbox a with --yes
// and checks the machine, the second sandbox, and the project tree.
func checkDestroyOfARunningSandbox(ctx context.Context, t *testing.T, client *orbstack.Client, devNull *os.File, dirA, nameA, nameB string, before []string) {
	t.Helper()
	code, stdout, stderr := orbit(ctx, t, client, dirA, devNull, "destroy", "--yes")
	if want := "Destroyed sandbox " + nameA + " for " + dirA + "\n"; code != exitOK || stdout != want || stderr != "" {
		t.Fatalf("destroy --yes = %d, %q, %q, want 0, %q", code, stdout, stderr, want)
	}
	expectState(ctx, t, client, nameA, "", "after the destroy")
	expectState(ctx, t, client, nameB, "running", "after the destroy of "+nameA)
	if after := treeOf(t, dirA); !slices.Equal(before, after) {
		t.Errorf("the project tree changed:\nbefore: %q\nafter:  %q", before, after)
	}
}

// checkLsAfterDestroy runs orbit ls after the destroy of sandbox a and checks
// that it exits 0, no longer lists a, and still lists b as running with its
// directory (owner decision Q3 on #12).
func checkLsAfterDestroy(ctx context.Context, t *testing.T, client *orbstack.Client, dirB, nameA, nameB string) {
	t.Helper()
	stdout, stderr := runLs(ctx, t, client)
	t.Logf("orbit ls after the destroy of %s:\n%s", nameA, stdout)
	if stderr != "" {
		t.Errorf("orbit ls after the destroy wrote %q to standard error", stderr)
	}
	if strings.Contains(stdout, nameA) {
		t.Errorf("orbit ls after the destroy still lists %s:\n%s", nameA, stdout)
	}
	checkRow(t, stdout, nameB, "running", dirB)
	checkEveryNameHasThePrefix(t, stdout)
}

// checkReinitHasNoAgentState runs init again in the directory of the
// destroyed sandbox and checks that the new sandbox has none of its state.
func checkReinitHasNoAgentState(ctx context.Context, t *testing.T, client *orbstack.Client, setUp initialize.Handler, dirA, nameA string) {
	t.Helper()
	out, err := setUp.Handle(ctx, initialize.Input{Dir: dirA, Distro: integrationDistro(t), Agent: claude.Definition.Name})
	if err != nil || !out.Created || out.Name != nameA {
		t.Fatalf("init after the destroy = %+v, %v, want the sandbox %s created again; machines that this test did not create: %s", out, err, nameA, otherMachines(ctx, client, nameA))
	}
	expectAgent(ctx, t, client, nameA, dirA, "agent state after the destroy and init", expectNoAgentState)
}

// checkDestroyOfAStoppedSandbox stops sandbox a with orbit stop, destroys it while a poller
// reads its state, and checks that it was never seen running. Sampling
// cannot prove a negative; the guarantee is the two-method interface of the
// slice.
func checkDestroyOfAStoppedSandbox(ctx context.Context, t *testing.T, client *orbstack.Client, devNull *os.File, dirA, nameA, nameB string) {
	t.Helper()
	stopThroughTheCommand(ctx, t, client, devNull, dirA, nameA)
	polling := startPolling(ctx, client, nameA)
	code, stdout, stderr := orbit(ctx, t, client, dirA, devNull, "destroy", "--yes")
	seen := polling.stop()
	if want := "Destroyed sandbox " + nameA + " for " + dirA + "\n"; code != exitOK || stdout != want || stderr != "" {
		t.Errorf("destroy --yes of a stopped sandbox = %d, %q, %q, want 0, %q", code, stdout, stderr, want)
	}
	if slices.Contains(seen, "running") {
		t.Errorf("states seen during the destroy of a stopped sandbox = %q, want no running", seen)
	}
	t.Logf("states seen during the destroy of the stopped sandbox: %q", seen)
	expectState(ctx, t, client, nameA, "", "after the destroy of the stopped sandbox")
	expectState(ctx, t, client, nameB, "running", "after the destroy of "+nameA)
}

// stopThroughTheCommand runs orbit stop in dir and checks that the sandbox
// name is stopped.
func stopThroughTheCommand(ctx context.Context, t *testing.T, client *orbstack.Client, devNull *os.File, dir, name string) {
	t.Helper()
	if code, stdout, stderr := orbit(ctx, t, client, dir, devNull, "stop"); code != exitOK {
		t.Fatalf("orbit stop = %d, %q, %q", code, stdout, stderr)
	}
	expectState(ctx, t, client, name, "stopped", "after the stop")
}
