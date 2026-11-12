package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/cultofthecode/orbit/internal/orbstack"
	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/list"
)

// stubLister returns fixed machines or an error.
type stubLister struct {
	machines []orbstack.Machine
	err      error
}

func (s stubLister) List(context.Context) ([]orbstack.Machine, error) {
	return s.machines, s.err
}

func TestListMachinesKeepsSourceAndDestinationApart(t *testing.T) {
	lister := stubLister{machines: []orbstack.Machine{
		{Name: "orbit-api-1", State: "running", Mounts: []orbstack.Mount{{Source: "/host/api", Destination: "/mnt/api"}}},
		{Name: "orbit-web-2", State: "stopped", Mounts: []orbstack.Mount{{Source: "/a", Destination: "/b"}, {Source: "/c", Destination: "/d"}}},
		{Name: "orbit-bare"},
	}}

	got, err := listMachines{lister}.List(context.Background())

	want := []list.Machine{
		{Name: "orbit-api-1", State: "running", Mounts: []sandbox.Mount{{Source: "/host/api", Destination: "/mnt/api"}}},
		{Name: "orbit-web-2", State: "stopped", Mounts: []sandbox.Mount{{Source: "/a", Destination: "/b"}, {Source: "/c", Destination: "/d"}}},
		{Name: "orbit-bare"},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("List = %+v, %v, want %+v", got, err, want)
	}
}

func TestListMachinesReturnsTheErrorOfTheAdapter(t *testing.T) {
	failure := errors.New("machine list cannot be read")

	got, err := listMachines{stubLister{err: failure}}.List(context.Background())

	if !errors.Is(err, failure) || got != nil {
		t.Errorf("List = %+v, %v, want %v", got, err, failure)
	}
}
