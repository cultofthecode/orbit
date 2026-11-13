//go:build integration

package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/cultofthecode/orbit/internal/orbstack"
	"github.com/cultofthecode/orbit/internal/provision"
	"github.com/cultofthecode/orbit/internal/proxy"
	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/initialize"
)

// provisionTimeout bounds the provisioning of one test when the test
// binary has no deadline: two runs of the root script, each with a package
// install, and the probes.
const provisionTimeout = 10 * time.Minute

// cleanupGrace is the time that boundedContext leaves before the deadline
// of the test binary, so that t.Cleanup can delete the machine.
const cleanupGrace = 3 * time.Minute

// initLockPath is the file that serializes the integration runs on one
// host: TestActiveMountsShowASwappedPath in internal/orbstack holds an
// exclusive lock on the same path while its machine makes init refuse
// every directory.
const initLockPath = "/tmp/orbit-integration-init.lock"

// rootOnlyFiles are the files of the boundary, besides the units, that only
// root may write.
var rootOnlyFiles = []string{"/etc/nftables.conf", proxy.ConfigPath, proxy.AllowlistPath, "/etc/environment"}

func TestInitProvisionsTheSandbox(t *testing.T) {
	waitToRunInit(t)
	ctx := provisionContext(t)
	client := orbstack.New(sandbox.NamePrefix)
	dir, name := newSandboxDir(ctx, t, client)
	handler := initHandler(client, claudeRegistry(t))

	distro := integrationDistro(t)
	first, err := handler.Handle(ctx, initialize.Input{Dir: dir, Distro: distro})
	if err != nil || !first.Created || string(first.Distribution) != distro {
		t.Fatalf("first Handle = %+v, %v, want a created sandbox; machines that this test did not create: %s", first, err, otherMachines(ctx, client, name))
	}
	second, err := handler.Handle(ctx, initialize.Input{Dir: dir, Distro: distro})
	if err != nil || second.Created || string(second.Distribution) != distro {
		t.Fatalf("second Handle = %+v, %v, want the existing sandbox provisioned again; machines that this test did not create: %s", second, err, otherMachines(ctx, client, name))
	}

	port := macPortReachableByRoot(ctx, t, client, name)
	container := hostContainerReachableByRoot(ctx, t, client, name)
	writeHostFile(t, dir)
	script := fmt.Sprintf(probeScript, os.Getuid(), port, container, dir, strings.Join(rootOnlyPaths(), " "), provision.Version)
	if err := client.RunAsRoot(ctx, name, []byte(script)); err != nil {
		t.Errorf("probes in %s:\n%v", name, err)
	}
	checkAgentWroteTheProject(t, dir)
	checkProxyClosedFromTheMac(t, name)
}

// integrationDistro returns the distribution that this run of the integration
// tests creates with init: the value of ORBIT_IT_DISTRO, or ubuntu when it is
// unset. It fails the test for a value that is not a distribution, and logs
// the distribution so that a transcript shows which one it ran on.
func integrationDistro(t *testing.T) string {
	t.Helper()
	value := os.Getenv("ORBIT_IT_DISTRO")
	if value == "" {
		value = string(sandbox.DefaultDistribution)
	}
	if _, err := sandbox.ParseDistribution(value); err != nil {
		t.Fatalf("ORBIT_IT_DISTRO: %v", err)
	}
	t.Logf("distribution: %s", value)
	return value
}

// TestInitRefusesAnotherDistribution creates a sandbox on Ubuntu, then runs
// init --distro debian over it. Init must fail with the mismatch before it
// runs anything in the machine, and the machine must be untouched. It runs
// on Ubuntu only: the mismatch needs a sandbox of the other distribution.
func TestInitRefusesAnotherDistribution(t *testing.T) {
	if integrationDistro(t) != string(sandbox.Ubuntu) {
		t.Skip("the refusal is checked from an Ubuntu sandbox")
	}
	waitToRunInit(t)
	ctx := provisionContext(t)
	client := orbstack.New(sandbox.NamePrefix)
	dir, name := newSandboxDir(ctx, t, client)
	handler := initHandler(client, claudeRegistry(t))
	if _, err := handler.Handle(ctx, initialize.Input{Dir: dir, Distro: "ubuntu"}); err != nil {
		t.Fatalf("first Handle = %v; machines that this test did not create: %s", err, otherMachines(ctx, client, name))
	}
	if err := client.RunAsRoot(ctx, name, []byte("test \"$(cat "+provision.MarkerPath+")\" = "+fmt.Sprint(provision.Version)+"\n")); err != nil {
		t.Fatalf("marker before the refusal: %v", err)
	}
	if err := client.RunAsRoot(ctx, name, []byte("rm "+provision.MarkerPath+"\n")); err != nil {
		t.Fatalf("remove the marker to detect a script run: %v", err)
	}

	_, err := handler.Handle(ctx, initialize.Input{Dir: dir, Distro: "debian"})

	if !errors.Is(err, sandbox.ErrDistributionMismatch) {
		t.Fatalf("Handle --distro debian on an Ubuntu sandbox = %v, want %v", err, sandbox.ErrDistributionMismatch)
	}
	if err := client.RunAsRoot(ctx, name, []byte("test ! -e "+provision.MarkerPath+"\n")); err != nil {
		t.Errorf("the refused init ran a script in the machine (the marker came back): %v", err)
	}
	if exists, err := client.Exists(ctx, name); err != nil || !exists {
		t.Errorf("Exists after the refusal = %v, %v, want the machine kept", exists, err)
	}
}

// TestInitPinsTheStoredImage checks that Distribution reads back the key that
// Create used, for the distribution of this run. It pins the spelling that
// OrbStack stores for each image: ubuntu / resolute and debian / trixie.
func TestInitPinsTheStoredImage(t *testing.T) {
	distro := integrationDistro(t)
	waitToRunInit(t)
	ctx := provisionContext(t)
	client := orbstack.New(sandbox.NamePrefix)
	dir, name := newSandboxDir(ctx, t, client)
	if err := client.Create(ctx, name, dir, distro, false); err != nil {
		t.Fatalf("Create(%s) = %v", distro, err)
	}
	if got, err := client.Distribution(ctx, name); err != nil || got != distro {
		t.Errorf("Distribution after Create(%s) = %q, %v, want %q", distro, got, err, distro)
	}
}

// waitToRunInit holds a shared lock on initLockPath until the end of t. It
// waits while a test of any run on the host holds the exclusive lock, so
// that init does not refuse because of a machine of another run. Call it
// before the deadline of the test starts.
func waitToRunInit(t *testing.T) {
	t.Helper()
	file, err := os.OpenFile(initLockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err == nil {
		return
	}
	t.Logf("waiting for a test of another run that blocks init (%s)", initLockPath)
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_SH); err != nil {
		t.Fatalf("lock %s: %v", initLockPath, err)
	}
}

// provisionContext returns boundedContext with provisionTimeout as the
// bound for a test binary that has no deadline.
func provisionContext(t *testing.T) context.Context {
	t.Helper()
	return boundedContext(t, provisionTimeout)
}

// boundedContext returns a context that ends cleanupGrace before the
// deadline of the test binary, so that the -timeout the caller chose absorbs
// the load of other runs on one OrbStack and t.Cleanup still deletes the
// machine. Without a deadline it ends after fallback.
func boundedContext(t *testing.T, fallback time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithDeadline(context.Background(), contextEnd(t, fallback))
	t.Cleanup(cancel)
	return ctx
}

// contextEnd returns cleanupGrace before the deadline of the test binary, or
// fallback from now when it has none.
func contextEnd(t *testing.T, fallback time.Duration) time.Time {
	t.Helper()
	if deadline, ok := t.Deadline(); ok {
		return deadline.Add(-cleanupGrace)
	}
	return time.Now().Add(fallback)
}

// otherMachines returns the names of the machines on the host besides
// name, for the failure message of an init test: init checks the mounts of
// every machine, so a machine that the test did not create can make it
// refuse.
func otherMachines(ctx context.Context, client *orbstack.Client, name string) string {
	mounts, err := client.Mounts(context.WithoutCancel(ctx))
	if err != nil {
		return fmt.Sprintf("unknown (%v)", err)
	}
	names := slices.DeleteFunc(slices.Sorted(maps.Keys(mounts)), func(machine string) bool { return machine == name })
	return strings.Join(names, ", ")
}

// hardenRestarts restarts the harden unit as every run of init does, one
// time more than the default start limit of systemd allows (5 starts in
// 10 s). Each restart also restarts systemd-user-sessions.service, which
// requires the harden unit. The burst does not depend on how fast apt-get
// runs in init. Both units stay active, and logins stay enabled. The
// restart of the harden unit returns before the restart of user sessions
// ends, so the script waits until user sessions leave a transient state.
const hardenRestarts = `
set -eu
for run in 1 2 3 4 5 6; do systemctl restart orbit-harden.service; done
for wait in $(seq 300); do
	case "$(systemctl is-active systemd-user-sessions.service)" in activating|deactivating|reloading) sleep 0.1;; *) break;; esac
done
systemctl is-active --quiet orbit-harden.service
systemctl is-active --quiet systemd-user-sessions.service
! test -e /run/nologin
`

// TestInitRunsAgainAfterRepeatedRuns re-runs init on one sandbox right
// after the harden unit restarted more often than the default start limit
// of systemd allows (#66).
func TestInitRunsAgainAfterRepeatedRuns(t *testing.T) {
	waitToRunInit(t)
	ctx := provisionContext(t)
	client := orbstack.New(sandbox.NamePrefix)
	dir, name := newSandboxDir(ctx, t, client)
	handler := initHandler(client, claudeRegistry(t))

	distro := integrationDistro(t)
	if _, err := handler.Handle(ctx, initialize.Input{Dir: dir, Distro: distro}); err != nil {
		t.Fatalf("first Handle = %v; machines that this test did not create: %s", err, otherMachines(ctx, client, name))
	}
	for run := 2; run <= 3; run++ {
		if err := client.RunAsRoot(ctx, name, []byte(hardenRestarts)); err != nil {
			t.Fatalf("restarts of orbit-harden.service before run %d in %s:\n%v", run, name, err)
		}
		if out, err := handler.Handle(ctx, initialize.Input{Dir: dir, Distro: distro}); err != nil || out.Created {
			t.Fatalf("Handle run %d = %+v, %v, want the existing sandbox provisioned again; machines that this test did not create: %s", run, out, err, otherMachines(ctx, client, name))
		}
	}
}

// rootOnlyPaths returns rootOnlyFiles and the units and the harden script of
// the boundary.
func rootOnlyPaths() []string {
	paths := slices.Clone(rootOnlyFiles)
	for _, unit := range provision.Units(uint32(os.Getuid())) {
		paths = append(paths, unit.Path)
	}
	return paths
}

// writeHostFile writes hostFile into the project directory dir on the host.
func writeHostFile(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, hostFile), []byte(hostFileContent+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// checkAgentWroteTheProject expects that the agent deleted hostFile and
// wrote agentFile in the project directory dir, as the host sees it.
func checkAgentWroteTheProject(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, hostFile)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s on the host after the agent deleted it: %v", hostFile, err)
	}
	content, err := os.ReadFile(filepath.Join(dir, agentFile))
	if err != nil || string(content) != agentFileContent+"\n" {
		t.Errorf("%s on the host = %q, %v, want %q", agentFile, content, err, agentFileContent+"\n")
	}
}

// projectDir returns the canonical path of a new project directory that
// the mount rules accept.
func projectDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// serveOnTheMac starts an HTTP server on every address of the Mac and
// returns its port. Root in the machine reaching it is the positive control
// of the probes to the Mac.
func serveOnTheMac(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return listener.Addr().(*net.TCPAddr).Port
}

// macPortReachableByRoot serves on the Mac and returns the port when root in
// the machine name reaches it, or 0 after it logs that the probes to the Mac
// cannot run.
func macPortReachableByRoot(ctx context.Context, t *testing.T, client *orbstack.Client, name string) int {
	t.Helper()
	port := serveOnTheMac(t)
	control := fmt.Sprintf("curl -sSf --noproxy '*' -m 5 -o /dev/null http://%s:%d\n", macBridgeAddr, port)
	if err := client.RunAsRoot(ctx, name, []byte(control)); err != nil {
		t.Logf("root in %s cannot reach the Mac at %s:%d (%v); the probes to the Mac did not run", name, macBridgeAddr, port, err)
		return 0
	}
	return port
}

// hostContainerReachableByRoot starts a container on the host engine that
// listens on hostContainerPort and returns its address when root in the
// machine name reaches it, or "" after it logs that the probe of the
// container cannot run.
func hostContainerReachableByRoot(ctx context.Context, t *testing.T, client *orbstack.Client, name string) string {
	t.Helper()
	addr, err := startHostContainer(ctx, t)
	if err != nil {
		t.Logf("no container on the host engine (%v); the probe of the host-engine container did not run", err)
		return ""
	}
	control := fmt.Sprintf("for try in 1 2 3 4 5; do timeout 5 bash -c 'exec 3<>/dev/tcp/%s/%s' && exit 0; sleep 1; done; exit 1\n", addr, hostContainerPort)
	if err := client.RunAsRoot(ctx, name, []byte(control)); err != nil {
		t.Logf("root in %s cannot reach the host-engine container at %s:%s (%v); the probe of the host-engine container did not run", name, addr, hostContainerPort, err)
		return ""
	}
	return addr
}

// startHostContainer runs a busybox HTTP server on the host engine, removes
// it at the end of the test, and returns its IPv4 address.
func startHostContainer(ctx context.Context, t *testing.T) (string, error) {
	t.Helper()
	id, err := hostDocker(ctx, "run", "-d", "--rm", "busybox:1.37", "httpd", "-f", "-p", hostContainerPort)
	if err != nil {
		return "", err
	}
	t.Cleanup(func() { _, _ = hostDocker(context.WithoutCancel(ctx), "rm", "-f", id) })
	addr, err := hostDocker(ctx, "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", id)
	if err != nil {
		return "", err
	}
	if _, err := netip.ParseAddr(addr); err != nil {
		return "", fmt.Errorf("container %s has no address: %w", id, err)
	}
	return addr, nil
}

// hostDocker runs the docker client of the Mac and returns its trimmed
// standard output.
func hostDocker(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", args...).Output()
	if err != nil {
		return "", fmt.Errorf("docker %s: %w", args[0], err)
	}
	return strings.TrimSpace(string(out)), nil
}

// checkProxyClosedFromTheMac dials the proxy port of the machine from the
// Mac and expects no connection.
func checkProxyClosedFromTheMac(t *testing.T, name string) {
	t.Helper()
	host := name + ".orb.local"
	if _, err := net.LookupHost(host); err != nil {
		t.Logf("%s does not resolve on the Mac (%v); the check of the proxy port from outside did not run", host, err)
		return
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, "3128"), 5*time.Second)
	if err == nil {
		_ = conn.Close()
		t.Errorf("the Mac connected to %s:3128", host)
	} else if !strings.Contains(err.Error(), "timeout") && !strings.Contains(err.Error(), "refused") {
		t.Logf("dial %s:3128 failed with %v", host, err)
	}
}
