// Package agent holds the definitions of the coding agents that Orbit can
// install in a sandbox (D16). An agent is data, not code paths: one package
// under internal/agent exports one Definition, and cmd/orbit registers it.
// No code outside the package of an agent compares its name. The package is
// pure and does no I/O.
package agent

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

var (
	// ErrUnknownAgent reports a name that no registered definition has.
	ErrUnknownAgent = errors.New("unknown agent")
	// ErrInvalidDefinition reports a definition that a registry refuses: an
	// empty name, or a name that another definition already has.
	ErrInvalidDefinition = errors.New("invalid agent definition")
)

// Definition is what Orbit needs to know about one agent.
type Definition struct {
	// Name is the value of "orbit init --agent".
	Name string
	// Install is the bash script that installs the official client of the
	// agent as the agent user, through the proxy. It holds no secret and no
	// value of the user.
	Install string
	// EgressDomains are the hosts that the client and its sign-in need.
	// They become allow rules of the proxy in a sandbox of this agent.
	EgressDomains []string
}

// Registry holds the definitions that Orbit can install, by name.
type Registry struct {
	definitions map[string]Definition
}

// NewRegistry returns a registry of definitions. It refuses a definition
// with an empty name and two definitions with the same name.
func NewRegistry(definitions ...Definition) (*Registry, error) {
	byName := make(map[string]Definition, len(definitions))
	for _, definition := range definitions {
		if err := checkName(byName, definition.Name); err != nil {
			return nil, err
		}
		byName[definition.Name] = definition
	}
	return &Registry{definitions: byName}, nil
}

// checkName refuses an empty name and a name that is already in byName.
func checkName(byName map[string]Definition, name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty name", ErrInvalidDefinition)
	}
	if _, taken := byName[name]; taken {
		return fmt.Errorf("%w: the name %q is defined twice", ErrInvalidDefinition, name)
	}
	return nil
}

// Lookup returns the definition named name. It fails with ErrUnknownAgent,
// naming the known agents, when no definition has that name. The caller
// owns the returned EgressDomains.
func (r *Registry) Lookup(name string) (Definition, error) {
	definition, found := r.definitions[name]
	if !found {
		return Definition{}, fmt.Errorf("%w %q (known agents: %s)", ErrUnknownAgent, name, strings.Join(r.Names(), ", "))
	}
	definition.EgressDomains = slices.Clone(definition.EgressDomains)
	return definition, nil
}

// Names returns the names of the registered definitions, sorted.
func (r *Registry) Names() []string {
	return slices.Sorted(maps.Keys(r.definitions))
}
