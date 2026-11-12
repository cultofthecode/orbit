// Package list is the slice of "orbit ls": it lists every sandbox with its
// state and the project directory that OrbStack stores for it, from one read
// of the machine list. It starts nothing and runs no command in a machine.
package list

// Input is the request to list the sandboxes. It has no field: the listing
// does not depend on the current directory.
type Input struct{}

// Output is the list of the sandboxes, sorted by name.
type Output struct {
	Sandboxes []Sandbox
}

// Sandbox is one sandbox as OrbStack stores it.
type Sandbox struct {
	// Name is the name of the machine.
	Name string
	// State is the state as OrbStack reports it, such as "running" or
	// "stopped". It is empty when OrbStack gave none.
	State string
	// Dir is the project directory that OrbStack stores as the only mount
	// of the machine. It is empty when Problem is set.
	Dir string
	// Problem is the reason why the project directory is unknown. It is set
	// exactly when Dir is empty.
	Problem string
}

// The handler fails with the error of the machine list, wrapped, when the
// list cannot be read as a whole, and then returns no sandbox. A machine
// whose project directory is unknown is a Sandbox with a Problem, not an
// error.
