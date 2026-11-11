package agent

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

var (
	first  = Definition{Name: "first", Install: "true\n", EgressDomains: []string{"first.example"}}
	second = Definition{Name: "second", Install: "true\n"}
)

func TestNewRegistryRefusesInvalidDefinitions(t *testing.T) {
	tests := []struct {
		name        string
		definitions []Definition
	}{
		{name: "empty name", definitions: []Definition{{Install: "true\n"}}},
		{name: "duplicate name", definitions: []Definition{first, {Name: "first", Install: "false\n"}}},
		{name: "duplicate after a valid one", definitions: []Definition{first, second, first}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry, err := NewRegistry(test.definitions...)
			if !errors.Is(err, ErrInvalidDefinition) || registry != nil {
				t.Errorf("NewRegistry = %v, %v, want nil, %v", registry, err, ErrInvalidDefinition)
			}
		})
	}
}

func TestLookup(t *testing.T) {
	registry, err := NewRegistry(first, second)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		lookup  string
		want    Definition
		wantErr error
	}{
		{name: "first", lookup: "first", want: first},
		{name: "second", lookup: "second", want: second},
		{name: "unknown", lookup: "codex", wantErr: ErrUnknownAgent},
		{name: "empty", lookup: "", wantErr: ErrUnknownAgent},
		{name: "other case", lookup: "First", wantErr: ErrUnknownAgent},
		{name: "surrounding space", lookup: " first", wantErr: ErrUnknownAgent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := registry.Lookup(test.lookup)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Lookup(%q) = %v, want %v", test.lookup, err, test.wantErr)
			}
			if got.Name != test.want.Name || got.Install != test.want.Install || !slices.Equal(got.EgressDomains, test.want.EgressDomains) {
				t.Errorf("Lookup(%q) = %+v, want %+v", test.lookup, got, test.want)
			}
		})
	}
}

func TestLookupOfAnUnknownAgentNamesTheKnownAgents(t *testing.T) {
	registry, err := NewRegistry(second, first)
	if err != nil {
		t.Fatal(err)
	}
	_, err = registry.Lookup("codex")
	if err == nil || !strings.Contains(err.Error(), `"codex"`) || !strings.Contains(err.Error(), "first, second") {
		t.Errorf("Lookup(codex) = %v, want the name and the known agents in the text", err)
	}
}

func TestLookupReturnsACopyOfTheDomains(t *testing.T) {
	registry, err := NewRegistry(first)
	if err != nil {
		t.Fatal(err)
	}
	got, err := registry.Lookup("first")
	if err != nil {
		t.Fatal(err)
	}
	got.EgressDomains[0] = "changed.example"
	again, err := registry.Lookup("first")
	if err != nil || again.EgressDomains[0] != "first.example" {
		t.Errorf("second Lookup = %+v, %v, want the domains unchanged", again, err)
	}
}

func TestNames(t *testing.T) {
	registry, err := NewRegistry(second, first)
	if err != nil {
		t.Fatal(err)
	}
	if got := registry.Names(); !slices.Equal(got, []string{"first", "second"}) {
		t.Errorf("Names() = %q, want sorted names", got)
	}
	empty, err := NewRegistry()
	if err != nil || len(empty.Names()) != 0 {
		t.Errorf("NewRegistry() = %v, %v, want an empty registry", empty, err)
	}
}
