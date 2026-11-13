package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// weakenForPrivilegedLeak is what root does in M02: the rule of M01 goes,
// and a rule that accepts the agent uid takes its place. It never persists.
// M01's rule is the one that says `skuid != <agent uid> accept` and has no
// `skuid <`: the native output rule of the firewall also says `skuid !=`,
// with the default uid, and must stay.
const weakenForPrivilegedLeak = `set -eu
agent=$(id -u agent)
before=$(nft -a list chain inet orbit_egress output | grep -c ' handle ')
native=$(nft list chain inet orbit_egress output | grep -c 'skuid <')
handle=$(nft -a list chain inet orbit_egress output | awk -v u="$agent" '/skuid </ {next} $0 ~ "meta skuid != " u " accept" {print $NF}')
[ -n "$handle" ] && [ "$(printf '%s\n' "$handle" | grep -c .)" = 1 ] || { echo "M01's rule is not in the chain exactly once" >&2; exit 1; }
nft delete rule inet orbit_egress output handle "$handle"
nft insert rule inet orbit_egress output meta skuid "$agent" accept
after=$(nft -a list chain inet orbit_egress output | grep -c ' handle ')
[ "$after" = "$before" ] || { echo "the chain has $after rules, want $before: M01's rule out, the M02 rule in" >&2; exit 1; }
[ "$(nft list chain inet orbit_egress output | grep -c 'skuid <')" = "$native" ] || { echo "the native rule of the firewall is gone" >&2; exit 1; }
nft list chain inet orbit_egress output | grep -q "skuid $agent accept"
! nft list chain inet orbit_egress output | grep -q "skuid != $agent accept"
`

// fakeNft keeps the output chain in the file $CHAIN, one rule per line with
// its handle, and answers the four nft commands that weakenForPrivilegedLeak
// uses, so the script runs without a machine.
const fakeNft = `id() { echo 1001; }
nft() {
  case "$*" in
  "-a list chain inet orbit_egress output") echo "table inet orbit_egress {"; cat "$CHAIN"; echo "}";;
  "list chain inet orbit_egress output") echo "table inet orbit_egress {"; sed 's/ # handle [0-9]*$//' "$CHAIN"; echo "}";;
  "delete rule inet orbit_egress output handle "*)
    h=${@: -1}; grep -q " # handle $h\$" "$CHAIN" || { echo "no rule $h" >&2; return 1; }
    grep -v " # handle $h\$" "$CHAIN" > "$CHAIN.new"; mv "$CHAIN.new" "$CHAIN";;
  "insert rule inet orbit_egress output meta skuid "*) { echo "		meta skuid ${@: -2:1} ${@: -1} # handle 99"; cat "$CHAIN"; } > "$CHAIN.new"; mv "$CHAIN.new" "$CHAIN";;
  *) echo "unexpected nft $*" >&2; return 1;;
  esac
}
`

func TestWeakenForPrivilegedLeak(t *testing.T) {
	tests := []struct {
		name  string
		chain string
		ok    bool
	}{
		{"M01 and the native rule", "\t\tmeta skuid != 1001 accept # handle 7\n\t\tmeta skuid < 1000 meta skuid != 501 accept # handle 5\n\t\tmeta skuid 1001 counter drop # handle 6\n", true},
		{"M01's rule is absent", "\t\tmeta skuid < 1000 meta skuid != 501 accept # handle 5\n\t\tmeta skuid 1001 counter drop # handle 6\n", false},
		{"only the native rule has skuid !=", "\t\tmeta skuid < 1000 meta skuid != 1001 accept # handle 5\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			chain := filepath.Join(dir, "chain")
			if err := os.WriteFile(chain, []byte(tt.chain), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", "-c", fakeNft+weakenForPrivilegedLeak)
			cmd.Env = append(os.Environ(), "CHAIN="+chain)
			out, err := cmd.CombinedOutput()
			if (err == nil) != tt.ok {
				t.Fatalf("script error = %v, want ok %t\n%s", err, tt.ok, out)
			}
			after, _ := os.ReadFile(chain)
			if tt.ok && (!strings.Contains(string(after), "skuid < 1000 meta skuid != 501 accept") || strings.Contains(string(after), "skuid != 1001 accept")) {
				t.Errorf("chain after the weakening:\n%s", after)
			}
		})
	}
}
